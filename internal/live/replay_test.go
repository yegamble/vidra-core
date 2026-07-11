package live

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// fakePipeline records the replay pipeline calls so tests can assert the draft
// metadata, attached bytes, and processed key.
type fakePipeline struct {
	draftChannel uuid.UUID
	draftInput   video.CreateInput
	draftID      uuid.UUID
	attachOwner  uuid.UUID
	attachVideo  uuid.UUID
	attachName   string
	attachBytes  []byte
	processedKey string
	createCalls  int

	createErr, attachErr, processErr error
}

func (f *fakePipeline) CreateDraft(_ context.Context, channelID uuid.UUID, in video.CreateInput) (sqlcgen.Video, error) {
	f.createCalls++
	f.draftChannel, f.draftInput = channelID, in
	if f.createErr != nil {
		return sqlcgen.Video{}, f.createErr
	}
	f.draftID = uuid.New()
	return sqlcgen.Video{ID: f.draftID, ChannelID: channelID}, nil
}

func (f *fakePipeline) AttachOriginal(_ context.Context, ownerID, videoID uuid.UUID, in video.UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error) {
	f.attachOwner, f.attachVideo, f.attachName = ownerID, videoID, in.Filename
	b, _ := io.ReadAll(in.Reader)
	f.attachBytes = b
	if f.attachErr != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, f.attachErr
	}
	return sqlcgen.Video{ID: videoID}, sqlcgen.VideoFile{StorageKey: "videos/" + videoID.String() + "/original.flv"}, nil
}

func (f *fakePipeline) Process(_ context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error) {
	f.processedKey = originalKey
	if f.processErr != nil {
		return sqlcgen.Video{}, f.processErr
	}
	return sqlcgen.Video{ID: videoID, State: "published"}, nil
}

// fakeRecordingStore returns fixed bytes, or ErrNoRecording when data is nil.
type fakeRecordingStore struct {
	data     []byte
	filename string
	err      error
}

func (f *fakeRecordingStore) OpenRecording(uuid.UUID) (io.ReadCloser, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	if f.data == nil {
		return nil, "", ErrNoRecording
	}
	return io.NopCloser(bytes.NewReader(f.data)), f.filename, nil
}

// fakeAuditor collects audit events.
type fakeAuditor struct{ events []audit.Event }

func (f *fakeAuditor) Record(_ context.Context, ev audit.Event) error {
	f.events = append(f.events, ev)
	return nil
}

// replayStream creates a stream with the given replay flag and returns svc + id.
func replayStream(t *testing.T, repo *fakeRepo, opts []Option, replay bool) (*Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	svc := NewService(repo, opts...)
	ch := uuid.New()
	s, _, err := svc.Create(context.Background(), ch, CreateInput{
		Title: "My Show", Description: "desc", Privacy: "unlisted", ReplayEnabled: replay,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return svc, s.ID, ch
}

func TestRunReplayPublishesRecording(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	pipe := &fakePipeline{}
	auditor := &fakeAuditor{}
	svc, id, ch := replayStream(t, repo, []Option{
		WithReplayPipeline(pipe), WithRecordingStore(&fakeRecordingStore{data: []byte("SESSION"), filename: "session.flv"}),
		WithAuditor(auditor),
	}, true)

	svc.RunReplay(context.Background(), id)

	if pipe.createCalls != 1 {
		t.Fatalf("CreateDraft called %d times, want 1", pipe.createCalls)
	}
	if pipe.draftChannel != ch {
		t.Errorf("draft channel = %v, want the stream's channel %v", pipe.draftChannel, ch)
	}
	if pipe.draftInput.Title != "My Show (replay)" {
		t.Errorf("draft title = %q, want the stream title + replay suffix", pipe.draftInput.Title)
	}
	if pipe.draftInput.Privacy != "unlisted" {
		t.Errorf("draft privacy = %q, want inherited 'unlisted'", pipe.draftInput.Privacy)
	}
	if pipe.attachOwner != owner {
		t.Errorf("attach owner = %v, want the channel owner %v", pipe.attachOwner, owner)
	}
	if pipe.attachVideo != pipe.draftID {
		t.Errorf("attached to %v, want the freshly created draft %v", pipe.attachVideo, pipe.draftID)
	}
	if string(pipe.attachBytes) != "SESSION" {
		t.Errorf("attached bytes = %q, want the recording content", string(pipe.attachBytes))
	}
	if pipe.processedKey == "" || pipe.processedKey != "videos/"+pipe.draftID.String()+"/original.flv" {
		t.Errorf("processed key = %q, want the attached original's key", pipe.processedKey)
	}
	if len(auditor.events) != 1 || auditor.events[0].Result != observability.ResultSuccess ||
		auditor.events[0].Action != observability.ActionLiveReplay || auditor.events[0].Actor.Kind != "system" {
		t.Errorf("audit = %+v, want one success content.live.replay event", auditor.events)
	}
}

func TestRunReplaySkippedWhenDisabled(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	pipe := &fakePipeline{}
	svc, id, _ := replayStream(t, repo, []Option{
		WithReplayPipeline(pipe), WithRecordingStore(&fakeRecordingStore{data: []byte("x"), filename: "r.flv"}),
	}, false) // replay disabled

	svc.RunReplay(context.Background(), id)
	if pipe.createCalls != 0 {
		t.Errorf("CreateDraft called %d times for a replay-disabled stream, want 0", pipe.createCalls)
	}
}

func TestRunReplayNoRecordingIsQuietNoop(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	pipe := &fakePipeline{}
	auditor := &fakeAuditor{}
	svc, id, _ := replayStream(t, repo, []Option{
		WithReplayPipeline(pipe), WithRecordingStore(&fakeRecordingStore{}), // no data → ErrNoRecording
		WithAuditor(auditor),
	}, true)

	svc.RunReplay(context.Background(), id)
	if pipe.createCalls != 0 {
		t.Errorf("CreateDraft called %d times with no recording, want 0", pipe.createCalls)
	}
	if len(auditor.events) != 0 {
		t.Errorf("audit events = %+v, want none (a missing recording is not a failure)", auditor.events)
	}
}

func TestRunReplayFailureIsSwallowedAndAudited(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	pipe := &fakePipeline{processErr: errors.New("probe blew up")}
	auditor := &fakeAuditor{}
	svc, id, _ := replayStream(t, repo, []Option{
		WithReplayPipeline(pipe), WithRecordingStore(&fakeRecordingStore{data: []byte("x"), filename: "r.flv"}),
		WithAuditor(auditor),
	}, true)

	// Must not panic / must return normally despite the pipeline error.
	svc.RunReplay(context.Background(), id)
	if len(auditor.events) != 1 || auditor.events[0].Result != observability.ResultFailure {
		t.Errorf("audit = %+v, want one failure event", auditor.events)
	}
}

func TestRunReplayNoopWhenUnconfigured(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	svc, id, _ := replayStream(t, repo, nil, true) // no pipeline/store wired
	// No panic, no work — replay stays dormant on an instance without a media plane.
	svc.RunReplay(context.Background(), id)
}
