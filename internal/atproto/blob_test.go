package atproto

import (
	"context"
	"encoding/json"
	"testing"
)

// canonical is the IPLD blob ref shape a spec-compliant PDS returns and the only
// shape createRecord accepts as embed.external.thumb.
func assertCanonical(t *testing.T, b json.RawMessage) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("blob is not JSON: %v (%s)", err, b)
	}
	if !isCanonicalBlob(obj) {
		t.Fatalf("blob is not a canonical typed blob ref: %s", b)
	}
}

func TestNormalizeBlobCanonicalPassesThrough(t *testing.T) {
	// (a) An already-canonical blob is returned verbatim, byte for byte, so the
	// compliant Bluesky path is unchanged.
	in := json.RawMessage(`{"$type":"blob","ref":{"$link":"bafycompliant"},"mimeType":"image/jpeg","size":42}`)
	out, err := normalizeBlob(in)
	if err != nil {
		t.Fatalf("normalizeBlob(canonical): %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("canonical blob was not passed through verbatim:\n got %s\nwant %s", out, in)
	}
	assertCanonical(t, out)
}

func TestNormalizeBlobLiftsNestedLegacyBlob(t *testing.T) {
	// (b) A non-compliant PDS wraps the typed blob under ".original" and the top
	// level lacks "$type":"blob". normalizeBlob lifts the nested canonical blob.
	in := json.RawMessage(`{"original":{"$type":"blob","ref":{"$link":"bafynested"},"mimeType":"image/png","size":99},"cid":"legacycid"}`)
	out, err := normalizeBlob(in)
	if err != nil {
		t.Fatalf("normalizeBlob(nested): %v", err)
	}
	assertCanonical(t, out)
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(out, &obj)
	var ref struct {
		Link string `json:"$link"`
	}
	_ = json.Unmarshal(obj["ref"], &ref)
	if ref.Link != "bafynested" {
		t.Errorf("lifted blob ref = %q, want bafynested", ref.Link)
	}
}

func TestNormalizeBlobLiftsNestedUnderArbitraryKey(t *testing.T) {
	// A wrapper key other than "original" is still handled deterministically.
	in := json.RawMessage(`{"blob":{"$type":"blob","ref":{"$link":"bafyother"},"mimeType":"image/webp","size":7}}`)
	out, err := normalizeBlob(in)
	if err != nil {
		t.Fatalf("normalizeBlob(other key): %v", err)
	}
	assertCanonical(t, out)
}

func TestNormalizeBlobRejectsNonBlob(t *testing.T) {
	// No canonical blob anywhere → error, so the caller posts without a card image
	// (best-effort) rather than embedding garbage that would fail createRecord.
	for name, in := range map[string]string{
		"empty":          ``,
		"legacy_no_type": `{"cid":"legacycid","mimeType":"image/jpeg"}`,
		"wrong_type":     `{"$type":"notablob","ref":{"$link":"x"}}`,
		"missing_link":   `{"$type":"blob","mimeType":"image/jpeg","size":1}`,
	} {
		if _, err := normalizeBlob(json.RawMessage(in)); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// TestDrainPostsNormalizesNonCompliantThumbnail is case (c): a thumbnailed
// cross-post against a PDS that returns a NON-compliant (nested) blob still
// produces a createRecord whose embed.external.thumb is a canonical blob ref —
// i.e. the createRecord the real PDS would accept.
func TestDrainPostsNormalizesNonCompliantThumbnail(t *testing.T) {
	repo := newFakeRepo()
	pds := newFakePDS()
	// Non-compliant uploadBlob result: valid typed blob nested under "original".
	pds.blob = Blob(`{"original":{"$type":"blob","ref":{"$link":"bafynested"},"mimeType":"image/jpeg","size":10}}`)
	_, vid := seedPublishable(t, repo)
	s := NewService(repo, WithEnabled(true), WithPDSClient(pds), WithBaseURL("https://x"),
		WithThumbnails(fakeThumbs{data: []byte("jpegbytes"), mime: "image/jpeg", ok: true}))

	if _, err := s.DrainPosts(context.Background(), 10); err != nil {
		t.Fatalf("DrainPosts: %v", err)
	}
	if repo.postForVideo(vid).State != "posted" {
		t.Fatalf("post state = %q, want posted", repo.postForVideo(vid).State)
	}
	external := pds.recordCalls[0]["embed"].(map[string]any)["external"].(map[string]any)
	thumb, ok := external["thumb"]
	if !ok {
		t.Fatalf("embed is missing the thumbnail blob")
	}
	// The embedded thumb, re-marshalled, must be a canonical blob ref.
	raw, err := json.Marshal(thumb)
	if err != nil {
		t.Fatalf("marshal thumb: %v", err)
	}
	assertCanonical(t, raw)
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	var ref struct {
		Link string `json:"$link"`
	}
	_ = json.Unmarshal(obj["ref"], &ref)
	if ref.Link != "bafynested" {
		t.Errorf("embedded thumb ref = %q, want bafynested", ref.Link)
	}
}
