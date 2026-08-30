package federation

// Golden tests for every OUTBOUND ActivityPub document Vidra mints (fix_plan
// P10 "federation contract tests using fixtures"): actor documents, WebFinger,
// Accept{Follow}, Follow/Undo{Follow}, Create/Update/Delete{Video},
// Create/Update/Delete{Note}, and the outbox collections. Each document is
// canonicalized (volatile activity-id UUIDs and minted key PEMs scrubbed, keys
// sorted, stable indentation) and byte-compared against testdata/golden/*.json —
// so ANY shape regression (renamed/dropped/retyped field) flips a test.
//
// Regenerate after an INTENTIONAL shape change with:
//
//	UPDATE_GOLDEN=1 go test ./internal/federation -run ContractGolden
//
// and review the golden diff like source code.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// volatileActivityID matches the random UUID suffix of minted activity ids
// (…/activities/<verb>/<uuid>) so goldens stay stable across runs.
var volatileActivityID = regexp.MustCompile(
	`(/activities/[a-z]+/)[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// scrubVolatile normalizes the run-to-run volatile parts of an AP document:
// minted activity-id UUIDs and (never-committed) generated public key PEMs.
func scrubVolatile(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "publicKeyPem" {
				x[k] = "<public-key-pem>"
				continue
			}
			x[k] = scrubVolatile(val)
		}
		return x
	case []any:
		for i := range x {
			x[i] = scrubVolatile(x[i])
		}
		return x
	case string:
		return volatileActivityID.ReplaceAllString(x, "${1}00000000-0000-0000-0000-000000000000")
	default:
		return v
	}
}

// canonicalAP renders a marshaled AP document in canonical byte-stable form:
// volatile parts scrubbed, object keys sorted (encoding/json map order), fixed
// indentation, trailing newline.
func canonicalAP(t *testing.T, doc []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatalf("document is not valid JSON: %v\n%s", err, doc)
	}
	out, err := json.MarshalIndent(scrubVolatile(v), "", "  ")
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	return append(out, '\n')
}

// assertGolden byte-compares the canonical form of doc against
// testdata/golden/<name>. UPDATE_GOLDEN=1 rewrites the golden instead.
func assertGolden(t *testing.T, name string, doc []byte) {
	t.Helper()
	got := canonicalAP(t, doc)
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s — regenerate with UPDATE_GOLDEN=1 go test ./internal/federation -run ContractGolden: %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("outbound document drifted from golden %s\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}

// newGoldenRepo seeds the deterministic world every golden document is built
// from: ada + films + two public videos, one remote follower inbox, and a local
// comment thread (incl. a reply under a remote parent).
func newGoldenRepo() fakeRepo {
	r := newContractRepo()
	r.followerInboxes[ctChannelID] = []string{"https://peertube.example/inbox"}
	r.localFollowers[ctChannelID] = 3
	r.remoteFollowerN[ctChannelID] = 2
	r.channelVideoN[ctChannelID] = 2
	r.videosByID[ctVideo2ID] = sqlcgen.GetVideoByIDRow{
		ID: ctVideo2ID, ChannelID: ctChannelID,
		Title: "Night paddle", Description: "Part two.",
		Privacy: "public", State: "published",
	}
	r.outboxVideos[ctChannelID] = []sqlcgen.ListChannelOutboxVideosRow{
		{ID: ctVideo2ID, Title: "Night paddle", Description: "Part two."},
		{ID: ctVideoID, Title: "Dawn over the fjord", Description: "A quiet opening."},
	}
	// ada's top-level comment on the local video…
	r.commentsByID[ctCommentID] = sqlcgen.Comment{
		ID: ctCommentID, VideoID: ctVideoID,
		UserID: pgtype.UUID{Bytes: ctUserID, Valid: true},
		Body:   "Beautiful grade — what camera?",
	}
	// …and her reply under kaisa's federated comment (remote parent keeps its
	// origin object URL as the reply target).
	parentID := uuid.MustParse("e6f7a8b9-c0d1-4e2f-8a3b-4c5d6e7f8091")
	actor, note, name := ctMastoUser, ctMastoNote, "kaisa"
	r.commentsByID[parentID] = sqlcgen.Comment{
		ID: parentID, VideoID: ctVideoID, Body: "The drone shots are stunning!",
		RemoteActorUrl: &actor, RemoteAuthorName: &name, RemoteObjectUrl: &note,
	}
	r.commentsByID[ctReplyID] = sqlcgen.Comment{
		ID: ctReplyID, VideoID: ctVideoID,
		UserID:   pgtype.UUID{Bytes: ctUserID, Valid: true},
		ParentID: pgtype.UUID{Bytes: parentID, Valid: true},
		Body:     "Thanks — the route map is in the description.",
	}
	return r
}

// takeSingleDelivery asserts exactly one queued delivery, returns its payload,
// and clears the queue for the next capture.
func takeSingleDelivery(t *testing.T, repo fakeRepo) []byte {
	t.Helper()
	if len(repo.deliveries) != 1 {
		t.Fatalf("deliveries = %d, want exactly 1", len(repo.deliveries))
	}
	var payload []byte
	for id, d := range repo.deliveries {
		payload = d.row.Payload
		delete(repo.deliveries, id)
	}
	return payload
}

func TestContractGoldenActorDocuments(t *testing.T) {
	ctx := context.Background()
	repo := newGoldenRepo()
	svc := NewService(repo, WithBaseURL(contractBase))

	person, err := svc.AccountActor(ctx, "ada")
	if err != nil {
		t.Fatalf("AccountActor: %v", err)
	}
	doc, err := json.Marshal(person)
	if err != nil {
		t.Fatalf("marshal person: %v", err)
	}
	assertGolden(t, "actor_person.json", doc)

	group, err := svc.ChannelActor(ctx, "films")
	if err != nil {
		t.Fatalf("ChannelActor: %v", err)
	}
	doc, err = json.Marshal(group)
	if err != nil {
		t.Fatalf("marshal group: %v", err)
	}
	assertGolden(t, "actor_group.json", doc)

	jrd, err := svc.WebFinger(ctx, "acct:ada@videos.example")
	if err != nil {
		t.Fatalf("WebFinger: %v", err)
	}
	doc, err = json.Marshal(jrd)
	if err != nil {
		t.Fatalf("marshal jrd: %v", err)
	}
	assertGolden(t, "webfinger_account.json", doc)
}

func TestContractGoldenAcceptFollow(t *testing.T) {
	repo := newGoldenRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase))

	// A real-shaped Mastodon Follow lands; the queued Accept is our reply.
	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	assertGolden(t, "accept_follow.json", takeSingleDelivery(t, repo))
}

func TestContractGoldenRejectFollow(t *testing.T) {
	// federation_allow_channel_followers off (config-parity W12): the same
	// real-shaped Mastodon Follow gets a Reject instead of the auto-Accept.
	// The admin queue's reject path mints the identical document (see
	// TestRejectFollowerRequestDeliversReject).
	repo := newGoldenRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithAllowChannelFollowersFunc(gateOff))

	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	assertGolden(t, "reject_follow.json", takeSingleDelivery(t, repo))
}

func TestContractGoldenFollowBack(t *testing.T) {
	// federation_auto_follow_back on (config-parity W12): an accepted channel
	// Follow queues an Accept plus a follow-back Follow signed by the FOLLOWED
	// CHANNEL's actor (vidra has no instance-level AP actor).
	repo := newGoldenRepo()
	cacheContractActor(repo, ctMastoUser, "Person", "kaisa", "mastodon.example")
	svc := NewService(repo, WithBaseURL(contractBase), WithAutoFollowBackFunc(gateOn))

	if err := svc.HandleInbox(context.Background(), ctMastoUser, readFixture(t, "inbound/mastodon_follow.json")); err != nil {
		t.Fatalf("HandleInbox: %v", err)
	}
	var followBack []byte
	for id, d := range repo.deliveries {
		var doc map[string]any
		if err := json.Unmarshal(d.row.Payload, &doc); err != nil {
			t.Fatalf("delivery payload: %v", err)
		}
		if doc["type"] == "Follow" {
			followBack = d.row.Payload
		}
		delete(repo.deliveries, id)
	}
	if followBack == nil {
		t.Fatal("no follow-back Follow was queued")
	}
	assertGolden(t, "follow_back.json", followBack)
}

func TestContractGoldenFollowAndUndo(t *testing.T) {
	ctx := context.Background()
	repo := newGoldenRepo()
	cacheContractActor(repo, ctPTChannel, "Group", "kayak_diaries", "peertube.example")
	svc := NewService(repo, WithBaseURL(contractBase))

	follow, err := svc.FollowRemoteChannel(ctx, ctUserID, ctPTChannel)
	if err != nil {
		t.Fatalf("FollowRemoteChannel: %v", err)
	}
	assertGolden(t, "follow.json", takeSingleDelivery(t, repo))

	if err := svc.UnfollowRemoteChannel(ctx, ctUserID, follow.ID); err != nil {
		t.Fatalf("UnfollowRemoteChannel: %v", err)
	}
	assertGolden(t, "undo_follow.json", takeSingleDelivery(t, repo))
}

func TestContractGoldenVideoActivities(t *testing.T) {
	ctx := context.Background()
	repo := newGoldenRepo()
	svc := NewService(repo, WithBaseURL(contractBase))

	if err := svc.AnnounceVideo(ctx, ctVideoID); err != nil {
		t.Fatalf("AnnounceVideo: %v", err)
	}
	assertGolden(t, "create_video.json", takeSingleDelivery(t, repo))

	if err := svc.UpdateVideo(ctx, ctVideoID); err != nil {
		t.Fatalf("UpdateVideo: %v", err)
	}
	assertGolden(t, "update_video.json", takeSingleDelivery(t, repo))

	if err := svc.DeleteVideo(ctx, ctVideoID, ctChannelID, true); err != nil {
		t.Fatalf("DeleteVideo: %v", err)
	}
	assertGolden(t, "delete_video.json", takeSingleDelivery(t, repo))
}

func TestContractGoldenNoteActivities(t *testing.T) {
	ctx := context.Background()
	repo := newGoldenRepo()
	svc := NewService(repo, WithBaseURL(contractBase))

	if err := svc.AnnounceComment(ctx, ctCommentID); err != nil {
		t.Fatalf("AnnounceComment: %v", err)
	}
	assertGolden(t, "create_note.json", takeSingleDelivery(t, repo))

	// A reply under a REMOTE parent threads to the parent's origin object URL.
	if err := svc.AnnounceComment(ctx, ctReplyID); err != nil {
		t.Fatalf("AnnounceComment(reply): %v", err)
	}
	assertGolden(t, "create_note_reply.json", takeSingleDelivery(t, repo))

	if err := svc.UpdateComment(ctx, ctCommentID); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	assertGolden(t, "update_note.json", takeSingleDelivery(t, repo))

	if err := svc.DeleteComment(ctx, ctCommentID, ctVideoID, ctUserID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	assertGolden(t, "delete_note.json", takeSingleDelivery(t, repo))
}

func TestContractGoldenOutboxCollections(t *testing.T) {
	ctx := context.Background()
	repo := newGoldenRepo()
	svc := NewService(repo, WithBaseURL(contractBase))

	col, err := svc.ChannelCollection(ctx, "films", "outbox")
	if err != nil {
		t.Fatalf("ChannelCollection(outbox): %v", err)
	}
	doc, err := json.Marshal(col)
	if err != nil {
		t.Fatalf("marshal collection: %v", err)
	}
	assertGolden(t, "outbox_collection.json", doc)

	page, err := svc.ChannelOutboxPage(ctx, "films", 1)
	if err != nil {
		t.Fatalf("ChannelOutboxPage: %v", err)
	}
	doc, err = json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}
	assertGolden(t, "outbox_page.json", doc)

	followers, err := svc.ChannelCollection(ctx, "films", "followers")
	if err != nil {
		t.Fatalf("ChannelCollection(followers): %v", err)
	}
	doc, err = json.Marshal(followers)
	if err != nil {
		t.Fatalf("marshal followers: %v", err)
	}
	assertGolden(t, "followers_collection.json", doc)
}

// Keep the deterministic seed honest: the golden world must never drift from
// the fixture corpus identities.
func TestContractGoldenSeedMatchesCorpus(t *testing.T) {
	// Canonical form: what we MINT today (AP id/url, outbox items, inReplyTo) —
	// the same shape RSS/sitemap/oEmbed emit and the frontend actually routes.
	// Pinned to the literal the outbound golden fixtures carry.
	if got := contractBase + "/videos/" + ctVideoID.String(); got != "https://videos.example/videos/7a1d9e42-5b3c-4f8e-a6d0-9c8b7a6f5e4d" {
		t.Errorf("canonical local video URL %q no longer matches the outbound golden fixtures", got)
	}
	// Legacy form: what we USED to mint and still accept inbound. Pinned to the
	// literal the inbound Mastodon Note fixtures use as inReplyTo, so a reply
	// federated under the old format keeps resolving to the same video.
	if got := contractBase + "/videos/watch/" + ctVideoID.String(); got != "https://videos.example/videos/watch/7a1d9e42-5b3c-4f8e-a6d0-9c8b7a6f5e4d" {
		t.Errorf("legacy local video URL %q no longer matches the inReplyTo used by the Note fixtures", got)
	}
	if _, err := time.Parse(time.RFC3339, "2026-05-12T08:30:00.000Z"); err != nil {
		t.Errorf("fixture publish timestamp is not RFC3339: %v", err)
	}
}
