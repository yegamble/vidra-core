package peertubeimport

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ── change detection ──
//
// The whole of source-authoritative rests on one claim: two calls to the same
// digest function agree exactly when the mapped fields agree. If they can agree
// on fields that differ, a change is silently dropped; if they can differ on
// fields that agree, every entity is rewritten on every run and the 21-second
// re-run is gone.

func TestDigestIsStableAndFieldSensitive(t *testing.T) {
	channel := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	base := func() string {
		return videoDigest(channel, "First Video", "hello", "public", "published", "1", "en", "1", 120)
	}
	if base() != base() {
		t.Fatal("the same fields hashed twice gave different answers")
	}
	changed := map[string]string{
		"channel":     videoDigest(uuid.MustParse("22222222-2222-2222-2222-222222222222"), "First Video", "hello", "public", "published", "1", "en", "1", 120),
		"title":       videoDigest(channel, "First Video (edited)", "hello", "public", "published", "1", "en", "1", 120),
		"description": videoDigest(channel, "First Video", "goodbye", "public", "published", "1", "en", "1", 120),
		"privacy":     videoDigest(channel, "First Video", "hello", "unlisted", "published", "1", "en", "1", 120),
		"state":       videoDigest(channel, "First Video", "hello", "public", "draft", "1", "en", "1", 120),
		"category":    videoDigest(channel, "First Video", "hello", "public", "published", "2", "en", "1", 120),
		"language":    videoDigest(channel, "First Video", "hello", "public", "published", "1", "fr", "1", 120),
		"license":     videoDigest(channel, "First Video", "hello", "public", "published", "1", "en", "2", 120),
		"duration":    videoDigest(channel, "First Video", "hello", "public", "published", "1", "en", "1", 121),
	}
	for field, d := range changed {
		if d == base() {
			t.Errorf("changing %s did not change the digest — that change would never be carried", field)
		}
	}
}

// Field boundaries have to be part of the hash. Without length prefixes, moving
// a character from a title into a description is invisible — and title/
// description is exactly the pair an editor moves text between.
func TestDigestSeparatesAdjacentFields(t *testing.T) {
	id := uuid.Nil
	a := videoDigest(id, "ab", "c", "public", "published", "", "", "", 0)
	b := videoDigest(id, "a", "bc", "public", "published", "", "", "", 0)
	if a == b {
		t.Fatal("(\"ab\",\"c\") and (\"a\",\"bc\") share a digest; a field boundary is not being hashed")
	}
}

// A digest is compared, never displayed, so it must not carry what it summarises:
// applied_value and ledger notes are read by whoever reads the ledger, and an
// email address or a private video's title has no business in either.
func TestDigestCarriesNoContent(t *testing.T) {
	const secret = "alice@example.test"
	d := userDigest("$2a$04$hashhashhash", "admin", secret, true)
	if strings.Contains(d, secret) || strings.Contains(d, "hashhash") {
		t.Fatalf("digest %q carries its input", d)
	}
	if len(d) != 64 {
		t.Fatalf("digest length = %d, want a fixed 64 hex characters", len(d))
	}
}

// Different families must not collide even when their fields line up: every
// digest is domain-separated by the family name.
func TestDigestFamiliesAreSeparated(t *testing.T) {
	same := userDigest("x", "y", "z", false) == channelDigest(uuid.Nil, "x", "y")
	if same {
		t.Fatal("two families produced the same digest for different data")
	}
}

func TestUserDigestCoversEveryFieldTheResyncWrites(t *testing.T) {
	base := userDigest("hash-1", "user", "Alice", false)
	for field, d := range map[string]string{
		"password hash":  userDigest("hash-2", "user", "Alice", false),
		"role":           userDigest("hash-1", "moderator", "Alice", false),
		"display name":   userDigest("hash-1", "user", "Alicia", false),
		"email verified": userDigest("hash-1", "user", "Alice", true),
	} {
		if d == base {
			t.Errorf("a changed %s did not change the digest", field)
		}
	}
}

// ── tag sets ──

func TestNormalizeTagSetIsASet(t *testing.T) {
	got := normalizeTagSet([]string{"Music", "music", "  TEST  ", "", strings.Repeat("x", 51), "alpha"})
	want := []string{"alpha", "music", "test"}
	if len(got) != len(want) {
		t.Fatalf("normalised %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalised %v, want %v (sorted, de-duplicated, unstorable dropped)", got, want)
		}
	}
	// Never nil: it is handed straight to a `= ANY($1::text[])`, and a nil array
	// would make the delete a no-op instead of "the source has no tags".
	if normalizeTagSet(nil) == nil {
		t.Fatal("an empty tag set must be an empty array, not nil")
	}
}

func TestTagSetDigestIgnoresOrderButNotMembership(t *testing.T) {
	a := tagSetDigest(normalizeTagSet([]string{"music", "test"}))
	b := tagSetDigest(normalizeTagSet([]string{"test", "music"}))
	if a != b {
		t.Fatal("tag order changed the digest; video_tags is a set and the order rows come back in is not information")
	}
	if a == tagSetDigest(normalizeTagSet([]string{"music"})) {
		t.Fatal("a removed tag did not change the digest — the removal would never be carried")
	}
	if a == tagSetDigest(normalizeTagSet([]string{"music", "test", "extra"})) {
		t.Fatal("an added tag did not change the digest")
	}
}

// ── chapter sets ──

// The reason chapters are replaced as a set: video_chapters is keyed
// (video_id, start_seconds), so a MOVED mark is a different row. A digest that
// ignored the start would let a move through unnoticed; an upsert would leave
// both marks standing.
func TestChapterSetDigestNoticesAMove(t *testing.T) {
	before := chapterSetDigest([]chapterMark{{0, "Intro"}, {90, "Middle"}})
	moved := chapterSetDigest([]chapterMark{{0, "Intro"}, {95, "Middle"}})
	renamed := chapterSetDigest([]chapterMark{{0, "Intro"}, {90, "Midpoint"}})
	removed := chapterSetDigest([]chapterMark{{0, "Intro"}})
	for name, d := range map[string]string{"moved": moved, "renamed": renamed, "removed": removed} {
		if d == before {
			t.Errorf("a %s chapter did not change the set digest", name)
		}
	}
}

func TestDesiredChapterSetDropsWhatVideoChaptersRefuses(t *testing.T) {
	marks, dropped := desiredChapterSet([]SourceChapter{
		{ID: 1, Timecode: 0, Title: "Intro"},
		{ID: 2, Timecode: 90, Title: "Middle"},
		{ID: 3, Timecode: 150, Title: "   "},  // CHECK: 1..120 characters
		{ID: 4, Timecode: -5, Title: "Early"}, // CHECK: start_seconds >= 0
		{ID: 5, Timecode: 90, Title: "Clash"}, // the primary key admits one mark per second
	})
	if len(marks) != 2 || marks[0].start != 0 || marks[1].start != 90 || marks[1].title != "Middle" {
		t.Fatalf("marks = %+v, want the two storable ones with the FIRST of the clashing pair", marks)
	}
	if len(dropped) != 3 {
		t.Fatalf("dropped %d rows, want 3 (blank title, negative start, duplicate start)", len(dropped))
	}
	// A duplicated start in the source must not become two rows: the reinsert
	// would fail the primary key, and the transaction would take the whole video's
	// chapter set down with it.
	seen := map[int32]bool{}
	for _, m := range marks {
		if seen[m.start] {
			t.Fatalf("desiredChapterSet returned two marks at %d seconds", m.start)
		}
		seen[m.start] = true
	}
}

func TestGroupChaptersWalksPerVideo(t *testing.T) {
	groups := groupChapters([]SourceChapter{
		{ID: 1, VideoID: 7, Timecode: 0},
		{ID: 2, VideoID: 7, Timecode: 90},
		{ID: 3, VideoID: 9, Timecode: 0},
	})
	if len(groups) != 2 || groups[0].videoID != 7 || len(groups[0].chapters) != 2 || groups[1].videoID != 9 {
		t.Fatalf("groups = %+v, want one run per video", groups)
	}
	if groupChapters(nil) != nil {
		t.Fatal("no chapters must yield no groups")
	}
}

// ── playlist item sets ──

func TestPlaylistItemsDigestNoticesAReorder(t *testing.T) {
	a := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	b := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	original := playlistItemsDigest([]playlistSlot{{a, 1}, {b, 2}})
	if original == playlistItemsDigest([]playlistSlot{{b, 1}, {a, 2}}) {
		t.Fatal("swapping two videos' positions did not change the digest; a re-ordered playlist is a changed playlist")
	}
	if original == playlistItemsDigest([]playlistSlot{{a, 1}}) {
		t.Fatal("removing a slot did not change the digest")
	}
}

// ── rating provenance ──
//
// An unrate is the ABSENCE of a source row: no user, no video, no value. The
// provenance the ledger records is the only thing that makes removing one safe,
// so it has to survive the round trip exactly.

func TestRatingProvenanceRoundTrips(t *testing.T) {
	u := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	v := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	gotU, gotV, gotR, ok := parseRatingProvenance(ratingProvenance(u, v, "like"))
	if !ok || gotU != u || gotV != v || gotR != "like" {
		t.Fatalf("round trip = %v %v %q %v, want the pair and value back", gotU, gotV, gotR, ok)
	}
}

func TestRatingProvenanceRejectsWhatItCannotTrust(t *testing.T) {
	for name, s := range map[string]string{
		"empty (a row written before the memory existed)": "",
		"not the shape":         "like",
		"user is not a uuid":    "u:alice|v:22222222-2222-2222-2222-222222222222|r:like",
		"video is not a uuid":   "u:11111111-1111-1111-1111-111111111111|v:|r:like",
		"a digest, not a pair":  "0123456789abcdef",
		"too many fields":       "u:1|v:2|r:3|x:4",
		"fields out of order":   "v:22222222-2222-2222-2222-222222222222|u:11111111-1111-1111-1111-111111111111|r:like",
		"missing value segment": "u:11111111-1111-1111-1111-111111111111|v:22222222-2222-2222-2222-222222222222",
	} {
		if _, _, _, ok := parseRatingProvenance(s); ok {
			t.Errorf("%s: parsed as provenance — an unparseable memory must leave the rating ALONE, never delete on a guess", name)
		}
	}
}

// ownsRating is the guard between "carry the unrate" and "delete somebody's own
// vote". It has to answer no to every case where the evidence does not line up.
func TestOwnsRatingDemandsExactEvidence(t *testing.T) {
	u := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	v := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	other := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	key := ratingKey{user: u, video: v}
	im := &Importer{resync: &resyncState{ratingOwned: map[string]string{
		"1": ratingProvenance(u, v, "like"),
		"2": ratingProvenance(other, v, "like"),
		"3": "written-before-the-memory-existed",
	}}}
	if !im.ownsRating("1", key, "like") {
		t.Fatal("the import's own write was not recognised")
	}
	if im.ownsRating("1", key, "dislike") {
		t.Fatal("a rating that has CHANGED since the import wrote it was claimed; that is somebody's own vote")
	}
	if im.ownsRating("2", key, "like") {
		t.Fatal("a provenance naming a different user was accepted")
	}
	if im.ownsRating("3", key, "like") {
		t.Fatal("an unparseable provenance was accepted; with no evidence the rating must be left alone")
	}
	if im.ownsRating("404", key, "like") {
		t.Fatal("a source id the ledger has never seen was claimed")
	}
}

// ── the fold that turns member rows back into per-entity sets ──

func TestForEachRunSplitsOnKeyChange(t *testing.T) {
	keys := []string{"a", "a", "b", "c", "c", "c"}
	var got [][2]int
	var seen []string
	forEachRun(len(keys), func(i int) string { return keys[i] }, func(key string, lo, hi int) {
		seen = append(seen, key)
		got = append(got, [2]int{lo, hi})
	})
	want := [][2]int{{0, 2}, {2, 3}, {3, 6}}
	if len(got) != len(want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runs = %v, want %v", got, want)
		}
	}
	if strings.Join(seen, ",") != "a,b,c" {
		t.Fatalf("keys = %v, want a,b,c", seen)
	}
	forEachRun(0, func(int) string { return "" }, func(string, int, int) {
		t.Fatal("an empty slice must produce no runs")
	})
}

// derefText makes nil and "" the same value on purpose: the destination read
// COALESCEs an unset column to "", so a distinction this side can make and the
// other cannot would rewrite every such video on every run, forever.
func TestDerefTextTreatsUnsetAsEmpty(t *testing.T) {
	empty := ""
	if derefText(nil) != derefText(&empty) {
		t.Fatal("nil and \"\" must fold to the same digest input")
	}
	value := "42"
	if derefText(&value) != "42" {
		t.Fatal("a set value must survive")
	}
}
