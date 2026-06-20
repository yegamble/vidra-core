package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Deduper records first-occurrence-within-a-window using Redis SETNX. It backs
// view-count abuse protection: the first ping for a (video, viewer) key in the
// window counts, repeats within it do not. It satisfies video.ViewDeduper.
type Deduper struct {
	client redis.Cmdable
}

// NewDeduper builds a Deduper over a Redis client.
func NewDeduper(client redis.Cmdable) *Deduper {
	return &Deduper{client: client}
}

// First reports whether key is seen for the first time within window. It sets
// key (value "1") with the window TTL only if absent; a true result means the
// caller should count the event.
func (d *Deduper) First(ctx context.Context, key string, window time.Duration) (bool, error) {
	return d.client.SetNX(ctx, key, 1, window).Result()
}
