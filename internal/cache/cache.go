package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gravatar-proxy/internal/log"
)

type Metadata struct {
	CreatedAt      time.Time         `json:"created_at"`
	LastAccessedAt time.Time         `json:"last_accessed_at"`
	Headers        map[string]string `json:"headers"`
	StatusCode     int               `json:"status_code"`
	Size           int64             `json:"size"`
}

type CacheEntry struct {
	Key      string
	FilePath string
	Metadata Metadata
}

type Cache struct {
	dir           string
	ttl           time.Duration
	maxBytes      int64
	mu            sync.RWMutex
	index         map[string]*CacheEntry
	accessList    []string
	currentBytes  int64
}

func New(dir string, ttl time.Duration, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	c := &Cache{
		dir:        dir,
		ttl:        ttl,
		maxBytes:   maxBytes,
		index:      make(map[string]*CacheEntry),
		accessList: make([]string, 0),
	}

	if err := c.loadIndex(); err != nil {
		log.Warn("failed to load cache index, starting fresh", "error", err)
	}

	return c, nil
}

func (c *Cache) GenerateKey(path string, query map[string]string) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := []string{path}
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, query[k]))
	}

	fullURL := strings.Join(parts, "?")
	hash := sha256.Sum256([]byte(fullURL))
	return hex.EncodeToString(hash[:])
}

func (c *Cache) Get(key string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.index[key]
	if !exists {
		return nil, false
	}

	if time.Since(entry.Metadata.CreatedAt) > c.ttl {
		return entry, false
	}

	return entry, true
}

func (c *Cache) Set(key string, data []byte, metadata Metadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	filePath := filepath.Join(c.dir, key)
	metaPath := filepath.Join(c.dir, key+".meta")

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	metadata.Size = int64(len(data))
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metaPath, metaBytes, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	entry := &CacheEntry{
		Key:      key,
		FilePath: filePath,
		Metadata: metadata,
	}

	if existing, exists := c.index[key]; exists {
		c.currentBytes -= existing.Metadata.Size
	}

	c.index[key] = entry
	c.currentBytes += metadata.Size
	c.updateAccessList(key)

	c.evictIfNeeded()

	if err := c.saveIndex(); err != nil {
		log.Error("failed to save cache index", "error", err)
	}

	return nil
}

func (c *Cache) ReadData(key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.index[key]
	if !exists {
		return nil, fmt.Errorf("cache entry not found")
	}

	entry.Metadata.LastAccessedAt = time.Now()
	c.updateAccessList(key)

	if err := c.saveMetadata(key, entry.Metadata); err != nil {
		log.Warn("failed to update metadata", "error", err)
	}

	data, err := os.ReadFile(entry.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	return data, nil
}

func (c *Cache) UpdateMetadata(key string, metadata Metadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.index[key]
	if !exists {
		return fmt.Errorf("cache entry not found")
	}

	entry.Metadata = metadata
	return c.saveMetadata(key, metadata)
}

func (c *Cache) saveMetadata(key string, metadata Metadata) error {
	metaPath := filepath.Join(c.dir, key+".meta")
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	return os.WriteFile(metaPath, metaBytes, 0644)
}

func (c *Cache) updateAccessList(key string) {
	for i, k := range c.accessList {
		if k == key {
			c.accessList = append(c.accessList[:i], c.accessList[i+1:]...)
			break
		}
	}
	c.accessList = append(c.accessList, key)
}

func (c *Cache) evictIfNeeded() {
	for c.currentBytes > c.maxBytes && len(c.accessList) > 0 {
		lruKey := c.accessList[0]
		c.accessList = c.accessList[1:]

		entry, exists := c.index[lruKey]
		if !exists {
			continue
		}

		os.Remove(entry.FilePath)
		os.Remove(entry.FilePath + ".meta")

		c.currentBytes -= entry.Metadata.Size
		delete(c.index, lruKey)

		log.Info("evicted cache entry", "key", lruKey, "size", entry.Metadata.Size)
	}
}

func (c *Cache) loadIndex() error {
	indexPath := filepath.Join(c.dir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var index struct {
		Entries    map[string]*CacheEntry `json:"entries"`
		AccessList []string               `json:"access_list"`
	}

	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}

	c.index = index.Entries
	c.accessList = index.AccessList

	for _, entry := range c.index {
		c.currentBytes += entry.Metadata.Size
	}

	return nil
}

func (c *Cache) saveIndex() error {
	indexPath := filepath.Join(c.dir, "index.json")
	index := struct {
		Entries    map[string]*CacheEntry `json:"entries"`
		AccessList []string               `json:"access_list"`
	}{
		Entries:    c.index,
		AccessList: c.accessList,
	}

	data, err := json.Marshal(index)
	if err != nil {
		return err
	}

	return os.WriteFile(indexPath, data, 0644)
}

func (c *Cache) CheckConditional(key string, req *http.Request) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.index[key]
	if !exists {
		return false
	}

	if time.Since(entry.Metadata.CreatedAt) > c.ttl {
		return false
	}

	ifNoneMatch := req.Header.Get("If-None-Match")
	if ifNoneMatch != "" && entry.Metadata.Headers["ETag"] == ifNoneMatch {
		return true
	}

	ifModifiedSince := req.Header.Get("If-Modified-Since")
	if ifModifiedSince != "" {
		t, err := http.ParseTime(ifModifiedSince)
		if err == nil {
			lastModified := entry.Metadata.Headers["Last-Modified"]
			if lastModified != "" {
				lmt, err := http.ParseTime(lastModified)
				if err == nil && !lmt.After(t) {
					return true
				}
			}
		}
	}

	return false
}

func (c *Cache) GetMetadata(key string) (*Metadata, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.index[key]
	if !exists {
		return nil, fmt.Errorf("cache entry not found")
	}

	metadata := entry.Metadata
	return &metadata, nil
}

func (c *Cache) WriteResponse(w http.ResponseWriter, key string, ttlSeconds int) error {
	data, err := c.ReadData(key)
	if err != nil {
		return err
	}

	metadata, err := c.GetMetadata(key)
	if err != nil {
		return err
	}

	for k, v := range metadata.Headers {
		w.Header().Set(k, v)
	}

	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", ttlSeconds))
	w.WriteHeader(metadata.StatusCode)

	_, err = w.Write(data)
	return err
}

func ExtractHeaders(resp *http.Response) map[string]string {
	headers := make(map[string]string)
	for _, key := range []string{"Content-Type", "ETag", "Last-Modified", "Cache-Control", "Content-Length"} {
		if val := resp.Header.Get(key); val != "" {
			headers[key] = val
		}
	}
	return headers
}

func ReadResponseBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
