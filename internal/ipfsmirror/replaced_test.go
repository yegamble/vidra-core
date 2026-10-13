package ipfsmirror

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/ipfs"
)

func publicVideoLookups(files []VideoFileRef, captions []string) *fakeLookups {
	return &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true, videoFiles: files, captionKeys: captions}
}

// TestDeletedCaptionIsUnpinnedFromTheNode: F5 end to end through the worker. Before
// the hook a deleted caption's row stayed 'pinned' for ever and the gateway kept
// serving it.
func TestDeletedCaptionIsUnpinnedFromTheNode(t *testing.T) {
	ctx := context.Background()
	repo, blobs, client := newFakeRepo(), newBlobs(t), ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	key := "captions/" + vid.String() + "/en.vtt"
	putBlob(t, blobs, key, "WEBVTT\n\nhello")
	seedPendingVideo(repo, key, string(ClassCaption), vid)
	s := New(repo, publicVideoLookups(nil, []string{key}), blobs, client, testConfig())
	if n, err := s.DrainDue(ctx, 10); err != nil || n != 1 {
		t.Fatalf("pin: %d %v", n, err)
	}
	cid := repo.rows[key].Cid
	if pinned, _ := client.IsPinned(ctx, cid); !pinned {
		t.Fatal("caption not pinned")
	}

	if err := s.OnCaptionChanged(ctx, vid, key, true); err != nil {
		t.Fatalf("OnCaptionChanged: %v", err)
	}
	if n, err := s.DrainDue(ctx, 10); err != nil || n != 1 {
		t.Fatalf("unpin drain: %d %v", n, err)
	}
	if pinned, _ := client.IsPinned(ctx, cid); pinned {
		t.Fatal("deleted caption is still pinned on the node")
	}
	if repo.state(key) != "unpinned" {
		t.Fatalf("state = %q, want unpinned", repo.state(key))
	}
}

// TestReplacedCaptionIsRePinnedAndTheOldBytesUnpinned: a same-key overwrite.
func TestReplacedCaptionIsRePinnedAndTheOldBytesUnpinned(t *testing.T) {
	ctx := context.Background()
	repo, blobs, client := newFakeRepo(), newBlobs(t), ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	key := "captions/" + vid.String() + "/en.vtt"
	putBlob(t, blobs, key, "WEBVTT\n\nold text")
	seedPendingVideo(repo, key, string(ClassCaption), vid)
	s := New(repo, publicVideoLookups(nil, []string{key}), blobs, client, testConfig())
	if n, err := s.DrainDue(ctx, 10); err != nil || n != 1 {
		t.Fatalf("pin: %d %v", n, err)
	}
	oldCID := repo.rows[key].Cid

	putBlob(t, blobs, key, "WEBVTT\n\nnew text") // the overwrite AddCaption does
	if err := s.OnCaptionChanged(ctx, vid, key, false); err != nil {
		t.Fatalf("OnCaptionChanged: %v", err)
	}
	if n, err := s.DrainDue(ctx, 10); err != nil || n < 1 {
		t.Fatalf("re-pin drain: %d %v", n, err)
	}
	newCID := repo.rows[key].Cid
	if newCID == "" || newCID == oldCID {
		t.Fatalf("cid after replace = %q (was %q): the new bytes were not pinned", newCID, oldCID)
	}
	if pinned, _ := client.IsPinned(ctx, newCID); !pinned {
		t.Fatal("new caption bytes not pinned")
	}
	if pinned, _ := client.IsPinned(ctx, oldCID); pinned {
		t.Fatal("the replaced caption's old bytes are still pinned")
	}
}

// TestReplacedPosterAndStoryboardAreRePinned: the existing in-place-replacement
// hook (the one CDN invalidation uses) now reaches the mirror too.
func TestReplacedPosterAndStoryboardAreRePinned(t *testing.T) {
	ctx := context.Background()
	vid := uuid.New()
	thumb, sheet, vtt := "thumbnails/"+vid.String()+".jpg", "storyboards/"+vid.String()+".jpg", "storyboards/"+vid.String()+".vtt"
	files := []VideoFileRef{{Kind: "thumbnail", StorageKey: thumb}, {Kind: "storyboard", StorageKey: sheet}, {Kind: "storyboard_vtt", StorageKey: vtt}}
	for _, tc := range []struct {
		kind      string
		rearmed   []string
		untouched []string
	}{
		{"thumbnail", []string{thumb}, []string{sheet, vtt}},
		{"storyboard", []string{sheet, vtt}, []string{thumb}},
		{"unknown-kind", nil, []string{thumb, sheet, vtt}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			repo := newFakeRepo()
			s := New(repo, publicVideoLookups(files, nil), newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
			seedPinned(repo, thumb, string(ClassThumbnail), "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", vid)
			seedPinned(repo, sheet, string(ClassStoryboard), "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", vid)
			seedPinned(repo, vtt, string(ClassStoryboardVTT), "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", vid)
			if err := s.OnMediaReplaced(ctx, vid, tc.kind); err != nil {
				t.Fatalf("OnMediaReplaced: %v", err)
			}
			for _, k := range tc.rearmed {
				if repo.state(k) != "pending" || repo.rows[k].Cid == "" {
					t.Errorf("%s: state=%q cid=%q, want pending with the old cid kept for the swap", k, repo.state(k), repo.rows[k].Cid)
				}
			}
			for _, k := range tc.untouched {
				if repo.state(k) != "pinned" {
					t.Errorf("%s: state=%q, want untouched", k, repo.state(k))
				}
			}
		})
	}
}

// TestChangedCaptionOfAnIneligibleVideoArmsNothing: a private video's caption must
// not be pinned because someone touched it.
func TestChangedCaptionOfAnIneligibleVideoArmsNothing(t *testing.T) {
	repo := newFakeRepo()
	lk := publicVideoLookups(nil, nil)
	lk.videoPrivacy = "private"
	s := New(repo, lk, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	if err := s.OnCaptionChanged(context.Background(), uuid.New(), "captions/x/en.vtt", false); err != nil {
		t.Fatal(err)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("rows armed for a private video: %v", repo.rows)
	}
}

// TestSyncVideoDoesNotResurrectADeletedCaption: SyncVideo re-routes every ledger
// row it does not recognise as an orphan, and RouteIPFSPinIntent would flip an
// 'unpinning' row back to 'pending' — re-publishing a track the creator deleted.
// It also takes down a deleted caption that is still pinned from before F5.
func TestSyncVideoDoesNotResurrectADeletedCaption(t *testing.T) {
	ctx := context.Background()
	vid := uuid.New()
	gone, live := "captions/"+vid.String()+"/fr.vtt", "captions/"+vid.String()+"/en.vtt"
	for _, state := range []string{"unpinning", "pinned"} {
		t.Run(state, func(t *testing.T) {
			repo := newFakeRepo()
			seedPinned(repo, gone, string(ClassCaption), "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", vid)
			seedPinned(repo, live, string(ClassCaption), "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", vid)
			repo.rows[gone].State = state
			s := New(repo, publicVideoLookups(nil, []string{live}), newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
			if err := s.SyncVideo(ctx, vid); err != nil {
				t.Fatal(err)
			}
			if got := repo.state(gone); got != "unpinning" {
				t.Errorf("deleted caption state = %q, want unpinning", got)
			}
			if got := repo.state(live); got != "pinned" {
				t.Errorf("live caption state = %q, want pinned", got)
			}
		})
	}
}

// TestSameKeyAvatarReplacementIsRePinned: profileimage calls the *ImagePin wrapper
// on every upload; a same-extension replacement keeps its key, and the pinned row
// used to keep its old bytes.
func TestSameKeyAvatarReplacementIsRePinned(t *testing.T) {
	ctx := context.Background()
	repo, blobs, client := newFakeRepo(), newBlobs(t), ipfs.NewFakeIPFSClient()
	user := uuid.New()
	key := "avatars/users/" + user.String() + ".png"
	lk := publicVideoLookups(nil, nil)
	s := New(repo, lk, blobs, client, testConfig())

	putBlob(t, blobs, key, "avatar v1")
	if err := s.EnqueueUserImagePin(ctx, user, "avatar", key); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DrainDue(ctx, 10); err != nil || n != 1 {
		t.Fatalf("pin: %d %v", n, err)
	}
	oldCID := repo.rows[key].Cid

	putBlob(t, blobs, key, "avatar v2") // same key, new bytes
	if err := s.EnqueueUserImagePin(ctx, user, "avatar", key); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DrainDue(ctx, 10); err != nil || n < 1 {
		t.Fatalf("re-pin: %d %v", n, err)
	}
	if got := repo.rows[key].Cid; got == "" || got == oldCID {
		t.Fatalf("cid = %q (was %q): the replacement was not pinned", got, oldCID)
	}
	if pinned, _ := client.IsPinned(ctx, oldCID); pinned {
		t.Fatal("the replaced avatar's old bytes are still pinned")
	}
}
