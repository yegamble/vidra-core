package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/watchword"
)

func TestWatchedWordMatchesFlow(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin.
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	// The admin adds a watched word.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"spam"}`, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add watched word = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A viewer posts a comment containing the watched term.
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	crec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"this is SPAM"}`, bob)
	if crec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", crec.Code, crec.Body.String())
	}
	var cv commentView
	_ = json.Unmarshal(crec.Body.Bytes(), &cv)

	// The flagged comment appears in the watched-word matches queue.
	rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("matches = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body watchedWordMatchListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Matches) != 1 || body.Matches[0].Word != "spam" || body.Matches[0].CommentID != cv.ID {
		t.Fatalf("matches = %+v, want one spam match for comment %s", body.Matches, cv.ID)
	}

	// A clean comment adds no new match.
	if rec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"nice video"}`, bob); rec.Code != http.StatusCreated {
		t.Fatalf("clean comment = %d", rec.Code)
	}
	var after watchedWordMatchListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/watched-word-matches", admin).Body.Bytes(), &after)
	if len(after.Matches) != 1 {
		t.Errorf("after a clean comment, matches = %d, want still 1", len(after.Matches))
	}

	// A regular user cannot read the matches queue; anon is unauthorized.
	if rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", bob); rec.Code != http.StatusForbidden {
		t.Errorf("non-mod matches = %d, want 403", rec.Code)
	}
	if rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon matches = %d, want 401", rec.Code)
	}

	// Comment matches carry the "comment" type badge (§12 adds video ones).
	if body.Matches[0].Type != "comment" {
		t.Errorf("comment match type = %q, want comment", body.Matches[0].Type)
	}
}

// TestWatchedWordVideoMatchesFlow proves §12: a video's title+description are
// matched on create AND edit, recorded once per term+video, and surface in the
// shared review queue typed "video" with a title + owner context (no comment
// fields).
func TestWatchedWordVideoMatchesFlow(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"spam"}`, admin); rec.Code != http.StatusCreated {
		t.Fatalf("add watched word = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A creator (regular user) with a channel.
	bob := createChannelFor(t, srv, "bob", "bob@example.test", "bobtube")

	matches := func() []watchedWordMatchView {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches", admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("matches = %d; body=%s", rec.Code, rec.Body.String())
		}
		var body watchedWordMatchListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Matches
	}

	// A clean video create flags nothing.
	clean := createVideo(t, srv, bob, "bobtube", `{"title":"wholesome cooking","privacy":"public"}`)
	if got := matches(); len(got) != 0 {
		t.Fatalf("clean create flagged %d matches, want 0", len(got))
	}

	// A create whose DESCRIPTION carries the term is flagged on create.
	flagged := createVideo(t, srv, bob, "bobtube", `{"title":"my mixtape","description":"pure SPAM inside","privacy":"public"}`)
	got := matches()
	if len(got) != 1 {
		t.Fatalf("create-flagged matches = %d, want 1", len(got))
	}
	m := got[0]
	if m.Type != "video" || m.Word != "spam" || m.VideoID != flagged {
		t.Errorf("match = %+v, want a video/spam match for %s", m, flagged)
	}
	if m.VideoTitle != "my mixtape" || m.AuthorUsername != "bob" {
		t.Errorf("match context = (%q, %q), want (my mixtape, bob)", m.VideoTitle, m.AuthorUsername)
	}
	if m.CommentID != "" || m.CommentBody != "" {
		t.Errorf("video match carries comment fields: %+v", m)
	}

	// An EDIT that newly introduces the term flags the clean video too.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+clean, `{"title":"now with Spam"}`, bob); rec.Code != http.StatusOK {
		t.Fatalf("edit = %d; body=%s", rec.Code, rec.Body.String())
	}
	got = matches()
	if len(got) != 2 {
		t.Fatalf("after edit, matches = %d, want 2", len(got))
	}
	if got[0].VideoID != clean || got[0].Type != "video" {
		t.Errorf("newest match = %+v, want the edited video", got[0])
	}

	// Re-editing with the same term stays idempotent (one row per term+video).
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+clean, `{"description":"still spam"}`, bob); rec.Code != http.StatusOK {
		t.Fatalf("re-edit = %d", rec.Code)
	}
	if got := matches(); len(got) != 2 {
		t.Errorf("after idempotent re-edit, matches = %d, want still 2", len(got))
	}
}

// TestWatchedWordMatchSnapshotAndTriage proves the A16 ruling at the HTTP layer:
// the queue quotes the SNAPSHOT and not the live body, an edit that removes the
// term leaves both the flag and its evidence standing, an edit that introduces a
// new term raises its own match, and a moderator can finally resolve or dismiss
// a row. The store integration test proves the SQL; this proves the wiring, the
// filter, the codes and the audit envelope.
func TestWatchedWordMatchSnapshotAndTriage(t *testing.T) {
	srv := videoServer(t)
	var buf bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	for _, w := range []string{"pineapple", "anchovy"} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"`+w+`"}`, admin); rec.Code != http.StatusCreated {
			t.Fatalf("add watched word %s = %d; body=%s", w, rec.Code, rec.Body.String())
		}
	}

	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	original := "I want pineapple on it"
	crec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"`+original+`"}`, bob)
	if crec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", crec.Code, crec.Body.String())
	}
	var cv commentView
	_ = json.Unmarshal(crec.Body.Bytes(), &cv)

	queue := func(query string) []watchedWordMatchView {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches"+query, admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("matches%s = %d; body=%s", query, rec.Code, rec.Body.String())
		}
		var body watchedWordMatchListResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if len(body.Matches) != int(body.Total) {
			t.Errorf("matches%s returned %d rows while total says %d", query, len(body.Matches), body.Total)
		}
		return body.Matches
	}

	got := queue("")
	if len(got) != 1 {
		t.Fatalf("after flagging, queue = %d, want 1", len(got))
	}
	if got[0].MatchedText != original {
		t.Errorf("snapshot = %q, want the flag-time body %q", got[0].MatchedText, original)
	}
	if got[0].TargetStatus != watchword.TargetPresent || got[0].SnapshotBackfilled || !got[0].TermActive {
		t.Errorf("fresh match = %+v, want present, not backfilled, term active", got[0])
	}
	if got[0].Status != watchword.StatusOpen || got[0].ModeratorNote != "" || got[0].ResolvedAt != nil {
		t.Errorf("fresh match triage state = %+v, want open with no note", got[0])
	}
	// The offset locates the term inside the snapshot, in runes.
	if int(got[0].MatchOffset) < 0 || got[0].MatchLength != int32(len("pineapple")) {
		t.Errorf("match position = (%d, %d), want the term located", got[0].MatchOffset, got[0].MatchLength)
	}
	if runes := []rune(got[0].MatchedText); string(runes[got[0].MatchOffset:got[0].MatchOffset+got[0].MatchLength]) != "pineapple" {
		t.Errorf("slicing the snapshot at the reported offset does not yield the term")
	}

	// THE DEFECT. The author edits the term away and swaps in another. Before
	// this slice the queue kept the flag and started quoting the CLEAN body.
	edited := "never mind, anchovy instead"
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/comments/"+cv.ID, `{"body":"`+edited+`"}`, bob); rec.Code != http.StatusOK {
		t.Fatalf("edit comment = %d; body=%s", rec.Code, rec.Body.String())
	}
	got = queue("")
	if len(got) != 2 {
		t.Fatalf("after the edit, queue = %d, want 2 (the old flag survives, the new term is added)", len(got))
	}
	byWord := map[string]watchedWordMatchView{}
	for _, m := range got {
		byWord[m.Word] = m
	}
	old, ok := byWord["pineapple"]
	if !ok {
		t.Fatalf("the original flag vanished: %+v", got)
	}
	if old.MatchedText != original {
		t.Errorf("the snapshot moved to %q; it must stay %q — this is the defect", old.MatchedText, original)
	}
	if old.TargetStatus != watchword.TargetEditedAway {
		t.Errorf("target_status = %q, want edited_away", old.TargetStatus)
	}
	if old.CommentBody != edited {
		t.Errorf("live excerpt = %q, want the edited body beside the snapshot", old.CommentBody)
	}
	if old.AuthorUsername != "bob" {
		t.Errorf("author = %q, want bob (the queue names whose text it was)", old.AuthorUsername)
	}
	fresh, ok := byWord["anchovy"]
	if !ok || fresh.MatchedText != edited || fresh.TargetStatus != watchword.TargetPresent {
		t.Errorf("the newly introduced term = %+v, want a present match snapshotting the edited body", fresh)
	}

	// Triage. A moderator (not just the admin) can resolve and dismiss.
	dana := registerAndToken(t, srv, `{"username":"dana","email":"dana@example.test","password":"supersecret"}`)
	danaID := userIDByName(t, adminUsers(t, srv, "", admin).Users, "dana")
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+danaID, `{"role":"moderator"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("promote dana = %d; body=%s", rec.Code, rec.Body.String())
	}
	resolve := func(id, body, token string) int {
		return sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-word-matches/"+id+"/resolve", body, token).Code
	}
	if code := resolve(old.ID, `{"status":"resolved","note":"removed the comment"}`, dana); code != http.StatusNoContent {
		t.Fatalf("moderator resolve = %d, want 204", code)
	}
	// Idempotent, like the report queue: a repeat overwrites and still succeeds.
	if code := resolve(old.ID, `{"status":"dismissed","note":"on reflection, fine"}`, dana); code != http.StatusNoContent {
		t.Errorf("repeat triage = %d, want 204 (idempotent)", code)
	}
	if code := resolve(uuid.NewString(), `{"status":"resolved"}`, admin); code != http.StatusNotFound {
		t.Errorf("triage unknown id = %d, want 404", code)
	}
	if code := resolve(old.ID, `{"status":"maybe"}`, admin); code != http.StatusUnprocessableEntity {
		t.Errorf("triage with a bogus outcome = %d, want 422", code)
	}
	if code := resolve(old.ID, `{"status":"resolved"}`, bob); code != http.StatusForbidden {
		t.Errorf("ordinary user triage = %d, want 403", code)
	}
	if code := resolve(old.ID, `{"status":"resolved"}`, ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous triage = %d, want 401", code)
	}

	// The queue lists OPEN by default; the triaged row is behind a filter.
	if open := queue(""); len(open) != 1 || open[0].Word != "anchovy" {
		t.Errorf("default queue = %+v, want only the untriaged match", open)
	}
	if all := queue("?status=all"); len(all) != 2 {
		t.Errorf("?status=all = %d, want 2", len(all))
	}
	dismissed := queue("?status=dismissed")
	if len(dismissed) != 1 || dismissed[0].ID != old.ID {
		t.Fatalf("?status=dismissed = %+v, want the triaged row", dismissed)
	}
	if dismissed[0].ModeratorNote != "on reflection, fine" || dismissed[0].ResolvedByUsername != "dana" || dismissed[0].ResolvedAt == nil {
		t.Errorf("triaged row = %+v, want the second note attributed to dana with a timestamp", dismissed[0])
	}
	if rec := getWithAuth(srv, "/api/v1/admin/watched-word-matches?status=bogus", admin); rec.Code != http.StatusBadRequest {
		t.Errorf("?status=bogus = %d, want 400 (an unrecognised filter is not silently 'all')", rec.Code)
	}

	// Audited with the outcome, and WITHOUT the moderator's prose: audit_log's
	// metadata allowlist rejects prose, and a note in the ledger would delete
	// the event rather than enrich it (the 0130 lesson).
	events := auditEvents(t, &buf)
	ev := findAudit(events, observability.ActionWatchedWordMatchResolve, observability.ResultSuccess)
	if ev == nil {
		t.Fatalf("no %s audit event", observability.ActionWatchedWordMatchResolve)
	}
	if got := ev["resource_id"]; got != old.ID {
		t.Errorf("audit resource_id = %v, want the match id %s", got, old.ID)
	}
	if bytes.Contains(buf.Bytes(), []byte("on reflection, fine")) {
		t.Error("the moderator's note reached the audit log stream")
	}
	if findAudit(events, observability.ActionWatchedWordMatchResolve, observability.ResultFailure) == nil {
		t.Error("the unknown-id refusal was not audited")
	}
}

// TestWatchedWordMatchOutlivesItsWord proves the second half of the cascade
// ruling: deleting a WORD used to delete its whole review history (0030's
// ON DELETE CASCADE), so a moderator pruning the term list silently discarded
// the record of everything it had caught. The match now survives with its term
// readable from the snapshot and term_active false.
func TestWatchedWordMatchOutlivesItsWord(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, admin, "ada", `{"title":"Clip","privacy":"public"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/watched-words", `{"word":"pineapple"}`, admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add watched word = %d", rec.Code)
	}
	var word watchedWordView
	_ = json.Unmarshal(rec.Body.Bytes(), &word)

	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if crec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"pineapple please"}`, bob); crec.Code != http.StatusCreated {
		t.Fatalf("comment = %d", crec.Code)
	}

	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/watched-words/"+word.ID, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete word = %d", rec.Code)
	}

	var body watchedWordMatchListResponse
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/admin/watched-word-matches", admin).Body.Bytes(), &body)
	if len(body.Matches) != 1 {
		t.Fatalf("after deleting the word, queue = %d, want 1 — the history must survive", len(body.Matches))
	}
	m := body.Matches[0]
	if m.Word != "pineapple" {
		t.Errorf("word = %q, want it read back from the snapshot", m.Word)
	}
	if m.TermActive {
		t.Error("term_active is true for a deleted term")
	}
	if m.MatchedText != "pineapple please" {
		t.Errorf("snapshot = %q, want the flagged body", m.MatchedText)
	}
}
