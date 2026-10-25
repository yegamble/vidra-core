package federation

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeFlagReports stands in for *moderation.Service on the inbound-Flag path.
// It mirrors the SQL's two partial unique indexes (migration 0153) — one report
// per (Flag id, target) and one OPEN report per (domain, target) — a repeat
// returning uuid.Nil with no error, as ON CONFLICT DO NOTHING does.
type fakeFlagReports struct {
	filed   []moderation.RemoteReport
	seen    map[string]bool
	missing map[uuid.UUID]bool // targets whose FK would fail → ErrInvalidTarget
	err     error
}

func (f *fakeFlagReports) ReportFromRemote(_ context.Context, r moderation.RemoteReport) (uuid.UUID, error) {
	if f.err != nil {
		return uuid.Nil, f.err
	}
	if f.missing[r.TargetID] {
		return uuid.Nil, moderation.ErrInvalidTarget
	}
	key := r.FlagActivityID + "|" + r.TargetType + "|" + r.TargetID.String()
	open := r.Domain + "|" + r.TargetType + "|" + r.TargetID.String() // every fake report stays open
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] || f.seen[open] {
		return uuid.Nil, nil
	}
	f.seen[key], f.seen[open] = true, true
	f.filed = append(f.filed, r)
	return uuid.New(), nil
}

// fakeReportNotifier records the new_report staff fan-out calls.
type fakeReportNotifier struct {
	notified []uuid.UUID
	err      error
}

func (f *fakeReportNotifier) NotifyNewReport(_ context.Context, id uuid.UUID) (int64, error) {
	f.notified = append(f.notified, id)
	return 1, f.err
}

const (
	ptActor      = "https://peertube.example/accounts/peertube"
	mastoActor   = "https://mastodon.example/actor"
	flagBaseURL  = "https://videos.example"
	ptFlagID     = "https://peertube.example/admin/abuses/7"
	mastoFlagID  = "https://mastodon.example/a1b2c3d4"
	aliceAccount = flagBaseURL + "/accounts/alice"
)

func newFlagFixture() (fakeRepo, *fakeFlagReports, *fakeReportNotifier, *Service, uuid.UUID) {
	repo := newInboxRepo()
	aliceID := uuid.New()
	repo.usersByName = map[string]sqlcgen.GetUserActorByUsernameRow{"alice": {ID: aliceID, Username: "alice"}}
	films := repo.channels["films"]
	films.OwnerID = aliceID
	repo.channels["films"] = films
	repo.channelsByID[films.ID] = films
	repo.blockedDomains = map[string]bool{}
	repo.adminActorBlocks = map[string]string{}
	reports, notifier := &fakeFlagReports{}, &fakeReportNotifier{}
	svc := NewService(repo, WithBaseURL(flagBaseURL), WithFlagReporting(reports.ReportFromRemote, notifier.NotifyNewReport))
	return repo, reports, notifier, svc, aliceID
}

// A PeerTube abuse report is a Flag from the reporting instance with ONE object
// (the video's url), markdown content, and the predefined reasons as Hashtag
// tags (send-flag.ts / AbuseModel.toActivityPubObject). It must land in the
// local moderation queue against the local video, attributed to the remote
// actor, and fan out the staff new_report notification once.
func TestHandleInboxFlagPeerTubeVideo(t *testing.T) {
	repo, reports, notifier, svc, _ := newFlagFixture()
	videoID := uuid.New()
	body := `{"@context":["https://www.w3.org/ns/activitystreams"],"id":"` + ptFlagID + `","type":"Flag",` +
		`"actor":"` + ptActor + `","content":"Re-upload of **my** film","mediaType":"text/markdown",` +
		`"object":"` + flagBaseURL + `/videos/watch/` + videoID.String() + `",` +
		`"tag":[{"type":"Hashtag","name":"rights"},{"type":"Hashtag","name":"spamOrMisleading"},{"type":"Hashtag","name":"notAReason"}],` +
		`"startAt":null,"endAt":null,"to":["` + aliceAccount + `"],"cc":[]}`

	if err := svc.HandleInbox(context.Background(), ptActor, []byte(body)); err != nil {
		t.Fatalf("HandleInbox(Flag): %v", err)
	}
	if len(reports.filed) != 1 {
		t.Fatalf("filed %d reports, want 1: %+v", len(reports.filed), reports.filed)
	}
	r := reports.filed[0]
	if r.TargetType != moderation.TargetVideo || r.TargetID != videoID {
		t.Errorf("target = %s/%s, want video/%s", r.TargetType, r.TargetID, videoID)
	}
	if r.ActorURL != ptActor || r.Domain != "peertube.example" || r.FlagActivityID != ptFlagID {
		t.Errorf("reporter = %q @ %q (flag %q), want the remote actor + its domain + the Flag id", r.ActorURL, r.Domain, r.FlagActivityID)
	}
	// vidra has no predefined-reason column: the KNOWN PeerTube reasons are
	// prefixed into the reason; an unknown tag is not echoed.
	if want := "[rights, spamOrMisleading] Re-upload of **my** film"; r.Reason != want {
		t.Errorf("reason = %q, want %q", r.Reason, want)
	}
	if len(notifier.notified) != 1 {
		t.Errorf("new_report fan-out fired %d times, want 1", len(notifier.notified))
	}
	if !repo.processed[ptFlagID] {
		t.Error("Flag not marked processed")
	}
}

// Mastodon's Flag: `object` is an ARRAY — the reported account first, then the
// statuses — `content` is the reporter's comment, and the actor is the remote
// INSTANCE actor. Every local object becomes its own report; a channel (Group)
// actor reports the account that owns it.
func TestHandleInboxFlagMastodonAccountAndStatuses(t *testing.T) {
	_, reports, notifier, svc, aliceID := newFlagFixture()
	videoID, commentID := uuid.New(), uuid.New()
	body := `{"@context":"https://www.w3.org/ns/activitystreams","id":"` + mastoFlagID + `","type":"Flag",` +
		`"actor":"` + mastoActor + `","content":"<p>harassing &amp; spamming</p>",` +
		`"object":["` + aliceAccount + `","` + flagBaseURL + `/videos/` + videoID.String() + `","` +
		flagBaseURL + `/comments/` + commentID.String() + `"]}`

	if err := svc.HandleInbox(context.Background(), mastoActor, []byte(body)); err != nil {
		t.Fatalf("HandleInbox(Flag): %v", err)
	}
	if len(reports.filed) != 3 {
		t.Fatalf("filed %d reports, want 3 (account, video, comment): %+v", len(reports.filed), reports.filed)
	}
	want := []struct {
		typ string
		id  uuid.UUID
	}{{moderation.TargetAccount, aliceID}, {moderation.TargetVideo, videoID}, {moderation.TargetComment, commentID}}
	for i, w := range want {
		got := reports.filed[i]
		if got.TargetType != w.typ || got.TargetID != w.id {
			t.Errorf("report %d = %s/%s, want %s/%s", i, got.TargetType, got.TargetID, w.typ, w.id)
		}
		if got.Domain != "mastodon.example" || got.Reason != "harassing & spamming" {
			t.Errorf("report %d reporter/reason = %q / %q", i, got.Domain, got.Reason)
		}
	}
	if len(notifier.notified) != 3 {
		t.Errorf("new_report fan-out fired %d times, want 3", len(notifier.notified))
	}

	// A channel actor names the account that owns it; reported twice in one
	// Flag (as the channel AND the account) it is still ONE report. (From a
	// second instance: mastodon.example already has an open report on alice.)
	const otherActor = "https://other.example/actor"
	body = `{"id":"https://other.example/f/2","type":"Flag","actor":"` + otherActor + `","content":"x",` +
		`"object":["` + filmsActor + `","` + aliceAccount + `"]}`
	if err := svc.HandleInbox(context.Background(), otherActor, []byte(body)); err != nil {
		t.Fatalf("HandleInbox(channel Flag): %v", err)
	}
	last := reports.filed[len(reports.filed)-1]
	if len(reports.filed) != 4 || last.TargetType != moderation.TargetAccount || last.TargetID != aliceID {
		t.Errorf("channel Flag filed %+v, want exactly one account report on the owner", reports.filed[3:])
	}
}

// A Flag from a blocked INSTANCE, or from an actor the admin blocked
// instance-wide, is dropped and surfaces as ErrSenderBlocked — the signal the
// HTTP layer answers 202 to and writes the federation-inbox-rejected audit row
// for. Nothing is filed, nobody is notified, and the activity is NOT marked
// processed (an unblock lets a redelivery through, like every other block).
func TestHandleInboxFlagFromBlockedSenderIsDropped(t *testing.T) {
	body := `{"id":"` + ptFlagID + `","type":"Flag","actor":"` + ptActor + `","content":"x","object":"` + aliceAccount + `"}`
	for name, block := range map[string]func(fakeRepo){
		"instance": func(r fakeRepo) { r.blockedDomains["peertube.example"] = true },
		"actor":    func(r fakeRepo) { r.adminActorBlocks[ptActor] = "spam" },
	} {
		t.Run(name, func(t *testing.T) {
			repo, reports, notifier, svc, _ := newFlagFixture()
			block(repo)
			err := svc.HandleInbox(context.Background(), ptActor, []byte(body))
			if !errors.Is(err, ErrSenderBlocked) {
				t.Fatalf("HandleInbox err = %v, want ErrSenderBlocked", err)
			}
			if len(reports.filed) != 0 || len(notifier.notified) != 0 {
				t.Errorf("blocked Flag filed %d / notified %d, want 0 / 0", len(reports.filed), len(notifier.notified))
			}
			if repo.processed[ptFlagID] {
				t.Error("blocked Flag was marked processed")
			}
		})
	}
}

// Only THIS instance's objects are reportable through its inbox: a remote
// instance must not be able to file reports about third-party content (another
// server's video, a remote video we merely mirror) through us. An unknown local
// id is skipped without failing the rest.
func TestHandleInboxFlagIgnoresNonLocalObjects(t *testing.T) {
	repo, reports, notifier, svc, _ := newFlagFixture()
	ghost := uuid.New()
	reports.missing = map[uuid.UUID]bool{ghost: true}
	body := `{"id":"` + mastoFlagID + `","type":"Flag","actor":"` + mastoActor + `","content":"x","object":[` +
		`"https://elsewhere.example/videos/watch/` + uuid.NewString() + `",` +
		`"https://elsewhere.example/accounts/alice",` +
		`"https://videos.example.evil/accounts/alice",` +
		`"` + flagBaseURL + `/accounts/nobody",` +
		`"` + flagBaseURL + `/videos/watch/not-a-uuid",` +
		`"` + flagBaseURL + `/videos/watch/` + ghost.String() + `",` +
		`{"id":"` + flagBaseURL + `/videos/watch/` + ghost.String() + `/extra"}]}`

	if err := svc.HandleInbox(context.Background(), mastoActor, []byte(body)); err != nil {
		t.Fatalf("HandleInbox(Flag): %v", err)
	}
	if len(reports.filed) != 0 || len(notifier.notified) != 0 {
		t.Errorf("non-local Flag filed %+v / notified %d, want nothing", reports.filed, len(notifier.notified))
	}
	if !repo.processed[mastoFlagID] {
		t.Error("a Flag with nothing local to report is still processed (accepted-and-ignored)")
	}
}

// The same Flag delivered twice files its report once and notifies once.
func TestHandleInboxFlagDuplicateDelivery(t *testing.T) {
	_, reports, notifier, svc, _ := newFlagFixture()
	body := []byte(`{"id":"` + ptFlagID + `","type":"Flag","actor":"` + ptActor + `","content":"spam","object":"` + aliceAccount + `"}`)
	for i := 0; i < 2; i++ {
		if err := svc.HandleInbox(context.Background(), ptActor, body); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	if len(reports.filed) != 1 || len(notifier.notified) != 1 {
		t.Errorf("two deliveries filed %d / notified %d, want 1 / 1", len(reports.filed), len(notifier.notified))
	}
}

// A redelivery that slips past the processed-activity ledger (the first attempt
// failed mid-way, so it was never marked) is still idempotent: the store
// answers uuid.Nil for a report it already holds, and that must not re-notify.
func TestHandleInboxFlagRetryAfterFailureDoesNotRenotify(t *testing.T) {
	repo, reports, notifier, svc, _ := newFlagFixture()
	body := []byte(`{"id":"` + ptFlagID + `","type":"Flag","actor":"` + ptActor + `","content":"spam","object":"` + aliceAccount + `"}`)
	if err := svc.HandleInbox(context.Background(), ptActor, body); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	delete(repo.processed, ptFlagID) // as if MarkActivityProcessed had failed
	if err := svc.HandleInbox(context.Background(), ptActor, body); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if len(reports.filed) != 1 || len(notifier.notified) != 1 {
		t.Errorf("redelivery filed %d / notified %d, want 1 / 1", len(reports.filed), len(notifier.notified))
	}
}

// Bounds: a Flag names at most maxFlagObjects objects (the rest are ignored) and
// its reason is truncated, never rejected. A store error fails the activity so
// the remote retries; a notification error never does.
func TestHandleInboxFlagBoundsAndFailures(t *testing.T) {
	repo, reports, notifier, svc, _ := newFlagFixture()
	objs := make([]string, 0, maxFlagObjects+5)
	for i := 0; i < maxFlagObjects+5; i++ {
		objs = append(objs, `"`+flagBaseURL+`/videos/watch/`+uuid.NewString()+`"`)
	}
	long := strings.Repeat("é", maxFlagReasonLen+50)
	body := `{"id":"` + mastoFlagID + `","type":"Flag","actor":"` + mastoActor + `","content":"` + long + `","object":[` + strings.Join(objs, ",") + `]}`
	notifier.err = errors.New("notifications down")
	if err := svc.HandleInbox(context.Background(), mastoActor, []byte(body)); err != nil {
		t.Fatalf("HandleInbox(Flag): %v", err)
	}
	if len(reports.filed) != maxFlagObjects {
		t.Errorf("filed %d reports, want the cap %d", len(reports.filed), maxFlagObjects)
	}
	if n := len([]rune(reports.filed[0].Reason)); n != maxFlagReasonLen {
		t.Errorf("reason is %d runes, want truncated to %d", n, maxFlagReasonLen)
	}

	reports.err = errors.New("db down")
	retry := []byte(`{"id":"` + ptFlagID + `","type":"Flag","actor":"` + ptActor + `","content":"x","object":"` + aliceAccount + `"}`)
	if err := svc.HandleInbox(context.Background(), ptActor, retry); err == nil {
		t.Error("a store failure was swallowed; the remote would never retry")
	}
	if repo.processed[ptFlagID] {
		t.Error("a failed Flag was marked processed")
	}

	// The signer must be the Flag's actor, even another actor on its host.
	spoof := []byte(`{"id":"` + ptFlagID + `/s","type":"Flag","actor":"` + ptActor + `","content":"x","object":"` + aliceAccount + `"}`)
	if err := svc.HandleInbox(context.Background(), "https://peertube.example/accounts/eve", spoof); !errors.Is(err, ErrActorMismatch) {
		t.Errorf("spoofed Flag err = %v, want ErrActorMismatch", err)
	}
}

// Without the reporting seam wired (a bare service, as in most tests), a Flag
// is accepted and ignored — the shipped behaviour before this change.
func TestHandleInboxFlagWithoutReporterIsIgnored(t *testing.T) {
	repo := newInboxRepo()
	svc := NewService(repo, WithBaseURL(flagBaseURL))
	body := []byte(`{"id":"` + ptFlagID + `","type":"Flag","actor":"` + ptActor + `","content":"x","object":"` + aliceAccount + `"}`)
	if err := svc.HandleInbox(context.Background(), ptActor, body); err != nil {
		t.Fatalf("HandleInbox(Flag): %v", err)
	}
}

// A Flag's id must live on the signer's host, like every other activity id the
// inbox accepts, and must fit the length the store indexes. Either failure is
// ErrBadResource (400) before anything is filed or marked processed.
func TestHandleInboxFlagIDBoundToSigner(t *testing.T) {
	for name, tc := range map[string]struct {
		id   string
		want error
	}{
		"other host":     {"https://elsewhere.example/abuses/7", ErrBadResource},
		"lookalike host": {"https://peertube.example.evil/abuses/7", ErrBadResource},
		"too long":       {ptFlagID + "/" + strings.Repeat("a", maxFlagIDLen-len(ptFlagID)), ErrBadResource},
		"at the cap":     {ptFlagID + "/" + strings.Repeat("a", maxFlagIDLen-len(ptFlagID)-1), nil},
	} {
		t.Run(name, func(t *testing.T) {
			repo, reports, notifier, svc, _ := newFlagFixture()
			body := `{"id":"` + tc.id + `","type":"Flag","actor":"` + ptActor + `","content":"x","object":"` + aliceAccount + `"}`
			err := svc.HandleInbox(context.Background(), ptActor, []byte(body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("HandleInbox err = %v, want %v", err, tc.want)
			}
			if tc.want == nil {
				if len(reports.filed) != 1 {
					t.Errorf("an id at the cap filed %d reports, want 1", len(reports.filed))
				}
				return
			}
			if len(reports.filed) != 0 || len(notifier.notified) != 0 || repo.processed[tc.id] {
				t.Errorf("rejected Flag filed %d / notified %d / processed %v, want nothing",
					len(reports.filed), len(notifier.notified), repo.processed[tc.id])
			}
		})
	}
}

// One open report per (reporting instance, target): a second Flag from the
// same instance about a target it already has an open report on files nothing
// and tells staff nothing.
func TestHandleInboxFlagSameInstanceSameTargetWhileOpen(t *testing.T) {
	_, reports, notifier, svc, _ := newFlagFixture()
	for i := 1; i <= 2; i++ {
		body := `{"id":"` + ptFlagID + `/` + string(rune('0'+i)) + `","type":"Flag","actor":"` + ptActor +
			`","content":"x","object":"` + aliceAccount + `"}`
		if err := svc.HandleInbox(context.Background(), ptActor, []byte(body)); err != nil {
			t.Fatalf("Flag %d: %v", i, err)
		}
	}
	if len(reports.filed) != 1 || len(notifier.notified) != 1 {
		t.Errorf("two Flags from one instance on one open target filed %d / notified %d, want 1 / 1",
			len(reports.filed), len(notifier.notified))
	}
}

func TestFlagReasonReturnsKnownPredefinedReasons(t *testing.T) {
	tags := json.RawMessage(`[{"name":"spamOrMisleading"},{"name":"mystery"},{"name":"spamOrMisleading"},{"name":"rights"}]`)
	reason, reasons := flagReason(json.RawMessage(`"<p>bad</p>"`), tags)
	if want := []string{"spamOrMisleading", "rights"}; !slices.Equal(reasons, want) {
		t.Errorf("reasons = %v, want %v (unknown dropped, deduped)", reasons, want)
	}
	if reason != "[spamOrMisleading, rights] bad" {
		t.Errorf("reason = %q", reason)
	}
}
