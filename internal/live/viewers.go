package live

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The concurrent-viewer count for a live broadcast.
//
// WHAT IS COUNTED. Every fetch of a live stream's HLS PLAYLIST — not the
// segments. A player refetches the playlist roughly every target duration (2 s
// with the shipped ingest config) for as long as it is watching and stops the
// moment it is not, so the playlist is the only request in the live path that
// means "someone is still watching right now". Counting segments would count
// bandwidth, and counting `GET /live/{id}` would count people reading the page
// with the video paused.
//
// HOW SESSIONS ARE DISTINGUISHED. A keyed, day-scoped digest of the viewer, the
// same construction internal/qoe stores and for the same reason: a bare hash of
// an IP is reversible against the whole IPv4 space in minutes. The live digest
// gets its OWN domain-separation label, so a live viewer digest and a QoE viewer
// digest for the same person on the same day are unrelated values and one
// dataset can never be joined against the other.
//
// It is deliberately NOT the QoE beacon's `?s=` session id, which was the other
// candidate. That id is minted by the client and sent in a body the client
// controls: a count keyed on it is a count anyone can inflate by rotating a
// UUID, and a "concurrent viewers" number that a viewer can set is worse than no
// number. The digest is derived server-side from the request principal and
// cannot be steered.
//
// THE PRIVACY RULE (A35/A13). An opted-out signed-in viewer COUNTS but leaves no
// account-derived value: they are digested as the anonymous visitor they asked
// to be treated as. Note what the alternative would have been — QoE's rule is to
// store the EMPTY digest for an opted-out viewer, and copying that here would
// collapse every opted-out viewer in the instance into ONE set member and
// under-report them. "Opted-out viewers count" is the ruling; being counted
// anonymously is how it is honoured. The falls-back-to-IP digest is also far
// more ephemeral here than in QoE: it lives in Redis for the rolling window and
// nothing reads it back except as a cardinality.
//
// MEMORY. One Redis sorted set per LIVE stream, member = digest, score = unix
// seconds. Every write prunes the members that have aged out of the window and
// re-arms a TTL, so a stream that stops being watched evicts itself even if it
// is never terminated cleanly, and the whole structure is bounded by
// (streams currently live) x (distinct viewers in the last window). No Redis, no
// count: the field is simply absent rather than zero, because "0 viewers" and "I
// cannot tell" are different statements.

// viewerWindow is how long one playlist fetch keeps a viewer in the count.
//
// 90 seconds, against a ~2 s playlist refresh. It is deliberately far longer
// than the refresh interval rather than a tight multiple of it: a player that
// stalls, a phone that locks for a moment, or a viewer on a congested link must
// not flicker out of the count and back in. The cost of being generous is that
// the number lags a mass exit by up to 90 s, which is the right way round — a
// count that undershoots during a broadcast is more misleading to a creator than
// one that decays a minute late after it.
const viewerWindow = 90 * time.Second

// viewerCountRefresh is the "at most every N seconds" bound on the READ. The
// count is rendered on the public "Live now" rail as well as on the stream page,
// so an instance with a busy rail would otherwise pay one Redis round trip per
// card per request. Ten seconds is well inside the window's own resolution, so
// the cached answer is never more stale than the window already makes it.
const viewerCountRefresh = 10 * time.Second

// viewerKeyTTL is the Redis key's own expiry: the window plus a margin, re-armed
// on every write. It is the backstop that makes an abandoned stream's set
// disappear without anyone cleaning up after it.
const viewerKeyTTL = viewerWindow + time.Minute

// ViewerDigestDomain is the domain-separation label for the live viewer digest.
// It is exported so the wiring in cmd/api names it once and a test can assert it
// differs from the QoE label — the property that keeps the two datasets
// unjoinable. Changing the trailing version re-derives the key and makes every
// in-flight digest incomparable, which for a 90-second window costs one window.
const ViewerDigestDomain = "vidra/live-viewer-digest/v1"

// viewerSetKey is the Redis key holding one stream's current viewers.
func viewerSetKey(streamID uuid.UUID) string { return "live:viewers:" + streamID.String() }

// viewerStore is the three operations this counter needs from Redis, and
// nothing else.
//
// It exists so the POLICY here — the window, the read cache, the degradation
// when the store is unreachable, the reset on end-of-session — is testable
// without a Redis, while the store itself stays a thin adapter with no logic to
// get wrong. redis.Cmdable is a 250-method interface; faking it to assert that
// an abandoned stream evicts itself would be a day's typing and would prove
// nothing about the sorted set.
type viewerStore interface {
	// Seen records member as present at `now`, forgets members older than
	// `cutoff`, and re-arms the key's TTL — one round trip.
	Seen(ctx context.Context, key, member string, now, cutoff int64, ttl time.Duration) error
	// CountSince returns how many members have a score at or after cutoff.
	CountSince(ctx context.Context, key string, cutoff int64) (int64, error)
	// Drop removes the key entirely.
	Drop(ctx context.Context, key string) error
}

// redisViewerStore is the sorted-set implementation: member = viewer digest,
// score = unix seconds.
type redisViewerStore struct{ client redis.Cmdable }

func (r redisViewerStore) Seen(ctx context.Context, key, member string, now, cutoff int64, ttl time.Duration) error {
	// One pipeline for the three commands. The prune rides every write rather
	// than a sweep of its own: the set only needs to be correct when it is read,
	// and a write is the only thing that can make it wrong.
	pipe := r.client.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: member})
	pipe.ZRemRangeByScore(ctx, key, "-inf", "("+strconv.FormatInt(cutoff, 10))
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r redisViewerStore) CountSince(ctx context.Context, key string, cutoff int64) (int64, error) {
	return r.client.ZCount(ctx, key, strconv.FormatInt(cutoff, 10), "+inf").Result()
}

func (r redisViewerStore) Drop(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

// ViewerCounter tracks and reports concurrent viewers per live stream.
//
// A nil *ViewerCounter is a valid receiver whose Touch is a no-op and whose
// Count reports "unknown", so an instance with no Redis wires nil and every
// caller stays unconditional.
type ViewerCounter struct {
	store viewerStore
	now   func() time.Time

	// The read-side cache. Per stream, so one hot stream does not stale out the
	// others, and guarded by a plain mutex because the map is tiny (one entry
	// per stream that has been READ recently) and the critical section is a map
	// lookup.
	mu     sync.Mutex
	cached map[uuid.UUID]viewerSnapshot
}

type viewerSnapshot struct {
	count int64
	at    time.Time
}

// NewViewerCounter builds a counter over a Redis client. A nil client yields
// nil — the documented "no count on this instance" state.
func NewViewerCounter(client redis.Cmdable) *ViewerCounter {
	if client == nil {
		return nil
	}
	return newViewerCounter(redisViewerStore{client: client})
}

// newViewerCounter builds a counter over any store — the seam the unit tests
// use.
func newViewerCounter(store viewerStore) *ViewerCounter {
	if store == nil {
		return nil
	}
	return &ViewerCounter{store: store, now: time.Now, cached: map[uuid.UUID]viewerSnapshot{}}
}

// SetNowFunc pins the clock for tests.
func (v *ViewerCounter) SetNowFunc(f func() time.Time) {
	if v != nil && f != nil {
		v.now = f
	}
}

// Touch records that the viewer identified by digest is watching streamID now.
//
// It is fire-and-forget by design: this runs on the live playlist path, which is
// the hottest request in a broadcast, and a Redis hiccup must degrade the COUNT
// rather than the PLAYBACK. Errors are returned for the caller to log at debug
// level and for tests to assert on; no caller may fail a playlist on one.
//
// An empty digest is ignored rather than counted under a shared member — see the
// privacy note above for why the empty-digest fallback is wrong here.
func (v *ViewerCounter) Touch(ctx context.Context, streamID uuid.UUID, digest string) error {
	if v == nil || digest == "" {
		return nil
	}
	now := v.now()
	return v.store.Seen(ctx, viewerSetKey(streamID), digest,
		now.Unix(), now.Add(-viewerWindow).Unix(), viewerKeyTTL)
}

// Count returns the number of distinct viewers of streamID within the window,
// and whether the answer is known at all.
//
// The (int64, bool) shape is the point: false means "this instance cannot tell"
// — no Redis, or Redis is not answering — and a caller must OMIT the field
// rather than render 0, which would tell a creator mid-broadcast that nobody is
// watching.
func (v *ViewerCounter) Count(ctx context.Context, streamID uuid.UUID) (int64, bool) {
	if v == nil {
		return 0, false
	}
	now := v.now()

	v.mu.Lock()
	if snap, ok := v.cached[streamID]; ok && now.Sub(snap.at) < viewerCountRefresh {
		v.mu.Unlock()
		return snap.count, true
	}
	v.mu.Unlock()

	// Counted with a range rather than ZCARD so a stale member that no write has
	// pruned yet — a stream nobody is watching any more — cannot inflate the
	// answer. The read is therefore correct on its own terms and does not depend
	// on a write having happened recently.
	n, err := v.store.CountSince(ctx, viewerSetKey(streamID), now.Add(-viewerWindow).Unix())
	if err != nil {
		// Serve the last known answer rather than nothing: a count that is a few
		// seconds stale is a better answer during a Redis blip than a number
		// that vanishes off a creator's page and comes back.
		v.mu.Lock()
		defer v.mu.Unlock()
		if snap, ok := v.cached[streamID]; ok && now.Sub(snap.at) < viewerKeyTTL {
			return snap.count, true
		}
		return 0, false
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.cached[streamID] = viewerSnapshot{count: n, at: now}
	// Bound the cache the same way the sets are bounded: entries older than the
	// key TTL describe streams that are no longer live and are dropped on the
	// next read that passes through here.
	for id, snap := range v.cached {
		if now.Sub(snap.at) > viewerKeyTTL {
			delete(v.cached, id)
		}
	}
	return n, true
}

// Reset drops a stream's viewer set outright. Called when a broadcast ends
// (stop hook, watchdog or termination) so a stream that goes live again starts
// from zero instead of inheriting the last session's tail — 90 seconds of
// phantom viewers on a brand-new broadcast is exactly the kind of number nobody
// can explain later.
func (v *ViewerCounter) Reset(ctx context.Context, streamID uuid.UUID) {
	if v == nil {
		return
	}
	_ = v.store.Drop(ctx, viewerSetKey(streamID))
	v.mu.Lock()
	delete(v.cached, streamID)
	v.mu.Unlock()
}
