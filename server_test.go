package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	return newServer(config{
		token:       testToken,
		epubDir:     t.TempDir(),
		externalURL: testExternalURL,
	})
}

func TestTokenAuth_correct(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/kobo/testtoken/v1/initialization", nil))
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
}

func TestTokenAuth_wrong(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/kobo/wrongtoken/v1/initialization", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestCatchAll_returnsEmptyJSON(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/kobo/testtoken/v1/anything", nil))

	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("want application/json, got %q", ct)
	}
	var got any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestCatchAll_withBody(t *testing.T) {
	srv := newTestServer(t)
	body := `{"CurrentBookmark":{"ContentId":"file.epub","Location":"epubcfi(/6/4!/4/2/6:0)"}}`
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("PUT", "/kobo/testtoken/v1/library/abc/state",
		strings.NewReader(body)))

	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	var got any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestCatchAll_allMethods(t *testing.T) {
	srv := newTestServer(t)
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(method, "/kobo/testtoken/v1/library/sync", nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s: want 200, got %d", method, w.Code)
		}
	}
}

func TestOutsidePrefix_404(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/", "/v1/initialization", "/kobo/"} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("path %q: want 404, got %d", path, w.Code)
		}
	}
}

func TestResponseRecorder_capturesCode(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &responseRecorder{ResponseWriter: inner, code: http.StatusOK}
	rec.WriteHeader(http.StatusCreated)
	if rec.code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.code)
	}
}

func TestResponseRecorder_defaultCode(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &responseRecorder{ResponseWriter: inner, code: http.StatusOK}
	// Write without calling WriteHeader — code should stay at default 200
	rec.Write([]byte("hello"))
	if rec.code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.code)
	}
}
