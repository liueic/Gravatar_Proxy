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
	cache          *cache.Cache
	upstreamBase   string
	client         *http.Client
	ttl            time.Duration
	allowedOrigins []string
}

func NewHandler(cfg *config.Config, c *cache.Cache) (*Handler, error) {
	return &Handler{
		cache:          c,
		upstreamBase:   cfg.UpstreamBase,
		ttl:            cfg.CacheTTL,
		allowedOrigins: cfg.AllowedOrigins,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	requestID := generateRequestID()

	// 处理OPTIONS预检请求
	if r.Method == "OPTIONS" {
		if h.checkAccessControl(w, r) {
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Forbidden", http.StatusForbidden)
			log.LogRequest(r.Method, r.URL.Path, http.StatusForbidden, time.Since(startTime), requestID)
		}
		return
	}

	// 检查访问控制
	if !h.checkAccessControl(w, r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		log.LogRequest(r.Method, r.URL.Path, http.StatusForbidden, time.Since(startTime), requestID)
		return
	}

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

// normalizeOrigin 规范化Origin格式，提取域名部分
func normalizeOrigin(origin string) string {
	if origin == "" {
		return ""
	}
	origin = strings.TrimSpace(origin)
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	return strings.ToLower(host)
}

// extractDomainFromReferer 从Referer URL中提取域名
func extractDomainFromReferer(referer string) string {
	if referer == "" {
		return ""
	}
	u, err := url.Parse(referer)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	return strings.ToLower(host)
}

// isOriginAllowed 检查Origin是否在允许列表中
// 支持精确匹配和子域名匹配（如允许example.com时，也允许sub.example.com）
func isOriginAllowed(origin string, allowedOrigins []string) bool {
	if len(allowedOrigins) == 0 {
		return true // 未配置允许列表时，允许所有来源（向后兼容）
	}
	if origin == "" {
		return false
	}
	originDomain := normalizeOrigin(origin)
	if originDomain == "" {
		return false
	}
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(strings.ToLower(allowed))
		if allowed == "" {
			continue
		}
		// 精确匹配
		if originDomain == allowed {
			return true
		}
		// 子域名匹配：如果允许example.com，则sub.example.com也允许
		if strings.HasSuffix(originDomain, "."+allowed) {
			return true
		}
	}
	return false
}

// checkAccessControl 检查访问控制并设置CORS响应头
// 返回true表示允许访问，false表示拒绝访问
func (h *Handler) checkAccessControl(w http.ResponseWriter, r *http.Request) bool {
	// 如果未配置允许列表，跳过检查（向后兼容）
	if len(h.allowedOrigins) == 0 {
		return true
	}

	origin := r.Header.Get("Origin")
	referer := r.Header.Get("Referer")

	// 检查Origin请求头（用于CORS预检和实际请求）
	if origin != "" {
		if isOriginAllowed(origin, h.allowedOrigins) {
			// 设置CORS响应头
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Cache-Control, If-None-Match, If-Modified-Since")
			return true
		}
	}

	// 检查Referer请求头（用于直接请求，防止绕过CORS）
	if referer != "" {
		refererDomain := extractDomainFromReferer(referer)
		if refererDomain != "" && isOriginAllowed(refererDomain, h.allowedOrigins) {
			// 如果Origin存在但不匹配，但Referer匹配，也允许访问
			// 设置CORS响应头（如果Origin存在）
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Cache-Control, If-None-Match, If-Modified-Since")
			return true
		}
	}

	// 如果既没有Origin也没有Referer，或者都不匹配，拒绝访问
	return false
}

func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
