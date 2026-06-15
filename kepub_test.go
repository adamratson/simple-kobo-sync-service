package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// makeConvertibleEPUB writes a minimal but complete EPUB — manifest, spine, and
// an XHTML content document — so kepubify has real content to transform, unlike
// makeTestEPUB which omits the spine.
func makeConvertibleEPUB(t *testing.T, dir, filename, title, uuid string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	w := zip.NewWriter(f)

	writeZipEntry(t, w, "META-INF/container.xml",
		`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)

	opf := fmt.Sprintf(`<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="BookId">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title>%s</dc:title><dc:creator>An Author</dc:creator>
<dc:identifier id="BookId">urn:uuid:%s</dc:identifier><dc:language>en</dc:language></metadata>
<manifest><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="ch1"/></spine></package>`, title, uuid)
	writeZipEntry(t, w, "OEBPS/content.opf", opf)

	writeZipEntry(t, w, "OEBPS/ch1.xhtml",
		`<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter 1</title></head><body><p>It was a dark and stormy night.</p></body></html>`)

	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	f.Close()
	return path
}

// assertValidEPUBZip fails the test unless body is a readable EPUB-shaped zip.
func assertValidEPUBZip(t *testing.T, body []byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("body is not a valid zip: %v", err)
	}
	if _, err := zr.Open("META-INF/container.xml"); err != nil {
		t.Errorf("kepub missing META-INF/container.xml: %v", err)
	}
}

func TestKepubFilename(t *testing.T) {
	cases := map[string]string{
		"hobbit.epub":     "hobbit.kepub.epub",
		"The Hobbit.EPUB": "The Hobbit.kepub.epub",
		"book.kepub.epub": "book.kepub.epub", // already converted, unchanged
		"noext":           "noext.kepub.epub",
		"a.b.epub":        "a.b.kepub.epub",
	}
	for in, want := range cases {
		if got := kepubFilename(in); got != want {
			t.Errorf("kepubFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKepubStore_convertAndCache(t *testing.T) {
	cacheDir := t.TempDir()
	src := makeConvertibleEPUB(t, t.TempDir(), "book.epub", "A Book", "11112222-3333-4444-5555-666677778888")
	book := epubMeta{UUID: "11112222-3333-4444-5555-666677778888", FilePath: src, Title: "A Book"}

	k := newKepubStore(cacheDir)

	// No cache yet.
	if _, _, ok := k.cached(book); ok {
		t.Fatal("cached() reported a hit before any conversion")
	}

	out, err := k.convert(context.Background(), book)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if want := k.kepubPath(book.UUID); out != want {
		t.Errorf("convert path = %q, want %q", out, want)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read kepub: %v", err)
	}
	assertValidEPUBZip(t, data)

	// Now it's cached, and the size is reported.
	p, size, ok := k.cached(book)
	if !ok || p != out {
		t.Fatalf("cached() miss after convert: ok=%v p=%q", ok, p)
	}
	if size != int64(len(data)) {
		t.Errorf("cached size %d != file size %d", size, len(data))
	}

	// A second convert must reuse the file, not rewrite it.
	fi1, _ := os.Stat(out)
	out2, err := k.convert(context.Background(), book)
	if err != nil || out2 != out {
		t.Fatalf("second convert: out=%q err=%v", out2, err)
	}
	fi2, _ := os.Stat(out)
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Error("cached kepub was rewritten on second convert")
	}
}

func TestKepubStore_staleWhenSourceNewer(t *testing.T) {
	cacheDir := t.TempDir()
	src := makeConvertibleEPUB(t, t.TempDir(), "book.epub", "A Book", "aaaa1111-2222-3333-4444-555566667777")
	book := epubMeta{UUID: "aaaa1111-2222-3333-4444-555566667777", FilePath: src, Title: "A Book"}

	k := newKepubStore(cacheDir)
	if _, err := k.convert(context.Background(), book); err != nil {
		t.Fatalf("convert: %v", err)
	}
	if _, _, ok := k.cached(book); !ok {
		t.Fatal("expected cache hit after convert")
	}

	// Touch the source so it is newer than the cached kepub.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, _, ok := k.cached(book); ok {
		t.Error("cached() should report a miss when the source EPUB is newer")
	}
}

func TestHandleDownload_servesKepub(t *testing.T) {
	dir := t.TempDir()
	uuid := "dddd1111-2222-3333-4444-555566667777"
	makeConvertibleEPUB(t, dir, "novel.epub", "A Novel", uuid)

	srv := newServer(config{
		token:       testToken,
		epubDir:     dir,
		externalURL: testExternalURL,
		cacheDir:    t.TempDir(),
		kepubify:    true,
	})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/kobo/"+testToken+"/v1/library/"+uuid+"/download", nil))

	res := w.Result()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download: want 200, got %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/epub+zip" {
		t.Errorf("Content-Type = %q, want application/epub+zip", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, ".kepub.epub") {
		t.Errorf("Content-Disposition = %q, want a .kepub.epub filename", cd)
	}
	if cl := res.Header.Get("Content-Length"); cl != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q != body length %d", cl, len(body))
	}
	assertValidEPUBZip(t, body)

	// The kepub must be cached on disk under the cache dir, not served from epubDir.
	if _, err := os.Stat(filepath.Join(srv.kepub.kepubsDir(), uuid+".kepub.epub")); err != nil {
		t.Errorf("kepub not cached after download: %v", err)
	}
}

func TestHandleLibrarySync_advertisesKepubFormat(t *testing.T) {
	dir := t.TempDir()
	uuid := "eeee1111-2222-3333-4444-555566667777"
	makeConvertibleEPUB(t, dir, "novel.epub", "A Novel", uuid)

	srv := newServer(config{
		token:       testToken,
		epubDir:     dir,
		externalURL: testExternalURL,
		cacheDir:    t.TempDir(),
		kepubify:    true,
	})

	// Before any download there is no cached kepub, so the size falls back to the
	// EPUB's size, but the advertised format is already KEPUB.
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/kobo/"+testToken+"/v1/library/sync", nil))
	urls := decodeFirstDownloadURLs(t, w)
	if len(urls) != 1 || urls[0].Format != "KEPUB" {
		t.Fatalf("sync DownloadUrls = %+v, want a single KEPUB entry", urls)
	}

	// After a download caches the kepub, the advertised size matches the kepub.
	dw := httptest.NewRecorder()
	srv.ServeHTTP(dw, httptest.NewRequest("GET", "/kobo/"+testToken+"/v1/library/"+uuid+"/download", nil))
	kepubSize, _ := io.Copy(io.Discard, dw.Result().Body)

	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, httptest.NewRequest("GET", "/kobo/"+testToken+"/v1/library/sync", nil))
	urls2 := decodeFirstDownloadURLs(t, w2)
	if urls2[0].Size != kepubSize {
		t.Errorf("advertised KEPUB size = %d, want cached kepub size %d", urls2[0].Size, kepubSize)
	}
}

func TestHandleLibrarySync_advertisesEpubWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	makeConvertibleEPUB(t, dir, "novel.epub", "A Novel", "ffff1111-2222-3333-4444-555566667777")

	srv := newServer(config{token: testToken, epubDir: dir, externalURL: testExternalURL}) // kepubify off

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/kobo/"+testToken+"/v1/library/sync", nil))
	urls := decodeFirstDownloadURLs(t, w)
	if len(urls) != 2 || urls[0].Format != "EPUB3" || urls[1].Format != "EPUB" {
		t.Errorf("sync DownloadUrls = %+v, want EPUB3/EPUB when kepubify disabled", urls)
	}
}

// decodeFirstDownloadURLs pulls the DownloadUrls of the first synced book out of a
// recorded /library/sync response.
func decodeFirstDownloadURLs(t *testing.T, w *httptest.ResponseRecorder) []syncDownloadURL {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("sync: want 200, got %d", w.Code)
	}
	var items []syncItem
	if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
		t.Fatalf("decode sync: %v", err)
	}
	if len(items) == 0 || items[0].NewEntitlement == nil {
		t.Fatal("sync returned no entitlements")
	}
	return items[0].NewEntitlement.BookMetadata.DownloadUrls
}
