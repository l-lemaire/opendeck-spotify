// Package art fetches and caches album covers. Spotify serves them from
// https://i.scdn.co as JPEG; the plugin needs them decoded, at key size,
// and must not re-download the same cover on every redraw.
package art

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder with image.Decode
	_ "image/png"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// Cache keeps the last few decoded covers.
type Cache struct {
	http *http.Client
	log  *log.Logger

	mu    sync.Mutex
	items map[string]image.Image
	order []string // insertion order, for eviction
	limit int
}

// New returns a cache keeping up to limit covers (8 if zero).
func New(logger *log.Logger, limit int) *Cache {
	if limit <= 0 {
		limit = 8
	}
	return &Cache{
		http:  &http.Client{Timeout: 10 * time.Second},
		log:   logger,
		items: map[string]image.Image{},
		limit: limit,
	}
}

// Get returns the cover at url, fetching it on a miss.
func (c *Cache) Get(ctx context.Context, url string) (image.Image, error) {
	if url == "" {
		return nil, fmt.Errorf("art: empty url")
	}
	c.mu.Lock()
	img, ok := c.items[url]
	c.mu.Unlock()
	if ok {
		return img, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("art: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("art: HTTP %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("art: %w", err)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("art: decode %s: %w", url, err)
	}
	if c.log != nil {
		c.log.Printf("art: fetched %s (%s, %dx%d, %d bytes)", url, format, img.Bounds().Dx(), img.Bounds().Dy(), len(data))
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[url]; !exists {
		c.items[url] = img
		c.order = append(c.order, url)
		for len(c.order) > c.limit {
			delete(c.items, c.order[0])
			c.order = c.order[1:]
		}
	}
	return img, nil
}
