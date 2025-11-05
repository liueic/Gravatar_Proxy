package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port           string
	CacheDir       string
	CacheTTL       time.Duration
	MaxCacheBytes  int64
	UpstreamBase   string
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

	return &Config{
		Port:          port,
		CacheDir:      cacheDir,
		CacheTTL:      cacheTTL,
		MaxCacheBytes: maxCacheBytes,
		UpstreamBase:  upstreamBase,
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
