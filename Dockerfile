# ── Frontend ────────────────────────────────────────────────────────────────
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ── Server ──────────────────────────────────────────────────────────────────
FROM golang:1.27-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=frontend /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /docvault ./cmd/docvault

# ── Runtime ─────────────────────────────────────────────────────────────────
FROM alpine:3.23
# poppler (page count, thumbnails, text layers), tesseract with English and
# Hindi (OCR), libheif (iPhone HEIC photos), and a font so PDFs that don't
# embed theirs still render. They run only as short background jobs.
RUN apk add --no-cache ca-certificates poppler-utils tesseract-ocr tesseract-ocr-data-eng \
        tesseract-ocr-data-hin libheif-tools font-dejavu \
    && adduser -D -H -u 1000 -s /sbin/nologin docvault \
    && mkdir -p /data && chown 1000:1000 /data
# In PATH, so `docker exec docvault docvault <command>` works.
COPY --from=server /docvault /usr/local/bin/docvault
ENV DOCVAULT_DATA_DIR=/data \
    DOCVAULT_PORT=8080 \
    GOMEMLIMIT=32MiB
USER 1000:1000
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["docvault", "healthcheck"]
ENTRYPOINT ["docvault"]
