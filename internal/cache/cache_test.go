package cache

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateKey(t *testing.T) {
	c := &Cache{}

	tests := []struct {
		name     string
		path     string
		query1   map[string]string
		query2   map[string]string
		shouldEq bool
	}{
		{
			name:     "identical queries produce same key",
			path:     "/avatar/test",
			query1:   map[string]string{"s": "80", "d": "identicon"},
			query2:   map[string]string{"s": "80", "d": "identicon"},
			shouldEq: true,
		},
		{
			name:     "different order same params produce same key",
			path:     "/avatar/test",
			query1:   map[string]string{"d": "identicon", "s": "80"},
			query2:   map[string]string{"s": "80", "d": "identicon"},
			shouldEq: true,
		},
		{
			name:     "different values produce different keys",
			path:     "/avatar/test",
			query1:   map[string]string{"s": "80"},
			query2:   map[string]string{"s": "100"},
			shouldEq: false,
		},
		{
			name:     "different paths produce different keys",
			path:     "/avatar/test1",
			query1:   map[string]string{"s": "80"},
			query2:   map[string]string{"s": "80"},
			shouldEq: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := c.GenerateKey(tt.path, tt.query1)
			var key2 string
			if tt.name == "different paths produce different keys" {
				key2 = c.GenerateKey("/avatar/test2", tt.query2)
			} else {
				key2 = c.GenerateKey(tt.path, tt.query2)
			}

			if tt.shouldEq && key1 != key2 {
				t.Errorf("expected keys to be equal, got %s != %s", key1, key2)
			}
			if !tt.shouldEq && key1 == key2 {
				t.Errorf("expected keys to be different, got %s == %s", key1, key2)
			}
		})
	}
}

func TestCacheTTL(t *testing.T) {
	tmpDir := t.TempDir()
	ttl := 100 * time.Millisecond

	c, err := New(tmpDir, ttl, 1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	key := "testkey"
	data := []byte("test data")
	metadata := Metadata{
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		Headers:        map[string]string{"Content-Type": "text/plain"},
		StatusCode:     200,
	}

	if err := c.Set(key, data, metadata); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	entry, valid := c.Get(key)
	if !valid {
		t.Error("expected cache entry to be valid immediately after set")
	}
	if entry == nil {
		t.Fatal("expected cache entry to exist")
	}

	time.Sleep(150 * time.Millisecond)

	entry, valid = c.Get(key)
	if valid {
		t.Error("expected cache entry to be invalid after TTL expiration")
	}
	if entry == nil {
		t.Error("expected cache entry to still exist but be expired")
	}
}

func TestCacheSetAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	ttl := 1 * time.Hour

	c, err := New(tmpDir, ttl, 1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	key := "testkey"
	data := []byte("test data content")
	metadata := Metadata{
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		Headers: map[string]string{
			"Content-Type": "image/png",
			"ETag":         `"abc123"`,
		},
		StatusCode: 200,
	}

	if err := c.Set(key, data, metadata); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	retrieved, err := c.ReadData(key)
	if err != nil {
		t.Fatalf("failed to read data: %v", err)
	}

	if string(retrieved) != string(data) {
		t.Errorf("expected %s, got %s", string(data), string(retrieved))
	}

	retrievedMeta, err := c.GetMetadata(key)
	if err != nil {
		t.Fatalf("failed to get metadata: %v", err)
	}

	if retrievedMeta.Headers["Content-Type"] != "image/png" {
		t.Errorf("expected Content-Type image/png, got %s", retrievedMeta.Headers["Content-Type"])
	}
}

func TestCacheEviction(t *testing.T) {
	tmpDir := t.TempDir()
	ttl := 1 * time.Hour
	maxBytes := int64(100)

	c, err := New(tmpDir, ttl, maxBytes)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	data1 := make([]byte, 40)
	data2 := make([]byte, 40)
	data3 := make([]byte, 40)

	metadata := Metadata{
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		Headers:        map[string]string{},
		StatusCode:     200,
	}

	if err := c.Set("key1", data1, metadata); err != nil {
		t.Fatalf("failed to set key1: %v", err)
	}

	if err := c.Set("key2", data2, metadata); err != nil {
		t.Fatalf("failed to set key2: %v", err)
	}

	if err := c.Set("key3", data3, metadata); err != nil {
		t.Fatalf("failed to set key3: %v", err)
	}

	if _, exists := c.index["key1"]; exists {
		t.Error("expected key1 to be evicted")
	}

	if _, exists := c.index["key2"]; !exists {
		t.Error("expected key2 to still exist")
	}

	if _, exists := c.index["key3"]; !exists {
		t.Error("expected key3 to still exist")
	}
}

func TestCheckConditional(t *testing.T) {
	tmpDir := t.TempDir()
	ttl := 1 * time.Hour

	c, err := New(tmpDir, ttl, 1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	key := "testkey"
	data := []byte("test data")
	etag := `"abc123"`
	lastModified := time.Now().UTC().Format(http.TimeFormat)

	metadata := Metadata{
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		Headers: map[string]string{
			"ETag":          etag,
			"Last-Modified": lastModified,
		},
		StatusCode: 200,
	}

	if err := c.Set(key, data, metadata); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	tests := []struct {
		name     string
		header   string
		value    string
		expected bool
	}{
		{
			name:     "matching ETag",
			header:   "If-None-Match",
			value:    etag,
			expected: true,
		},
		{
			name:     "non-matching ETag",
			header:   "If-None-Match",
			value:    `"xyz789"`,
			expected: false,
		},
		{
			name:     "matching Last-Modified",
			header:   "If-Modified-Since",
			value:    lastModified,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			req.Header.Set(tt.header, tt.value)

			result := c.CheckConditional(key, req)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestCachePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	ttl := 1 * time.Hour

	c1, err := New(tmpDir, ttl, 1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	key := "testkey"
	data := []byte("persistent data")
	metadata := Metadata{
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		Headers:        map[string]string{"Content-Type": "text/plain"},
		StatusCode:     200,
	}

	if err := c1.Set(key, data, metadata); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	c2, err := New(tmpDir, ttl, 1024*1024)
	if err != nil {
		t.Fatalf("failed to create second cache instance: %v", err)
	}

	entry, valid := c2.Get(key)
	if !valid {
		t.Error("expected cache entry to be valid after reload")
	}
	if entry == nil {
		t.Fatal("expected cache entry to exist after reload")
	}

	retrieved, err := c2.ReadData(key)
	if err != nil {
		t.Fatalf("failed to read data after reload: %v", err)
	}

	if string(retrieved) != string(data) {
		t.Errorf("expected %s, got %s", string(data), string(retrieved))
	}
}

func TestNew(t *testing.T) {
	tmpDir := filepath.Join(t.TempDir(), "newcache")

	c, err := New(tmpDir, time.Hour, 1024*1024)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	if c.dir != tmpDir {
		t.Errorf("expected dir %s, got %s", tmpDir, c.dir)
	}

	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Error("expected cache directory to be created")
	}
}
