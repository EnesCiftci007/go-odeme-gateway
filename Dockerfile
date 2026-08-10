# 1. Aşama: Build (Derleme) Aşaması
FROM golang:alpine AS builder

# SQLite ve CGO için gerekli derleme araçlarını indir
RUN apk add --no-cache gcc musl-dev

# Çalışma dizinini ayarla
WORKDIR /app

# Önce bağımlılık dosyalarını kopyala ve indir (Cache optimizasyonu için)
COPY go.mod go.sum ./
RUN go mod download

# Tüm proje kodunu kopyala
COPY . .

# Projeyi derle (CGO_ENABLED=1 SQLite/GORM için şarttır)
RUN CGO_ENABLED=1 GOOS=linux go build -o main .

# 2. Aşama: Çalıştırma (Production) Aşaması - Küçük ve Hafif İmaj
FROM alpine:latest

RUN apk add --no-cache ca-certificates sqlite

WORKDIR /app

# Build aşamasından çıkan derlenmiş 'main' binary dosyasını kopyala
COPY --from=builder /app/main .
COPY --from=builder /app/templates ./templates

# Sunucunun çalışacağı portu belirt
EXPOSE 8080

# Uygulamayı başlat
CMD ["./main"]