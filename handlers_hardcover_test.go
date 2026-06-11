package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newEnrichedServer builds a server backed by a stub Hardcover store and a temp
// epub dir, returning the server and the book's UUID.
func newEnrichedServer(t *testing.T) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	const bookUUID = "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	makeTestEPUB(t, dir, "test.epub", "The Hobbit", "J.R.R. Tolkien", bookUUID)

	srv := newServer(config{token: testToken, epubDir: dir, externalURL: testExternalURL})
	store, _ := newEnrichTestStore(t, true)
	srv.store = store
	return srv, bookUUID
}

func TestLibrarySync_enrichesMetadata(t *testing.T) {
	srv, _ := newEnrichedServer(t)
	req := httptest.NewRequest("GET", testBaseURL+"/v1/library/sync", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var items []syncItem
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	m := items[0].NewEntitlement.BookMetadata
	if m.Description == nil || *m.Description != "desc" {
		t.Errorf("Description: want %q, got %v", "desc", m.Description)
	}
	if m.Series == nil || m.Series.Name != "Middle-earth" || m.Series.NumberFloat != 1 {
		t.Errorf("Series: got %+v", m.Series)
	}
	if m.Publisher.Name != "Allen & Unwin" {
		t.Errorf("Publisher: got %q", m.Publisher.Name)
	}
	if m.PublicationDate != "1937-09-21" {
		t.Errorf("PublicationDate: got %q", m.PublicationDate)
	}
}

func TestCoverImage_servesCachedJPEG(t *testing.T) {
	srv, bookUUID := newEnrichedServer(t)

	// Sync first so the cover is fetched and cached.
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", testBaseURL+"/v1/library/sync", nil))

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", testBaseURL+"/images/"+bookUUID+"/200/300/false/image.jpg", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: want image/jpeg, got %q", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("empty cover body")
	}
}

func TestCoverImage_unknownReturns404(t *testing.T) {
	srv, _ := newEnrichedServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", testBaseURL+"/images/00000000-0000-0000-0000-000000000000/200/300/false/image.jpg", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestCoverImage_disabledReturns404(t *testing.T) {
	// Server with no store (Hardcover disabled) must 404 cover requests.
	srv := newServer(config{token: testToken, epubDir: t.TempDir(), externalURL: testExternalURL})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", testBaseURL+"/images/aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb/200/300/false/image.jpg", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestLibrarySync_disabledOmitsEnrichment(t *testing.T) {
	// With no store, Description stays null and Series absent — unchanged behavior.
	dir := t.TempDir()
	makeTestEPUB(t, dir, "test.epub", "The Hobbit", "J.R.R. Tolkien", "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb")
	srv := newServer(config{token: testToken, epubDir: dir, externalURL: testExternalURL})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", testBaseURL+"/v1/library/sync", nil))
	var items []syncItem
	json.NewDecoder(w.Body).Decode(&items)
	m := items[0].NewEntitlement.BookMetadata
	if m.Description != nil {
		t.Errorf("Description: want nil, got %v", *m.Description)
	}
	if m.Series != nil {
		t.Errorf("Series: want nil, got %+v", m.Series)
	}
}
