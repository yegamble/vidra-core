package video

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- fakeRepo tag methods (mirror the video_tags queries) ---

func (f *fakeRepo) DeleteVideoTags(_ context.Context, videoID uuid.UUID) error {
	delete(f.tags, videoID)
	return nil
}

func (f *fakeRepo) InsertVideoTags(_ context.Context, a sqlcgen.InsertVideoTagsParams) error {
	if f.tags == nil {
		f.tags = map[uuid.UUID][]string{}
	}
	existing := map[string]bool{}
	for _, t := range f.tags[a.VideoID] {
		existing[t] = true
	}
	for _, t := range a.Tags {
		if !existing[t] {
			f.tags[a.VideoID] = append(f.tags[a.VideoID], t)
			existing[t] = true
		}
	}
	return nil
}

func (f *fakeRepo) ListVideoTags(_ context.Context, videoID uuid.UUID) ([]string, error) {
	out := append([]string(nil), f.tags[videoID]...)
	sort.Strings(out)
	return out, nil
}

// --- tests ---

func TestNormalizeTags(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"lowercases and trims", []string{"  Go ", "REDIS"}, []string{"go", "redis"}},
		{"dedupes case-insensitively", []string{"Go", "go", "GO"}, []string{"go"}},
		{"drops empties", []string{"", "  ", "a"}, []string{"a"}},
		{"nil in, empty out", nil, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeTags(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormalizeTags(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestCreateDraftStoresNormalizedTags proves tags supplied at creation are
// normalized and readable back through Tags.
func TestCreateDraftStoresNormalizedTags(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)

	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{
		Title: "tagged", Privacy: "public",
		Tags: []string{" Go ", "REDIS", "go"},
	})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	tags, err := svc.Tags(ctx, v.ID)
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if want := []string{"go", "redis"}; !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
}

// TestUpdateReplacesTags proves a non-nil Tags update replaces the whole set
// (and an empty slice clears it), while nil leaves tags untouched.
func TestUpdateReplacesTags(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)

	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public", Tags: []string{"old"}})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	// nil Tags: unchanged.
	title := "renamed"
	if _, err := svc.Update(ctx, owner, v.ID, UpdateInput{Title: &title}); err != nil {
		t.Fatalf("Update (nil tags): %v", err)
	}
	if tags, _ := svc.Tags(ctx, v.ID); !reflect.DeepEqual(tags, []string{"old"}) {
		t.Fatalf("tags after unrelated update = %v, want [old]", tags)
	}

	// Non-nil: full replace.
	newTags := []string{"B", "a "}
	if _, err := svc.Update(ctx, owner, v.ID, UpdateInput{Tags: &newTags}); err != nil {
		t.Fatalf("Update (replace tags): %v", err)
	}
	if tags, _ := svc.Tags(ctx, v.ID); !reflect.DeepEqual(tags, []string{"a", "b"}) {
		t.Fatalf("tags after replace = %v, want [a b]", tags)
	}

	// Empty slice: clears.
	empty := []string{}
	if _, err := svc.Update(ctx, owner, v.ID, UpdateInput{Tags: &empty}); err != nil {
		t.Fatalf("Update (clear tags): %v", err)
	}
	if tags, _ := svc.Tags(ctx, v.ID); len(tags) != 0 {
		t.Fatalf("tags after clear = %v, want none", tags)
	}
}
