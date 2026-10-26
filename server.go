package main

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
)

type config struct {
	token           string
	epubDir         string
	externalURL     string // e.g. "http://192.168.1.50:8080" — set at startup, not derived from r.Host
	debug           bool   // enable verbose header/body logging for catch-all routes (KOBO_DEBUG=1)
	hardcoverAPIKey string // optional; when empty, Hardcover metadata enrichment is disabled
	cacheDir        string // writable dir for the Hardcover metadata cache, cached covers, and converted kepubs
	kepubify        bool   // when true, EPUBs are converted to Kobo's KEPUB format before download
	uiPassword      string // optional; when set, the web UI and /api require HTTP basic auth with this password
	ui              fs.FS  // built web UI assets (index.html at the root); nil disables the UI
}

type server struct {
	cfg      config
	mux      *http.ServeMux
	store    *metadataStore // nil when Hardcover enrichment is disabled
	kepub    *kepubStore    // nil when kepub conversion is disabled
	activity *activityStore // device syncs, downloads, reading progress; shown in the web UI

	epubWritable bool // whether the web UI can add/remove books in epubDir
}

func newServer(cfg config) *server {
	s := &server{cfg: cfg, mux: http.NewServeMux(), activity: newActivityStore(cfg.cacheDir)}
	if cfg.hardcoverAPIKey != "" {
		s.store = newMetadataStore(cfg.cacheDir, newHardcoverClient(cfg.hardcoverAPIKey))
	}
	if cfg.kepubify {
		s.kepub = newKepubStore(cfg.cacheDir)
	}
	s.routes()
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *server) routes() {
	s.mux.HandleFunc("GET /kobo/{token}/v1/initialization", s.logged(s.authenticated(s.handleInitialization)))
	s.mux.HandleFunc("POST /kobo/{token}/v1/auth/device", s.logged(s.authenticated(s.handleAuthDevice)))
	s.mux.HandleFunc("POST /kobo/{token}/v1/auth/refresh", s.logged(s.authenticated(s.handleAuthDevice)))
	s.mux.HandleFunc("GET /kobo/{token}/v1/library/sync", s.logged(s.authenticated(s.handleLibrarySync)))
	s.mux.HandleFunc("GET /kobo/{token}/v1/library/{id}/metadata", s.logged(s.authenticated(s.handleLibraryMetadata)))
	s.mux.HandleFunc("GET /kobo/{token}/v1/library/{id}/download", s.logged(s.authenticated(s.handleDownload)))
	s.mux.HandleFunc("PUT /kobo/{token}/v1/library/{id}/state", s.logged(s.authenticated(s.handleLibraryState)))
	s.mux.HandleFunc("GET /kobo/{token}/v1/user/profile", s.logged(s.authenticated(s.handleUserProfile)))

	// Cover images — matches the ImageURLTemplate advertised in /v1/initialization.
	// {id} is the book's CoverImageId (its UUID); width/height/greyscale are ignored and
	// the cached original JPEG is served. Registered before the catch-all below.
	s.mux.HandleFunc("GET /kobo/{token}/images/{id}/{width}/{height}/{greyscale}/image.jpg", s.logged(s.authenticated(s.handleCoverImage)))

	// OIDC discovery — firmware 4.45+ fetches this before proceeding to library/sync.
	// Register at both the Calibre-Web path and the standard OIDC path since we don't
	// know which one this firmware version requests.
	s.mux.HandleFunc("/kobo/{token}/oauth/.well-known/openid-configuration", s.logged(s.authenticated(s.handleOidcDiscovery)))
	s.mux.HandleFunc("/kobo/{token}/.well-known/openid-configuration", s.logged(s.authenticated(s.handleOidcDiscovery)))

	// OAuth token endpoints — device may call these as part of the OIDC flow.
	s.mux.HandleFunc("/kobo/{token}/oauth/token", s.logged(s.authenticated(s.handleOAuth)))
	s.mux.HandleFunc("/kobo/{token}/oauth/authorize", s.logged(s.authenticated(s.handleOAuth)))
	s.mux.HandleFunc("/kobo/{token}/oauth/refresh", s.logged(s.authenticated(s.handleOAuth)))
	s.mux.HandleFunc("/kobo/{token}/oauth/userinfo", s.logged(s.authenticated(s.handleOAuth)))
	s.mux.HandleFunc("/kobo/{token}/oauth/", s.logged(s.authenticated(s.handleOAuth)))

	// Catch anything inside the token prefix not matched above — logs + returns {} for every
	// unimplemented Kobo endpoint. Real handlers get registered above this line as we build them.
	s.mux.HandleFunc("/kobo/{token}/", s.logged(s.authenticated(s.catchAll)))

	// Explicit handler for /kobo/ without a token segment. Go 1.22's ServeMux returns a 301
	// for this path without it, because it sees registered subtree patterns under /kobo/ and
	// redirects instead of falling through to the root handler.
	s.mux.HandleFunc("/kobo/", func(w http.ResponseWriter, r *http.Request) {
		slog.Warn("request outside kobo prefix", "method", r.Method, "path", r.URL.RequestURI())
		http.NotFound(w, r)
	})

	s.apiRoutes()

	// Anything outside the token prefix and the web UI gets a 404 with a helpful log line.
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.Warn("request outside kobo prefix", "method", r.Method, "path", r.URL.RequestURI())
		http.NotFound(w, r)
	})
}

// logged wraps a handler with timing and status logging.
func (s *server) logged(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, code: http.StatusOK}
		next(rec, r)
		slog.Info("→",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.code,
			"ms", time.Since(start).Milliseconds(),
		)
	}
}

// authenticated rejects requests whose URL token doesn't match the configured token.
func (s *server) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("token") != s.cfg.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// catchAll returns an empty JSON object for any unmapped Kobo endpoint.
// With KOBO_DEBUG=1 it also logs all headers and the request body.
func (s *server) catchAll(w http.ResponseWriter, r *http.Request) {
	if s.cfg.debug {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		attrs := []any{"method", r.Method, "path", r.URL.RequestURI()}
		for k, vs := range r.Header {
			attrs = append(attrs, k, vs[0])
		}
		if len(body) > 0 {
			attrs = append(attrs, "body", string(body))
		}
		slog.Debug("kobo catchall", attrs...)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct{}{})
}

type responseRecorder struct {
	http.ResponseWriter
	code int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}
