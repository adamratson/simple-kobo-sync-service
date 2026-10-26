package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func newAPITestServer(t *testing.T) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	return newServer(config{token: testToken, epubDir: dir, externalURL: testExternalURL}), dir
}

func serve(srv *server, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// uploadRequest builds a multipart POST /api/books carrying the given files.
func uploadRequest(t *testing.T, files map[string][]byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, data := range files {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/api/books", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func decodeBooks(t *testing.T, w *httptest.ResponseRecorder) []apiBook {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var books []apiBook
	if err := json.NewDecoder(w.Body).Decode(&books); err != nil {
		t.Fatal(err)
	}
	return books
}

func TestAPIBooks_listsLibrary(t *testing.T) {
	srv, dir := newAPITestServer(t)
	makeTestEPUB(t, dir, "hobbit.epub", "The Hobbit", "J.R.R. Tolkien", "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb")

	books := decodeBooks(t, serve(srv, httptest.NewRequest("GET", "/api/books", nil)))
	if len(books) != 1 {
		t.Fatalf("want 1 book, got %d", len(books))
	}
	b := books[0]
	if b.ID != "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb" || b.Title != "The Hobbit" || b.Author != "J.R.R. Tolkien" {
		t.Errorf("unexpected book: %+v", b)
	}
	if b.FileName != "hobbit.epub" || b.Size == 0 || b.AddedAt.IsZero() {
		t.Errorf("missing file info: %+v", b)
	}
	if b.Metadata != "disabled" {
		t.Errorf("Metadata: want disabled, got %q", b.Metadata)
	}
}

func TestAPIBooks_reflectsDeviceActivity(t *testing.T) {
	srv, dir := newAPITestServer(t)
	const id = "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	makeTestEPUB(t, dir, "hobbit.epub", "The Hobbit", "J.R.R. Tolkien", id)

	sync := httptest.NewRequest("GET", testBaseURL+"/v1/library/sync", nil)
	sync.Header.Set("User-Agent", "Kobo Touch 0387/4.45")
	serve(srv, sync)
	serve(srv, httptest.NewRequest("GET", testBaseURL+"/v1/library/"+id+"/download", nil))
	state := `{"ReadingStates":[{"CurrentBookmark":{"ProgressPercent":42},"StatusInfo":{"Status":"Reading"},"Statistics":{"SpentReadingMinutes":30}}]}`
	serve(srv, httptest.NewRequest("PUT", testBaseURL+"/v1/library/"+id+"/state", strings.NewReader(state)))

	books := decodeBooks(t, serve(srv, httptest.NewRequest("GET", "/api/books", nil)))
	b := books[0]
	if b.LastDownloaded == nil {
		t.Error("LastDownloaded not recorded")
	}
	if b.Progress == nil || b.Progress.Percent != 42 || b.Progress.Status != "Reading" || b.Progress.SpentReadingMinutes != 30 {
		t.Errorf("Progress not recorded: %+v", b.Progress)
	}

	var st apiStatus
	json.NewDecoder(serve(srv, httptest.NewRequest("GET", "/api/status", nil)).Body).Decode(&st)
	if st.LastSync == nil || st.LastSync.Books != 1 || st.LastSync.UserAgent != "Kobo Touch 0387/4.45" {
		t.Errorf("LastSync: %+v", st.LastSync)
	}
	if st.BookCount != 1 || st.APIEndpoint != testBaseURL || !st.EpubDirWritable {
		t.Errorf("status: %+v", st)
	}

	var events []activityEvent
	json.NewDecoder(serve(srv, httptest.NewRequest("GET", "/api/activity", nil)).Body).Decode(&events)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, ","); got != "status,download,sync" {
		t.Errorf("events: want status,download,sync, got %s", got)
	}
}

func TestActivity_persistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	a := newActivityStore(dir)
	a.recordSync("ua", 3)
	b := newActivityStore(dir)
	if ls := b.lastSync(); ls == nil || ls.Books != 3 {
		t.Errorf("lastSync not persisted: %+v", ls)
	}
}

func TestAPIUpload_savesValidEPUB(t *testing.T) {
	srv, dir := newAPITestServer(t)
	src := makeTestEPUB(t, t.TempDir(), "x.epub", "Dune", "Frank Herbert", "11112222-3333-4444-5555-666677778888")
	data, _ := os.ReadFile(src)

	w := serve(srv, uploadRequest(t, map[string][]byte{"dune.epub": data}))
	var res []uploadResult
	json.NewDecoder(w.Body).Decode(&res)
	if len(res) != 1 || res[0].Error != "" || res[0].Book == nil || res[0].Book.Title != "Dune" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "dune.epub")); err != nil {
		t.Errorf("file not written: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("staging dir left behind: %v", entries)
	}
}

func TestAPIUpload_rejectsBadFiles(t *testing.T) {
	srv, dir := newAPITestServer(t)
	makeTestEPUB(t, dir, "dune.epub", "Dune", "Frank Herbert", "11112222-3333-4444-5555-666677778888")
	dupe, _ := os.ReadFile(makeTestEPUB(t, t.TempDir(), "x.epub", "Dune", "Frank Herbert", "11112222-3333-4444-5555-666677778888"))
	renamed, _ := os.ReadFile(makeTestEPUB(t, t.TempDir(), "x.epub", "Dune", "Frank Herbert", "99990000-3333-4444-5555-666677778888"))

	cases := map[string]struct {
		data []byte
		want string
	}{
		"notes.txt":      {[]byte("hello"), "only .epub"},
		"broken.epub":    {[]byte("not a zip"), "not a valid EPUB"},
		"dune.epub":      {dupe, "already exists"},
		"dune-copy.epub": {dupe, "already in library"},
		// Same book, different identifier: caught by title+author.
		"dune-2.epub": {renamed, "already in library"},
	}
	for name, tc := range cases {
		w := serve(srv, uploadRequest(t, map[string][]byte{name: tc.data}))
		var res []uploadResult
		json.NewDecoder(w.Body).Decode(&res)
		if len(res) != 1 || !strings.Contains(res[0].Error, tc.want) {
			t.Errorf("%s: want error containing %q, got %+v", name, tc.want, res)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "dune-copy.epub")); err == nil {
		t.Error("duplicate was written")
	}
}

func TestAPIUpload_stripsPathFromFilename(t *testing.T) {
	srv, dir := newAPITestServer(t)
	data, _ := os.ReadFile(makeTestEPUB(t, t.TempDir(), "x.epub", "Dune", "", "11112222-3333-4444-5555-666677778888"))
	serve(srv, uploadRequest(t, map[string][]byte{"../../evil.epub": data}))
	if _, err := os.Stat(filepath.Join(dir, "evil.epub")); err != nil {
		t.Errorf("want file saved as evil.epub inside library: %v", err)
	}
}

func TestAPIDelete_removesBook(t *testing.T) {
	srv, dir := newAPITestServer(t)
	const id = "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	path := makeTestEPUB(t, dir, "hobbit.epub", "The Hobbit", "", id)

	if w := serve(srv, httptest.NewRequest("DELETE", "/api/books/"+id, nil)); w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still present")
	}
	if w := serve(srv, httptest.NewRequest("DELETE", "/api/books/"+id, nil)); w.Code != http.StatusNotFound {
		t.Errorf("second delete: want 404, got %d", w.Code)
	}
}

func TestAPIFile_downloadsOriginal(t *testing.T) {
	srv, dir := newAPITestServer(t)
	const id = "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	path := makeTestEPUB(t, dir, "hobbit.epub", "The Hobbit", "", id)
	want, _ := os.ReadFile(path)

	w := serve(srv, httptest.NewRequest("GET", "/api/books/"+id+"/file", nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), want) {
		t.Errorf("want original bytes, got %d (%d bytes)", w.Code, w.Body.Len())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "hobbit.epub") {
		t.Errorf("Content-Disposition: %q", cd)
	}
}

func TestUI_servedWhenBuilt(t *testing.T) {
	srv := newServer(config{
		token: testToken, epubDir: t.TempDir(), externalURL: testExternalURL,
		ui: fstest.MapFS{
			"index.html":    {Data: []byte("<html>ui</html>")},
			"assets/app.js": {Data: []byte("console.log(1)")},
		},
	})
	if w := serve(srv, httptest.NewRequest("GET", "/", nil)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ui") {
		t.Errorf("/: want index.html, got %d %q", w.Code, w.Body)
	}
	if w := serve(srv, httptest.NewRequest("GET", "/assets/app.js", nil)); w.Code != http.StatusOK {
		t.Errorf("asset: want 200, got %d", w.Code)
	}
	if w := serve(srv, httptest.NewRequest("GET", "/v1/initialization", nil)); w.Code != http.StatusNotFound {
		t.Errorf("unknown path: want 404, got %d", w.Code)
	}
}

func TestUIAuth_requiresPassword(t *testing.T) {
	srv := newServer(config{
		token: testToken, epubDir: t.TempDir(), externalURL: testExternalURL,
		uiPassword: "hunter2",
		ui:         fstest.MapFS{"index.html": {Data: []byte("ui")}},
	})
	for _, p := range []string{"/", "/api/books", "/api/status"} {
		if w := serve(srv, httptest.NewRequest("GET", p, nil)); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without auth: want 401, got %d", p, w.Code)
		}
		req := httptest.NewRequest("GET", p, nil)
		req.SetBasicAuth("any", "hunter2")
		if w := serve(srv, req); w.Code != http.StatusOK {
			t.Errorf("%s with auth: want 200, got %d", p, w.Code)
		}
	}
	// The Kobo API is guarded by its URL token, not the UI password.
	if w := serve(srv, httptest.NewRequest("GET", testBaseURL+"/v1/initialization", nil)); w.Code != http.StatusOK {
		t.Errorf("kobo API: want 200, got %d", w.Code)
	}
}
