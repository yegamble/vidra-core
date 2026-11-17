package federation

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// crawlQueueRepo adds the outbox-read queue (migration 0190) to a fake
// repository, in memory. A pending row is always due here: the lease, the
// backoff and the cooldown are the store's to prove (internal/store
// TestOutboxCrawlQueueLeasesAndCoolsDown).
type crawlQueueRepo struct {
	fakeRepo
	crawls map[string]*sqlcgen.FederationOutboxCrawl
}

func withCrawlQueue(repo fakeRepo) crawlQueueRepo {
	return crawlQueueRepo{fakeRepo: repo, crawls: map[string]*sqlcgen.FederationOutboxCrawl{}}
}

func (r crawlQueueRepo) EnqueueOutboxCrawl(_ context.Context, arg sqlcgen.EnqueueOutboxCrawlParams) error {
	if _, queued := r.crawls[arg.ActorUrl]; !queued {
		r.crawls[arg.ActorUrl] = &sqlcgen.FederationOutboxCrawl{ActorUrl: arg.ActorUrl, State: "pending"}
	}
	return nil
}

func (r crawlQueueRepo) ClaimDueOutboxCrawls(_ context.Context, arg sqlcgen.ClaimDueOutboxCrawlsParams) ([]sqlcgen.ClaimDueOutboxCrawlsRow, error) {
	var rows []sqlcgen.ClaimDueOutboxCrawlsRow
	for _, c := range r.crawls {
		if c.State == "pending" && len(rows) < int(arg.BatchSize) {
			rows = append(rows, sqlcgen.ClaimDueOutboxCrawlsRow{ActorUrl: c.ActorUrl, Attempts: c.Attempts})
		}
	}
	return rows, nil
}

func (r crawlQueueRepo) SettleOutboxCrawl(_ context.Context, arg sqlcgen.SettleOutboxCrawlParams) error {
	c := r.crawls[arg.ActorUrl]
	c.State, c.LastError, c.Attempts = arg.State, arg.LastError, c.Attempts+1
	return nil
}

// An Accept of a Follow from here queues one read of the accepting actor's
// outbox — a channel a user followed, an instance the admin followed — and an
// Accept that matches no follow queues nothing. Without the read the followed
// actor shows nothing until its next upload: the interop lab staged a public
// video on PeerTube before each kind of follow and neither arrived.
func TestAcceptedFollowQueuesAReadOfTheFollowedOutbox(t *testing.T) {
	ctx := context.Background()
	t.Run("a channel", func(t *testing.T) {
		userID := uuid.New()
		repo := withCrawlQueue(newFollowRepo(userID))
		cacheRemoteChannel(repo.fakeRepo, remoteChan, "remote.example", nil)
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		follow, err := svc.FollowRemoteChannel(ctx, userID, remoteChan)
		if err != nil {
			t.Fatalf("follow: %v", err)
		}
		followURL := repo.rcFollows[follow.ID].FollowActivityUrl

		stranger := "https://evil.example/accounts/mallory"
		if err := svc.HandleInbox(ctx, stranger, []byte(acceptActivity("Accept", "https://evil.example/act/1", stranger, followURL, stranger))); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
		if len(repo.crawls) != 0 {
			t.Fatalf("an Accept that accepted nothing queued %v", repo.crawls)
		}
		if err := svc.HandleInbox(ctx, remoteChan, []byte(acceptActivity("Accept", "https://remote.example/act/2", remoteChan, followURL, remoteChan))); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
		if c := repo.crawls[remoteChan]; c == nil || c.State != "pending" || len(repo.crawls) != 1 {
			t.Errorf("queue = %v, want one pending read of %s", repo.crawls, remoteChan)
		}
	})
	t.Run("an instance", func(t *testing.T) {
		const theirs, followURL = "https://peer.example/accounts/peertube", ourInstance + "/activities/follow/1"
		repo := withCrawlQueue(newFollowingRepo())
		repo.inst.following[theirs] = &sqlcgen.InstanceFollowing{RemoteActorUrl: theirs, Host: "peer.example", State: "pending", FollowActivityUrl: followURL}
		svc := NewService(repo, WithBaseURL("https://videos.example"))
		accept := `{"id":"https://peer.example/accepts/1","type":"Accept","actor":"` + theirs + `","object":{"id":"` + followURL +
			`","type":"Follow","actor":"` + ourInstance + `","object":"` + theirs + `"}}`
		if err := svc.HandleInbox(ctx, theirs, []byte(accept)); err != nil {
			t.Fatalf("HandleInbox: %v", err)
		}
		if c := repo.crawls[theirs]; c == nil || c.State != "pending" || len(repo.crawls) != 1 {
			t.Errorf("queue = %v, want one pending read of %s", repo.crawls, theirs)
		}
	})
}

// newQueuedCrawl is a vidra that follows the fixture channel, with a read of
// its outbox queued, reaching the origin through docs.
func newQueuedCrawl(docs map[string][]byte) (*Service, crawlQueueRepo) {
	repo := withCrawlQueue(newContractRepo())
	_ = repo.EnqueueOutboxCrawl(context.Background(), sqlcgen.EnqueueOutboxCrawlParams{ActorUrl: ctPTChannel})
	return NewService(repo, WithBaseURL(contractBase), WithFollowEdgeChecker(edgesFor(ctPTChannel)),
		WithFetchClient(&http.Client{Transport: fakeOrigin{docs: docs, seen: &[]string{}}})), repo
}

// The worker's drain runs a queued read: the video the followed channel
// published earlier is stored and the read is done, with nothing left to drain.
func TestDrainOutboxCrawlsReadsAQueuedOutbox(t *testing.T) {
	svc, repo := newQueuedCrawl(peertubeOrigin(t, ctPTChannel))
	if over, err := svc.DrainOutboxCrawls(context.Background(), 5); err != nil || over != 1 {
		t.Fatalf("DrainOutboxCrawls = %d, %v; want 1 read over", over, err)
	}
	assertFiledUnderChannel(t, repo.fakeRepo)
	if c := repo.crawls[ctPTChannel]; c.State != "done" || c.LastError != "" {
		t.Errorf("read = %+v, want done", c)
	}
	if over, err := svc.DrainOutboxCrawls(context.Background(), 5); err != nil || over != 0 {
		t.Errorf("second DrainOutboxCrawls = %d, %v; want nothing left", over, err)
	}
}

// A read that fails is tried again, and given up on after
// outboxCrawlMaxAttempts with the reason kept. Only then does the drain count
// it as over — a pass that counted attempts would spin on it.
func TestDrainOutboxCrawlsRetriesThenGivesUp(t *testing.T) {
	docs := peertubeOrigin(t, ctPTChannel)
	delete(docs, ctPTChannel+"/outbox") // the outbox answers 404
	svc, repo := newQueuedCrawl(docs)
	for attempt := int32(1); attempt <= outboxCrawlMaxAttempts; attempt++ {
		wantOver, wantState := 0, "pending"
		if attempt == outboxCrawlMaxAttempts {
			wantOver, wantState = 1, "failed"
		}
		over, err := svc.DrainOutboxCrawls(context.Background(), 5)
		c := repo.crawls[ctPTChannel]
		if err != nil || over != wantOver || c.State != wantState || c.Attempts != attempt || c.LastError == "" {
			t.Fatalf("attempt %d: over %d, err %v, read %+v; want over %d and state %s with the reason kept", attempt, over, err, c, wantOver, wantState)
		}
	}
	if len(repo.remoteVideos) != 0 {
		t.Errorf("a read that never succeeded stored %d videos", len(repo.remoteVideos))
	}
}
