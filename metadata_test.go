package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newEnrichTestStore wires a metadataStore to a stub server that answers both the
// GraphQL lookup and the cover download. graphCalls counts GraphQL requests so
// tests can assert cache hits perform no network I/O.
func newEnrichTestStore(t *testing.T, found bool) (store *metadataStore, graphCalls *int) {
	t.Helper()
	var calls int
	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "cover.jpg") {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte("\xff\xd8\xff\xe0JPEGDATA"))
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if !found {
			w.Write([]byte(`{"data":{"search":{"results":{"hits":[]}}}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "search(") {
			// Title path: first resolve an id via search.
			w.Write([]byte(`{"data":{"search":{"results":{"hits":[{"document":{"id":"42","title":"The Hobbit","author_names":["J.R.R. Tolkien"]}}]}}}}`))
			return
		}
		book := `{"title":"The Hobbit","description":"desc","release_date":"1937-09-21",` +
			`"image":{"url":"` + serverURL + `/cover.jpg"},` +
			`"contributions":[{"author":{"name":"J.R.R. Tolkien"}}],` +
			`"book_series":[{"position":1,"series":{"name":"Middle-earth"}}],` +
			`"editions":[{"publisher":{"name":"Allen & Unwin"}}]}`
		w.Write([]byte(`{"data":{"books":[` + book + `]}}`))
	}))
	t.Cleanup(ts.Close)
	serverURL = ts.URL

	client := newHardcoverClient("testkey")
	client.endpoint = ts.URL
	return newMetadataStore(t.TempDir(), client), &calls
}

func sampleBook() epubMeta {
	return epubMeta{UUID: "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb", Title: "The Hobbit", Author: "J.R.R. Tolkien"}
}

func TestEnrich_fetchesAndCaches(t *testing.T) {
	store, calls := newEnrichTestStore(t, true)
	b := sampleBook()

	got := store.enrich(b)
	if got.Description != "desc" || got.Series != "Middle-earth" || got.Publisher != "Allen & Unwin" {
		t.Errorf("enriched fields wrong: %+v", got)
	}
	if !got.HasCover {
		t.Error("HasCover: want true")
	}

	// Cache file and cover should exist on disk.
	if _, err := os.Stat(filepath.Join(store.dir, cacheFileName)); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
	if _, err := os.Stat(store.coverPath(b.UUID)); err != nil {
		t.Errorf("cover not written: %v", err)
	}

	// Second enrich must be a pure cache hit — no additional GraphQL call.
	before := *calls
	store.enrich(b)
	if *calls != before {
		t.Errorf("second enrich hit the network: calls went %d -> %d", before, *calls)
	}
}

func TestEnrich_cachesNegativeResult(t *testing.T) {
	store, calls := newEnrichTestStore(t, false)
	b := sampleBook()

	got := store.enrich(b)
	if got.Description != "" || got.HasCover {
		t.Errorf("expected no enrichment on miss, got %+v", got)
	}
	if *calls != 1 {
		t.Fatalf("first enrich: want 1 call, got %d", *calls)
	}
	store.enrich(b)
	if *calls != 1 {
		t.Errorf("negative result not cached: calls=%d", *calls)
	}
}

func TestEnrich_cacheSurvivesReload(t *testing.T) {
	store, _ := newEnrichTestStore(t, true)
	b := sampleBook()
	store.enrich(b)

	// A fresh store over the same dir should load the cache and not re-query.
	reloaded := newMetadataStore(store.dir, store.client)
	if _, ok := reloaded.cache[b.UUID]; !ok {
		t.Fatal("reloaded store did not load cached entry")
	}
	got := reloaded.enrich(b)
	if got.Series != "Middle-earth" {
		t.Errorf("reloaded enrich lost data: %+v", got)
	}
}

// --- load: corrupt cache file ---

func TestLoad_corruptCacheFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, cacheFileName), []byte("not valid json {{{"), 0o644)
	store := newMetadataStore(dir, newHardcoverClient("key"))
	if len(store.cache) != 0 {
		t.Errorf("want empty cache after corrupt file, got %d entries", len(store.cache))
	}
}

// --- save: write/mkdir failure branches ---

func TestSave_mkdirFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Skip("cannot chmod:", err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })

	// dir doesn't exist yet and parent is read-only → MkdirAll fails.
	store := &metadataStore{
		dir:    filepath.Join(parent, "cache"),
		client: newHardcoverClient("key"),
		cache:  map[string]cachedMeta{"k": {Found: true}},
	}
	store.mu.Lock()
	store.save() // must not panic; logs the error
	store.mu.Unlock()
}

func TestSave_writeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Skip("cannot chmod:", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	// dir exists (MkdirAll succeeds) but is read-only → WriteFile fails.
	store := &metadataStore{
		dir:    dir,
		client: newHardcoverClient("key"),
		cache:  map[string]cachedMeta{"k": {Found: true}},
	}
	store.mu.Lock()
	store.save() // must not panic; logs the error
	store.mu.Unlock()
}

// --- downloadCover error branches ---

// errTransport is an http.RoundTripper that always returns an error.
type errTransport struct{ msg string }

func (e *errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("%s", e.msg)
}

func TestDownloadCover_httpError(t *testing.T) {
	store := &metadataStore{
		dir:        t.TempDir(),
		httpClient: &http.Client{Transport: &errTransport{msg: "connection refused"}},
		cache:      map[string]cachedMeta{},
	}
	if store.downloadCover("uuid1", "http://invalid.local/cover.jpg") {
		t.Error("want false for HTTP Get error, got true")
	}
}

func TestDownloadCover_non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)
	store := &metadataStore{
		dir:        t.TempDir(),
		httpClient: ts.Client(),
		cache:      map[string]cachedMeta{},
	}
	if store.downloadCover("uuid2", ts.URL+"/cover.jpg") {
		t.Error("want false for non-200 response, got true")
	}
}

func TestDownloadCover_createFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("\xff\xd8\xff")) // minimal JPEG header
	}))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	coversDir := filepath.Join(dir, "covers")
	os.MkdirAll(coversDir, 0o555) // read-only; MkdirAll will succeed but Create will fail
	t.Cleanup(func() { os.Chmod(coversDir, 0o755) })

	store := &metadataStore{
		dir:        dir,
		httpClient: ts.Client(),
		cache:      map[string]cachedMeta{},
	}
	if store.downloadCover("uuid3", ts.URL+"/cover.jpg") {
		t.Error("want false for file create failure, got true")
	}
}
