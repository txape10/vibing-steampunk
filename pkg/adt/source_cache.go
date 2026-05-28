package adt

import (
	"strings"
	"sync"
	"time"
)

const sourceCacheTTL = 10 * time.Minute

type cacheEntry struct {
	source string
	expiry time.Time
}

// SourceCache is an in-memory, write-invalidating cache for ABAP source reads.
// Keyed by "TYPE:NAME:METHOD:INCLUDE:PARENT"; invalidated by object name on any write.
type SourceCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

func newSourceCache() *SourceCache {
	return &SourceCache{
		entries: make(map[string]cacheEntry),
		ttl:     sourceCacheTTL,
	}
}

func (sc *SourceCache) cacheKey(objectType, name, method, include, parent string) string {
	return objectType + ":" + name + ":" + method + ":" + include + ":" + parent
}

func (sc *SourceCache) get(objectType, name, method, include, parent string) (string, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	e, ok := sc.entries[sc.cacheKey(objectType, name, method, include, parent)]
	if !ok || time.Now().After(e.expiry) {
		return "", false
	}
	return e.source, true
}

func (sc *SourceCache) set(objectType, name, method, include, parent, source string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.entries[sc.cacheKey(objectType, name, method, include, parent)] = cacheEntry{
		source: source,
		expiry: time.Now().Add(sc.ttl),
	}
}

// InvalidateByName removes all entries for the given object name (case-insensitive).
func (sc *SourceCache) InvalidateByName(name string) {
	name = strings.ToUpper(name)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for k := range sc.entries {
		// Key format: TYPE:NAME:method:include:parent
		if colon := strings.Index(k, ":"); colon >= 0 {
			rest := k[colon+1:]
			if colon2 := strings.Index(rest, ":"); colon2 >= 0 {
				if strings.ToUpper(rest[:colon2]) == name {
					delete(sc.entries, k)
				}
			}
		}
	}
}

// InvalidateByURL derives the object name from an ADT object URL and invalidates all
// cache entries for that object. Handles class includes (extracts class name, not
// the include component name).
func (sc *SourceCache) InvalidateByURL(objectURL string) {
	sc.InvalidateByName(cacheNameFromURL(objectURL))
}

// cacheNameFromURL extracts the ABAP object name (uppercase) from an ADT URL for cache keying.
// For class includes (/oo/classes/{name}/includes/...) returns the class name, not the include type.
func cacheNameFromURL(objectURL string) string {
	// /sap/bc/adt/oo/classes/{name}[/includes/...] → class name
	if idx := strings.Index(objectURL, "/oo/classes/"); idx >= 0 {
		rest := objectURL[idx+len("/oo/classes/"):]
		if slash := strings.Index(rest, "/"); slash > 0 {
			return strings.ToUpper(rest[:slash])
		}
		return strings.ToUpper(rest)
	}
	// All other types: last non-empty path segment
	trimmed := strings.TrimRight(objectURL, "/")
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
		return strings.ToUpper(trimmed[slash+1:])
	}
	return strings.ToUpper(trimmed)
}
