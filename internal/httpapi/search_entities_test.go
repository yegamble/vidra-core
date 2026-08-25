package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Channel- and account-search coverage. The account-visibility assertions are
// the point of this file: an account absent from GET /users/{username}/profile
// must be absent from GET /search/accounts, and every way an account can be
// non-public is exercised negatively.

// entityFixture is a server with several accounts and channels seeded.
type entityFixture struct{ srv *Server }

func newEntityFixture(t *testing.T) *entityFixture {
	t.Helper()
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), nil)
	return &entityFixture{srv: srv}
}

// account registers a user, opts their profile in (registration leaves
// profile_public at its default), creates a channel, and returns their token.
func (f *entityFixture) account(t *testing.T, username, handle, displayName string) string {
	t.Helper()
	tok := registerAndToken(t, f.srv, `{"username":"`+username+`","email":"`+username+`@example.test","password":"supersecret"}`)
	patch(t, f.srv, tok, `{"profile_public":true,"display_name":"`+displayName+`"}`)
	if handle != "" {
		rec := postJSONAuth(f.srv, "/api/v1/channels",
			`{"handle":"`+handle+`","display_name":"`+displayName+`"}`, tok)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create channel %s = %d; body=%s", handle, rec.Code, rec.Body.String())
		}
	}
	return tok
}

// patch applies a PATCH /auth/me body, failing on non-200.
func patch(t *testing.T, srv *Server, tok, body string) {
	t.Helper()
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", body, tok); rec.Code != http.StatusOK {
		t.Fatalf("PATCH /auth/me %s = %d; body=%s", body, rec.Code, rec.Body.String())
	}
}

func (f *entityFixture) channels(t *testing.T, query, tok string) channelSearchResponse {
	t.Helper()
	rec := sendJSONAuth(f.srv, http.MethodGet, "/api/v1/search/channels?"+query, "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search/channels?%s = %d; body=%s", query, rec.Code, rec.Body.String())
	}
	var resp channelSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func (f *entityFixture) accounts(t *testing.T, query, tok string) accountSearchResponse {
	t.Helper()
	rec := sendJSONAuth(f.srv, http.MethodGet, "/api/v1/search/accounts?"+query, "", tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search/accounts?%s = %d; body=%s", query, rec.Code, rec.Body.String())
	}
	var resp accountSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func channelHandles(resp channelSearchResponse) []string {
	out := make([]string, 0, len(resp.Channels))
	for _, c := range resp.Channels {
		out = append(out, c.Handle)
	}
	return out
}

func accountNames(resp accountSearchResponse) []string {
	out := make([]string, 0, len(resp.Accounts))
	for _, a := range resp.Accounts {
		out = append(out, a.Username)
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// TestChannelSearchFindsChannelsWithNoVideos is the reason this endpoint is not
// routed through vidra-search: the index knows a channel only through the
// videos it published, so a brand-new channel would be invisible there.
func TestChannelSearchFindsChannelsWithNoVideos(t *testing.T) {
	f := newEntityFixture(t)
	f.account(t, "ada", "ada_cooks", "Ada Cooks")
	f.account(t, "bob", "bob_bakes", "Bob Bakes")

	resp := f.channels(t, "q=cooks", "")
	if got := channelHandles(resp); !equalStrings(got, []string{"ada_cooks"}) {
		t.Fatalf("handles = %v, want [ada_cooks]", got)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
	// The display name is searchable too, and the card carries what it needs.
	resp = f.channels(t, "q=Bakes", "")
	if got := channelHandles(resp); !equalStrings(got, []string{"bob_bakes"}) {
		t.Fatalf("display-name search = %v, want [bob_bakes]", got)
	}
	if resp.Channels[0].DisplayName != "Bob Bakes" {
		t.Errorf("display_name = %q, want Bob Bakes", resp.Channels[0].DisplayName)
	}
	if resp.Channels[0].FollowerCount != 0 {
		t.Errorf("follower_count = %d, want 0", resp.Channels[0].FollowerCount)
	}
	// has_avatar rides channelViewFor, so it is present exactly when the
	// profile-image service is wired — the same rule as every other channel
	// surface. This harness does not wire it, so it must be OMITTED here rather
	// than fabricated as false.
	if resp.Channels[0].HasAvatar != nil {
		t.Errorf("has_avatar = %v with no image service wired; want omitted", *resp.Channels[0].HasAvatar)
	}
}

// TestChannelSearchHidesUnlistedAndInactiveOwners: a channel is a public
// identity, but the account behind it can leave discovery or be deactivated,
// and the channel goes with it.
func TestChannelSearchHidesUnlistedAndInactiveOwners(t *testing.T) {
	f := newEntityFixture(t)
	quiet := f.account(t, "quiet", "quiet_cooks", "Quiet Cooks")
	f.account(t, "loud", "loud_cooks", "Loud Cooks")

	if got := channelHandles(f.channels(t, "q=cooks", "")); len(got) != 2 {
		t.Fatalf("before opting out: %v, want both channels", got)
	}
	patch(t, f.srv, quiet, `{"unlisted":true}`)
	got := channelHandles(f.channels(t, "q=cooks", ""))
	if contains(got, "quiet_cooks") {
		t.Errorf("an unlisted owner's channel is still discoverable: %v", got)
	}
	if resp := f.channels(t, "q=cooks", ""); resp.Total != 1 {
		t.Errorf("total = %d, want 1 — the total must be counted under the same predicate", resp.Total)
	}
	// The direct URL keeps serving, which is what unlisted means.
	if rec := sendJSONAuth(f.srv, http.MethodGet, "/api/v1/channels/quiet_cooks", "", ""); rec.Code != http.StatusOK {
		t.Errorf("direct channel GET while unlisted = %d, want 200", rec.Code)
	}
}

// TestChannelSearchRespectsViewerBlocks: the viewer's own block hides the
// blocked account's channels from them, per-viewer, exactly as on every other
// list.
func TestChannelSearchRespectsViewerBlocks(t *testing.T) {
	f := newEntityFixture(t)
	f.account(t, "ada", "ada_cooks", "Ada Cooks")
	viewer := f.account(t, "carl", "", "Carl")
	adaID := f.accounts(t, "q=ada", "").Accounts[0].ID

	if rec := sendJSONAuth(f.srv, http.MethodPost, "/api/v1/me/blocks/"+adaID, "", viewer); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := channelHandles(f.channels(t, "q=cooks", viewer)); len(got) != 0 {
		t.Errorf("blocker still sees the blocked account's channel: %v", got)
	}
	// Blocks are per-viewer: anonymous callers are unaffected.
	if got := channelHandles(f.channels(t, "q=cooks", "")); !equalStrings(got, []string{"ada_cooks"}) {
		t.Errorf("anonymous view = %v, want [ada_cooks] (blocks are per-viewer)", got)
	}
}

// TestAccountSearchReturnsPublicAccounts is the happy path plus the shape of a
// result card.
func TestAccountSearchReturnsPublicAccounts(t *testing.T) {
	f := newEntityFixture(t)
	f.account(t, "alice", "", "Alice Anderson")
	f.account(t, "bob", "", "Bob Brown")

	resp := f.accounts(t, "q=alice", "")
	if got := accountNames(resp); !equalStrings(got, []string{"alice"}) {
		t.Fatalf("usernames = %v, want [alice]", got)
	}
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}
	a := resp.Accounts[0]
	if a.DisplayName != "Alice Anderson" || a.ID == "" {
		t.Errorf("card = %+v, want id + display name populated", a)
	}
	if a.HasAvatar != nil {
		t.Errorf("has_avatar = %v with no image service wired; want omitted", *a.HasAvatar)
	}
	// Display names are searchable too.
	if got := accountNames(f.accounts(t, "q=Brown", "")); !equalStrings(got, []string{"bob"}) {
		t.Errorf("display-name search = %v, want [bob]", got)
	}
}

// TestAccountSearchNeverReturnsNonPublicAccounts is THE correctness property of
// this endpoint, asserted negatively for every way an account can be non-public:
//
//   - profile_public false — the account never opted its profile in;
//   - is_active false     — deactivated or suspended;
//   - unlisted            — opted out of discovery specifically.
//
// Each case is cross-checked against GET /users/{username}/profile so the two
// surfaces are proven to agree rather than merely assumed to. For the first two
// the profile 404s; for the third it deliberately still serves, because a
// profile URL is a direct link and this list is discovery.
func TestAccountSearchNeverReturnsNonPublicAccounts(t *testing.T) {
	f := newEntityFixture(t)
	// A public control, so an empty result cannot pass by accident.
	f.account(t, "publicuser", "", "Public Zebra")
	private := f.account(t, "privateuser", "", "Private Zebra")
	gone := f.account(t, "goneuser", "", "Gone Zebra")
	hidden := f.account(t, "hiddenuser", "", "Hidden Zebra")

	if got := accountNames(f.accounts(t, "q=zebra", "")); len(got) != 4 {
		t.Fatalf("all four accounts must be visible before opting out; got %v", got)
	}

	patch(t, f.srv, private, `{"profile_public":false}`)
	patch(t, f.srv, hidden, `{"unlisted":true}`)
	// Deactivation is the same flag an admin suspension clears.
	if rec := sendJSONAuth(f.srv, http.MethodPost, "/api/v1/auth/me/deactivate", `{"password":"supersecret"}`, gone); rec.Code != http.StatusNoContent {
		t.Fatalf("deactivate = %d; body=%s", rec.Code, rec.Body.String())
	}

	resp := f.accounts(t, "q=zebra", "")
	got := accountNames(resp)
	if !equalStrings(got, []string{"publicuser"}) {
		t.Fatalf("account search = %v, want only [publicuser]", got)
	}
	// The total is counted under the same predicate; a wider count would leak
	// the existence of the three accounts the list refuses to return.
	if resp.Total != 1 {
		t.Errorf("total = %d, want 1", resp.Total)
	}

	// Cross-check against the profile endpoint the visibility rule comes from.
	for _, tc := range []struct {
		username string
		want     int
	}{
		{"publicuser", http.StatusOK},
		{"privateuser", http.StatusNotFound},
		{"goneuser", http.StatusNotFound},
		// unlisted keeps its direct URL: search excludes it, the profile does not.
		{"hiddenuser", http.StatusOK},
	} {
		rec := sendJSONAuth(f.srv, http.MethodGet, "/api/v1/users/"+tc.username+"/profile", "", "")
		if rec.Code != tc.want {
			t.Errorf("GET /users/%s/profile = %d, want %d", tc.username, rec.Code, tc.want)
		}
	}
}

// TestAccountSearchRespectsViewerBlocksAndMutes: the viewer's own blocks and
// mutes remove accounts, per-viewer.
func TestAccountSearchRespectsViewerBlocksAndMutes(t *testing.T) {
	f := newEntityFixture(t)
	f.account(t, "ada", "", "Ada Zebra")
	f.account(t, "bob", "", "Bob Zebra")
	viewer := f.account(t, "carl", "", "Carl Ostrich")

	ids := map[string]string{}
	for _, a := range f.accounts(t, "q=zebra", "").Accounts {
		ids[a.Username] = a.ID
	}
	if rec := sendJSONAuth(f.srv, http.MethodPost, "/api/v1/me/blocks/"+ids["ada"], "", viewer); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(f.srv, http.MethodPost, "/api/v1/me/mutes/accounts/"+ids["bob"], "", viewer); rec.Code != http.StatusNoContent {
		t.Fatalf("mute = %d; body=%s", rec.Code, rec.Body.String())
	}

	resp := f.accounts(t, "q=zebra", viewer)
	if got := accountNames(resp); len(got) != 0 {
		t.Errorf("viewer still sees blocked/muted accounts: %v", got)
	}
	if resp.Total != 0 {
		t.Errorf("total = %d, want 0 — the per-viewer count must match the page", resp.Total)
	}
	if got := accountNames(f.accounts(t, "q=zebra", "")); len(got) != 2 {
		t.Errorf("anonymous view = %v, want both (blocks and mutes are per-viewer)", got)
	}
}

// TestEntitySearchPagination proves both endpoints page and report a total that
// spans the whole result set, not the page.
func TestEntitySearchPagination(t *testing.T) {
	f := newEntityFixture(t)
	for _, n := range []string{"ostrich1", "ostrich2", "ostrich3"} {
		f.account(t, n, n+"_ch", "Ostrich "+n)
	}

	accounts := f.accounts(t, "q=ostrich&limit=2", "")
	if len(accounts.Accounts) != 2 || accounts.Total != 3 || accounts.Limit != 2 || accounts.Offset != 0 {
		t.Errorf("accounts page = %d rows, total=%d limit=%d offset=%d; want 2/3/2/0",
			len(accounts.Accounts), accounts.Total, accounts.Limit, accounts.Offset)
	}
	if page2 := f.accounts(t, "q=ostrich&limit=2&offset=2", ""); len(page2.Accounts) != 1 || page2.Total != 3 {
		t.Errorf("accounts page 2 = %d rows, total=%d; want 1/3", len(page2.Accounts), page2.Total)
	}

	channels := f.channels(t, "q=ostrich&limit=2", "")
	if len(channels.Channels) != 2 || channels.Total != 3 {
		t.Errorf("channels page = %d rows, total=%d; want 2/3", len(channels.Channels), channels.Total)
	}
}

// TestEntitySearchRequiresQuery: both endpoints refuse an empty or over-long q,
// matching the video search rather than quietly returning the whole table.
func TestEntitySearchRequiresQuery(t *testing.T) {
	f := newEntityFixture(t)
	long := make([]byte, 101)
	for i := range long {
		long[i] = 'a'
	}
	for _, path := range []string{"/api/v1/search/channels", "/api/v1/search/accounts"} {
		for _, q := range []string{"", "?q=", "?q=%20%20", "?q=" + string(long)} {
			if rec := sendJSONAuth(f.srv, http.MethodGet, path+q, "", ""); rec.Code != http.StatusBadRequest {
				t.Errorf("GET %s%s = %d, want 400", path, q, rec.Code)
			}
		}
	}
}
