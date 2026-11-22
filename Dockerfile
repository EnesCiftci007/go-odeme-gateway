# --- Derleme ---
FROM golang:1.26-alpine AS builder

# SQLite sürücüsü CGO gerektirir.
RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w -extldflags "-static"' -o /out/server ./cmd/server

# --- Çalışma ---
FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 appuser \
    && mkdir -p /app/data && chown appuser /app/data

WORKDIR /app
COPY --from=builder /out/server /app/server

ENV DB_PATH=/app/data/odeme_sistemi.db \
    LOG_FORMAT=json
VOLUME /app/data
USER appuser
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

CMD ["/app/server"]
