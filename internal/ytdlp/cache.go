package ytdlp

import (
	"sync"
	"time"
)

// safetyMargin keeps a resolution from being handed out so close to its expiry
// that the decode using it outlives it.
const safetyMargin = 2 * time.Minute

// fallbackTTL applies when an address carries no expiry of its own. It is far
// shorter than the several hours these usually last, because guessing high
// risks handing out something already dead.
const fallbackTTL = 20 * time.Minute

// maxEntries bounds the cache. Seeking and replaying touch one track at a time,
// so this only has to be large enough that moving around a queue does not keep
// re-resolving; a two thousand entry playlist must not grow it without bound.
const maxEntries = 32

// Cache remembers resolved addresses for as long as they are good for.
//
// Resolving costs a subprocess and a round trip to YouTube, and every seek
// restarts playback — so without this, dragging the seek bar on a link would
// stall for a second or two each time.
//
// It is keyed on the video's page URL rather than on anything the queue owns,
// so the same video queued twice resolves once and removing and re-adding a
// track does not throw the resolution away. The zero value is ready to use.
type Cache struct {
	mu      sync.Mutex
	entries map[string]entry
}

type entry struct {
	media Media
	until time.Time
}

// Get returns a resolution that is still good, if there is one.
func (c *Cache) Get(url string) (Media, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	found, ok := c.entries[url]
	if !ok {
		return Media{}, false
	}
	if !time.Now().Before(found.until) {
		delete(c.entries, url)
		return Media{}, false
	}
	return found.media, true
}

// Put remembers a resolution until shortly before it expires. One that has
// already expired is not kept at all.
func (c *Cache) Put(url string, media Media) {
	until := usableUntil(media, time.Now())

	c.mu.Lock()
	defer c.mu.Unlock()

	if !time.Now().Before(until) {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]entry)
	}
	c.evictLocked()
	c.entries[url] = entry{media: media, until: until}
}

// Forget drops a resolution, which is what a caller does when playing it failed.
func (c *Cache) Forget(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, url)
}

// evictLocked makes room, dropping anything already expired first and then, if
// that was not enough, whichever entry the map offers. Which one goes does not
// matter: losing a resolution costs one yt-dlp run, not correctness.
func (c *Cache) evictLocked() {
	now := time.Now()
	for url, found := range c.entries {
		if !now.Before(found.until) {
			delete(c.entries, url)
		}
	}
	for url := range c.entries {
		if len(c.entries) < maxEntries {
			return
		}
		delete(c.entries, url)
	}
}

// usableUntil is when a resolution stops being worth handing out.
func usableUntil(media Media, now time.Time) time.Time {
	if media.ExpiresAt.IsZero() {
		return now.Add(fallbackTTL)
	}
	return media.ExpiresAt.Add(-safetyMargin)
}
