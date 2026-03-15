package samba

import (
	"sync"
	"time"
)

// cache는 samba-tool 결과를 단기 캐싱합니다.
// DNS/FSMO처럼 samba-tool 호출이 느린 경우에 사용합니다.
type cacheEntry struct {
	value     interface{}
	expiresAt time.Time
}

type resultCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

var globalCache = &resultCache{entries: make(map[string]cacheEntry)}

func (c *resultCache) get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.value, true
}

func (c *resultCache) set(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{value: value, expiresAt: time.Now().Add(ttl)}
}

func (c *resultCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// cachedResult는 fn 결과를 ttl 동안 캐싱합니다.
func cachedResult(key string, ttl time.Duration, fn func() Result) Result {
	if v, ok := globalCache.get(key); ok {
		return v.(Result)
	}
	r := fn()
	if r.Success {
		globalCache.set(key, r, ttl)
	}
	return r
}

// cachedMap은 map 결과를 ttl 동안 캐싱합니다.
func cachedMap(key string, ttl time.Duration, fn func() map[string]interface{}) map[string]interface{} {
	if v, ok := globalCache.get(key); ok {
		return v.(map[string]interface{})
	}
	r := fn()
	if r["success"] == true {
		globalCache.set(key, r, ttl)
	}
	return r
}
