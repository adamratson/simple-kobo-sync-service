package main

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// epubUUIDNamespace is the fixed UUID v5 namespace for this application's book IDs.
// Derived deterministically so the same filename always produces the same UUID.
const epubUUIDNamespace = "f47ac10b-58cc-4372-a567-0e02b2c3d479"

type epubMeta struct {
	UUID     string
	Title    string
	Author   string
	Language string
	FilePath string
	Size     int64
}

// deriveUUID returns a UUID v5 (SHA-1 based) for the given name using our fixed namespace.
func deriveUUID(name string) string {
	ns := uuidToBytes(epubUUIDNamespace)
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(name))
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func uuidToBytes(u string) []byte {
	clean := strings.ReplaceAll(u, "-", "")
	b, _ := hex.DecodeString(clean)
	return b
}

func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// normalizeID strips common EPUB identifier prefixes and validates UUID format.
// Falls back to deriveUUID(filename) if the identifier isn't a valid UUID.
func normalizeID(id, filename string) string {
	id = strings.TrimPrefix(id, "urn:uuid:")
	id = strings.TrimPrefix(id, "urn:UUID:")
	id = strings.ToLower(strings.TrimSpace(id))
	if isValidUUID(id) {
		return id
	}
	return deriveUUID(filename)
}

// scanEpubs returns metadata for every .epub file in dir.
// Unreadable files are logged and assigned filename-derived metadata.
func scanEpubs(dir string) ([]epubMeta, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var result []epubMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".epub") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		meta, err := readEpubMeta(path)
		if err != nil {
			slog.Warn("skipping epub", "file", e.Name(), "err", err)
			fi, _ := os.Stat(path)
			var size int64
			if fi != nil {
				size = fi.Size()
			}
			result = append(result, epubMeta{
				UUID:     deriveUUID(e.Name()),
				Title:    strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
				Language: "en",
				FilePath: path,
				Size:     size,
			})
			continue
		}
		result = append(result, meta)
	}
	return result, nil
}

// readEpubMeta opens an EPUB (ZIP) and extracts Dublin Core metadata from the OPF file.
func readEpubMeta(path string) (epubMeta, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return epubMeta{}, err
	}
	defer r.Close()

	opfPath, err := findOPFPath(r)
	if err != nil {
		return epubMeta{}, err
	}

	title, author, identifier, language := parseOPFMeta(r, opfPath)

	fi, _ := os.Stat(path)
	var size int64
	if fi != nil {
		size = fi.Size()
	}

	filename := filepath.Base(path)
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	if language == "" {
		language = "en"
	}

	uuid := normalizeID(identifier, filename)

	return epubMeta{
		UUID:     uuid,
		Title:    title,
		Author:   author,
		Language: language,
		FilePath: path,
		Size:     size,
	}, nil
}

// findEpubByUUID scans dir and returns the path of the epub whose UUID matches id.
func findEpubByUUID(dir, id string) (string, bool) {
	books, err := scanEpubs(dir)
	if err != nil {
		return "", false
	}
	id = strings.ToLower(id)
	for _, b := range books {
		if strings.ToLower(b.UUID) == id {
			return b.FilePath, true
		}
	}
	return "", false
}

// findOPFPath reads META-INF/container.xml and returns the rootfile full-path.
func findOPFPath(r *zip.ReadCloser) (string, error) {
	for _, f := range r.File {
		if f.Name != "META-INF/container.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		d := xml.NewDecoder(rc)
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "rootfile" {
				for _, attr := range se.Attr {
					if attr.Name.Local == "full-path" {
						return attr.Value, nil
					}
				}
			}
		}
		return "", fmt.Errorf("no rootfile in container.xml")
	}
	return "", fmt.Errorf("META-INF/container.xml not found")
}

// parseOPFMeta extracts title, creator, identifier, and language from an OPF file
// using a token scanner so it handles any dc: namespace prefix.
func parseOPFMeta(r *zip.ReadCloser, path string) (title, author, identifier, language string) {
	for _, f := range r.File {
		if f.Name != path {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return
		}
		defer rc.Close()

		d := xml.NewDecoder(rc)
		var inMeta bool
		var current string
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "metadata" {
					inMeta = true
				}
				if inMeta {
					current = t.Name.Local
				}
			case xml.EndElement:
				if t.Name.Local == "metadata" {
					inMeta = false
				}
				current = ""
			case xml.CharData:
				if !inMeta {
					continue
				}
				s := strings.TrimSpace(string(t))
				if s == "" {
					continue
				}
				switch current {
				case "title":
					if title == "" {
						title = s
					}
				case "creator":
					if author == "" {
						author = s
					}
				case "identifier":
					if identifier == "" {
						identifier = s
					}
				case "language":
					if language == "" {
						language = s
					}
				}
			}
		}
		return
	}
	return
}
