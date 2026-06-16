package runtime

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

var nonWord = regexp.MustCompile(`[^a-z0-9\s]+`)

type cacheEntry struct {
	AgentName  string
	Normalized string
	Text       string
	Provider   string
}

type memorySemanticCache struct {
	maxEntries int
	mu         sync.RWMutex
	entries    []cacheEntry
}

func newMemorySemanticCache(maxEntries int) *memorySemanticCache {
	if maxEntries <= 0 {
		maxEntries = 128
	}
	return &memorySemanticCache{maxEntries: maxEntries, entries: make([]cacheEntry, 0, maxEntries)}
}

func (c *memorySemanticCache) Lookup(agentName, prompt string, threshold float64) (cacheEntry, bool) {
	normalized := normalize(prompt)
	c.mu.RLock()
	defer c.mu.RUnlock()
	bestScore := 0.0
	best := cacheEntry{}
	for _, entry := range c.entries {
		if entry.AgentName != agentName {
			continue
		}
		score := jaccard(entry.Normalized, normalized)
		if score >= threshold && score > bestScore {
			bestScore = score
			best = entry
		}
	}
	if bestScore == 0 {
		return cacheEntry{}, false
	}
	return best, true
}

func (c *memorySemanticCache) Store(agentName, prompt, text, provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := cacheEntry{AgentName: agentName, Normalized: normalize(prompt), Text: text, Provider: provider}
	if len(c.entries) >= c.maxEntries {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, entry)
}

func normalize(s string) string {
	s = strings.ToLower(s)
	s = nonWord.ReplaceAllString(s, " ")
	fields := strings.Fields(s)
	sort.Strings(fields)
	return strings.Join(fields, " ")
}

func jaccard(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	as := strings.Fields(a)
	bs := strings.Fields(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	am := make(map[string]struct{}, len(as))
	bm := make(map[string]struct{}, len(bs))
	for _, t := range as {
		am[t] = struct{}{}
	}
	for _, t := range bs {
		bm[t] = struct{}{}
	}
	inter := 0
	for k := range am {
		if _, ok := bm[k]; ok {
			inter++
		}
	}
	union := len(am) + len(bm) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
