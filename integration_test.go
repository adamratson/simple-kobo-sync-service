package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// startIntegrationServer runs the server on a real TCP socket with externalURL
// pointing back at the listener, so every URL it advertises is dialable. The
// chicken-and-egg (externalURL must be known before newServer, but the listener
// URL is only known after starting it) is solved by handler indirection.
func startIntegrationServer(t *testing.T, epubDir string) (*httptest.Server, *server) {
	t.Helper()
	var srv *server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	srv = newServer(config{token: testToken, epubDir: epubDir, externalURL: ts.URL})
	return ts, srv
}

// getJSON fetches url over the wire and decodes the body into out, asserting status 200.
func getJSON(t *testing.T, c *http.Client, url string, out any) *http.Response {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d", url, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatalf("GET %s: decode: %v", url, err)
	}
	return res
}

func TestIntegration_fullDeviceSyncFlow(t *testing.T) {
	dir := t.TempDir()
	makeTestEPUB(t, dir, "hobbit.epub", "The Hobbit", "J.R.R. Tolkien", "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb")
	makeTestEPUB(t, dir, "fellowship.epub", "The Fellowship of the Ring", "J.R.R. Tolkien", "bbbbcccc-dddd-eeee-ffff-aaaabbbbcccc")

	ts, srv := startIntegrationServer(t, dir)
	store, _ := newEnrichTestStore(t, true)
	srv.store = store
	c := ts.Client()

	// Step 1: initialization — the only URL a device is configured with.
	var init struct {
		Resources map[string]string `json:"Resources"`
	}
	initURL := ts.URL + "/kobo/" + testToken + "/v1/initialization"
	res := getJSON(t, c, initURL, &init)
	if res.Header.Get("x-kobo-apitoken") == "" {
		t.Error("initialization: x-kobo-apitoken header missing")
	}
	for _, key := range []string{"library_sync", "library_metadata", "reading_state", "device_auth", "image_url_template"} {
		v := init.Resources[key]
		if v == "" {
			t.Fatalf("initialization: resources[%q] missing", key)
		}
		if !strings.HasPrefix(v, ts.URL) {
			t.Fatalf("initialization: resources[%q] = %q does not point at this server %q", key, v, ts.URL)
		}
	}

	// Step 2: device auth, at the URL the server advertised.
	authRes, err := c.Post(init.Resources["device_auth"], "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST device_auth: %v", err)
	}
	var auth authDeviceResponse
	if err := json.NewDecoder(authRes.Body).Decode(&auth); err != nil {
		t.Fatalf("device_auth decode: %v", err)
	}
	authRes.Body.Close()
	if authRes.StatusCode != http.StatusOK || auth.AccessToken == "" || auth.UserKey == "" {
		t.Fatalf("device_auth: status %d, tokens %+v", authRes.StatusCode, auth)
	}

	// Step 3: library sync with a sync token, following the advertised URL.
	syncReq, _ := http.NewRequest("GET", init.Resources["library_sync"], nil)
	syncReq.Header.Set("x-kobo-synctoken", "device-token-123")
	syncRes, err := c.Do(syncReq)
	if err != nil {
		t.Fatalf("GET library_sync: %v", err)
	}
	defer syncRes.Body.Close()
	if syncRes.StatusCode != http.StatusOK {
		t.Fatalf("library_sync: want 200, got %d", syncRes.StatusCode)
	}
	if got := syncRes.Header.Get("x-kobo-synctoken"); got != "device-token-123" {
		t.Errorf("library_sync: synctoken not echoed, got %q", got)
	}
	var items []syncItem
	if err := json.NewDecoder(syncRes.Body).Decode(&items); err != nil {
		t.Fatalf("library_sync decode: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("library_sync: want 2 items, got %d", len(items))
	}
	first := items[0].NewEntitlement.BookMetadata
	if first.Description == nil || *first.Description != "desc" {
		t.Errorf("library_sync: enrichment missing, Description = %v", first.Description)
	}
	if first.Series == nil || first.Series.Name != "Middle-earth" {
		t.Errorf("library_sync: enrichment missing, Series = %+v", first.Series)
	}
	if len(first.DownloadUrls) == 0 {
		t.Fatal("library_sync: no DownloadUrls")
	}

	// Enrichment side effects must be on disk after the sync.
	if _, err := os.Stat(filepath.Join(store.dir, cacheFileName)); err != nil {
		t.Errorf("cache file not persisted after sync: %v", err)
	}
	if _, err := os.Stat(store.coverPath(first.CoverImageId)); err != nil {
		t.Errorf("cover not cached after sync: %v", err)
	}

	// Step 4: download the book at the advertised URL, verbatim.
	dlRes, err := c.Get(first.DownloadUrls[0].Url)
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	body, err := io.ReadAll(dlRes.Body)
	dlRes.Body.Close()
	if err != nil {
		t.Fatalf("download read: %v", err)
	}
	if dlRes.StatusCode != http.StatusOK {
		t.Fatalf("download: want 200, got %d", dlRes.StatusCode)
	}
	if ct := dlRes.Header.Get("Content-Type"); ct != "application/epub+zip" {
		t.Errorf("download Content-Type: want application/epub+zip, got %q", ct)
	}
	if cl := dlRes.Header.Get("Content-Length"); cl != strconv.Itoa(len(body)) {
		t.Errorf("download Content-Length %q does not match body length %d", cl, len(body))
	}
	if _, err := zip.NewReader(bytes.NewReader(body), int64(len(body))); err != nil {
		t.Errorf("downloaded body is not a valid EPUB zip: %v", err)
	}

	// Step 5: per-book metadata via the advertised template.
	metaURL := strings.Replace(init.Resources["library_metadata"], "{Ids}", first.EntitlementId, 1)
	var meta []bookMetadata
	getJSON(t, c, metaURL, &meta)
	if len(meta) != 1 || meta[0].EntitlementId != first.EntitlementId {
		t.Errorf("library_metadata: want 1 item for %s, got %+v", first.EntitlementId, meta)
	}

	// Step 6: cover image via the advertised ImageURLTemplate.
	coverURL := strings.NewReplacer(
		"{ImageId}", first.CoverImageId,
		"{Width}", "200",
		"{Height}", "300",
	).Replace(init.Resources["image_url_template"])
	coverRes, err := c.Get(coverURL)
	if err != nil {
		t.Fatalf("GET cover: %v", err)
	}
	coverBody, _ := io.ReadAll(coverRes.Body)
	coverRes.Body.Close()
	if coverRes.StatusCode != http.StatusOK {
		t.Fatalf("cover %s: want 200, got %d", coverURL, coverRes.StatusCode)
	}
	if ct := coverRes.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("cover Content-Type: want image/jpeg, got %q", ct)
	}
	if len(coverBody) == 0 {
		t.Error("cover body is empty")
	}

	// Step 7: upload reading state at the advertised URL.
	stateURL := strings.Replace(init.Resources["reading_state"], "{Ids}", first.EntitlementId, 1)
	stateBody := `{"ReadingStates":[{"EntitlementId":"` + first.EntitlementId + `","StatusInfo":{"Status":"Reading"}}]}`
	stateReq, _ := http.NewRequest("PUT", stateURL, strings.NewReader(stateBody))
	stateReq.Header.Set("Content-Type", "application/json")
	stateRes, err := c.Do(stateReq)
	if err != nil {
		t.Fatalf("PUT reading_state: %v", err)
	}
	var state map[string]any
	if err := json.NewDecoder(stateRes.Body).Decode(&state); err != nil {
		t.Fatalf("reading_state decode: %v", err)
	}
	stateRes.Body.Close()
	if state["EntitlementId"] != first.EntitlementId {
		t.Errorf("reading_state: EntitlementId not echoed, got %v", state["EntitlementId"])
	}
	if s, _ := state["ReadingStateModified"].(string); s == "" {
		t.Error("reading_state: ReadingStateModified missing")
	}

	// Step 8: the same advertised URL with the wrong token must be rejected.
	badURL := strings.Replace(init.Resources["library_sync"], "/kobo/"+testToken+"/", "/kobo/wrongtoken/", 1)
	badRes, err := c.Get(badURL)
	if err != nil {
		t.Fatalf("GET wrong-token sync: %v", err)
	}
	badRes.Body.Close()
	if badRes.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", badRes.StatusCode)
	}
}

func TestIntegration_syncWithoutEnrichment(t *testing.T) {
	dir := t.TempDir()
	makeTestEPUB(t, dir, "plain.epub", "Plain Book", "An Author", "cccc1111-2222-3333-4444-555566667777")

	ts, _ := startIntegrationServer(t, dir) // no Hardcover store
	c := ts.Client()

	var init struct {
		Resources map[string]string `json:"Resources"`
	}
	getJSON(t, c, ts.URL+"/kobo/"+testToken+"/v1/initialization", &init)

	var items []syncItem
	getJSON(t, c, init.Resources["library_sync"], &items)
	if len(items) != 1 {
		t.Fatalf("sync: want 1 item, got %d", len(items))
	}
	m := items[0].NewEntitlement.BookMetadata
	if m.Description != nil {
		t.Errorf("Description: want nil without enrichment, got %q", *m.Description)
	}
	if m.Series != nil {
		t.Errorf("Series: want nil without enrichment, got %+v", m.Series)
	}

	// Download still works end-to-end.
	dlRes, err := c.Get(m.DownloadUrls[0].Url)
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	dlRes.Body.Close()
	if dlRes.StatusCode != http.StatusOK {
		t.Errorf("download: want 200, got %d", dlRes.StatusCode)
	}

	// Cover URL from the template must 404 cleanly when enrichment is disabled.
	coverURL := strings.NewReplacer(
		"{ImageId}", m.CoverImageId,
		"{Width}", "200",
		"{Height}", "300",
	).Replace(init.Resources["image_url_template"])
	coverRes, err := c.Get(coverURL)
	if err != nil {
		t.Fatalf("GET cover: %v", err)
	}
	coverRes.Body.Close()
	if coverRes.StatusCode != http.StatusNotFound {
		t.Errorf("cover without enrichment: want 404, got %d", coverRes.StatusCode)
	}
}
