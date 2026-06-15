package main

import (
	"archive/zip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adamratson/kepubify/v4/kepub"
)

// kepubStore converts EPUBs to Kobo's KEPUB format and caches the results on
// disk so each book is converted at most once per source revision. A cached
// kepub is reused until its source EPUB is modified (gains a newer mtime), so
// replacing a file in the library refreshes its kepub on the next download.
// Safe for concurrent use.
type kepubStore struct {
	dir  string
	conv *kepub.Converter

	mu       sync.Mutex
	inflight map[string]*sync.Mutex // per-uuid locks; collapse duplicate concurrent conversions
}

func newKepubStore(dir string) *kepubStore {
	return &kepubStore{
		dir:      dir,
		conv:     kepub.NewConverter(),
		inflight: map[string]*sync.Mutex{},
	}
}

// kepubFilename converts an .epub filename to its .kepub.epub form. Kobo keys
// its native kepub rendering off this double extension, so the download must
// advertise it. A name that already ends in .kepub.epub is returned unchanged.
func kepubFilename(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".kepub.epub") {
		return name
	}
	return strings.TrimSuffix(name, filepath.Ext(name)) + ".kepub.epub"
}

// kepubsDir is where converted kepubs live.
func (k *kepubStore) kepubsDir() string { return filepath.Join(k.dir, "kepubs") }

// kepubPath returns the on-disk path for a book's cached kepub.
func (k *kepubStore) kepubPath(uuid string) string {
	return filepath.Join(k.kepubsDir(), uuid+".kepub.epub")
}

// lock returns the per-uuid mutex, creating it on first use.
func (k *kepubStore) lock(uuid string) *sync.Mutex {
	k.mu.Lock()
	defer k.mu.Unlock()
	mu, ok := k.inflight[uuid]
	if !ok {
		mu = &sync.Mutex{}
		k.inflight[uuid] = mu
	}
	return mu
}

// cached returns the path and size of an up-to-date cached kepub for book, or
// ok=false when none exists or the source EPUB is newer than the cache. It
// never performs a conversion, so it is cheap enough for the sync hot path.
func (k *kepubStore) cached(book epubMeta) (path string, size int64, ok bool) {
	src, err := os.Stat(book.FilePath)
	if err != nil {
		return "", 0, false
	}
	p := k.kepubPath(book.UUID)
	fi, err := os.Stat(p)
	if err != nil {
		return "", 0, false
	}
	if fi.ModTime().Before(src.ModTime()) {
		return "", 0, false // stale: source changed since we converted
	}
	return p, fi.Size(), true
}

// convert returns the path to an up-to-date cached kepub for book, performing
// the conversion first if necessary. Concurrent callers for the same book share
// a single conversion rather than racing on the same output file.
func (k *kepubStore) convert(ctx context.Context, book epubMeta) (string, error) {
	if p, _, ok := k.cached(book); ok {
		return p, nil
	}
	mu := k.lock(book.UUID)
	mu.Lock()
	defer mu.Unlock()
	// Re-check under the lock: another goroutine may have just converted it.
	if p, _, ok := k.cached(book); ok {
		return p, nil
	}
	if err := k.write(ctx, book); err != nil {
		return "", err
	}
	return k.kepubPath(book.UUID), nil
}

// write converts book's EPUB into a temp file and atomically renames it into the
// cache, mirroring the metadata/cover cache's write pattern so a partial file is
// never observed.
func (k *kepubStore) write(ctx context.Context, book epubMeta) error {
	zr, err := zip.OpenReader(book.FilePath)
	if err != nil {
		return fmt.Errorf("open epub: %w", err)
	}
	defer zr.Close()

	if err := os.MkdirAll(k.kepubsDir(), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(k.kepubsDir(), book.UUID+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	// *zip.Reader satisfies fs.FS, which is exactly what Convert consumes.
	if err := k.conv.Convert(ctx, tmp, &zr.Reader); err != nil {
		tmp.Close()
		return fmt.Errorf("convert: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, k.kepubPath(book.UUID)); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	slog.Info("kepubified", "title", book.Title, "uuid", book.UUID)
	return nil
}
