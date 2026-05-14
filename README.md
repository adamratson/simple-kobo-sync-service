# simple-kobo-sync-service

A self-hosted Go server that emulates the Kobo store API, letting a Kobo device sync a local folder of EPUB files via its native Sync button — no cloud account required.

Tested on firmware 4.45 (Kobo Clara Colour). Should work on 4.36+.

## Quick start (Docker)

```sh
KOBO_TOKEN=$(openssl rand -hex 8) EPUB_DIR=/path/to/your/epubs docker compose up -d
```

The server logs the exact line to put in your Kobo config on startup:

```
set in .kobo/Kobo/Kobo eReader.conf under [OneStoreServices]
api_endpoint=http://192.168.1.50:8080/kobo/<token>
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
| `KOBO_EXTERNAL_URL` | auto-detected | No | Full base URL the Kobo device can reach, e.g. `http://192.168.1.50:8080`. Auto-detection works for bare-metal and Docker with `network_mode: host`. Set explicitly if auto-detection picks the wrong interface. |
| `KOBO_EPUB_DIR` | `.` | No | Directory containing `.epub` files to serve. |
| `KOBO_ADDR` | `:8080` | No | Listen address, e.g. `0.0.0.0:9000`. |
| `KOBO_DEBUG` | unset | No | Set to any non-empty value to log all request headers and bodies for unmapped endpoints. |

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
