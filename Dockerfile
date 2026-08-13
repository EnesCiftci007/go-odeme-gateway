FROM golang:alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download


COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -o main .


FROM alpine:latest

RUN apk add --no-cache ca-certificates sqlite

WORKDIR /app

COPY --from=builder /app/main .
COPY --from=builder /app/templates ./templates

EXPOSE 8080


CMD ["./main"]