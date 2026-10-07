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
    CGO_ENABLED=0 go build -trimpath -o /out/recommendation-stream ./cmd/recommendation-stream && \
    mkdir -p /private-media

FROM python:3.12-slim-bookworm AS pothole-export
COPY services/vision/export-requirements.txt /opt/vision/
RUN pip install --no-cache-dir -r /opt/vision/export-requirements.txt && \
    pip install --no-cache-dir --no-deps torch==2.10.0+cpu torchvision==0.25.0+cpu --index-url https://download.pytorch.org/whl/cpu
COPY services/vision/export_pothole.py /opt/vision/
COPY tests/fixtures/pothole-positive.png /tests/fixtures/pothole-positive.png
RUN python -I /opt/vision/export_pothole.py

FROM python:3.12-slim-bookworm AS media-runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr=5.3.0-2 tesseract-ocr-eng=1:4.1.0-2 && \
    rm -rf /var/lib/apt/lists/* && mkdir -p /data/media && chown 65532:65532 /data/media
WORKDIR /app
COPY services/vision/ /opt/vision/
COPY --from=pothole-export /opt/vision/pothole.onnx /opt/vision/pothole-manifest.json /opt/vision/
RUN python -m venv /opt/vision/.venv && \
    /opt/vision/.venv/bin/pip install --no-cache-dir -r /opt/vision/requirements.txt && \
    /opt/vision/.venv/bin/python /opt/vision/download_model.py && \
    chmod +x /opt/vision/run /opt/vision/pothole-run && \
    /opt/vision/run --version && /opt/vision/pothole-run --version
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
