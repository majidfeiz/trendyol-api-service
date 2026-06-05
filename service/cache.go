package service

import (
	"sync"
	"time"

	"trendyol-api-service/models"
)

type cache struct {
	mu      sync.RWMutex
	entries map[int64]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	product   *models.Product
	expiresAt time.Time
}

func newCache(ttl time.Duration) *cache {
	c := &cache{
		entries: make(map[int64]*cacheEntry),
		ttl:     ttl,
	}
	go c.evictLoop()
	return c
}

func (c *cache) get(id int64) (*models.Product, bool) {
	c.mu.RLock()
	e, ok := c.entries[id]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.product, true
}

func (c *cache) set(id int64, p *models.Product) {
	c.mu.Lock()
	c.entries[id] = &cacheEntry{
		product:   p,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *cache) evictLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		for id, e := range c.entries {
			if now.After(e.expiresAt) {
				delete(c.entries, id)
			}
		}
		c.mu.Unlock()
	}
}
