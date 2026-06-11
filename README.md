# simple-kobo-sync-service

A self-hosted Go server that emulates the Kobo store API, letting a Kobo device sync a local folder of EPUB files via its native Sync button — no cloud account required.

Tested on firmware 4.45 (Kobo Clara Colour). Should work on 4.36+.

## Quick start (Docker)

```sh
KOBO_TOKEN=$(openssl rand -hex 8) \
KOBO_EXTERNAL_URL=http://{your-ip}:8080 \
EPUB_DIR=/path/to/your/epubs \
docker compose up -d
```

The server logs the exact line to put in your Kobo config on startup:

```
set in .kobo/Kobo/Kobo eReader.conf under [OneStoreServices]
api_endpoint=http://{your-ip}:8080/kobo/<token>
```

## Kobo device setup

1. Connect the Kobo via USB and open `.kobo/Kobo/Kobo eReader.conf`.
2. Under `[OneStoreServices]`, set:

   ```ini
   api_endpoint=http://<your-server-ip>:8080/kobo/<your-token>
   ```

3. Save, eject, and press the Sync button on the device.

## Environment variables

| Variable | Default | Required | Description |
| --- | --- | --- | --- |
| `KOBO_TOKEN` | — | **Yes** | Secret token embedded in all API paths. Generate with `openssl rand -hex 8`. |
| `KOBO_EXTERNAL_URL` | — | **Yes** | Full base URL the Kobo device can reach, e.g. `http://192.168.0.3:8080`. |
| `KOBO_EPUB_DIR` | `.` | No | Directory containing `.epub` files to serve. |
| `KOBO_ADDR` | `:8080` | No | Listen address, e.g. `0.0.0.0:9000`. |
| `KOBO_DEBUG` | unset | No | Set to any non-empty value to log all request headers and bodies for unmapped endpoints. |
| `HARDCOVER_API_KEY` | unset | No | API key for [Hardcover](https://hardcover.app/account/api). When set, books are enriched with description, series, publisher, publication date, genres, and cover art on first sync. |
| `HARDCOVER_CACHE_DIR` | `KOBO_EPUB_DIR` | No | Writable directory for the metadata cache (`hardcover-cache.json`) and downloaded covers. In Docker this defaults to `/data/cache` (mount a writable volume) since the EPUB dir is read-only. |

## Hardcover metadata enrichment

Set `HARDCOVER_API_KEY` to look up each book on [Hardcover](https://hardcover.app) the first time it is seen during a sync. Matching uses the EPUB's ISBN (from the OPF `dc:identifier`) when available, falling back to a title + author search.

Results — description, series, publisher, publication date, genres, and a cover image — are cached on disk so each book is fetched only once (Hardcover is rate-limited to 60 requests/minute). Negative results are cached too. Delete `hardcover-cache.json` (and the `covers/` folder) in the cache dir to force a re-fetch.

## Building from source

```sh
go build -o kobo-sync .
KOBO_TOKEN=mysecret KOBO_EPUB_DIR=/path/to/epubs ./kobo-sync
```

## Docker image

Images are published to GHCR on every push to `main` and on version tags:

```sh
docker pull ghcr.io/adamratson/simple-kobo-sync-service:latest
```

### Volume permissions

The distroless image runs as uid `65532` (`nonroot`). Ensure your EPUB directory is readable by that user:

```sh
chmod o+rx /path/to/your/epubs
```

## Releasing

Push a tag to trigger the multi-arch (`linux/amd64`, `linux/arm64`) build and publish:

```sh
git tag v0.1.0
git push --tags
```

The `release` workflow publishes `ghcr.io/adamratson/simple-kobo-sync-service:v0.1.0` and `:latest`.
