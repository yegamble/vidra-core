package live

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/observability"
)

// fakeIngestControl is the ingest control surface with no nginx anywhere: it
// records what it was asked to drop and answers whatever the test pins.
type fakeIngestControl struct {
	dropped []string
	// dropCount is what the ingest says it closed. nil is the ordinary outcome —
	// one publisher on one socket — so a test only pins it to describe a
	// different reality.
	dropCount *int
	dropErr   error
	probeErr  error
	calls     int
}

func (f *fakeIngestControl) DropPublisher(_ context.Context, name string) (int, error) {
	f.calls++
	f.dropped = append(f.dropped, name)
	if f.dropErr != nil {
		return 0, f.dropErr
	}
	if f.dropCount != nil {
		return *f.dropCount, nil
	}
	return 1, nil
}

func dropCount(n int) *int { return &n }

func (f *fakeIngestControl) Probe(context.Context) error { return f.probeErr }

// liveStreamFixture creates a stream and puts it on air, returning the service,
// the repo, the fake ingest and the stream id.
func liveStreamFixture(t *testing.T, opts ...Option) (*Service, *fakeRepo, *fakeIngestControl, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	ctrl := &fakeIngestControl{}
	all := append([]Option{WithIngestController(ctrl), WithAuditor(&fakeAuditor{})}, opts...)
	svc := NewService(repo, all...)
	st, key, err := svc.Create(context.Background(), uuid.New(), CreateInput{Title: "A live thing"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.StartIngest(context.Background(), key); err != nil {
		t.Fatalf("start ingest: %v", err)
	}
	return svc, repo, ctrl, st.ID
}

// TestTerminateEndsRotatesAndDrops is the SC1 sequence in one assertion set: the
// broadcast is off the air, the key the publisher holds is dead, and the socket
// is dropped by STREAM ID (never by the raw key, which is the whole reason the
// on-publish rename exists).
func TestTerminateEndsRotatesAndDrops(t *testing.T) {
	auditor := &fakeAuditor{}
	svc, repo, ctrl, id := liveStreamFixture(t, WithAuditor(auditor))
	before := repo.hashes[id]
	actor := uuid.New()

	res, err := svc.Terminate(context.Background(), id, TerminateInput{
		ActorID: actor, ReasonCode: string(ReasonPolicyViolation), Reason: "stop that",
	})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}

	st, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if st.State != StateEnded {
		t.Errorf("state = %q, want %q — the broadcast is still on air", st.State, StateEnded)
	}
	if st.StartedAt != nil {
		t.Error("started_at survived the termination; leaving live must clear it")
	}
	if repo.hashes[id] == before {
		t.Error("the stream key was NOT rotated — the publisher reconnects with the credential they are holding and goes straight back on air")
	}
	if !res.KeyRotated {
		t.Error("result claims the key was not rotated when it was")
	}
	if len(ctrl.dropped) != 1 || ctrl.dropped[0] != id.String() {
		t.Errorf("dropped %v, want exactly [%s] — the drop must name the STREAM ID, not the stream key", ctrl.dropped, id)
	}
	if !res.PublisherDropped {
		t.Error("result claims the publisher survived when the ingest confirmed the drop")
	}

	// The reason is stored where a block's reason is stored, and the creator can
	// read both halves.
	if !st.TerminatedByModerator() {
		t.Error("stream does not report a moderator termination")
	}
	if st.TerminationReasonCode != string(ReasonPolicyViolation) || st.TerminationReason != "stop that" {
		t.Errorf("stored reason = %q/%q, want policy_violation/\"stop that\"", st.TerminationReasonCode, st.TerminationReason)
	}
	if st.TerminatedBy == nil || *st.TerminatedBy != actor {
		t.Error("terminated_by does not name the moderator")
	}

	// The audit row: the CODE, the stream in resource_id, and no prose.
	var ev = findAudit(t, auditor, observability.ActionLiveTerminate)
	if ev.ResourceID != id.String() {
		t.Errorf("audit resource_id = %q, want the stream id — A26 measured live rows leaving it empty, which is why the audit filter could not target a stream", ev.ResourceID)
	}
	if ev.Reason != string(ReasonPolicyViolation) {
		t.Errorf("audit reason = %q, want the bare reason code", ev.Reason)
	}
	if ev.Actor.ID != actor.String() || ev.Actor.Kind != "user" {
		t.Errorf("audit actor = %+v, want the moderator", ev.Actor)
	}
}

// TestTerminateRotatesBeforeDropping pins the ORDER. Rotating after the drop
// leaves a window in which the publisher reconnects with a still-valid key and
// is back on air before the rotation lands.
func TestTerminateRotatesBeforeDropping(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	var order []string
	ctrl := &orderingControl{onDrop: func() { order = append(order, "drop") }}
	repo.onKeyRotate = func() { order = append(order, "rotate") }
	svc := NewService(repo, WithIngestController(ctrl), WithAuditor(&fakeAuditor{}))
	st, key, _ := svc.Create(context.Background(), uuid.New(), CreateInput{Title: "t"})
	if _, err := svc.StartIngest(context.Background(), key); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := svc.Terminate(context.Background(), st.ID, TerminateInput{ReasonCode: string(ReasonSpam)}); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if len(order) != 2 || order[0] != "rotate" || order[1] != "drop" {
		t.Errorf("order = %v, want [rotate drop]: dropping first leaves a window in which the publisher reconnects on a key that still works", order)
	}
}

type orderingControl struct{ onDrop func() }

func (o *orderingControl) DropPublisher(context.Context, string) (int, error) {
	o.onDrop()
	return 1, nil
}
func (o *orderingControl) Probe(context.Context) error { return nil }

// TestTerminateWithoutControlStillEndsAndRotates is the documented degrade: an
// instance with no LIVE_INGEST_CONTROL_URL still takes the broadcast off the air
// and kills the credential, and SAYS the socket was left alone rather than
// reporting success.
func TestTerminateWithoutControlStillEndsAndRotates(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, WithAuditor(&fakeAuditor{}))
	st, key, _ := svc.Create(context.Background(), uuid.New(), CreateInput{Title: "t"})
	if _, err := svc.StartIngest(context.Background(), key); err != nil {
		t.Fatalf("start: %v", err)
	}
	before := repo.hashes[st.ID]

	res, err := svc.Terminate(context.Background(), st.ID, TerminateInput{ReasonCode: string(ReasonOther)})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if res.ControlConfigured {
		t.Error("result claims a control surface on an instance with none")
	}
	if res.PublisherDropped {
		t.Error("result claims the publisher was dropped with no way to drop them")
	}
	if !res.KeyRotated || repo.hashes[st.ID] == before {
		t.Error("the key was not rotated; without the drop the rotation is the ONLY thing keeping the publisher off")
	}
	got, _ := svc.Get(context.Background(), st.ID)
	if got.State != StateEnded {
		t.Errorf("state = %q, want ended", got.State)
	}
}

// TestTerminateUnreachableIngestIsAPartialOutcome: the ingest exists but does not
// answer. The broadcast still ends, and the caller is told the socket survived —
// a moderator told only "ok" while the streamer keeps uploading has been
// misinformed at the moment it matters most.
func TestTerminateUnreachableIngestIsAPartialOutcome(t *testing.T) {
	svc, _, ctrl, id := liveStreamFixture(t)
	ctrl.dropErr = ErrIngestControlUnavailable

	res, err := svc.Terminate(context.Background(), id, TerminateInput{ReasonCode: string(ReasonHarassment)})
	if err != nil {
		t.Fatalf("terminate must not fail because the ingest did not answer: %v", err)
	}
	if res.PublisherDropped {
		t.Error("result claims a drop the ingest refused")
	}
	if res.DropError == nil {
		t.Error("the drop failure was swallowed; the caller cannot tell the publisher is still connected")
	}
	got, _ := svc.Get(context.Background(), id)
	if got.State != StateEnded {
		t.Errorf("state = %q, want ended — the audience must leave even when the socket does not", got.State)
	}
}

// TestTerminateNoPublisherIsNotADisconnect is the A26 finding as a test: the
// ingest answers, and says it dropped NOTHING. That is not a confirmed
// disconnect, and reporting it as one is how a hook-only phantom session — and,
// for the whole of A26, a drop that reached the wrong nginx worker — told a
// moderator the streamer was off while they kept uploading.
func TestTerminateNoPublisherIsNotADisconnect(t *testing.T) {
	auditor := &fakeAuditor{}
	svc, _, ctrl, id := liveStreamFixture(t, WithAuditor(auditor))
	ctrl.dropErr = ErrIngestNoPublisher
	res, err := svc.Terminate(context.Background(), id, TerminateInput{ActorID: uuid.New(), ReasonCode: string(ReasonTechnical)})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if res.PublisherDropped {
		t.Error("a drop that closed nothing was reported as a disconnect — this is the exact claim A26 measured being made while a publisher streamed on")
	}
	if !errors.Is(res.DropError, ErrIngestNoPublisher) {
		t.Errorf("DropError = %v, want ErrIngestNoPublisher so the caller can say WHICH partial outcome this is", res.DropError)
	}
	if res.DroppedCount != 0 {
		t.Errorf("DroppedCount = %d, want 0", res.DroppedCount)
	}
	ev := findAudit(t, auditor, observability.ActionLiveTerminate)
	if ev.Reason != string(ReasonTechnical)+" no_publisher" {
		t.Errorf("audit reason = %q, want the code plus the no_publisher marker", ev.Reason)
	}
	if ev.Metadata["count"] != "0" {
		t.Errorf("audit metadata count = %q, want \"0\" — the count is the only thing in the trail that distinguishes a real disconnect from a no-op", ev.Metadata["count"])
	}
}

// TestTerminateZeroCountWithoutAnErrorIsStillNotADisconnect: the defensive half
// of the same rule. A controller that answers "closed 0" with no error at all
// (a future implementation, a different media server) must not be read as a
// success just because nothing went wrong — the number IS the outcome.
func TestTerminateZeroCountWithoutAnErrorIsStillNotADisconnect(t *testing.T) {
	svc, _, ctrl, id := liveStreamFixture(t)
	ctrl.dropCount = dropCount(0)
	res, err := svc.Terminate(context.Background(), id, TerminateInput{ReasonCode: string(ReasonOther)})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if res.PublisherDropped {
		t.Error("a drop that closed nothing was reported as a disconnect")
	}
	if !errors.Is(res.DropError, ErrIngestNoPublisher) {
		t.Errorf("DropError = %v, want ErrIngestNoPublisher", res.DropError)
	}
}

// TestTerminateCountedDropIsAudited: the ordinary outcome carries the number the
// ingest returned, so a row read a week later says whether the drop landed.
func TestTerminateCountedDropIsAudited(t *testing.T) {
	auditor := &fakeAuditor{}
	svc, _, ctrl, id := liveStreamFixture(t, WithAuditor(auditor))
	ctrl.dropCount = dropCount(2)
	actor := uuid.New()
	res, err := svc.Terminate(context.Background(), id, TerminateInput{
		ActorID: actor, ActorRole: "moderator", ReasonCode: string(ReasonSpam),
	})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if !res.PublisherDropped || res.DroppedCount != 2 {
		t.Errorf("dropped=%v count=%d, want a confirmed drop of 2", res.PublisherDropped, res.DroppedCount)
	}
	ev := findAudit(t, auditor, observability.ActionLiveTerminate)
	if ev.Metadata["count"] != "2" {
		t.Errorf("audit metadata count = %q, want \"2\"", ev.Metadata["count"])
	}
	if ev.ResourceType != "live_stream" {
		t.Errorf("audit resource_type = %q, want live_stream", ev.ResourceType)
	}
	if ev.Actor.Role != "moderator" {
		t.Errorf("audit actor_role = %q, want moderator — A26 measured it empty, so the trail could not answer \"was this staff?\"", ev.Actor.Role)
	}
}

// TestTerminatePermanentStreamGoesOffline: a permanent stream is reusable, so it
// returns to offline rather than being marked ended — the same choice the stop
// hook and the watchdog make.
func TestTerminatePermanentStreamGoesOffline(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, WithIngestController(&fakeIngestControl{}), WithAuditor(&fakeAuditor{}))
	st, key, _ := svc.Create(context.Background(), uuid.New(), CreateInput{Title: "t", Permanent: true})
	if _, err := svc.StartIngest(context.Background(), key); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := svc.Terminate(context.Background(), st.ID, TerminateInput{ReasonCode: string(ReasonSpam)}); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	got, _ := svc.Get(context.Background(), st.ID)
	if got.State != StateOffline {
		t.Errorf("state = %q, want offline for a permanent stream", got.State)
	}
}

// TestGoingLiveClearsTheTermination: a permanent stream that is allowed to
// broadcast again must not carry last month's takedown notice on its page. The
// audit trail keeps the history; the row describes what is true now.
func TestGoingLiveClearsTheTermination(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, WithIngestController(&fakeIngestControl{}), WithAuditor(&fakeAuditor{}))
	st, key, _ := svc.Create(context.Background(), uuid.New(), CreateInput{Title: "t", Permanent: true})
	if _, err := svc.StartIngest(context.Background(), key); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := svc.Terminate(context.Background(), st.ID, TerminateInput{ReasonCode: string(ReasonSpam), Reason: "no"}); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if got, _ := svc.Get(context.Background(), st.ID); !got.Terminated() {
		t.Fatal("termination did not stick")
	}
	// A new key was issued by the rotation; go live on it.
	newKey, err := svc.RegenerateKey(context.Background(), st.ID)
	if err != nil {
		t.Fatalf("regen: %v", err)
	}
	if _, err := svc.StartIngest(context.Background(), newKey); err != nil {
		t.Fatalf("restart: %v", err)
	}
	got, _ := svc.Get(context.Background(), st.ID)
	if got.Terminated() {
		t.Error("the new broadcast still advertises the previous session's termination")
	}
	if got.TerminationReason != "" || got.TerminationReasonCode != "" {
		t.Errorf("stale reason survived: %q/%q", got.TerminationReasonCode, got.TerminationReason)
	}
}

// TestTerminateRefusesAnUnknownReasonAndAnOfflineStream: the two refusals a
// caller can provoke, and neither leaves the row touched.
func TestTerminateRefusesAnUnknownReasonAndAnOfflineStream(t *testing.T) {
	svc, _, _, id := liveStreamFixture(t)
	if _, err := svc.Terminate(context.Background(), id, TerminateInput{ReasonCode: "because-i-said-so"}); !errors.Is(err, ErrInvalidTerminationReason) {
		t.Errorf("unknown reason code err = %v, want ErrInvalidTerminationReason", err)
	}
	if got, _ := svc.Get(context.Background(), id); got.State != StateLive {
		t.Error("a rejected reason code ended the broadcast anyway")
	}
	// Now end it properly and try again.
	if _, err := svc.Terminate(context.Background(), id, TerminateInput{ReasonCode: string(ReasonOther)}); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if _, err := svc.Terminate(context.Background(), id, TerminateInput{ReasonCode: string(ReasonOther)}); !errors.Is(err, ErrNotLive) {
		t.Errorf("second terminate err = %v, want ErrNotLive", err)
	}
}

// TestOwnerEndStoresNoReason: the creator's own end is the same mechanism, and
// must never look like a takedown to the UI that renders it.
func TestOwnerEndStoresNoReason(t *testing.T) {
	auditor := &fakeAuditor{}
	svc, _, ctrl, id := liveStreamFixture(t, WithAuditor(auditor))
	owner := uuid.New()
	if _, err := svc.Terminate(context.Background(), id, TerminateInput{
		Source: SourceOwner, ActorID: owner, ActorRole: "user",
	}); err != nil {
		t.Fatalf("owner end: %v", err)
	}
	// A26 measured the owner's own end writing NO audit row at all: the one
	// deliberate end of a broadcast a person performs that left no trace.
	ev := findAudit(t, auditor, observability.ActionLiveEnded)
	if ev.ResourceID != id.String() || ev.ResourceType != "live_stream" {
		t.Errorf("audit resource = %s/%s, want live_stream/%s", ev.ResourceType, ev.ResourceID, id)
	}
	if ev.Actor.Kind != "user" || ev.Actor.ID != owner.String() || ev.Actor.Role != "user" {
		t.Errorf("audit actor = %+v, want the creator with their role", ev.Actor)
	}
	if ev.Reason != "owner_ended" {
		t.Errorf("audit reason = %q, want owner_ended", ev.Reason)
	}
	for _, e := range auditor.events {
		if e.Action == observability.ActionLiveTerminate {
			t.Error("the creator's own end wrote a MODERATION action; it is not one, and an operator filtering the trail must not have to tell them apart by the actor")
		}
	}
	st, _ := svc.Get(context.Background(), id)
	if !st.Terminated() {
		t.Fatal("the owner's end did not stamp terminated_at")
	}
	if st.TerminatedByModerator() {
		t.Error("the creator's own end reads as a moderation action")
	}
	if st.TerminatedBy != nil {
		t.Error("the owner's end named an actor; it stores none")
	}
	if len(ctrl.dropped) != 1 {
		t.Error("the owner's end did not disconnect the publisher — an encoder that reconnects on its own would put the stream back on air")
	}
}

// TestTerminateWriteFailureLeavesTheStreamOnAir: the ONE failure that is the
// caller's error rather than a partial result. Nothing else may have run.
func TestTerminateWriteFailureLeavesTheStreamOnAir(t *testing.T) {
	svc, repo, ctrl, id := liveStreamFixture(t)
	repo.terminateErr = errors.New("boom")
	before := repo.hashes[id]
	if _, err := svc.Terminate(context.Background(), id, TerminateInput{ReasonCode: string(ReasonOther)}); err == nil {
		t.Fatal("a failed state write reported success")
	}
	if repo.hashes[id] != before {
		t.Error("the key was rotated after the state write failed — the stream is still live and its owner's key just stopped working")
	}
	if ctrl.calls != 0 {
		t.Error("the publisher was dropped after the state write failed, leaving a live row with no publisher")
	}
}

// TestTerminateKeyRotationFailureIsReported is the worst partial outcome: off the
// air, but the credential that started it still works.
func TestTerminateKeyRotationFailureIsReported(t *testing.T) {
	auditor := &fakeAuditor{}
	svc, repo, _, id := liveStreamFixture(t, WithAuditor(auditor))
	repo.keyRotateErr = errors.New("nope")
	res, err := svc.Terminate(context.Background(), id, TerminateInput{ReasonCode: string(ReasonCopyright)})
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if res.KeyRotated {
		t.Error("result claims a rotation that failed")
	}
	ev := findAudit(t, auditor, observability.ActionLiveTerminate)
	if ev.Reason != string(ReasonCopyright)+" key_not_rotated" {
		t.Errorf("audit reason = %q; a partial outcome must be reconstructable from the trail a week later", ev.Reason)
	}
}

// TestTerminationReasonsAreDistinct guards the allow-list against a duplicate
// slipping in, which would make one code unreachable in a picker.
func TestTerminationReasonsAreDistinct(t *testing.T) {
	seen := map[TerminationReason]bool{}
	for _, r := range TerminationReasons {
		if seen[r] {
			t.Errorf("duplicate reason code %q", r)
		}
		seen[r] = true
		if !ValidTerminationReason(string(r)) {
			t.Errorf("%q is in the list but ValidTerminationReason rejects it", r)
		}
	}
	if ValidTerminationReason("") || ValidTerminationReason("nonsense") {
		t.Error("ValidTerminationReason accepted a value outside the set")
	}
}

// TestIngestControlNilWithoutConfig: the constructor returns nil rather than a
// controller that would turn every drop into a wrapped error instead of the
// honest "not configured" degrade.
func TestIngestControlNilWithoutConfig(t *testing.T) {
	if NewHTTPIngestController("", "secret") != nil {
		t.Error("a controller was built with no base URL")
	}
	if NewHTTPIngestController("http://rtmp:8082", "") != nil {
		t.Error("a controller was built with no shared secret; an unauthenticated control surface is worse than none")
	}
	if NewHTTPIngestController("http://rtmp:8082/", "s") == nil {
		t.Error("a fully configured controller was refused")
	}
}

// TestIngestAppIsTheIngestApplication pins the literal the shipped nginx config
// declares. Dropping on `hls` instead would tear down the packaging push and
// leave the streamer connected — the audience goes dark and the publisher does
// not even notice.
func TestIngestAppIsTheIngestApplication(t *testing.T) {
	if ingestAppLive != "live" {
		t.Errorf("ingest app = %q, want \"live\" — it must match `application live` in deploy/media/nginx.conf.template", ingestAppLive)
	}
}

// TestLiveViewerDigestDomainIsItsOwn: a live viewer digest and a QoE viewer
// digest for the same person on the same day must be unrelated values, or the
// two datasets can be joined.
func TestLiveViewerDigestDomainIsItsOwn(t *testing.T) {
	if ViewerDigestDomain == "vidra/qoe-viewer-digest/v1" {
		t.Error("the live viewer digest reuses QoE's domain label; the two datasets would be joinable")
	}
}

// findAudit returns the one event with the given action.
func findAudit(t *testing.T, a *fakeAuditor, action string) auditEventView {
	t.Helper()
	// An event the audit envelope rejects is not an audit event: Record's error
	// is discarded everywhere in this package, so it leaves no row and no log.
	for _, err := range a.rejected {
		t.Errorf("a recorded audit event does not survive internal/audit's envelope: %v", err)
	}
	for _, ev := range a.events {
		if ev.Action == action {
			meta := map[string]string{}
			for _, f := range ev.Metadata {
				meta[f.Key] = f.Value
			}
			return auditEventView{
				Action: ev.Action, Result: ev.Result, Reason: ev.Reason,
				ResourceType: ev.ResourceType, ResourceID: ev.ResourceID, Metadata: meta,
				Actor: actorView{Kind: ev.Actor.Kind, ID: ev.Actor.ID, Role: ev.Actor.Role},
			}
		}
	}
	t.Fatalf("no %s audit event in %d recorded", action, len(a.events))
	return auditEventView{}
}

type auditEventView struct {
	Action, Result, Reason, ResourceType, ResourceID string
	Metadata                                         map[string]string
	Actor                                            actorView
}
type actorView struct{ Kind, ID, Role string }

var _ = time.Second

// TestIngestTemplateRunsOneWorker pins `worker_processes 1` in the shipped
// media config, and it is the drop contract's real precondition.
//
// nginx-rtmp's control and stat modules are PER WORKER: each nginx worker keeps
// its own stream index, and a control request can only reach publishers held by
// whichever worker happens to accept that HTTP connection. Under
// `worker_processes auto` the RTMP publisher lands on one worker and the control
// request on another, so the drop matches nothing — and this module build
// answers a no-match with 200 and a body of "0" rather than 404, which
// DropPublisher reads as success. The A26 rehearsal measured exactly that: 12
// consecutive drops against a live publisher all returned "0", the streamer
// stayed connected and kept writing segments and a recording for as long as the
// lab let it, and the moderator was told `publisher_disconnected: true` every
// time. One worker is what makes the control surface see the sessions it is
// asked about; an RTMP ingest that transcodes nothing does not need more.
func TestIngestTemplateRunsOneWorker(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "media", "nginx.conf.template"))
	if err != nil {
		t.Fatalf("read the shipped media template: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "worker_processes 1;") {
		t.Error("deploy/media/nginx.conf.template does not declare `worker_processes 1;`: " +
			"nginx-rtmp's control module is per-worker, so a drop issued to any other worker " +
			"silently matches nothing and the termination never reaches the publisher")
	}
	if strings.Contains(body, "worker_processes auto;") {
		t.Error("deploy/media/nginx.conf.template still declares `worker_processes auto;`")
	}
}

// TestWatchdogDisconnectsThePublisher is SC2, and the defect it pins is what
// A26 measured: the duration watchdog force-closed an over-limit session,
// flipped the state and 404'd the playlist — and the publisher was STILL
// INGESTING afterwards. "Force-closed" named something that had not happened to
// the only party uploading bytes.
func TestWatchdogDisconnectsThePublisher(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	ctrl := &fakeIngestControl{}
	auditor := &fakeAuditor{}
	now := time.Now()
	svc := NewService(repo,
		WithIngestController(ctrl),
		WithAuditor(auditor),
		WithMaxDurationSecsFunc(func() int64 { return 3600 }),
		WithNowFunc(func() time.Time { return now }),
	)
	st, key, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "Over the limit"})
	if _, err := svc.StartIngest(ctx, key); err != nil {
		t.Fatalf("start: %v", err)
	}
	beforeKey := repo.hashes[st.ID]
	r := repo.rows[st.ID]
	r.StartedAt = pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true}
	repo.rows[st.ID] = r

	n, err := svc.SweepOverdueLive(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep = (%d, %v), want (1, nil)", n, err)
	}
	if len(ctrl.dropped) != 1 || ctrl.dropped[0] != st.ID.String() {
		t.Errorf("dropped %v, want exactly [%s] — the watchdog cut the AUDIENCE and left the publisher uploading, which is the A26 finding", ctrl.dropped, st.ID)
	}
	if repo.hashes[st.ID] == beforeKey {
		t.Error("the key was not rotated: OBS reconnects within seconds on the key it holds and the next sweep cuts it again, so an over-limit publisher bounces every 30 s instead of stopping")
	}
	if got := repo.rows[st.ID].State; got != StateEnded {
		t.Errorf("state = %q, want ended", got)
	}

	// The row keeps NO termination columns. A duration cut is not a person's
	// decision, and the creator-facing copy for a stamped terminated_at with no
	// reason code is "You ended this stream".
	got, _ := svc.Get(ctx, st.ID)
	if got.Terminated() {
		t.Error("the watchdog stamped terminated_at; the creator's page would tell them they ended a stream the instance cut")
	}

	ev := findAudit(t, auditor, observability.ActionLiveForceClose)
	if ev.ResourceType != "live_stream" || ev.ResourceID != st.ID.String() {
		t.Errorf("audit resource = %s/%s, want live_stream/%s — A26 measured force_close leaving resource_id EMPTY", ev.ResourceType, ev.ResourceID, st.ID)
	}
	if ev.Reason != SystemReasonMaxDuration {
		t.Errorf("audit reason = %q, want %q", ev.Reason, SystemReasonMaxDuration)
	}
	if ev.Actor.Kind != "system" || ev.Actor.ID != "" {
		t.Errorf("audit actor = %+v, want a bare system actor", ev.Actor)
	}
	if ev.Metadata["count"] != "1" {
		t.Errorf("audit metadata count = %q, want \"1\"", ev.Metadata["count"])
	}
}

// TestWatchdogWithoutControlStillCloses: an instance with no control surface
// keeps exactly the behaviour it had — the state flip — rather than failing the
// sweep, and the trail says the publisher was not dropped.
func TestWatchdogWithoutControlStillCloses(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo(uuid.New())
	auditor := &fakeAuditor{}
	now := time.Now()
	svc := NewService(repo,
		WithAuditor(auditor),
		WithMaxDurationSecsFunc(func() int64 { return 60 }),
		WithNowFunc(func() time.Time { return now }),
	)
	st, key, _ := svc.Create(ctx, uuid.New(), CreateInput{Title: "t"})
	if _, err := svc.StartIngest(ctx, key); err != nil {
		t.Fatalf("start: %v", err)
	}
	r := repo.rows[st.ID]
	r.StartedAt = pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}
	repo.rows[st.ID] = r

	if n, err := svc.SweepOverdueLive(ctx); err != nil || n != 1 {
		t.Fatalf("sweep = (%d, %v), want (1, nil)", n, err)
	}
	if got := repo.rows[st.ID].State; got != StateEnded {
		t.Errorf("state = %q, want ended", got)
	}
	ev := findAudit(t, auditor, observability.ActionLiveForceClose)
	if ev.Reason != SystemReasonMaxDuration+" publisher_not_dropped" {
		t.Errorf("audit reason = %q, want the code plus publisher_not_dropped", ev.Reason)
	}
	if _, ok := ev.Metadata["count"]; ok {
		t.Error("a count was recorded on an instance that never asked an ingest anything")
	}
}
