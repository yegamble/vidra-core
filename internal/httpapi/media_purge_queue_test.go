package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/cdnpurge"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// These tests cover the half of the purge seam that OUTLIVES the request: the
// account-deletion snapshot, the instance-wide download revocation, and the
// persisted retry for a URL the edge refused. Each of the three was a measured
// zero-purge family in the A32/A33 edge run (docs/release-readiness.md).
//
// They assert what was ENQUEUED, not what was invalidated, and that is the
// contract at this layer: the queue's own drain is covered in
// internal/cdnpurge, and a handler that queued the wrong URL set would produce a
// perfectly successful run against an edge that keeps serving the media.

// queueRecorder is a cdnpurge.Repository that records enqueues and does nothing
// else. Only the enqueue half is exercised here; the drain is the queue
// package's own subject.
type queueRecorder struct {
	mu    sync.Mutex
	urls  [][]string
	kinds []string
	walks int
}

func (q *queueRecorder) EnqueueCDNPurgeURLs(_ context.Context, arg sqlcgen.EnqueueCDNPurgeURLsParams) (uuid.UUID, error) {
	var paths []string
	if err := json.Unmarshal(arg.Urls, &paths); err != nil {
		return uuid.Nil, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	sort.Strings(paths)
	q.urls = append(q.urls, paths)
	q.kinds = append(q.kinds, arg.Reason)
	return uuid.New(), nil
}

func (q *queueRecorder) EnqueueCDNPurgeDownloadsRevoked(context.Context) (uuid.UUID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.walks++
	return uuid.New(), nil
}

func (q *queueRecorder) ClaimDueCDNPurgeJobs(context.Context, sqlcgen.ClaimDueCDNPurgeJobsParams) ([]sqlcgen.ClaimDueCDNPurgeJobsRow, error) {
	return nil, nil
}
func (q *queueRecorder) CompleteCDNPurgeJob(context.Context, sqlcgen.CompleteCDNPurgeJobParams) error {
	return nil
}
func (q *queueRecorder) AdvanceCDNPurgeWalk(context.Context, sqlcgen.AdvanceCDNPurgeWalkParams) error {
	return nil
}
func (q *queueRecorder) RescheduleCDNPurgeJob(context.Context, sqlcgen.RescheduleCDNPurgeJobParams) error {
	return nil
}
func (q *queueRecorder) FailCDNPurgeJob(context.Context, sqlcgen.FailCDNPurgeJobParams) error {
	return nil
}
func (q *queueRecorder) DeleteFinishedCDNPurgeJobs(context.Context, sqlcgen.DeleteFinishedCDNPurgeJobsParams) (int64, error) {
	return 0, nil
}
func (q *queueRecorder) ListPublicDownloadableVideoIDs(context.Context, sqlcgen.ListPublicDownloadableVideoIDsParams) ([]uuid.UUID, error) {
	return nil, nil
}
func (q *queueRecorder) ListVideoDownloadFacts(context.Context, uuid.UUID) (sqlcgen.ListVideoDownloadFactsRow, error) {
	return sqlcgen.ListVideoDownloadFactsRow{}, nil
}
func (q *queueRecorder) CDNPurgeJobStats(context.Context) (sqlcgen.CDNPurgeJobStatsRow, error) {
	return sqlcgen.CDNPurgeJobStatsRow{Pending: 2, Running: 1, Failed: 3, OldestPendingAgeSeconds: 4242}, nil
}
func (q *queueRecorder) CDNPurgeRecentFailures(context.Context, int32) ([]sqlcgen.CDNPurgeRecentFailuresRow, error) {
	return nil, nil
}

func (q *queueRecorder) enqueuedURLs() [][]string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([][]string, len(q.urls))
	copy(out, q.urls)
	return out
}

func (q *queueRecorder) walkCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.walks
}

// queuedPurgeServer is purgeServer with the durable queue mounted too.
func queuedPurgeServer(t *testing.T) (*Server, storage.Backend, *transcodeFakeRepo, *purgeRecorder, *queueRecorder) {
	t.Helper()
	opt, rec := testCDNPurge(t)
	queue := &queueRecorder{}
	svc := cdnpurge.NewService(queue, rec.purge)
	if svc == nil {
		t.Fatal("cdnpurge.NewService returned nil with a repo and a purger")
	}
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), []Option{opt, WithCDNPurgeQueue(svc)})
	return srv, blobs, tcRepo, rec, queue
}

// waitForEnqueue waits for at least n enqueued URL sets. Enqueues from a
// detached purge are asynchronous for the same reason the purges are.
func waitForEnqueue(t *testing.T, q *queueRecorder, n int) [][]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := q.enqueuedURLs(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("only %d URL sets were enqueued, want %d", len(q.enqueuedURLs()), n)
	return nil
}

// TestAccountSnapshotNamesEveryServedURL is SC2's URL assertion.
//
// The account cascade removes the channels, the videos and the image rows
// without visiting a single handler that purges, so before this the whole
// account's media stayed at the edge with nothing left in the database to name
// it. The snapshot is exercised directly here because the account service and
// the video/channel services live in different test harnesses; the two-line
// handler wiring around it (snapshot before, enqueue after) is the channel
// cascade's, which has its own tests next door.
func TestAccountSnapshotNamesEveryServedURL(t *testing.T) {
	srv, blobs, tcRepo, _, _ := queuedPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	got, complete := srv.accountEdgePurgePaths(context.Background(), ownerIDFromToken(t, srv, tok))
	if !complete {
		t.Error("the snapshot reported itself incomplete for a fully readable account")
	}
	sort.Strings(got)
	want := wantVideoPurgePaths(t, srv, id)
	// The account's own avatar/banner and the channel's are absent because this
	// fixture sets none: an image the route 404s for has no edge entry and must
	// not spend a purge call.
	if len(got) != len(want) {
		t.Fatalf("snapshot named %d URLs, want %d\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("snapshot differs at %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// The snapshot has to be taken BEFORE the cascade, and this is what makes that
// ordering a property rather than a comment: once the channel is gone the same
// call names nothing at all, so a handler that snapshotted afterwards would
// enqueue an empty set and report a clean takedown.
func TestAccountSnapshotIsEmptyAfterTheCascade(t *testing.T) {
	srv, blobs, tcRepo, _, _ := queuedPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	owner := ownerIDFromToken(t, srv, tok)

	before, _ := srv.accountEdgePurgePaths(context.Background(), owner)
	if len(before) == 0 {
		t.Fatal("the snapshot named nothing for a live account with a public video")
	}
	if r := doJSON(srv, http.MethodDelete, "/api/v1/channels/ada", tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete channel = %d; body=%s", r.Code, r.Body.String())
	}
	after, _ := srv.accountEdgePurgePaths(context.Background(), owner)
	if len(after) != 0 {
		t.Fatalf("after the cascade the snapshot still named %v; the before/after ordering is not load-bearing", after)
	}
}

// An account with nothing public names nothing: the fence is the resolver's
// own, so a draft-only account has never been at the edge.
func TestAccountSnapshotSkipsUnpublishedMedia(t *testing.T) {
	srv, _, _, _, queue := queuedPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	paths, complete := srv.accountEdgePurgePaths(context.Background(), ownerIDFromToken(t, srv, tok))
	if len(paths) != 0 || !complete {
		t.Fatalf("snapshot = (%v, complete=%v) for an account with no public media", paths, complete)
	}
	// ...and an empty set queues no job: a pending row per deleted account with
	// nothing to invalidate would be noise on the operator's depth gauge.
	srv.purgeAccountEdgeCopies(context.Background(), paths, complete)
	time.Sleep(50 * time.Millisecond)
	if got := queue.enqueuedURLs(); len(got) != 0 {
		t.Fatalf("queued %v for an empty snapshot, want nothing", got)
	}
}

// A non-empty snapshot becomes exactly one queued job, under the reason an
// operator reads on the jobs page.
func TestAccountSnapshotIsQueuedAsOneJob(t *testing.T) {
	srv, blobs, tcRepo, _, queue := queuedPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	owner := ownerIDFromToken(t, srv, tok)

	paths, complete := srv.accountEdgePurgePaths(context.Background(), owner)
	srv.purgeAccountEdgeCopies(context.Background(), paths, complete)

	sets := waitForEnqueue(t, queue, 1)
	if len(sets) != 1 {
		t.Fatalf("queued %d jobs, want exactly 1 for one account", len(sets))
	}
	want := wantVideoPurgePaths(t, srv, id)
	if len(sets[0]) != len(want) {
		t.Fatalf("queued %d URLs, want %d", len(sets[0]), len(want))
	}
	if queue.kinds[0] != cdnpurge.ReasonAccountDelete {
		t.Fatalf("queued under reason %q, want %q", queue.kinds[0], cdnpurge.ReasonAccountDelete)
	}
}

// TestInstanceDownloadRevocationQueuesTheWalk is SC3's trigger half. Closing the
// instance gate revokes the download URLs of EVERY public video at once, which
// is a catalogue walk rather than a fan-out — so what the handler must do is
// enqueue exactly one leased job, not thousands of HTTP calls.
func TestInstanceDownloadRevocationQueuesTheWalk(t *testing.T) {
	srv, _, _, _, queue := queuedPurgeServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	if r := doJSON(srv, http.MethodPatch, "/api/v1/admin/instance-settings", admin,
		`{"downloads_enabled":false}`); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	if got := queue.walkCount(); got != 1 {
		t.Fatalf("queued %d revocation walks, want 1", got)
	}
}

// Re-stating downloads_enabled:false revokes nothing — the gate was already
// shut — so it must not start a second walk over the whole catalogue. Same
// idempotence the per-video snapshot has when downloads were already off.
func TestRestatingClosedDownloadsQueuesNoSecondWalk(t *testing.T) {
	srv, _, _, _, queue := queuedPurgeServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	for i := 0; i < 2; i++ {
		if r := doJSON(srv, http.MethodPatch, "/api/v1/admin/instance-settings", admin,
			`{"downloads_enabled":false}`); r.Code != http.StatusOK {
			t.Fatalf("patch %d = %d; body=%s", i, r.Code, r.Body.String())
		}
	}
	if got := queue.walkCount(); got != 1 {
		t.Fatalf("queued %d revocation walks across two identical PATCHes, want 1", got)
	}
}

// OPENING the gate purges nothing: more media becomes reachable, and nothing
// the edge holds becomes wrong.
func TestOpeningDownloadsQueuesNoWalk(t *testing.T) {
	srv, _, _, _, queue := queuedPurgeServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	if r := doJSON(srv, http.MethodPatch, "/api/v1/admin/instance-settings", admin,
		`{"downloads_enabled":false}`); r.Code != http.StatusOK {
		t.Fatalf("close = %d", r.Code)
	}
	if r := doJSON(srv, http.MethodPatch, "/api/v1/admin/instance-settings", admin,
		`{"downloads_enabled":true}`); r.Code != http.StatusOK {
		t.Fatalf("open = %d", r.Code)
	}
	if got := queue.walkCount(); got != 1 {
		t.Fatalf("queued %d walks, want 1 (only the close)", got)
	}
}

// An unrelated settings PATCH must not touch the catalogue at all.
func TestUnrelatedSettingChangeQueuesNoWalk(t *testing.T) {
	srv, _, _, _, queue := queuedPurgeServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	if r := doJSON(srv, http.MethodPatch, "/api/v1/admin/instance-settings", admin,
		`{"instance_name":"Renamed"}`); r.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", r.Code, r.Body.String())
	}
	if got := queue.walkCount(); got != 0 {
		t.Fatalf("queued %d walks for an unrelated setting, want 0", got)
	}
}

// TestRefusedFanOutIsQueuedForRetry is SC4 at the handler layer: a takedown the
// edge rejected used to be one warning line and no second attempt.
func TestRefusedFanOutIsQueuedForRetry(t *testing.T) {
	srv, blobs, tcRepo, rec, queue := queuedPurgeServer(t)
	rec.failWith = errors.New("cdn: purge rejected with status 500")
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id, tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d; body=%s", r.Code, r.Body.String())
	}
	sets := waitForEnqueue(t, queue, 1)
	want := wantVideoPurgePaths(t, srv, id)
	if len(sets[0]) != len(want) {
		t.Fatalf("queued %d URLs for retry, want the whole refused set of %d\n got=%v", len(sets[0]), len(want), sets[0])
	}
}

// ...and a fan-out the edge ACCEPTED queues nothing: there is nothing to retry,
// and a queue row per successful takedown would turn the operator's pending
// count into noise.
func TestAcceptedFanOutQueuesNoRetry(t *testing.T) {
	srv, blobs, tcRepo, rec, queue := queuedPurgeServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	if r := doJSON(srv, http.MethodDelete, "/api/v1/videos/"+id, tok, ""); r.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", r.Code)
	}
	waitForPurge(t, rec, wantVideoPurgePaths(t, srv, id))
	time.Sleep(100 * time.Millisecond)
	if got := queue.enqueuedURLs(); len(got) != 0 {
		t.Fatalf("queued %v after a fully accepted purge, want nothing", got)
	}
}

// The admin status page's cdn_purge block carries the DURABLE half too — the
// half a restart must not erase.
func TestSystemCDNPurgeBlockReportsTheQueue(t *testing.T) {
	srv, _, _, _, _ := queuedPurgeServer(t)
	block := srv.cdnPurgeSnapshot(context.Background())
	if block == nil {
		t.Fatal("cdn_purge block absent with a CDN wired")
	}
	if block.PendingRetries != 3 {
		t.Errorf("pending_retries = %d, want 3 (pending + running)", block.PendingRetries)
	}
	if block.DeadLetters != 3 {
		t.Errorf("dead_letters = %d, want 3", block.DeadLetters)
	}
	if block.OldestPendingSeconds != 4242 {
		t.Errorf("oldest_pending_seconds = %d, want 4242", block.OldestPendingSeconds)
	}
}

// ownerIDFromToken resolves the account behind a bearer token, so a snapshot
// can be asked for the same user the fixture's channel belongs to.
func ownerIDFromToken(t *testing.T, srv *Server, token string) uuid.UUID {
	t.Helper()
	claims, err := srv.authsvc.Parse(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		t.Fatalf("token subject %q is not a uuid: %v", claims.Subject, err)
	}
	return id
}
