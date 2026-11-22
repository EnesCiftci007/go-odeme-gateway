FROM golang:alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download


COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -o main .


FROM alpine:latest

RUN apk add --no-cache ca-certificates sqlite

RUN adduser -D -H appuser && mkdir -p /app/data && chown appuser /app/data

WORKDIR /app

COPY --from=builder /app/main .
COPY --from=builder /app/templates ./templates

ENV DB_PATH=/app/data/odeme_sistemi.db
VOLUME /app/data
USER appuser

EXPOSE 8080


CMD ["./main"]