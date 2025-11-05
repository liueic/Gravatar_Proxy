FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gravatar-proxy ./cmd/gravatar-proxy

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /build/gravatar-proxy .

RUN mkdir -p /app/cache

ENV PORT=8080
ENV CACHE_DIR=/app/cache
ENV CACHE_TTL=24h
ENV MAX_CACHE_BYTES=268435456
ENV UPSTREAM_BASE=https://www.gravatar.com

EXPOSE 8080

CMD ["./gravatar-proxy"]
