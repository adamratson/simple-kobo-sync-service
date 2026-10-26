package main

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

func main() {
	addr := envOr("KOBO_ADDR", ":8080")
	token := envOr("KOBO_TOKEN", "")
	epubDir := envOr("KOBO_EPUB_DIR", ".")
	externalURL := envOr("KOBO_EXTERNAL_URL", "")
	debug := os.Getenv("KOBO_DEBUG") != ""
	hardcoverAPIKey := envOr("HARDCOVER_API_KEY", "")
	cacheDir := envOr("HARDCOVER_CACHE_DIR", epubDir)
	kepubify := !isFalsy(os.Getenv("KOBO_KEPUBIFY")) // on unless explicitly disabled
	uiPassword := envOr("KOBO_UI_PASSWORD", "")

	if token == "" {
		suggested := randomToken()
		slog.Error("KOBO_TOKEN is required",
			"hint", "set KOBO_TOKEN="+suggested+" in your environment or docker-compose.yml")
		os.Exit(1)
	}

	if externalURL == "" {
		slog.Error("KOBO_EXTERNAL_URL is required",
			"hint", "set KOBO_EXTERNAL_URL=http://<your-ip>:8080 in your environment or docker-compose.yml")
		os.Exit(1)
	}

	srv := newServer(config{
		token:           token,
		epubDir:         epubDir,
		externalURL:     externalURL,
		debug:           debug,
		hardcoverAPIKey: hardcoverAPIKey,
		cacheDir:        cacheDir,
		kepubify:        kepubify,
		uiPassword:      uiPassword,
		ui:              webUI(),
	})

	slog.Info("kobo-sync starting", "addr", addr, "epub_dir", epubDir)
	if kepubify {
		slog.Info("kepub conversion enabled", "cache_dir", cacheDir, "hint", "set KOBO_KEPUBIFY=0 to serve raw EPUBs")
	} else {
		slog.Info("kepub conversion disabled", "hint", "EPUBs are served as-is")
	}
	if kepubify || hardcoverAPIKey != "" {
		if err := checkWritable(cacheDir); err != nil {
			slog.Warn("cache dir is not writable; kepubs will be converted on every download and Hardcover results won't persist",
				"cache_dir", cacheDir, "err", err,
				"hint", "in Docker, chown the mounted cache dir to 65532:65532 (the image's nonroot user)")
		}
	}
	if hardcoverAPIKey != "" {
		slog.Info("hardcover metadata enrichment enabled", "cache_dir", cacheDir)
	} else {
		slog.Info("hardcover metadata enrichment disabled", "hint", "set HARDCOVER_API_KEY to enable")
	}
	if srv.cfg.ui == nil {
		slog.Warn("web UI not built; run `npm ci && npm run build` in web/ before `go build`")
	} else if uiPassword == "" {
		slog.Info("web UI enabled", "url", externalURL+"/", "hint", "set KOBO_UI_PASSWORD to require a password")
	} else {
		slog.Info("web UI enabled (password protected)", "url", externalURL+"/")
	}
	slog.Info("set in .kobo/Kobo/Kobo eReader.conf under [OneStoreServices]",
		"api_endpoint", externalURL+"/kobo/"+token,
	)

	if err := http.ListenAndServe(addr, srv); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// isFalsy reports whether an env var value explicitly disables a feature.
// An unset (empty) value is not falsy, so features default to on.
func isFalsy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

// checkWritable reports whether files can be created in dir, creating it first
// if it doesn't exist.
func checkWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

func randomToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "changeme"
	}
	return hex.EncodeToString(b)
}
