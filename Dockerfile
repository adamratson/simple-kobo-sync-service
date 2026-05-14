FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /out/kobo-sync .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kobo-sync /kobo-sync
ENV KOBO_ADDR=:8080 \
    KOBO_EPUB_DIR=/data/epubs
VOLUME ["/data/epubs"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/kobo-sync"]
