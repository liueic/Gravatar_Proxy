package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port           string
	CacheDir       string
	CacheTTL       time.Duration
	MaxCacheBytes  int64
	UpstreamBase   string
	AllowedOrigins []string
}

func Load() (*Config, error) {
	port := getEnv("PORT", "8080")
	cacheDir := getEnv("CACHE_DIR", "./cache")
	cacheTTLStr := getEnv("CACHE_TTL", "24h")
	maxCacheBytesStr := getEnv("MAX_CACHE_BYTES", "268435456")
	upstreamBase := getEnv("UPSTREAM_BASE", "https://www.gravatar.com")

	cacheTTL, err := time.ParseDuration(cacheTTLStr)
	if err != nil {
		return nil, err
	}

	maxCacheBytes, err := strconv.ParseInt(maxCacheBytesStr, 10, 64)
	if err != nil {
		return nil, err
	}

	allowedOriginsStr := getEnv("ALLOWED_ORIGINS", "")
	var allowedOrigins []string
	if allowedOriginsStr != "" {
		origins := strings.Split(allowedOriginsStr, ",")
		for _, origin := range origins {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				allowedOrigins = append(allowedOrigins, origin)
			}
		}
	}

	return &Config{
		Port:           port,
		CacheDir:       cacheDir,
		CacheTTL:       cacheTTL,
		MaxCacheBytes:  maxCacheBytes,
		UpstreamBase:   upstreamBase,
		AllowedOrigins: allowedOrigins,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
