package main

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	addr := envOr("KOBO_ADDR", ":8080")
	token := envOr("KOBO_TOKEN", "")
	epubDir := envOr("KOBO_EPUB_DIR", ".")
	externalURL := envOr("KOBO_EXTERNAL_URL", "")
	debug := os.Getenv("KOBO_DEBUG") != ""
	hardcoverAPIKey := envOr("HARDCOVER_API_KEY", "")
	cacheDir := envOr("HARDCOVER_CACHE_DIR", epubDir)

	if token == "" {
		suggested := randomToken()
		slog.Error("KOBO_TOKEN is required",
			"hint", "set KOBO_TOKEN="+suggested+" in your environment or docker-compose.yml")
		os.Exit(1)
	}

	if externalURL == "" {
		slog.Error("KOBO_EXTERNAL_URL is required",
			"hint", "set KOBO_EXTERNAL_URL=http://<your-ip>:8080 in your environment or docker-compose.yml")
		os.Exit(1)
	}

	srv := newServer(config{
		token:           token,
		epubDir:         epubDir,
		externalURL:     externalURL,
		debug:           debug,
		hardcoverAPIKey: hardcoverAPIKey,
		cacheDir:        cacheDir,
	})

	slog.Info("kobo-sync starting", "addr", addr, "epub_dir", epubDir)
	if hardcoverAPIKey != "" {
		slog.Info("hardcover metadata enrichment enabled", "cache_dir", cacheDir)
	} else {
		slog.Info("hardcover metadata enrichment disabled", "hint", "set HARDCOVER_API_KEY to enable")
	}
	slog.Info("set in .kobo/Kobo/Kobo eReader.conf under [OneStoreServices]",
		"api_endpoint", externalURL+"/kobo/"+token,
	)

	if err := http.ListenAndServe(addr, srv); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func randomToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "changeme"
	}
	return hex.EncodeToString(b)
}
