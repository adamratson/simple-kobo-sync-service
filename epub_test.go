package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTestEPUB writes a minimal valid EPUB into dir and returns its path.
func makeTestEPUB(t *testing.T, dir, filename, title, author, uuid string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	w := zip.NewWriter(f)

	container := `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`
	writeZipEntry(t, w, "META-INF/container.xml", container)

	opf := fmt.Sprintf(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>%s</dc:title><dc:creator>%s</dc:creator><dc:identifier id="BookId">urn:uuid:%s</dc:identifier><dc:language>en</dc:language></metadata></package>`, title, author, uuid)
	writeZipEntry(t, w, "OEBPS/content.opf", opf)

	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	f.Close()
	return path
}

func writeZipEntry(t *testing.T, w *zip.Writer, name, content string) {
	t.Helper()
	fw, err := w.Create(name)
	if err != nil {
		t.Fatalf("zip create %q: %v", name, err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("zip write %q: %v", name, err)
	}
}

func TestDeriveUUID_stable(t *testing.T) {
	a := deriveUUID("mybook.epub")
	b := deriveUUID("mybook.epub")
	if a != b {
		t.Errorf("same input produced different UUIDs: %q vs %q", a, b)
	}
}

func TestDeriveUUID_differentInputs(t *testing.T) {
	a := deriveUUID("book1.epub")
	b := deriveUUID("book2.epub")
	if a == b {
		t.Errorf("different inputs produced same UUID: %q", a)
	}
}

func TestDeriveUUID_validFormat(t *testing.T) {
	u := deriveUUID("test.epub")
	if !isValidUUID(u) {
		t.Errorf("deriveUUID produced invalid UUID: %q", u)
	}
}

func TestNormalizeID_stripsPrefix(t *testing.T) {
	id := normalizeID("urn:uuid:12345678-1234-5678-1234-567812345678", "book.epub")
	if id != "12345678-1234-5678-1234-567812345678" {
		t.Errorf("unexpected result: %q", id)
	}
}

func TestNormalizeID_invalidFallsBack(t *testing.T) {
	id := normalizeID("not-a-uuid", "book.epub")
	if !isValidUUID(id) {
		t.Errorf("fallback UUID is invalid: %q", id)
	}
	if id != deriveUUID("book.epub") {
		t.Errorf("fallback should equal deriveUUID(filename)")
	}
}

func TestIsValidUUID(t *testing.T) {
	valid := []string{
		"12345678-1234-5678-1234-567812345678",
		"00000000-0000-0000-0000-000000000000",
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	invalid := []string{
		"",
		"not-a-uuid",
		"12345678-1234-5678-1234-56781234567",  // too short
		"12345678-1234-5678-1234-5678123456789", // too long
		"12345678_1234_5678_1234_567812345678",  // underscores
	}
	for _, v := range valid {
		if !isValidUUID(v) {
			t.Errorf("expected valid: %q", v)
		}
	}
	for _, v := range invalid {
		if isValidUUID(v) {
			t.Errorf("expected invalid: %q", v)
		}
	}
}

func TestScanEpubs_emptyDir(t *testing.T) {
	dir := t.TempDir()
	books, err := scanEpubs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(books) != 0 {
		t.Errorf("want 0 books, got %d", len(books))
	}
}

func TestScanEpubs_ignoresNonEpub(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0644)
	books, _ := scanEpubs(dir)
	if len(books) != 0 {
		t.Errorf("want 0 books, got %d", len(books))
	}
}

func TestReadEpubMeta_extractsMetadata(t *testing.T) {
	dir := t.TempDir()
	const (
		wantTitle  = "My Test Book"
		wantAuthor = "Test Author"
		wantUUID   = "12345678-1234-5678-1234-567812345678"
	)
	makeTestEPUB(t, dir, "mybook.epub", wantTitle, wantAuthor, wantUUID)

	meta, err := readEpubMeta(filepath.Join(dir, "mybook.epub"))
	if err != nil {
		t.Fatalf("readEpubMeta: %v", err)
	}
	if meta.Title != wantTitle {
		t.Errorf("title: want %q, got %q", wantTitle, meta.Title)
	}
	if meta.Author != wantAuthor {
		t.Errorf("author: want %q, got %q", wantAuthor, meta.Author)
	}
	if meta.UUID != wantUUID {
		t.Errorf("uuid: want %q, got %q", wantUUID, meta.UUID)
	}
	if meta.Language != "en" {
		t.Errorf("language: want %q, got %q", "en", meta.Language)
	}
}

func TestScanEpubs_returnsBook(t *testing.T) {
	dir := t.TempDir()
	makeTestEPUB(t, dir, "test.epub", "A Book", "Someone", "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb")

	books, err := scanEpubs(dir)
	if err != nil {
		t.Fatalf("scanEpubs: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("want 1 book, got %d", len(books))
	}
	if books[0].Title != "A Book" {
		t.Errorf("title: want %q, got %q", "A Book", books[0].Title)
	}
}

func TestHandleLibrarySync_withBook(t *testing.T) {
	dir := t.TempDir()
	const bookUUID = "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	makeTestEPUB(t, dir, "test.epub", "Great Book", "Author", bookUUID)

	srv := newServer(config{token: testToken, epubDir: dir, externalURL: testExternalURL})
	req := httptest.NewRequest("GET", testBaseURL+"/v1/library/sync", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var items []syncItem
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	e := items[0].NewEntitlement
	if e == nil {
		t.Fatal("NewEntitlement is nil")
	}
	if e.BookEntitlement.Id != bookUUID {
		t.Errorf("Id: want %q, got %q", bookUUID, e.BookEntitlement.Id)
	}
	if e.BookMetadata.Title != "Great Book" {
		t.Errorf("Title: want %q, got %q", "Great Book", e.BookMetadata.Title)
	}
	if len(e.BookMetadata.DownloadUrls) != 2 {
		t.Fatalf("DownloadUrls: want 2 entries, got %d", len(e.BookMetadata.DownloadUrls))
	}
	wantURLPrefix := testBaseURL + "/v1/library/" + bookUUID
	wantFormats := []string{"EPUB3", "EPUB"}
	for i, du := range e.BookMetadata.DownloadUrls {
		if !strings.HasPrefix(du.Url, wantURLPrefix) {
			t.Errorf("DownloadUrls[%d].Url: want prefix %q, got %q", i, wantURLPrefix, du.Url)
		}
		if du.Format != wantFormats[i] {
			t.Errorf("DownloadUrls[%d].Format: want %q, got %q", i, wantFormats[i], du.Format)
		}
		if du.Platform != "Generic" {
			t.Errorf("DownloadUrls[%d].Platform: want Generic, got %q", i, du.Platform)
		}
	}
}

func TestHandleDownload_servesEpub(t *testing.T) {
	dir := t.TempDir()
	const bookUUID = "aaaabbbb-cccc-dddd-eeee-ffffaaaabbbb"
	makeTestEPUB(t, dir, "test.epub", "Great Book", "Author", bookUUID)

	srv := newServer(config{token: testToken, epubDir: dir, externalURL: testExternalURL})
	path := "/kobo/" + testToken + "/v1/library/" + bookUUID + "/download"
	req := httptest.NewRequest("GET", testExternalURL+path, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/epub+zip" {
		t.Errorf("Content-Type: want application/epub+zip, got %q", ct)
	}
	if cl := w.Header().Get("Content-Length"); cl == "" || cl == "0" {
		t.Errorf("Content-Length: want non-zero, got %q", cl)
	}
	cd := w.Header().Get("Content-Disposition")
	if cd == "" {
		t.Error("Content-Disposition header is missing")
	}
	if !strings.Contains(cd, "test.epub") {
		t.Errorf("Content-Disposition: want to contain %q, got %q", "test.epub", cd)
	}
	if w.Body.Len() == 0 {
		t.Error("body is empty")
	}
}

func TestHandleDownload_notFound(t *testing.T) {
	dir := t.TempDir()
	srv := newServer(config{token: testToken, epubDir: dir, externalURL: testExternalURL})
	req := httptest.NewRequest("GET", testBaseURL+"/v1/library/00000000-0000-0000-0000-000000000000/download", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}
