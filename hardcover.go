package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// hardcoverEndpoint is the default Hardcover GraphQL API URL.
const hardcoverEndpoint = "https://api.hardcover.app/v1/graphql"

// hardcoverClient is a minimal GraphQL client for the Hardcover book API.
// endpoint is a field (not a const) so tests can point it at an httptest server.
type hardcoverClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
}

func newHardcoverClient(apiKey string) *hardcoverClient {
	// Hardcover's dashboard hands out the token already prefixed with "Bearer ";
	// strip it so we don't end up sending "Bearer Bearer <token>".
	apiKey = strings.TrimPrefix(strings.TrimSpace(apiKey), "Bearer ")
	return &hardcoverClient{
		apiKey:     apiKey,
		endpoint:   hardcoverEndpoint,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// hardcoverBook holds the enrichment fields we extract from a Hardcover record.
type hardcoverBook struct {
	Title           string
	Description     string
	Series          string
	SeriesNumber    float64
	Publisher       string
	PublicationDate string
	Genres          []string
	CoverURL        string
}

// bookFields is the GraphQL selection set shared by both lookup paths.
// Kept within Hardcover's max query depth of 3 (books → book_series → series).
const bookFields = `
    title
    description
    release_date
    cached_tags
    image { url }
    contributions { author { name } }
    book_series { position series { name } }
    editions(limit: 1) { publisher { name } }`

// lookup resolves a Hardcover book by ISBN (exact) or, failing that, by title+author.
// Returns (nil, nil) when no match is found so callers can cache a negative result.
func (c *hardcoverClient) lookup(isbn, title, author string) (*hardcoverBook, error) {
	if isbn != "" {
		b, err := c.lookupByISBN(isbn)
		if err != nil {
			return nil, err
		}
		if b != nil {
			return b, nil
		}
		// fall through to title/author search on no ISBN match
	}
	if title == "" {
		return nil, nil
	}
	return c.lookupByTitle(title, author)
}

func (c *hardcoverClient) lookupByISBN(isbn string) (*hardcoverBook, error) {
	// Select the matched edition's own cover and publisher alongside book_id.
	// Hardcover's book-level image and the book's arbitrary edition publisher are
	// the *default*/first edition's values, which often differ from the edition
	// the user actually owns; preferring the matched edition's image and publisher
	// gives the device edition-accurate metadata at no extra round-trip.
	const q = `query ($isbn: String!) {
  editions(where: {_or: [{isbn_13: {_eq: $isbn}}, {isbn_10: {_eq: $isbn}}]}, limit: 1) { book_id image { url } publisher { name } }
}`
	var resp struct {
		Editions []struct {
			BookID int `json:"book_id"`
			Image  *struct {
				URL string `json:"url"`
			} `json:"image"`
			Publisher *struct {
				Name string `json:"name"`
			} `json:"publisher"`
		} `json:"editions"`
	}
	if err := c.do(q, map[string]any{"isbn": isbn}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Editions) == 0 || resp.Editions[0].BookID == 0 {
		return nil, nil
	}
	b, err := c.lookupByID(resp.Editions[0].BookID)
	if err != nil || b == nil {
		return b, err
	}
	if ed := resp.Editions[0].Image; ed != nil && ed.URL != "" {
		b.CoverURL = ed.URL
	}
	if pub := resp.Editions[0].Publisher; pub != nil && pub.Name != "" {
		b.Publisher = pub.Name
	}
	return b, nil
}

func (c *hardcoverClient) lookupByID(id int) (*hardcoverBook, error) {
	q := `query ($id: Int!) {
  books(where: {id: {_eq: $id}}, limit: 1) {` + bookFields + `
  }
}`
	var resp booksResponse
	if err := c.do(q, map[string]any{"id": id}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Books) == 0 {
		return nil, nil
	}
	return resp.Books[0].toHardcoverBook(), nil
}

func (c *hardcoverClient) lookupByTitle(title, author string) (*hardcoverBook, error) {
	// Hardcover blocks _ilike on the books table, so use its Typesense-backed
	// `search` query to resolve a book id, then fetch structured fields by id.
	id, err := c.searchBookID(title, author)
	if err != nil {
		return nil, err
	}
	if id == 0 {
		return nil, nil
	}
	return c.lookupByID(id)
}

// searchHit is one result document from the `search` query's Typesense payload.
type searchHit struct {
	Document struct {
		ID          string   `json:"id"` // Typesense returns the id as a string
		Title       string   `json:"title"`
		AuthorNames []string `json:"author_names"`
	} `json:"document"`
}

// searchBookID runs a full-text search and returns the id of the best match,
// or 0 if there are no results.
func (c *hardcoverClient) searchBookID(title, author string) (int, error) {
	q := `query ($q: String!) {
  search(query: $q, query_type: "Book", per_page: 5) { results }
}`
	query := strings.TrimSpace(title + " " + author)
	var resp struct {
		Search struct {
			Results struct {
				Hits []searchHit `json:"hits"`
			} `json:"results"`
		} `json:"search"`
	}
	if err := c.do(q, map[string]any{"q": query}, &resp); err != nil {
		return 0, err
	}
	hit := pickBestHit(resp.Search.Results.Hits, title, author)
	if hit == nil {
		return 0, nil
	}
	id, err := strconv.Atoi(hit.Document.ID)
	if err != nil {
		return 0, nil
	}
	return id, nil
}

// pickBestHit prefers a hit whose title matches (case-insensitive) and whose
// authors include the requested author, then a title match alone, then the
// first (most relevant) result.
func pickBestHit(hits []searchHit, title, author string) *searchHit {
	if len(hits) == 0 {
		return nil
	}
	wantTitle := strings.ToLower(strings.TrimSpace(title))
	wantAuthor := strings.ToLower(strings.TrimSpace(author))
	var titleOnly *searchHit
	for i := range hits {
		h := &hits[i]
		if strings.ToLower(strings.TrimSpace(h.Document.Title)) != wantTitle {
			continue
		}
		if wantAuthor == "" || hitHasAuthor(h, wantAuthor) {
			return h
		}
		if titleOnly == nil {
			titleOnly = h
		}
	}
	if titleOnly != nil {
		return titleOnly
	}
	// No exact title match — fall back to the most relevant result.
	return &hits[0]
}

func hitHasAuthor(h *searchHit, wantAuthor string) bool {
	for _, name := range h.Document.AuthorNames {
		if strings.Contains(strings.ToLower(name), wantAuthor) {
			return true
		}
	}
	return false
}

// do executes a GraphQL request and decodes data into out.
func (c *hardcoverClient) do(query string, variables map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("hardcover: http %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("hardcover: decode response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("hardcover: graphql error: %s", envelope.Errors[0].Message)
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("hardcover: empty response data")
	}
	return json.Unmarshal(envelope.Data, out)
}

// booksResponse and hcBook mirror the GraphQL shape of the books selection set.
type booksResponse struct {
	Books []hcBook `json:"books"`
}

type hcBook struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	ReleaseDate string          `json:"release_date"`
	CachedTags  json.RawMessage `json:"cached_tags"`
	Image       *struct {
		URL string `json:"url"`
	} `json:"image"`
	Contributions []struct {
		Author *struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"contributions"`
	BookSeries []struct {
		Position float64 `json:"position"`
		Series   *struct {
			Name string `json:"name"`
		} `json:"series"`
	} `json:"book_series"`
	Editions []struct {
		Publisher *struct {
			Name string `json:"name"`
		} `json:"publisher"`
	} `json:"editions"`
}

func (b *hcBook) toHardcoverBook() *hardcoverBook {
	out := &hardcoverBook{
		Title:           b.Title,
		Description:     b.Description,
		PublicationDate: b.ReleaseDate,
		Genres:          parseGenres(b.CachedTags),
	}
	if b.Image != nil {
		out.CoverURL = b.Image.URL
	}
	for _, s := range b.BookSeries {
		if s.Series != nil && s.Series.Name != "" {
			out.Series = s.Series.Name
			out.SeriesNumber = s.Position
			break
		}
	}
	for _, e := range b.Editions {
		if e.Publisher != nil && e.Publisher.Name != "" {
			out.Publisher = e.Publisher.Name
			break
		}
	}
	return out
}

// parseGenres pulls genre names out of Hardcover's cached_tags jsonb blob,
// which maps a category ("Genre", "Mood", …) to a list of {tag, count} objects.
// Returns up to 5 genre names; any parse failure yields no genres.
func parseGenres(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tags map[string][]struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil
	}
	var genres []string
	for _, t := range tags["Genre"] {
		if t.Tag == "" {
			continue
		}
		genres = append(genres, t.Tag)
		if len(genres) >= 5 {
			break
		}
	}
	return genres
}
