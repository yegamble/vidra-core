package video

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// uploadAndProcess creates a draft under a fresh channel, uploads an original,
// and runs Process — the full path a real upload takes to a terminal state.
func uploadAndProcess(t *testing.T, svc *Service, owner uuid.UUID, in CreateInput) uuid.UUID {
	t.Helper()
	v, err := svc.CreateDraft(context.Background(), uuid.New(), in)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	_, file, err := svc.AttachOriginal(context.Background(), owner, v.ID, UploadInput{
		Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader("bytes"),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	if _, err := svc.Process(context.Background(), v.ID, file.StorageKey); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return v.ID
}

// holdHarness builds a publish-after-transcode-ready service (transcoding "ready"
// per the ready arg) with publish + transcode hook recorders, then uploads and
// processes a draft created from in.
func holdHarness(t *testing.T, in CreateInput, ready bool, published *[]uuid.UUID, transcoded *[]string) (*Service, *fakeRepo, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, memBackend(t),
		WithPublishHook(func(_ context.Context, id uuid.UUID) { *published = append(*published, id) }),
		WithTranscodeHook(func(_ context.Context, _ uuid.UUID, key string) { *transcoded = append(*transcoded, key) }),
		WithTranscodeReadinessFunc(func() bool { return ready }),
	)
	id := uploadAndProcess(t, svc, owner, in)
	return svc, repo, id
}

// TestProcessHoldsForPublishAfterTranscode proves the flag + a ready transcoder
// parks the video in 'transcoding' with the transcode enqueue fired directly and
// NO publish hooks (it is on no public surface yet).
func TestProcessHoldsForPublishAfterTranscode(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	_, repo, id := holdHarness(t, CreateInput{Title: "hold", Privacy: "public", PublishAfterTranscode: true},
		true, &published, &transcoded)

	if got := repo.videos[id].State; got != "transcoding" {
		t.Fatalf("state after Process = %q, want transcoding", got)
	}
	if len(published) != 0 {
		t.Fatalf("publish hooks fired on hold: %v", published)
	}
	if len(transcoded) != 1 || !strings.Contains(transcoded[0], id.String()) {
		t.Fatalf("transcode hook = %v, want the original's key fired once", transcoded)
	}
}

// TestProcessPublishesWhenTranscodingUnavailable proves the flag is IGNORED when
// transcoding will not run (no ffmpeg / disabled): the video publishes
// immediately rather than stranding behind a gate that can never open.
func TestProcessPublishesWhenTranscodingUnavailable(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	_, repo, id := holdHarness(t, CreateInput{Title: "no-ffmpeg", Privacy: "public", PublishAfterTranscode: true},
		false, &published, &transcoded)

	if got := repo.videos[id].State; got != "published" {
		t.Fatalf("state after Process = %q, want published (never stranded behind a closed gate)", got)
	}
	if len(published) != 1 || len(transcoded) != 1 {
		t.Fatalf("hooks after immediate publish: publish=%v transcode=%v", published, transcoded)
	}
}

// TestProcessScheduledTrumpsHold proves a future publish_at takes precedence over
// the flag (v1 scope: no combined time+transcode gate).
func TestProcessScheduledTrumpsHold(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	future := time.Now().Add(time.Hour)
	_, repo, id := holdHarness(t, CreateInput{Title: "later+hold", Privacy: "public", PublishAt: &future, PublishAfterTranscode: true},
		true, &published, &transcoded)

	if got := repo.videos[id].State; got != "scheduled" {
		t.Fatalf("state after Process = %q, want scheduled (publish_at trumps the flag)", got)
	}
	if len(published) != 0 || len(transcoded) != 0 {
		t.Fatalf("hooks fired on scheduled hold: publish=%v transcode=%v", published, transcoded)
	}
}

// TestProcessQuarantineTrumpsHold proves the moderation quarantine gate takes
// precedence over the transcode hold.
func TestProcessQuarantineTrumpsHold(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	repo.requiresQuarantine = true
	var published []uuid.UUID
	var transcoded []string
	svc := NewService(repo, memBackend(t),
		WithQuarantineNewUploads(true),
		WithPublishHook(func(_ context.Context, id uuid.UUID) { published = append(published, id) }),
		WithTranscodeHook(func(_ context.Context, _ uuid.UUID, key string) { transcoded = append(transcoded, key) }),
		WithTranscodeReadinessFunc(func() bool { return true }),
	)
	id := uploadAndProcess(t, svc, owner, CreateInput{Title: "held", Privacy: "public", PublishAfterTranscode: true})

	if got := repo.videos[id].State; got != "quarantined" {
		t.Fatalf("state = %q, want quarantined (moderation trumps the transcode hold)", got)
	}
	if len(published) != 0 || len(transcoded) != 0 {
		t.Fatalf("hooks fired on quarantine hold: publish=%v transcode=%v", published, transcoded)
	}
}

// TestProcessFailedScanNeverReachesHold proves an infected upload fails before
// the hold decision is ever considered (no transcode enqueue).
func TestProcessFailedScanNeverReachesHold(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	var transcoded []string
	svc := NewService(repo, nil,
		WithScanner(fakeScanner{clean: false}),
		WithTranscodeHook(func(_ context.Context, _ uuid.UUID, key string) { transcoded = append(transcoded, key) }),
		WithTranscodeReadinessFunc(func() bool { return true }),
	)
	v, err := svc.CreateDraft(context.Background(), uuid.New(), CreateInput{Title: "infected", Privacy: "public", PublishAfterTranscode: true})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	got, err := svc.Process(context.Background(), v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed (an infected upload never reaches the hold)", got.State)
	}
	if len(transcoded) != 0 {
		t.Fatalf("transcode hook fired on a failed scan: %v", transcoded)
	}
}

// TestReleaseTranscodeHoldPublishes proves releasing a held video runs the real
// publish transition — publish hooks fire — WITHOUT re-firing the transcode hook
// (the transcode already ran while held), and is idempotent.
func TestReleaseTranscodeHoldPublishes(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	svc, repo, id := holdHarness(t, CreateInput{Title: "hold", Privacy: "public", PublishAfterTranscode: true},
		true, &published, &transcoded)
	if repo.videos[id].State != "transcoding" || len(published) != 0 || len(transcoded) != 1 {
		t.Fatalf("precondition: state=%q published=%v transcoded=%v", repo.videos[id].State, published, transcoded)
	}

	if err := svc.ReleaseTranscodeHold(context.Background(), id); err != nil {
		t.Fatalf("ReleaseTranscodeHold: %v", err)
	}
	if got := repo.videos[id].State; got != "published" {
		t.Fatalf("state after release = %q, want published", got)
	}
	if len(published) != 1 || published[0] != id {
		t.Errorf("publish hook after release = %v, want [%s]", published, id)
	}
	if len(transcoded) != 1 {
		t.Errorf("transcode hook re-fired on release: %v, want the single hold-time enqueue", transcoded)
	}

	// Idempotent: a second release is a no-op (state already published).
	if err := svc.ReleaseTranscodeHold(context.Background(), id); err != nil {
		t.Fatalf("second ReleaseTranscodeHold: %v", err)
	}
	if len(published) != 1 {
		t.Errorf("publish hook fired again on idempotent release: %v", published)
	}
}

// TestReleaseTranscodeHoldNoopOnPublished proves the release is a no-op for any
// non-'transcoding' state.
func TestReleaseTranscodeHoldNoopOnPublished(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	svc, repo, id := holdHarness(t, CreateInput{Title: "live", Privacy: "public"}, true, &published, &transcoded)
	if repo.videos[id].State != "published" {
		t.Fatalf("precondition state = %q, want published", repo.videos[id].State)
	}
	before := len(published)
	if err := svc.ReleaseTranscodeHold(context.Background(), id); err != nil {
		t.Fatalf("ReleaseTranscodeHold: %v", err)
	}
	if repo.videos[id].State != "published" || len(published) != before {
		t.Errorf("release on a published video mutated it: state=%q published=%v", repo.videos[id].State, published)
	}
}

// TestUpdateHoldsPublishedPrivateWithLiveJob proves the update edge case: turning
// the flag on for an already-published, not-yet-public video with a live job
// holds it in 'transcoding' (the "fast upload published before Publish" case).
func TestUpdateHoldsPublishedPrivateWithLiveJob(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	var published []uuid.UUID
	svc := NewService(repo, memBackend(t),
		WithPublishHook(func(_ context.Context, id uuid.UUID) { published = append(published, id) }),
		WithLiveTranscodeChecker(func(_ context.Context, _ uuid.UUID) bool { return true }),
	)
	id := uploadAndProcess(t, svc, owner, CreateInput{Title: "fast", Privacy: "private"})
	if repo.videos[id].State != "published" {
		t.Fatalf("precondition state = %q, want published", repo.videos[id].State)
	}

	updated, err := svc.Update(context.Background(), owner, id, UpdateInput{
		Privacy: strPtr("public"), PublishAfterTranscode: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.State != "transcoding" {
		t.Fatalf("state after toggle = %q, want transcoding (held until the live job finishes)", updated.State)
	}
	if repo.videos[id].State != "transcoding" {
		t.Fatalf("stored state = %q, want transcoding", repo.videos[id].State)
	}
	if updated.Privacy != "public" {
		t.Errorf("privacy = %q, want public (the toggle also flips it public)", updated.Privacy)
	}
	if !updated.PublishAfterTranscode {
		t.Errorf("publish_after_transcode not stored")
	}
}

// TestUpdateStoresFlagWhenAlreadyPublic proves a publicly-visible video is never
// yanked back to hidden: the flag is stored with no state change.
func TestUpdateStoresFlagWhenAlreadyPublic(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, memBackend(t),
		WithLiveTranscodeChecker(func(_ context.Context, _ uuid.UUID) bool { return true }),
	)
	id := uploadAndProcess(t, svc, owner, CreateInput{Title: "public", Privacy: "public"})
	if repo.videos[id].State != "published" {
		t.Fatalf("precondition state = %q, want published", repo.videos[id].State)
	}

	updated, err := svc.Update(context.Background(), owner, id, UpdateInput{PublishAfterTranscode: boolPtr(true)})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.State != "published" {
		t.Fatalf("state = %q, want published (a publicly-visible video is never hidden)", updated.State)
	}
	if !updated.PublishAfterTranscode {
		t.Errorf("publish_after_transcode not stored")
	}
}

// TestReleaseStuckTranscodeHolds proves the safety-net sweeper releases videos
// parked in the hold past the timeout (and leaves fresh holds alone), publishing
// through ReleaseTranscodeHold without re-firing the transcode.
func TestReleaseStuckTranscodeHolds(t *testing.T) {
	var published []uuid.UUID
	var transcoded []string
	svc, repo, id := holdHarness(t, CreateInput{Title: "stuck", Privacy: "public", PublishAfterTranscode: true},
		true, &published, &transcoded)
	if repo.videos[id].State != "transcoding" {
		t.Fatalf("precondition state = %q, want transcoding", repo.videos[id].State)
	}

	// Fresh hold: the sweep is a no-op.
	if n, err := svc.ReleaseStuckTranscodeHolds(context.Background(), time.Hour, 10); err != nil || n != 0 {
		t.Fatalf("sweep before timeout = (%d, %v), want (0, nil)", n, err)
	}
	if repo.videos[id].State != "transcoding" {
		t.Fatalf("state changed by an early sweep = %q", repo.videos[id].State)
	}

	// Backdate the hold past the timeout, then sweep.
	row := repo.videos[id]
	row.UpdatedAt = time.Now().Add(-2 * time.Hour)
	repo.videos[id] = row
	n, err := svc.ReleaseStuckTranscodeHolds(context.Background(), time.Hour, 10)
	if err != nil || n != 1 {
		t.Fatalf("sweep = (%d, %v), want (1, nil)", n, err)
	}
	if got := repo.videos[id].State; got != "published" {
		t.Fatalf("state after sweep = %q, want published", got)
	}
	if len(published) != 1 || published[0] != id {
		t.Errorf("publish hook after sweep = %v, want [%s]", published, id)
	}
	if len(transcoded) != 1 {
		t.Errorf("transcode hook re-fired on sweep: %v, want the single hold-time enqueue", transcoded)
	}

	// Idempotent: nothing stuck now.
	if n, _ := svc.ReleaseStuckTranscodeHolds(context.Background(), time.Hour, 10); n != 0 {
		t.Fatalf("second sweep = %d, want 0", n)
	}
}
