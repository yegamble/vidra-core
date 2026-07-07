package video

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- fakeRepo chapter methods (mirror the video_chapters queries) ---

func (f *fakeRepo) ListVideoChapters(_ context.Context, videoID uuid.UUID) ([]sqlcgen.ListVideoChaptersRow, error) {
	rows := append([]sqlcgen.ListVideoChaptersRow(nil), f.chapters[videoID]...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartSeconds < rows[j].StartSeconds })
	return rows, nil
}

func (f *fakeRepo) VideoHasChapters(_ context.Context, videoID uuid.UUID) (bool, error) {
	return len(f.chapters[videoID]) > 0, nil
}

func (f *fakeRepo) DeleteVideoChapters(_ context.Context, videoID uuid.UUID) error {
	delete(f.chapters, videoID)
	return nil
}

func (f *fakeRepo) InsertVideoChapters(_ context.Context, a sqlcgen.InsertVideoChaptersParams) error {
	if f.chapters == nil {
		f.chapters = map[uuid.UUID][]sqlcgen.ListVideoChaptersRow{}
	}
	for i := range a.StartSeconds {
		f.chapters[a.VideoID] = append(f.chapters[a.VideoID], sqlcgen.ListVideoChaptersRow{
			StartSeconds: a.StartSeconds[i], Title: a.Title[i],
		})
	}
	return nil
}

// --- tests ---

func i32(v int32) *int32 { return &v }

// TestValidateChapters is the pure validation table: ordering, bounds, title
// length, count cap, and the duration clamp.
func TestValidateChapters(t *testing.T) {
	long := strings.Repeat("x", MaxChapterTitleLen)      // 120 — ok
	tooLong := strings.Repeat("x", MaxChapterTitleLen+1) // 121 — rejected
	overCap := make([]Chapter, MaxChapters+1)
	for i := range overCap {
		overCap[i] = Chapter{StartSeconds: int32(i), Title: "c"}
	}
	atCap := make([]Chapter, MaxChapters)
	for i := range atCap {
		atCap[i] = Chapter{StartSeconds: int32(i), Title: "c"}
	}

	cases := []struct {
		name     string
		in       []Chapter
		duration *int32
		wantErr  bool
	}{
		{"empty is valid (clears)", nil, nil, false},
		{"single at zero", []Chapter{{0, "Intro"}}, nil, false},
		{"ascending ok", []Chapter{{0, "Intro"}, {30, "Middle"}, {90, "End"}}, nil, false},
		{"not strictly increasing", []Chapter{{0, "a"}, {0, "b"}}, nil, true},
		{"descending", []Chapter{{30, "a"}, {10, "b"}}, nil, true},
		{"negative start", []Chapter{{-1, "a"}}, nil, true},
		{"empty title", []Chapter{{0, ""}}, nil, true},
		{"whitespace-only title", []Chapter{{0, "   "}}, nil, true},
		{"title at max ok", []Chapter{{0, long}}, nil, false},
		{"title over max", []Chapter{{0, tooLong}}, nil, true},
		{"at cap ok", atCap, nil, false},
		{"over cap", overCap, nil, true},
		{"start before duration ok", []Chapter{{0, "a"}, {59, "b"}}, i32(60), false},
		{"start equals duration", []Chapter{{60, "a"}}, i32(60), true},
		{"start past duration", []Chapter{{61, "a"}}, i32(60), true},
		{"duration zero disables clamp", []Chapter{{9999, "a"}}, i32(0), false},
		{"no duration disables clamp", []Chapter{{9999, "a"}}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateChapters(tc.in, tc.duration)
			if tc.wantErr && err == nil {
				t.Fatalf("expected a validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr {
				var ve *ChapterValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("error = %T, want *ChapterValidationError", err)
				}
			}
		})
	}
}

// TestValidateChaptersTrimsTitle proves the stored title is trimmed.
func TestValidateChaptersTrimsTitle(t *testing.T) {
	out, err := validateChapters([]Chapter{{0, "  Intro  "}}, nil)
	if err != nil {
		t.Fatalf("validateChapters: %v", err)
	}
	if out[0].Title != "Intro" {
		t.Fatalf("title = %q, want %q", out[0].Title, "Intro")
	}
}

func newChapterVideo(t *testing.T, svc *Service, owner uuid.UUID) uuid.UUID {
	t.Helper()
	v, err := svc.CreateDraft(context.Background(), uuid.New(), CreateInput{Title: "v", Privacy: "public"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	return v.ID
}

// TestReplaceChaptersStoresSortedSet proves a replace stores the set and
// ListChapters returns it in ascending order.
func TestReplaceChaptersStoresSortedSet(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := newChapterVideo(t, svc, owner)

	stored, err := svc.ReplaceChapters(ctx, owner, id, []Chapter{{0, "Intro"}, {30, "Body"}, {75, "Outro"}})
	if err != nil {
		t.Fatalf("ReplaceChapters: %v", err)
	}
	if len(stored) != 3 || stored[0].StartSeconds != 0 || stored[2].StartSeconds != 75 {
		t.Fatalf("stored = %+v", stored)
	}
	got, err := svc.ListChapters(ctx, id)
	if err != nil {
		t.Fatalf("ListChapters: %v", err)
	}
	if len(got) != 3 || got[0].Title != "Intro" || got[1].StartSeconds != 30 || got[2].Title != "Outro" {
		t.Fatalf("ListChapters = %+v", got)
	}
	if !svc.HasChapters(ctx, id) {
		t.Fatalf("HasChapters = false, want true")
	}
}

// TestReplaceChaptersEmptyClears proves an empty replace clears the set.
func TestReplaceChaptersEmptyClears(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := newChapterVideo(t, svc, owner)

	if _, err := svc.ReplaceChapters(ctx, owner, id, []Chapter{{0, "Intro"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.ReplaceChapters(ctx, owner, id, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ := svc.ListChapters(ctx, id)
	if len(got) != 0 {
		t.Fatalf("after clear = %+v, want none", got)
	}
	if svc.HasChapters(ctx, id) {
		t.Fatalf("HasChapters = true after clear, want false")
	}
}

// TestReplaceChaptersInvalidLeavesOldSet proves a rejected (invalid) replace is
// validated before any write, so the previously stored chapters survive intact.
func TestReplaceChaptersInvalidLeavesOldSet(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := newChapterVideo(t, svc, owner)

	orig := []Chapter{{0, "Intro"}, {30, "Body"}}
	if _, err := svc.ReplaceChapters(ctx, owner, id, orig); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A non-ascending batch must be rejected without disturbing the stored set.
	_, err := svc.ReplaceChapters(ctx, owner, id, []Chapter{{50, "a"}, {10, "b"}})
	var ve *ChapterValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ChapterValidationError", err)
	}
	got, _ := svc.ListChapters(ctx, id)
	if len(got) != 2 || got[0].Title != "Intro" || got[1].StartSeconds != 30 {
		t.Fatalf("old set disturbed: %+v", got)
	}
}

// TestReplaceChaptersDurationClamp proves the probed duration bounds the starts.
func TestReplaceChaptersDurationClamp(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)
	id := newChapterVideo(t, svc, owner)
	if _, err := repo.UpsertVideoMetadata(ctx, sqlcgen.UpsertVideoMetadataParams{VideoID: id, DurationSeconds: i32(60)}); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	if _, err := svc.ReplaceChapters(ctx, owner, id, []Chapter{{0, "a"}, {59, "b"}}); err != nil {
		t.Fatalf("within duration: %v", err)
	}
	_, err := svc.ReplaceChapters(ctx, owner, id, []Chapter{{0, "a"}, {60, "b"}})
	var ve *ChapterValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ChapterValidationError (start >= duration)", err)
	}
}

// TestReplaceChaptersOwnership proves only the owner may write.
func TestReplaceChaptersOwnership(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	svc := NewService(newFakeRepo(owner), nil)
	id := newChapterVideo(t, svc, owner)

	if _, err := svc.ReplaceChapters(ctx, uuid.New(), id, []Chapter{{0, "a"}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner err = %v, want ErrForbidden", err)
	}
	if _, err := svc.ReplaceChapters(ctx, owner, uuid.New(), []Chapter{{0, "a"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id err = %v, want ErrNotFound", err)
	}
}
