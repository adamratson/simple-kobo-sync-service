package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxUploadBytes caps a single upload request (which may carry several books).
const maxUploadBytes = 1 << 30

// apiRoutes registers the web UI and the JSON API it talks to. Both sit outside
// the Kobo token prefix and are guarded by optional basic auth (KOBO_UI_PASSWORD).
func (s *server) apiRoutes() {
	// Checked once rather than per status poll, which would churn files in the library.
	s.epubWritable = checkWritable(s.cfg.epubDir) == nil

	s.mux.HandleFunc("GET /api/status", s.uiAuth(s.handleAPIStatus))
	s.mux.HandleFunc("GET /api/books", s.uiAuth(s.handleAPIBooks))
	s.mux.HandleFunc("POST /api/books", s.logged(s.uiAuth(s.handleAPIUpload)))
	s.mux.HandleFunc("DELETE /api/books/{id}", s.logged(s.uiAuth(s.handleAPIDelete)))
	s.mux.HandleFunc("GET /api/books/{id}/cover", s.uiAuth(s.handleCoverImage))
	s.mux.HandleFunc("GET /api/books/{id}/file", s.uiAuth(s.handleAPIFile))
	s.mux.HandleFunc("GET /api/activity", s.uiAuth(s.handleAPIActivity))

	if s.cfg.ui != nil {
		s.mux.HandleFunc("GET /{$}", s.uiAuth(s.handleUI))
		s.mux.HandleFunc("GET /assets/", s.uiAuth(s.handleUI))
		s.mux.HandleFunc("GET /favicon.svg", s.uiAuth(s.handleUI))
	}
}

// uiAuth enforces HTTP basic auth when a UI password is configured. Any username
// is accepted; only the password is checked.
func (s *server) uiAuth(next http.HandlerFunc) http.HandlerFunc {
	if s.cfg.uiPassword == "" {
		return next
	}
	want := []byte(s.cfg.uiPassword)
	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="kobo-sync"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handleUI serves the embedded single-page app.
func (s *server) handleUI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
		// The shell must never be cached so a new build's hashed assets are picked up.
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, s.cfg.ui, name)
}

type apiStatus struct {
	APIEndpoint     string      `json:"apiEndpoint"`
	EpubDir         string      `json:"epubDir"`
	EpubDirWritable bool        `json:"epubDirWritable"`
	Kepubify        bool        `json:"kepubify"`
	Hardcover       bool        `json:"hardcover"`
	BookCount       int         `json:"bookCount"`
	TotalSize       int64       `json:"totalSize"`
	LastSync        *syncRecord `json:"lastSync"`
}

func (s *server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	books, err := scanEpubs(s.cfg.epubDir)
	if err != nil {
		slog.Warn("scan epubs failed", "err", err)
	}
	var total int64
	for _, b := range books {
		total += b.Size
	}
	writeJSON(w, http.StatusOK, apiStatus{
		APIEndpoint:     s.baseURL(),
		EpubDir:         s.cfg.epubDir,
		EpubDirWritable: s.epubWritable,
		Kepubify:        s.kepub != nil,
		Hardcover:       s.store != nil,
		BookCount:       len(books),
		TotalSize:       total,
		LastSync:        s.activity.lastSync(),
	})
}

type apiBook struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Author          string           `json:"author"`
	Language        string           `json:"language"`
	FileName        string           `json:"fileName"`
	Size            int64            `json:"size"`
	AddedAt         time.Time        `json:"addedAt"` // file mtime
	ISBN            string           `json:"isbn,omitempty"`
	Description     string           `json:"description,omitempty"`
	Series          string           `json:"series,omitempty"`
	SeriesNumber    float64          `json:"seriesNumber,omitempty"`
	Publisher       string           `json:"publisher,omitempty"`
	PublicationDate string           `json:"publicationDate,omitempty"`
	Genres          []string         `json:"genres,omitempty"`
	HasCover        bool             `json:"hasCover"`
	Metadata        string           `json:"metadata"` // disabled, pending, matched, unmatched
	KepubReady      bool             `json:"kepubReady"`
	LastDownloaded  *time.Time       `json:"lastDownloaded,omitempty"`
	Progress        *readingProgress `json:"progress,omitempty"`
}

func (s *server) toAPIBook(b epubMeta) apiBook {
	metaStatus := "disabled"
	if s.store != nil {
		var known, found bool
		b, known, found = s.store.cachedOnly(b)
		switch {
		case !known:
			metaStatus = "pending"
		case found:
			metaStatus = "matched"
		default:
			metaStatus = "unmatched"
		}
	}
	var added time.Time
	if fi, err := os.Stat(b.FilePath); err == nil {
		added = fi.ModTime().UTC()
	}
	kepubReady := false
	if s.kepub != nil {
		_, _, kepubReady = s.kepub.cached(b)
	}
	act := s.activity.book(b.UUID)
	return apiBook{
		ID:              b.UUID,
		Title:           b.Title,
		Author:          b.Author,
		Language:        b.Language,
		FileName:        filepath.Base(b.FilePath),
		Size:            b.Size,
		AddedAt:         added,
		ISBN:            b.ISBN,
		Description:     b.Description,
		Series:          b.Series,
		SeriesNumber:    b.SeriesNumber,
		Publisher:       b.Publisher,
		PublicationDate: b.PublicationDate,
		Genres:          b.Genres,
		HasCover:        b.HasCover,
		Metadata:        metaStatus,
		KepubReady:      kepubReady,
		LastDownloaded:  act.LastDownloaded,
		Progress:        act.Progress,
	}
}

func (s *server) handleAPIBooks(w http.ResponseWriter, r *http.Request) {
	books, err := scanEpubs(s.cfg.epubDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read library: "+err.Error())
		return
	}
	out := make([]apiBook, 0, len(books))
	for _, b := range books {
		out = append(out, s.toAPIBook(b))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	writeJSON(w, http.StatusOK, out)
}

type uploadResult struct {
	FileName string   `json:"fileName"`
	Book     *apiBook `json:"book,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// handleAPIUpload accepts a multipart form with one or more "files" parts and
// writes each valid EPUB into the library. Files are streamed to disk, validated,
// and checked for duplicates individually, so one bad file doesn't sink the batch.
func (s *server) handleAPIUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart/form-data upload")
		return
	}
	existing, err := scanEpubs(s.cfg.epubDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read library: "+err.Error())
		return
	}
	lib := libraryIndex{}
	for _, b := range existing {
		lib.add(b)
	}

	results := []uploadResult{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
				return
			}
			writeError(w, http.StatusBadRequest, "malformed upload: "+err.Error())
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			part.Close()
			continue
		}
		res := s.saveUpload(part, lib)
		part.Close()
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, results)
}

// libraryIndex finds books already in the library, keyed both by UUID and by
// title+author: EPUBs without a UUID identifier get a filename-derived ID, so a
// renamed copy of the same book would otherwise slip through.
type libraryIndex map[string]string

func (l libraryIndex) keys(b epubMeta) []string {
	return []string{"id:" + b.UUID, "ta:" + strings.ToLower(b.Title) + "\x00" + strings.ToLower(b.Author)}
}

func (l libraryIndex) add(b epubMeta) {
	for _, k := range l.keys(b) {
		l[k] = b.Title
	}
}

func (l libraryIndex) find(b epubMeta) (title string, ok bool) {
	for _, k := range l.keys(b) {
		if t, ok := l[k]; ok {
			return t, true
		}
	}
	return "", false
}

func (s *server) saveUpload(part *multipart.Part, lib libraryIndex) uploadResult {
	name := filepath.Base(filepath.Clean("/" + part.FileName()))
	res := uploadResult{FileName: name}
	if !strings.HasSuffix(strings.ToLower(name), ".epub") || strings.HasPrefix(name, ".") {
		res.Error = "only .epub files are accepted"
		return res
	}
	dest := filepath.Join(s.cfg.epubDir, name)
	if _, err := os.Stat(dest); err == nil {
		res.Error = "a file with this name already exists"
		return res
	}

	// Stage inside a hidden temp dir in the library so the final rename is atomic
	// and metadata is read under the real filename (UUIDs fall back to it).
	tmpDir, err := os.MkdirTemp(s.cfg.epubDir, ".upload-")
	if err != nil {
		res.Error = "library folder is not writable"
		slog.Warn("upload: mkdir temp failed", "err", err)
		return res
	}
	defer os.RemoveAll(tmpDir)
	staged := filepath.Join(tmpDir, name)
	f, err := os.Create(staged)
	if err != nil {
		res.Error = "library folder is not writable"
		return res
	}
	_, err = io.Copy(f, part)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		res.Error = "upload interrupted"
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			res.Error = "upload too large"
		}
		return res
	}
	meta, err := readEpubMeta(staged)
	if err != nil {
		res.Error = "not a valid EPUB: " + err.Error()
		return res
	}
	if title, dup := lib.find(meta); dup {
		res.Error = fmt.Sprintf("already in library as %q", title)
		return res
	}
	if err := os.Rename(staged, dest); err != nil {
		res.Error = "could not save file"
		slog.Warn("upload: rename failed", "err", err)
		return res
	}
	meta.FilePath = dest
	lib.add(meta)
	slog.Info("uploaded", "file", name, "title", meta.Title, "uuid", meta.UUID)
	s.activity.recordUpload(meta.UUID, meta.Title)

	// Warm the caches in the background so covers show up in the UI and the
	// device's first download doesn't wait on a conversion.
	if s.store != nil || s.kepub != nil {
		go func(b epubMeta) {
			if s.store != nil {
				s.store.enrich(b)
			}
			if s.kepub != nil {
				if _, err := s.kepub.convert(context.Background(), b); err != nil {
					slog.Warn("kepub pre-conversion failed", "title", b.Title, "err", err)
				}
			}
		}(meta)
	}

	book := s.toAPIBook(meta)
	res.Book = &book
	return res
}

// handleAPIDelete removes a book's EPUB (and its cached kepub) from the library.
// The device keeps any copy it already downloaded.
func (s *server) handleAPIDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(r.PathValue("id"))
	p, ok := findEpubByUUID(s.cfg.epubDir, id)
	if !ok {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	title := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	if meta, err := readEpubMeta(p); err == nil {
		title = meta.Title
	}
	if err := os.Remove(p); err != nil {
		slog.Warn("delete failed", "file", p, "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete file (is the library folder read-only?)")
		return
	}
	if s.kepub != nil {
		os.Remove(s.kepub.kepubPath(id))
	}
	slog.Info("deleted", "file", filepath.Base(p), "title", title)
	s.activity.recordDelete(id, title)
	w.WriteHeader(http.StatusNoContent)
}

// handleAPIFile downloads the original EPUB as stored in the library.
func (s *server) handleAPIFile(w http.ResponseWriter, r *http.Request) {
	p, ok := findEpubByUUID(s.cfg.epubDir, r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/epub+zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filepath.Base(p)))
	http.ServeFile(w, r, p)
}

func (s *server) handleAPIActivity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.activity.events())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// uiFS returns the built web UI from dist, or nil when it hasn't been built
// (only a placeholder is present), so the server runs without a UI.
func uiFS(dist fs.FS) fs.FS {
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return nil
	}
	return dist
}
