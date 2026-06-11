package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const cacheFileName = "hardcover-cache.json"

// metadataStore enriches epub metadata with data fetched from Hardcover and
// caches the result on disk so each book is looked up at most once. It is safe
// for concurrent use.
type metadataStore struct {
	dir        string
	client     *hardcoverClient
	httpClient *http.Client

	mu    sync.Mutex
	cache map[string]cachedMeta
}

// cachedMeta is the persisted enrichment record for a single book, keyed by UUID.
// Found distinguishes a confirmed miss (cached negative) from an absent entry.
type cachedMeta struct {
	Found           bool      `json:"found"`
	FetchedAt       time.Time `json:"fetchedAt"`
	Description     string    `json:"description,omitempty"`
	Series          string    `json:"series,omitempty"`
	SeriesNumber    float64   `json:"seriesNumber,omitempty"`
	Publisher       string    `json:"publisher,omitempty"`
	PublicationDate string    `json:"publicationDate,omitempty"`
	Genres          []string  `json:"genres,omitempty"`
	CoverFile       string    `json:"coverFile,omitempty"`
}

func newMetadataStore(dir string, client *hardcoverClient) *metadataStore {
	m := &metadataStore{
		dir:        dir,
		client:     client,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cache:      map[string]cachedMeta{},
	}
	m.load()
	return m
}

// coversDir is where downloaded cover JPEGs live.
func (m *metadataStore) coversDir() string { return filepath.Join(m.dir, "covers") }

// coverPath returns the on-disk path for a book's cached cover.
func (m *metadataStore) coverPath(uuid string) string {
	return filepath.Join(m.coversDir(), uuid+".jpg")
}

// load reads the cache file into memory; a missing or corrupt file is ignored.
func (m *metadataStore) load() {
	data, err := os.ReadFile(filepath.Join(m.dir, cacheFileName))
	if err != nil {
		return
	}
	var c map[string]cachedMeta
	if err := json.Unmarshal(data, &c); err != nil {
		slog.Warn("hardcover cache: ignoring corrupt cache file", "err", err)
		return
	}
	m.mu.Lock()
	m.cache = c
	m.mu.Unlock()
}

// save atomically writes the in-memory cache to disk. Caller must hold m.mu.
func (m *metadataStore) save() {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		slog.Warn("hardcover cache: mkdir failed", "err", err)
		return
	}
	data, err := json.MarshalIndent(m.cache, "", "  ")
	if err != nil {
		slog.Warn("hardcover cache: marshal failed", "err", err)
		return
	}
	tmp := filepath.Join(m.dir, cacheFileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		slog.Warn("hardcover cache: write failed", "err", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(m.dir, cacheFileName)); err != nil {
		slog.Warn("hardcover cache: rename failed", "err", err)
	}
}

// enrich returns b with Hardcover metadata merged in. On a cache hit it performs
// no network I/O. On a miss it queries Hardcover, downloads the cover, persists
// the result (including negative results), and returns the enriched copy.
func (m *metadataStore) enrich(b epubMeta) epubMeta {
	m.mu.Lock()
	cm, ok := m.cache[b.UUID]
	m.mu.Unlock()
	if ok {
		return applyCached(b, cm, m.coverExists(b.UUID))
	}

	book, err := m.client.lookup(b.ISBN, b.Title, b.Author)
	if err != nil {
		slog.Warn("hardcover lookup failed", "title", b.Title, "err", err)
		return b // don't cache transient errors; retry on next sync
	}

	cm = cachedMeta{FetchedAt: time.Now().UTC()}
	if book != nil {
		cm.Found = true
		cm.Description = book.Description
		cm.Series = book.Series
		cm.SeriesNumber = book.SeriesNumber
		cm.Publisher = book.Publisher
		cm.PublicationDate = book.PublicationDate
		cm.Genres = book.Genres
		if book.CoverURL != "" && m.downloadCover(b.UUID, book.CoverURL) {
			cm.CoverFile = b.UUID + ".jpg"
		}
		slog.Info("hardcover enriched", "title", b.Title, "series", book.Series, "cover", cm.CoverFile != "")
	} else {
		slog.Info("hardcover no match", "title", b.Title, "author", b.Author)
	}

	m.mu.Lock()
	m.cache[b.UUID] = cm
	m.save()
	m.mu.Unlock()

	return applyCached(b, cm, cm.CoverFile != "")
}

// coverExists reports whether a cached cover file is present on disk.
func (m *metadataStore) coverExists(uuid string) bool {
	if _, err := os.Stat(m.coverPath(uuid)); err == nil {
		return true
	}
	return false
}

// downloadCover fetches url and writes it to the cover cache. Returns true on success.
func (m *metadataStore) downloadCover(uuid, url string) bool {
	res, err := m.httpClient.Get(url)
	if err != nil {
		slog.Warn("hardcover cover download failed", "url", url, "err", err)
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		slog.Warn("hardcover cover download failed", "url", url, "status", res.StatusCode)
		return false
	}
	if err := os.MkdirAll(m.coversDir(), 0o755); err != nil {
		slog.Warn("hardcover cover: mkdir failed", "err", err)
		return false
	}
	f, err := os.Create(m.coverPath(uuid))
	if err != nil {
		slog.Warn("hardcover cover: create failed", "err", err)
		return false
	}
	defer f.Close()
	if _, err := io.Copy(f, res.Body); err != nil {
		slog.Warn("hardcover cover: write failed", "err", err)
		return false
	}
	return true
}

// applyCached merges a cached record into b. hasCover reflects whether the cover
// file is actually available so the device is only told about covers we can serve.
func applyCached(b epubMeta, cm cachedMeta, hasCover bool) epubMeta {
	if !cm.Found {
		return b
	}
	if cm.Description != "" {
		b.Description = cm.Description
	}
	if cm.Series != "" {
		b.Series = cm.Series
		b.SeriesNumber = cm.SeriesNumber
	}
	if cm.Publisher != "" {
		b.Publisher = cm.Publisher
	}
	if cm.PublicationDate != "" {
		b.PublicationDate = cm.PublicationDate
	}
	if len(cm.Genres) > 0 {
		b.Genres = cm.Genres
	}
	b.HasCover = hasCover && cm.CoverFile != ""
	return b
}
