# Gravatar Proxy

A Go HTTP server that proxies Gravatar avatars and caches responses with configurable TTL.

## Features

- Proxies requests to Gravatar's avatar API
- Disk-based cache with configurable TTL
- LRU eviction when cache size exceeds limit
- Support for conditional requests (304 Not Modified)
- Graceful shutdown
- Health check endpoint
- Structured JSON logging

## Installation

```bash
go build -o gravatar-proxy ./cmd/gravatar-proxy
```

## Usage

Start the server with default settings:

```bash
go run ./cmd/gravatar-proxy
```

Or build and run:

```bash
./gravatar-proxy
```

## Configuration

Configure the server using environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `CACHE_DIR` | `./cache` | Directory for cache storage |
| `CACHE_TTL` | `24h` | Cache time-to-live (Go duration format: `5m`, `2h`, `24h`) |
| `MAX_CACHE_BYTES` | `268435456` (256MB) | Maximum cache size in bytes |
| `UPSTREAM_BASE` | `https://www.gravatar.com` | Upstream Gravatar base URL |

Example:

```bash
export PORT=3000
export CACHE_TTL=10s
export CACHE_DIR=/tmp/gravatar-cache
export MAX_CACHE_BYTES=536870912
go run ./cmd/gravatar-proxy
```

## API Endpoints

### Avatar Proxy

```
GET /avatar/{hash}?s={size}&d={default}&r={rating}&f={force_default}
```

Proxies Gravatar avatar requests. Supports the following query parameters:

- `s` - Size in pixels (1-2048)
- `d` - Default image (`404`, `mp`, `identicon`, `monsterid`, `wavatar`, `retro`, `robohash`, `blank`)
- `r` - Rating (`g`, `pg`, `r`, `x`)
- `f` - Force default (`y` to always show default image)

Example:

```bash
curl http://localhost:8080/avatar/00000000000000000000000000000000?s=80&d=identicon&r=g
```

### Health Check

```
GET /healthz
```

Returns server health status:

```json
{"status":"ok"}
```

## Caching Behavior

- Cache key is generated from the full request URL (path + sorted query parameters)
- Cache entries include metadata (headers, timestamps, status code)
- Entries are served from cache if within TTL
- After TTL expiration, the proxy revalidates with upstream using `If-None-Match`/`If-Modified-Since`
- On upstream 304 response, cache metadata is refreshed and cached data is served
- Client conditional requests are honored when cache entry is valid
- LRU eviction occurs when cache size exceeds `MAX_CACHE_BYTES`

## Development

Run tests:

```bash
go test ./...
```

Run tests with coverage:

```bash
go test -cover ./...
```

## Project Structure

```
.
├── cmd/
│   └── gravatar-proxy/
│       └── main.go           # Application entry point
├── internal/
│   ├── cache/
│   │   ├── cache.go          # Disk cache with TTL and LRU
│   │   └── cache_test.go     # Cache tests
│   ├── config/
│   │   └── config.go         # Environment configuration
│   ├── log/
│   │   └── log.go            # Structured logging
│   └── proxy/
│       └── proxy.go          # HTTP handlers and upstream client
├── go.mod
└── README.md
```

## License

MIT
