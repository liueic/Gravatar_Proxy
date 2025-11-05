package proxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gravatar-proxy/internal/cache"
	"gravatar-proxy/internal/config"
	"gravatar-proxy/internal/log"
)

type Handler struct {
	cache        *cache.Cache
	upstreamBase string
	client       *http.Client
	ttl          time.Duration
}

func NewHandler(cfg *config.Config, c *cache.Cache) (*Handler, error) {
	return &Handler{
		cache:        c,
		upstreamBase: cfg.UpstreamBase,
		ttl:          cfg.CacheTTL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := generateRequestID()

	hash := strings.TrimPrefix(r.URL.Path, "/avatar/")
	hash = normalizeHash(hash)

	if hash == "" {
		log.LogRequest(r.Method, r.URL.Path, http.StatusBadRequest, time.Since(startTime), requestID)
		http.Error(w, "Invalid hash", http.StatusBadRequest)
		return
	}

	queryParams := extractQueryParams(r.URL.Query())
	cacheKey := h.cache.GenerateKey("/avatar/"+hash, queryParams)

	if h.cache.CheckConditional(cacheKey, r) {
		log.LogRequest(r.Method, r.URL.Path, http.StatusNotModified, time.Since(startTime), requestID)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	entry, valid := h.cache.Get(cacheKey)
	if valid {
		log.Info("cache hit", "request_id", requestID, "key", cacheKey)
		ttlSeconds := int(h.ttl.Seconds())
		if err := h.cache.WriteResponse(w, cacheKey, ttlSeconds); err != nil {
			log.Error("failed to write cached response", "error", err, "request_id", requestID)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			log.LogRequest(r.Method, r.URL.Path, http.StatusInternalServerError, time.Since(startTime), requestID)
			return
		}
		log.LogRequest(r.Method, r.URL.Path, http.StatusOK, time.Since(startTime), requestID)
		return
	}

	upstreamURL := h.buildUpstreamURL(hash, queryParams)
	req, err := http.NewRequest("GET", upstreamURL, nil)
	if err != nil {
		log.Error("failed to create upstream request", "error", err, "request_id", requestID)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		log.LogRequest(r.Method, r.URL.Path, http.StatusInternalServerError, time.Since(startTime), requestID)
		return
	}

	if entry != nil {
		if etag := entry.Metadata.Headers["ETag"]; etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		if lastModified := entry.Metadata.Headers["Last-Modified"]; lastModified != "" {
			req.Header.Set("If-Modified-Since", lastModified)
		}
	}

	log.Info("fetching from upstream", "request_id", requestID, "url", upstreamURL)
	resp, err := h.client.Do(req)
	if err != nil {
		log.Error("upstream request failed", "error", err, "request_id", requestID)
		http.Error(w, "Failed to fetch from upstream", http.StatusBadGateway)
		log.LogRequest(r.Method, r.URL.Path, http.StatusBadGateway, time.Since(startTime), requestID)
		return
	}

	if resp.StatusCode == http.StatusNotModified && entry != nil {
		log.Info("upstream returned 304, refreshing cache", "request_id", requestID)
		metadata := entry.Metadata
		metadata.CreatedAt = time.Now()
		metadata.LastAccessedAt = time.Now()
		if err := h.cache.UpdateMetadata(cacheKey, metadata); err != nil {
			log.Warn("failed to update metadata", "error", err, "request_id", requestID)
		}

		ttlSeconds := int(h.ttl.Seconds())
		if err := h.cache.WriteResponse(w, cacheKey, ttlSeconds); err != nil {
			log.Error("failed to write cached response", "error", err, "request_id", requestID)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			log.LogRequest(r.Method, r.URL.Path, http.StatusInternalServerError, time.Since(startTime), requestID)
			return
		}
		log.LogRequest(r.Method, r.URL.Path, http.StatusOK, time.Since(startTime), requestID)
		return
	}

	data, err := cache.ReadResponseBody(resp)
	if err != nil {
		log.Error("failed to read response body", "error", err, "request_id", requestID)
		http.Error(w, "Failed to read upstream response", http.StatusInternalServerError)
		log.LogRequest(r.Method, r.URL.Path, http.StatusInternalServerError, time.Since(startTime), requestID)
		return
	}

	metadata := cache.Metadata{
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		Headers:        cache.ExtractHeaders(resp),
		StatusCode:     resp.StatusCode,
	}

	if err := h.cache.Set(cacheKey, data, metadata); err != nil {
		log.Warn("failed to cache response", "error", err, "request_id", requestID)
	}

	for k, v := range metadata.Headers {
		w.Header().Set(k, v)
	}
	ttlSeconds := int(h.ttl.Seconds())
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", ttlSeconds))
	w.WriteHeader(resp.StatusCode)
	w.Write(data)

	log.LogRequest(r.Method, r.URL.Path, resp.StatusCode, time.Since(startTime), requestID)
}

func (h *Handler) buildUpstreamURL(hash string, queryParams map[string]string) string {
	u, _ := url.Parse(h.upstreamBase)
	u.Path = fmt.Sprintf("/avatar/%s", hash)

	q := u.Query()
	for k, v := range queryParams {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	return u.String()
}

func normalizeHash(hash string) string {
	hash = strings.TrimSpace(hash)
	hash = strings.ToLower(hash)
	return hash
}

func extractQueryParams(query url.Values) map[string]string {
	allowed := map[string]bool{
		"s": true,
		"d": true,
		"r": true,
		"f": true,
	}

	params := make(map[string]string)
	for k, v := range query {
		if allowed[k] && len(v) > 0 {
			params[k] = v[0]
		}
	}
	return params
}

func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
