# ── Stage 1: build dashboard ──────────────────────────────────────────────────
FROM node:22-alpine AS web-builder
WORKDIR /build
COPY web/package.json web/package-lock.json* ./
RUN npm ci --prefer-offline
COPY web/ ./
RUN npm run build

# ── Stage 2: build Go binaries ───────────────────────────────────────────────
FROM golang:1.27-alpine AS go-builder
RUN apk add --no-cache git gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/chakram ./cmd/chakram
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/chakramctl ./cmd/chakramctl
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/genesis-tool ./cmd/genesis-tool

# ── Stage 3: final image ─────────────────────────────────────────────────────
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata curl jq && \
    addgroup -g 1000 chakram && \
    adduser  -u 1000 -G chakram -h /home/chakram -D chakram

COPY --from=go-builder  /out/chakram      /usr/local/bin/chakram
COPY --from=go-builder  /out/chakramctl   /usr/local/bin/chakramctl
COPY --from=go-builder  /out/genesis-tool /usr/local/bin/genesis-tool
COPY --from=web-builder /build/dist       /app/web

COPY docker/scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

WORKDIR /home/chakram

EXPOSE 26656 26657 8080

HEALTHCHECK --interval=10s --timeout=5s --start-period=15s --retries=3 \
  CMD curl -sf http://127.0.0.1:8080/api/status | jq -e '.height > 0' || exit 1

ENTRYPOINT ["/entrypoint.sh"]
CMD ["start"]
