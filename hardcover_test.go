package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubHardcover returns a test server whose handler is fed the GraphQL query
// string, and the client pointed at it. The handler returns the raw response body.
func stubHardcover(t *testing.T, handler func(query string) string) (*hardcoverClient, *int) {
	t.Helper()
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			t.Errorf("Authorization header: want %q, got %q", "Bearer testkey", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query string `json:"query"`
		}
		json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, handler(req.Query))
	}))
	t.Cleanup(ts.Close)
	c := newHardcoverClient("testkey")
	c.endpoint = ts.URL
	return c, &calls
}

const bookJSON = `{"title":"The Hobbit","description":"A hobbit goes on an adventure.","release_date":"1937-09-21","cached_tags":{"Genre":[{"tag":"Fantasy"},{"tag":"Adventure"}]},"image":{"url":"http://covers.test/hobbit.jpg"},"contributions":[{"author":{"name":"J.R.R. Tolkien"}}],"book_series":[{"position":1,"series":{"name":"Middle-earth"}}],"editions":[{"publisher":{"name":"Allen & Unwin"}}]}`

// searchHitsJSON builds a `search` response whose single hit resolves to the given id.
func searchHitsJSON(id, title, author string) string {
	return `{"data":{"search":{"results":{"hits":[{"document":{"id":"` + id +
		`","title":"` + title + `","author_names":["` + author + `"]}}]}}}}`
}

// respondTitlePath answers the two-step title lookup: a search query, then books-by-id.
func respondTitlePath(query string) string {
	if strings.Contains(query, "search(") {
		return searchHitsJSON("42", "The Hobbit", "J.R.R. Tolkien")
	}
	return `{"data":{"books":[` + bookJSON + `]}}`
}

func TestLookupByTitle(t *testing.T) {
	c, calls := stubHardcover(t, respondTitlePath)
	b, err := c.lookup("", "The Hobbit", "J.R.R. Tolkien")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b == nil {
		t.Fatal("want a match, got nil")
	}
	if b.Series != "Middle-earth" || b.SeriesNumber != 1 {
		t.Errorf("series: got %q #%v", b.Series, b.SeriesNumber)
	}
	if b.Publisher != "Allen & Unwin" {
		t.Errorf("publisher: got %q", b.Publisher)
	}
	if b.CoverURL != "http://covers.test/hobbit.jpg" {
		t.Errorf("cover: got %q", b.CoverURL)
	}
	if len(b.Genres) != 2 || b.Genres[0] != "Fantasy" {
		t.Errorf("genres: got %v", b.Genres)
	}
	if *calls != 2 {
		t.Errorf("calls: want 2 (search + books), got %d", *calls)
	}
}

func TestLookupByISBN_twoStep(t *testing.T) {
	c, calls := stubHardcover(t, func(query string) string {
		if strings.Contains(query, "editions(where:") {
			return `{"data":{"editions":[{"book_id":42}]}}`
		}
		// second query: books by id
		if !strings.Contains(query, "$id") {
			t.Errorf("expected books-by-id query, got: %s", query)
		}
		return `{"data":{"books":[` + bookJSON + `]}}`
	})
	b, err := c.lookup("9780261103283", "ignored", "")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b == nil || b.Title != "The Hobbit" {
		t.Fatalf("want Hobbit, got %+v", b)
	}
	if *calls != 2 {
		t.Errorf("calls: want 2 (editions + books), got %d", *calls)
	}
}

func TestLookupByISBN_prefersEditionCover(t *testing.T) {
	// The edition carries its own cover; it must override the book's default cover
	// (bookJSON's image is http://covers.test/hobbit.jpg).
	c, _ := stubHardcover(t, func(query string) string {
		if strings.Contains(query, "editions(where:") {
			return `{"data":{"editions":[{"book_id":42,"image":{"url":"http://covers.test/edition.jpg"}}]}}`
		}
		return `{"data":{"books":[` + bookJSON + `]}}`
	})
	b, err := c.lookup("9780261103283", "", "")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b == nil {
		t.Fatal("want a match, got nil")
	}
	if b.CoverURL != "http://covers.test/edition.jpg" {
		t.Errorf("cover: want edition cover, got %q", b.CoverURL)
	}
}

func TestLookupByISBN_prefersEditionPublisher(t *testing.T) {
	// The edition carries its own publisher; it must override the book's default
	// publisher (bookJSON's edition publisher is Allen & Unwin).
	c, _ := stubHardcover(t, func(query string) string {
		if strings.Contains(query, "editions(where:") {
			return `{"data":{"editions":[{"book_id":42,"publisher":{"name":"Houghton Mifflin"}}]}}`
		}
		return `{"data":{"books":[` + bookJSON + `]}}`
	})
	b, err := c.lookup("9780261103283", "", "")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b == nil {
		t.Fatal("want a match, got nil")
	}
	if b.Publisher != "Houghton Mifflin" {
		t.Errorf("publisher: want edition publisher, got %q", b.Publisher)
	}
}

func TestLookupByISBN_fallsBackToBookCoverWhenEditionHasNone(t *testing.T) {
	// Edition has no image of its own → keep the book's default cover.
	c, _ := stubHardcover(t, func(query string) string {
		if strings.Contains(query, "editions(where:") {
			return `{"data":{"editions":[{"book_id":42}]}}`
		}
		return `{"data":{"books":[` + bookJSON + `]}}`
	})
	b, err := c.lookup("9780261103283", "", "")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b == nil {
		t.Fatal("want a match, got nil")
	}
	if b.CoverURL != "http://covers.test/hobbit.jpg" {
		t.Errorf("cover: want book default cover, got %q", b.CoverURL)
	}
}

func TestLookupByISBN_missFallsBackToTitle(t *testing.T) {
	c, _ := stubHardcover(t, func(query string) string {
		if strings.Contains(query, "editions(where:") {
			return `{"data":{"editions":[]}}` // no ISBN match
		}
		return respondTitlePath(query)
	})
	b, err := c.lookup("9780261103283", "The Hobbit", "")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b == nil || b.Title != "The Hobbit" {
		t.Fatalf("want title fallback match, got %+v", b)
	}
}

func TestLookup_noMatch(t *testing.T) {
	c, _ := stubHardcover(t, func(query string) string {
		// search returns no hits
		return `{"data":{"search":{"results":{"hits":[]}}}}`
	})
	b, err := c.lookup("", "Nonexistent Book", "Nobody")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b != nil {
		t.Errorf("want nil for no match, got %+v", b)
	}
}

func TestLookup_graphqlError(t *testing.T) {
	c, _ := stubHardcover(t, func(query string) string {
		return `{"errors":[{"message":"field 'foo' not found"}]}`
	})
	_, err := c.lookup("", "Whatever", "")
	if err == nil {
		t.Fatal("want error from graphql errors, got nil")
	}
	if !strings.Contains(err.Error(), "foo") {
		t.Errorf("error should surface graphql message, got: %v", err)
	}
}

func TestPickBestHit_prefersAuthor(t *testing.T) {
	var hits []searchHit
	const j = `[
		{"document":{"id":"1","title":"Dune","author_names":["Someone Else"]}},
		{"document":{"id":"2","title":"Dune","author_names":["Frank Herbert"]}}
	]`
	if err := json.Unmarshal([]byte(j), &hits); err != nil {
		t.Fatal(err)
	}
	got := pickBestHit(hits, "Dune", "Frank Herbert")
	if got == nil || got.Document.ID != "2" {
		t.Errorf("want id 2 (Frank Herbert), got %+v", got)
	}
}

func TestPickBestHit_fallsBackToFirst(t *testing.T) {
	var hits []searchHit
	const j = `[
		{"document":{"id":"9","title":"Some Other Edition","author_names":["X"]}},
		{"document":{"id":"7","title":"Also Different","author_names":["Y"]}}
	]`
	if err := json.Unmarshal([]byte(j), &hits); err != nil {
		t.Fatal(err)
	}
	// No title matches exactly -> most relevant (first) hit wins.
	got := pickBestHit(hits, "Nonmatching Title", "Z")
	if got == nil || got.Document.ID != "9" {
		t.Errorf("want first hit id 9, got %+v", got)
	}
}

// --- hardcoverClient.do error paths ---

func TestDo_non200Response(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "server fault")
	}))
	t.Cleanup(ts.Close)
	c := newHardcoverClient("testkey")
	c.endpoint = ts.URL
	_, err := c.lookup("", "anything", "")
	if err == nil {
		t.Fatal("want error for non-200, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention HTTP 500, got: %v", err)
	}
}

func TestDo_badJSONEnvelope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "this is not json")
	}))
	t.Cleanup(ts.Close)
	c := newHardcoverClient("testkey")
	c.endpoint = ts.URL
	_, err := c.lookup("", "anything", "")
	if err == nil {
		t.Fatal("want error for malformed JSON envelope, got nil")
	}
}

func TestDo_emptyData(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{}`) // no "data" key → empty RawMessage
	}))
	t.Cleanup(ts.Close)
	c := newHardcoverClient("testkey")
	c.endpoint = ts.URL
	_, err := c.lookup("", "anything", "")
	if err == nil {
		t.Fatal("want error for empty response data, got nil")
	}
}

// --- lookup missing branches ---

func TestLookup_ISBNErrorPropagates(t *testing.T) {
	// lookupByISBN fails → lookup should return the error, not fall through to title search.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "unauthorized")
	}))
	t.Cleanup(ts.Close)
	c := newHardcoverClient("testkey")
	c.endpoint = ts.URL
	_, err := c.lookup("9780000000000", "Some Title", "")
	if err == nil {
		t.Fatal("want error propagated from ISBN lookup, got nil")
	}
}

func TestLookup_ISBNMissEmptyTitle(t *testing.T) {
	// ISBN present but no match, title empty → (nil, nil) without a title search.
	c, _ := stubHardcover(t, func(query string) string {
		return `{"data":{"editions":[]}}`
	})
	b, err := c.lookup("9780000000000", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != nil {
		t.Errorf("want nil for empty title after ISBN miss, got %+v", b)
	}
}

// --- lookupByID error path ---

func TestLookupByID_doError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(ts.Close)
	c := newHardcoverClient("testkey")
	c.endpoint = ts.URL
	b, err := c.lookupByID(42)
	if err == nil {
		t.Fatal("want error from do(), got nil")
	}
	if b != nil {
		t.Errorf("want nil book, got %+v", b)
	}
}

// --- searchBookID: non-integer hit ID ---

func TestSearchBookID_nonIntegerID(t *testing.T) {
	c, _ := stubHardcover(t, func(query string) string {
		return `{"data":{"search":{"results":{"hits":[{"document":{"id":"not-an-int","title":"The Hobbit","author_names":["Tolkien"]}}]}}}}`
	})
	b, err := c.lookup("", "The Hobbit", "Tolkien")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != nil {
		t.Errorf("want nil for non-integer search ID, got %+v", b)
	}
}

// --- pickBestHit: titleOnly fallback ---

func TestPickBestHit_titleOnlyFallback(t *testing.T) {
	var hits []searchHit
	const j = `[
		{"document":{"id":"5","title":"Dune","author_names":["Someone Else"]}},
		{"document":{"id":"6","title":"Something Different","author_names":["Frank Herbert"]}}
	]`
	if err := json.Unmarshal([]byte(j), &hits); err != nil {
		t.Fatal(err)
	}
	// Hit 5 matches title "Dune" but not author "Frank Herbert".
	// Hit 6 has the right author but different title.
	// titleOnly fallback should return hit 5.
	got := pickBestHit(hits, "Dune", "Frank Herbert")
	if got == nil || got.Document.ID != "5" {
		t.Errorf("want id 5 (titleOnly fallback), got %+v", got)
	}
}

// --- parseGenres edge cases ---

func TestParseGenres_unmarshalError(t *testing.T) {
	got := parseGenres(json.RawMessage(`not valid json`))
	if got != nil {
		t.Errorf("want nil for invalid JSON, got %v", got)
	}
}

func TestParseGenres_emptyTagSkipped(t *testing.T) {
	raw := json.RawMessage(`{"Genre":[{"tag":""},{"tag":"Fantasy"}]}`)
	got := parseGenres(raw)
	if len(got) != 1 || got[0] != "Fantasy" {
		t.Errorf("want [Fantasy] (empty tag skipped), got %v", got)
	}
}

func TestParseGenres_truncatesAtFive(t *testing.T) {
	raw := json.RawMessage(`{"Genre":[{"tag":"a"},{"tag":"b"},{"tag":"c"},{"tag":"d"},{"tag":"e"},{"tag":"f"}]}`)
	got := parseGenres(raw)
	if len(got) != 5 {
		t.Errorf("want exactly 5 genres, got %d: %v", len(got), got)
	}
}

func TestNormalizeISBN(t *testing.T) {
	cases := map[string]string{
		"urn:isbn:978-0-261-10328-3":                    "9780261103283",
		"ISBN 9780261103283":                            "9780261103283",
		"0-261-10328-X":                                 "026110328X",
		"urn:uuid:12345678-1234-5678-1234-567812345678": "",
		"":             "",
		"123":          "",
		"97802611032X": "", // 'X' not allowed in ISBN-13
	}
	for in, want := range cases {
		if got := normalizeISBN(in); got != want {
			t.Errorf("normalizeISBN(%q): want %q, got %q", in, want, got)
		}
	}
}
