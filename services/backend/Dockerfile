FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY services/backend/go.mod services/backend/go.sum ./
RUN go mod download
COPY services/backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/public-api ./cmd/public-api && \
    CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/vault ./cmd/vault && \
    CGO_ENABLED=0 go build -trimpath -o /out/media-worker ./cmd/media-worker && \
    mkdir -p /private-media

FROM debian:bookworm-slim AS media-runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr=5.3.0-2 tesseract-ocr-eng=1:4.1.0-2 && \
    rm -rf /var/lib/apt/lists/* && mkdir -p /data/media && chown 65532:65532 /data/media
WORKDIR /app
COPY --from=build /out/media-worker /app/media-worker
USER 65532:65532
CMD ["/app/media-worker"]

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/ /app/
COPY --from=build --chown=nonroot:nonroot /private-media/ /data/media/
COPY db/ /app/db/
USER nonroot:nonroot
CMD ["/app/public-api"]
