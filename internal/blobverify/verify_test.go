package blobverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/mediahash"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is a database described by a struct. It satisfies Repository —
// which is mediagc's reference set plus the storage_key columns the sweep never
// enumerates — so these tests exercise the real enumeration, including the real
// mediagc key derivation, against a real (local) storage backend.
type fakeRepo struct {
	videoFiles  []sqlcgen.ListAllVideoFileHashesRow
	captions    []string
	videoIDs    []uuid.UUID
	playlists   []sqlcgen.ListStreamingPlaylistRefsRow
	plThumbs    []sqlcgen.ListPlaylistThumbnailRefsRow
	userImages  []string
	chanImages  []string
	instImages  []string
	exports     []string
	attachments []string

	err error
}

func (f *fakeRepo) ListAllVideoFileKeys(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	keys := make([]string, 0, len(f.videoFiles))
	for _, r := range f.videoFiles {
		keys = append(keys, r.StorageKey)
	}
	return keys, nil
}

func (f *fakeRepo) ListAllCaptionKeys(context.Context) ([]string, error) { return f.captions, f.err }
func (f *fakeRepo) ListAllVideoIDs(context.Context) ([]uuid.UUID, error) { return f.videoIDs, f.err }

func (f *fakeRepo) ListStreamingPlaylistRefs(context.Context) ([]sqlcgen.ListStreamingPlaylistRefsRow, error) {
	return f.playlists, f.err
}

func (f *fakeRepo) ListPlaylistThumbnailRefs(context.Context) ([]sqlcgen.ListPlaylistThumbnailRefsRow, error) {
	return f.plThumbs, f.err
}

func (f *fakeRepo) ListAllUserImageKeys(context.Context) ([]string, error) {
	return f.userImages, f.err
}
func (f *fakeRepo) ListAllChannelImageKeys(context.Context) ([]string, error) {
	return f.chanImages, f.err
}
func (f *fakeRepo) ListAllInstanceImageKeys(context.Context) ([]string, error) {
	return f.instImages, f.err
}
func (f *fakeRepo) ListAllAccountExportKeys(context.Context) ([]string, error) {
	return f.exports, f.err
}
func (f *fakeRepo) ListAllMessageAttachmentKeys(context.Context) ([]string, error) {
	return f.attachments, f.err
}

func (f *fakeRepo) ListAllVideoFileHashes(context.Context) ([]sqlcgen.ListAllVideoFileHashesRow, error) {
	return f.videoFiles, f.err
}

var _ Repository = (*fakeRepo)(nil)

func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// store builds a local backend holding the given key→body objects.
func newStore(t *testing.T, objects map[string]string) storage.Backend {
	t.Helper()
	b, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	for k, body := range objects {
		if _, err := b.Put(context.Background(), k, strings.NewReader(body)); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
	}
	return b
}

func verify(t *testing.T, repo Repository, blobs storage.Backend, opt Options) Report {
	t.Helper()
	rep, err := Verify(context.Background(), repo, blobs, opt)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return rep
}

// TestEveryReferenceClassIsChecked is the coverage contract: a dangling
// reference in an avatar, an export archive or a DM attachment is exactly as
// broken as one in video_files, and those three tables are precisely the ones
// the GC sweep never enumerates — so nothing else in the system would ever
// notice.
func TestEveryReferenceClassIsChecked(t *testing.T) {
	vid := uuid.New()
	plID := uuid.New()
	ext := "jpg"
	repo := &fakeRepo{
		videoFiles:  []sqlcgen.ListAllVideoFileHashesRow{{StorageKey: "web-videos/" + vid.String() + ".mp4"}},
		captions:    []string{"captions/" + vid.String() + ".en.vtt"},
		videoIDs:    []uuid.UUID{vid},
		plThumbs:    []sqlcgen.ListPlaylistThumbnailRefsRow{{ID: plID, ThumbnailExt: &ext}},
		playlists:   []sqlcgen.ListStreamingPlaylistRefsRow{{VideoID: vid, MasterKey: "streaming-playlists/" + vid.String() + "/master.m3u8"}},
		userImages:  []string{"avatars/users/u1.png"},
		chanImages:  []string{"banners/channels/c1.png"},
		instImages:  []string{"logos/instance/favicon.png"},
		exports:     []string{"exports/e1.json"},
		attachments: []string{"dm/a1.png"},
	}
	blobs := newStore(t, map[string]string{
		"web-videos/" + vid.String() + ".mp4":                  "video",
		"captions/" + vid.String() + ".en.vtt":                 "WEBVTT",
		"playlist-thumbnails/" + plID.String() + ".jpg":        "cover",
		"streaming-playlists/" + vid.String() + "/master.m3u8": "#EXTM3U",
		"avatars/users/u1.png":                                 "a",
		"banners/channels/c1.png":                              "b",
		"logos/instance/favicon.png":                           "c",
		"exports/e1.json":                                      "{}",
		"dm/a1.png":                                            "d",
	})

	rep := verify(t, repo, blobs, Options{})
	if rep.Checked != 9 {
		t.Fatalf("checked = %d, want 9 (one per reference class): %+v", rep.Checked, rep)
	}
	if rep.Present != 9 || !rep.Consistent() {
		t.Fatalf("a complete store did not verify clean: %+v", rep)
	}

	// Now break exactly one, in the class the GC would never look at.
	if err := blobs.Delete(context.Background(), "dm/a1.png"); err != nil {
		t.Fatal(err)
	}
	rep = verify(t, repo, blobs, Options{})
	if rep.Missing != 1 || rep.Consistent() {
		t.Fatalf("a deleted DM attachment was not reported: %+v", rep)
	}
	if len(rep.MissingKeys) != 1 || rep.MissingKeys[0] != "dm/a1.png" {
		t.Fatalf("missing keys = %v, want the attachment named", rep.MissingKeys)
	}
}

// TestKnownMissingIsNotANewFinding is the sentinel contract. A row the hash
// backfill already recorded as dangling is reported every run — it is a real
// problem — but it does not make the run inconsistent, because a post-restore
// check that can never go green is one operators stop reading.
func TestKnownMissingIsNotANewFinding(t *testing.T) {
	repo := &fakeRepo{videoFiles: []sqlcgen.ListAllVideoFileHashesRow{
		{StorageKey: "web-videos/gone.mp4", Sha256: mediahash.SentinelMissing},
		{StorageKey: "web-videos/here.mp4", Sha256: digestOf("here")},
	}}
	blobs := newStore(t, map[string]string{"web-videos/here.mp4": "here"})

	rep := verify(t, repo, blobs, Options{})
	if rep.KnownMissing != 1 || rep.Missing != 0 {
		t.Fatalf("sentinel row was not told apart from a new loss: %+v", rep)
	}
	if !rep.Consistent() {
		t.Fatalf("an already-recorded dangling row must not fail the run: %+v", rep)
	}
	if len(rep.KnownMissingKeys) != 1 || rep.KnownMissingKeys[0] != "web-videos/gone.mp4" {
		t.Fatalf("known-missing keys = %v", rep.KnownMissingKeys)
	}
	if !strings.Contains(rep.Text(), "known missing:  1") {
		t.Fatalf("the summary never mentions the known-missing count:\n%s", rep.Text())
	}

	// And a NEW loss alongside it is still a new loss.
	if err := blobs.Delete(context.Background(), "web-videos/here.mp4"); err != nil {
		t.Fatal(err)
	}
	rep = verify(t, repo, blobs, Options{})
	if rep.Missing != 1 || rep.KnownMissing != 1 || rep.Consistent() {
		t.Fatalf("the two classes did not stay separate: %+v", rep)
	}
}

// TestStaleSentinel: the object came back. The row's terminal 'missing' will
// never be revisited by the backfill, so nothing but this check would say so.
func TestStaleSentinel(t *testing.T) {
	repo := &fakeRepo{videoFiles: []sqlcgen.ListAllVideoFileHashesRow{
		{StorageKey: "web-videos/back.mp4", Sha256: mediahash.SentinelMissing},
	}}
	blobs := newStore(t, map[string]string{"web-videos/back.mp4": "restored"})

	rep := verify(t, repo, blobs, Options{Hash: true})
	if rep.StaleSentinel != 1 || rep.Present != 1 {
		t.Fatalf("a resurrected object was not reported: %+v", rep)
	}
	if rep.Mismatched != 0 {
		t.Fatalf("the sentinel must never be compared as if it were a digest: %+v", rep)
	}
	if !rep.Consistent() {
		t.Fatalf("a stale sentinel is a note, not an inconsistency: %+v", rep)
	}
}

// TestHashDetectsCorruption is the whole reason --hash exists: bytes that
// changed under a store that still answers "yes, it is there".
func TestHashDetectsCorruption(t *testing.T) {
	repo := &fakeRepo{videoFiles: []sqlcgen.ListAllVideoFileHashesRow{
		{StorageKey: "web-videos/good.mp4", Sha256: digestOf("good")},
		{StorageKey: "web-videos/bad.mp4", Sha256: digestOf("original")},
		{StorageKey: "web-videos/unhashed.mp4", Sha256: ""},
	}}
	blobs := newStore(t, map[string]string{
		"web-videos/good.mp4":     "good",
		"web-videos/bad.mp4":      "TAMPERED",
		"web-videos/unhashed.mp4": "whatever",
	})

	// Without --hash every one of them is simply present.
	if rep := verify(t, repo, blobs, Options{}); !rep.Consistent() || rep.Present != 3 {
		t.Fatalf("the fast pass must not read bytes: %+v", rep)
	}

	rep := verify(t, repo, blobs, Options{Hash: true})
	if rep.Mismatched != 1 || rep.Verified != 1 || rep.Unhashed != 1 {
		t.Fatalf("hash classes wrong: %+v", rep)
	}
	if rep.Consistent() {
		t.Fatalf("corruption must fail the run: %+v", rep)
	}
	if len(rep.MismatchedKeys) != 1 || rep.MismatchedKeys[0] != "web-videos/bad.mp4" {
		t.Fatalf("mismatched keys = %v", rep.MismatchedKeys)
	}
	if !strings.Contains(rep.Text(), "CORRUPT") {
		t.Fatalf("the summary is not loud about corruption:\n%s", rep.Text())
	}
}

// TestDeepWalksTheHLSTree: the master manifest is the only object in a tree
// with a row behind it, so a tree that lost every segment passes the fast pass.
func TestDeepWalksTheHLSTree(t *testing.T) {
	whole, hollow := uuid.New(), uuid.New()
	master := func(id uuid.UUID) string { return "streaming-playlists/" + id.String() + "/master.m3u8" }
	repo := &fakeRepo{
		videoIDs: []uuid.UUID{whole, hollow},
		playlists: []sqlcgen.ListStreamingPlaylistRefsRow{
			{VideoID: whole, MasterKey: master(whole)},
			{VideoID: hollow, MasterKey: master(hollow)},
			// A dead-lettered transcode: no master to ask about at all.
			{VideoID: uuid.New(), MasterKey: ""},
		},
	}
	blobs := newStore(t, map[string]string{
		master(whole):  "#EXTM3U",
		master(hollow): "#EXTM3U",
		"streaming-playlists/" + whole.String() + "/720p.m3u8":   "#EXTM3U",
		"streaming-playlists/" + whole.String() + "/720p-000.ts": "segment",
	})

	// Fast pass: both masters are there, so both trees look fine.
	fast := verify(t, repo, blobs, Options{})
	if !fast.Consistent() || fast.Checked != 2 {
		t.Fatalf("fast pass: %+v", fast)
	}
	if fast.Skipped != 1 {
		t.Fatalf("the master-less playlist was not skipped: %+v", fast)
	}

	deep := verify(t, repo, blobs, Options{Deep: true})
	if deep.DeepPlaylists != 2 {
		t.Fatalf("deep walked %d trees, want 2: %+v", deep.DeepPlaylists, deep)
	}
	if deep.Incomplete != 1 || deep.Consistent() {
		t.Fatalf("the hollow tree was not caught: %+v", deep)
	}
	if len(deep.IncompletePrefixes) != 1 || !strings.Contains(deep.IncompletePrefixes[0], hollow.String()) {
		t.Fatalf("incomplete prefixes = %v, want the hollow video's tree", deep.IncompletePrefixes)
	}
}

// listlessBackend is a Backend with no ObjectLister, which is what --deep has
// to degrade gracefully against rather than fail on.
type listlessBackend struct{ storage.Backend }

func (b listlessBackend) ListKeys(context.Context, string) ([]string, error) {
	panic("must not be called: this backend does not advertise ObjectLister")
}

func TestDeepDegradesWhenTheBackendCannotList(t *testing.T) {
	vid := uuid.New()
	key := "streaming-playlists/" + vid.String() + "/master.m3u8"
	repo := &fakeRepo{
		videoIDs:  []uuid.UUID{vid},
		playlists: []sqlcgen.ListStreamingPlaylistRefsRow{{VideoID: vid, MasterKey: key}},
	}
	// Wrapped in a struct that does NOT implement ObjectLister.
	base := newStore(t, map[string]string{key: "#EXTM3U"})
	blobs := struct{ storage.Backend }{Backend: base}

	rep := verify(t, repo, blobs, Options{Deep: true})
	if !rep.DeepUnsupported {
		t.Fatalf("deep on a listless backend must report that it could not walk: %+v", rep)
	}
	if !rep.Consistent() {
		t.Fatalf("an unsupported capability is not an inconsistency: %+v", rep)
	}
	if !strings.Contains(rep.Text(), "cannot list objects") {
		t.Fatalf("the summary hides the degradation:\n%s", rep.Text())
	}
}

// TestOneObjectIsOneQuestion: a key referenced twice is checked once, or the
// counts stop being counts of objects.
func TestDuplicateReferencesCollapse(t *testing.T) {
	key := "web-videos/shared.mp4"
	repo := &fakeRepo{
		videoFiles: []sqlcgen.ListAllVideoFileHashesRow{
			{StorageKey: key, Sha256: digestOf("shared")},
			{StorageKey: key, Sha256: ""},
		},
		attachments: []string{key},
	}
	blobs := newStore(t, map[string]string{key: "shared"})

	rep := verify(t, repo, blobs, Options{Hash: true})
	if rep.Checked != 1 || rep.Present != 1 {
		t.Fatalf("a doubly-referenced object was counted twice: %+v", rep)
	}
	// A real digest on one of the two rows must win over the empty state, so the
	// object is verified rather than excused.
	if rep.Verified != 1 {
		t.Fatalf("the recorded digest was not used: %+v", rep)
	}
}

// TestEmptyKeysAreNotQuestions: account_exports.storage_key defaults to ” for
// the whole pending life of a job. Reporting those as dangling references would
// mean every queued export is a data-loss finding.
func TestEmptyKeysAreNotQuestions(t *testing.T) {
	repo := &fakeRepo{
		videoFiles: []sqlcgen.ListAllVideoFileHashesRow{{StorageKey: ""}},
		captions:   []string{"", "   "},
		exports:    []string{""},
	}
	rep := verify(t, repo, newStore(t, nil), Options{})
	if rep.Checked != 0 || !rep.Consistent() {
		t.Fatalf("empty keys became questions: %+v", rep)
	}
}

// erroringBackend answers every existence question with a transport failure.
type erroringBackend struct{ storage.Backend }

func (erroringBackend) Exists(context.Context, string) (bool, error) {
	return false, errors.New("503 slow down")
}

// TestUnreadableIsNotMissing. "I could not ask" and "it is not there" have
// different causes and opposite responses; collapsing them would turn a
// throttled bucket into a report of total data loss.
func TestUnreadableIsNotMissing(t *testing.T) {
	repo := &fakeRepo{videoFiles: []sqlcgen.ListAllVideoFileHashesRow{{StorageKey: "web-videos/a.mp4"}}}
	rep := verify(t, repo, erroringBackend{Backend: newStore(t, nil)}, Options{})
	if rep.Errors != 1 || rep.Missing != 0 {
		t.Fatalf("an unreadable object was reported as lost: %+v", rep)
	}
	if rep.Consistent() {
		t.Fatalf("a run that could not read the store did not verify anything: %+v", rep)
	}
}

func TestVerifyRefusesWithoutItsInputs(t *testing.T) {
	if _, err := Verify(context.Background(), nil, newStore(t, nil), Options{}); err == nil {
		t.Error("no database was accepted")
	}
	if _, err := Verify(context.Background(), &fakeRepo{}, nil, Options{}); err == nil {
		t.Error("no storage backend was accepted")
	}
}

func TestVerifyPropagatesADatabaseFailure(t *testing.T) {
	repo := &fakeRepo{err: errors.New("connection reset")}
	if _, err := Verify(context.Background(), repo, newStore(t, nil), Options{}); err == nil {
		t.Fatal("a database that would not answer produced a report")
	}
}

func TestVerifyHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &fakeRepo{videoFiles: []sqlcgen.ListAllVideoFileHashesRow{{StorageKey: "web-videos/a.mp4"}}}
	if _, err := Verify(ctx, repo, newStore(t, nil), Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSamplesAreCapped(t *testing.T) {
	var rows []sqlcgen.ListAllVideoFileHashesRow
	for i := 0; i < SampleLimit*3; i++ {
		rows = append(rows, sqlcgen.ListAllVideoFileHashesRow{StorageKey: "web-videos/" + strings.Repeat("x", i+1) + ".mp4"})
	}
	rep := verify(t, &fakeRepo{videoFiles: rows}, newStore(t, nil), Options{})
	if rep.Missing != SampleLimit*3 {
		t.Fatalf("missing = %d, want %d", rep.Missing, SampleLimit*3)
	}
	if len(rep.MissingKeys) != SampleLimit {
		t.Fatalf("sample = %d keys, want %d", len(rep.MissingKeys), SampleLimit)
	}
	if !strings.Contains(rep.Text(), "and 40 more") {
		t.Fatalf("the summary hides how much it truncated:\n%s", rep.Text())
	}
}

func TestIsDigest(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{digestOf("x"), true},
		{"", false},
		{mediahash.SentinelMissing, false},
		{strings.ToUpper(digestOf("x")), false}, // the column stores lowercase hex
		{strings.Repeat("z", 64), false},
		{digestOf("x")[:63], false},
	} {
		if got := isDigest(tc.in); got != tc.want {
			t.Errorf("isDigest(%q) = %t, want %t", tc.in, got, tc.want)
		}
	}
}

// A read that fails part-way through is an error, not a mismatch: a truncated
// stream says nothing about whether the stored bytes are right.
type truncatingBackend struct{ storage.Backend }

func (b truncatingBackend) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(iotest{}), nil
}

type iotest struct{}

func (iotest) Read([]byte) (int, error) { return 0, errors.New("connection reset mid-object") }

func TestAFailedReadIsNotAMismatch(t *testing.T) {
	key := "web-videos/a.mp4"
	repo := &fakeRepo{videoFiles: []sqlcgen.ListAllVideoFileHashesRow{{StorageKey: key, Sha256: digestOf("a")}}}
	rep := verify(t, repo, truncatingBackend{Backend: newStore(t, map[string]string{key: "a"})}, Options{Hash: true})
	if rep.Errors != 1 || rep.Mismatched != 0 {
		t.Fatalf("a truncated read was reported as corruption: %+v", rep)
	}
}
