FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /out/kobo-sync .
# Empty dir to seed /data/cache owned by nonroot, so a fresh volume is writable.
RUN mkdir -p /out/cache

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kobo-sync /kobo-sync
COPY --from=build --chown=65532:65532 /out/cache /data/cache
ENV KOBO_ADDR=:8080 \
    KOBO_EPUB_DIR=/data/epubs \
    HARDCOVER_CACHE_DIR=/data/cache
VOLUME ["/data/epubs", "/data/cache"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/kobo-sync"]
