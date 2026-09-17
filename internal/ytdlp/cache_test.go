package ytdlp

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func mediaExpiring(in time.Duration) Media {
	return Media{URL: "https://example.test/a", ExpiresAt: time.Now().Add(in)}
}

func TestCacheRoundTrip(t *testing.T) {
	var c Cache
	const url = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

	if _, ok := c.Get(url); ok {
		t.Fatal("an empty cache returned a hit")
	}

	want := mediaExpiring(time.Hour)
	c.Put(url, want)

	got, ok := c.Get(url)
	if !ok {
		t.Fatal("Get missed a resolution that was just stored")
	}
	if got.URL != want.URL {
		t.Errorf("URL = %q, want %q", got.URL, want.URL)
	}
}

// An address good for another thirty seconds is not worth handing out: the
// decode using it would outlive it.
func TestCacheDropsWhatIsAboutToExpire(t *testing.T) {
	var c Cache
	const url = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

	c.Put(url, mediaExpiring(30*time.Second))
	if _, ok := c.Get(url); ok {
		t.Error("cached an address that expires inside the safety margin")
	}

	c.Put(url, mediaExpiring(-time.Hour))
	if _, ok := c.Get(url); ok {
		t.Error("cached an address that had already expired")
	}
}

func TestCacheForget(t *testing.T) {
	var c Cache
	const url = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

	c.Put(url, mediaExpiring(time.Hour))
	c.Forget(url)

	if _, ok := c.Get(url); ok {
		t.Error("Get returned a resolution after Forget")
	}
}

// A long playlist must not grow the cache without bound.
func TestCacheIsBounded(t *testing.T) {
	var c Cache
	for i := range maxEntries * 4 {
		c.Put(fmt.Sprintf("https://example.test/%d", i), mediaExpiring(time.Hour))
	}

	c.mu.Lock()
	size := len(c.entries)
	c.mu.Unlock()

	if size > maxEntries {
		t.Errorf("cache holds %d entries, want at most %d", size, maxEntries)
	}
	if size == 0 {
		t.Error("cache evicted everything")
	}
}

func TestUsableUntil(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	t.Run("uses the expiry the address carries", func(t *testing.T) {
		media := Media{ExpiresAt: now.Add(6 * time.Hour)}
		want := now.Add(6*time.Hour - safetyMargin)
		if got := usableUntil(media, now); !got.Equal(want) {
			t.Errorf("usableUntil = %v, want %v", got, want)
		}
	})

	t.Run("falls back when there is no expiry", func(t *testing.T) {
		if got, want := usableUntil(Media{}, now), now.Add(fallbackTTL); !got.Equal(want) {
			t.Errorf("usableUntil = %v, want %v", got, want)
		}
	})

	t.Run("an expired address is not usable", func(t *testing.T) {
		media := Media{ExpiresAt: now.Add(-time.Hour)}
		if got := usableUntil(media, now); got.After(now) {
			t.Errorf("usableUntil = %v, want no later than %v", got, now)
		}
	})
}

func TestCacheIsSafeForConcurrentUse(t *testing.T) {
	var c Cache
	var wg sync.WaitGroup

	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			url := fmt.Sprintf("https://example.test/%d", i%4)
			for range 50 {
				c.Put(url, mediaExpiring(time.Hour))
				c.Get(url)
				c.Forget(url)
			}
		}()
	}
	wg.Wait()
}
