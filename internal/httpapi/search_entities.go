package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/profileimage"
)

// Channel and account search (GET /api/v1/search/channels, /search/accounts).
//
// These are LOCAL Postgres searches, and deliberately not routed through
// vidra-search like the video search is. The search index holds videos: a
// channel is present there only as denormalised columns on the video rows it
// published, so a channel that has published nothing does not exist as far as
// the index is concerned — and "find the channel I just made" is precisely what
// a channel search is for. Accounts are not indexed at all. pg_trgm already
// backs the local video-search fallback and the handle/username lookups; the
// two new queries follow that same pattern over channels and users, with the
// display-name halves indexed by migration 0118.
//
// Both endpoints are optionalAuth: they work anonymously, and a signed-in
// caller additionally has their own mutes and blocks applied, the same
// one-directional way every other list does it.

// channelSearchResponse is a page of channel results. The rows are the standard
// channelView every other channel surface returns — same shape, same JSON keys,
// including the follower count and avatar/banner flags a result card needs —
// rather than a search-only projection the frontend would have to type twice.
type channelSearchResponse struct {
	Query    string        `json:"query"`
	Channels []channelView `json:"channels"`
	pageMeta
}

// accountSearchView is a public account as a search result card. Its fields are
// a strict SUBSET of publicUserProfileView (GET /users/{username}/profile), with
// the same JSON keys and types, so a client can render a result card with the
// profile type it already has. The two omitted parts are the ones that cannot be
// answered per row without a query each: the account's channel list, and the
// Bluesky handle behind its own opt-in.
type accountSearchView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	// HasAvatar is set when the profile-image service is wired (omitted
	// otherwise); when true the image is served at GET /users/{username}/avatar.
	HasAvatar *bool `json:"has_avatar,omitempty"`
}

// accountSearchResponse is a page of account results.
type accountSearchResponse struct {
	Query    string              `json:"query"`
	Accounts []accountSearchView `json:"accounts"`
	pageMeta
}

// searchQueryParam reads and validates the shared ?q for the entity searches:
// required and bounded by the same maxSearchQueryLen as the video search, so
// one endpoint cannot be used to run a query the others refuse.
func searchQueryParam(c echo.Context) (string, error) {
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "query parameter q is required")
	}
	if len(q) > maxSearchQueryLen {
		return "", echo.NewHTTPError(http.StatusBadRequest, "query parameter q is too long")
	}
	return q, nil
}

// handleSearchChannels searches channels by handle and display name. No auth
// required. Requires a non-empty ?q (<=100 chars); paginated via ?limit (1–100,
// default 20)/?offset with a real total counted under the same predicate.
//
// Visibility is enforced in SQL (SearchPublicChannels): the owning account must
// be active and must not have opted out of discovery (unlisted). A signed-in
// caller's mutes and blocks hide the channels of the accounts they apply to.
func (s *Server) handleSearchChannels(c echo.Context) error {
	q, err := searchQueryParam(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	viewerID, _, authed := principalFromContext(c)
	found, total, err := s.channelsvc.SearchPublic(ctx, q, viewerID, authed, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]channelView, 0, len(found))
	for _, f := range found {
		// channelViewFor, not newChannelView: the avatar/banner flags are what a
		// result card renders, and this is the one helper that sets them.
		// AtprotoActive is deliberately left nil, as it is in every other bulk
		// listing — it costs a query per row and means nothing on a search card.
		views = append(views, s.channelViewFor(ctx, f.Channel, f.FollowerCount))
	}
	return c.JSON(http.StatusOK, channelSearchResponse{Query: q, Channels: views, pageMeta: page.meta(total)})
}

// handleSearchAccounts searches accounts by username and display name. No auth
// required. Requires a non-empty ?q (<=100 chars); paginated via ?limit (1–100,
// default 20)/?offset with a real total counted under the same predicate.
//
// ONLY PUBLICLY VISIBLE ACCOUNTS ARE RETURNED, and this handler does not decide
// what that means: the rule is GetPublicUserProfileByUsername's (active AND
// profile_public), enforced in SQL by SearchPublicAccounts so it cannot drift
// from the profile endpoint, plus the account-level discovery opt-out that a
// result list — unlike a direct profile URL — must honour. A deactivated,
// suspended, private, or unlisted account is absent from this list exactly as it
// is absent from GET /users/{username}/profile. A signed-in caller's mutes and
// blocks remove accounts on top of that.
func (s *Server) handleSearchAccounts(c echo.Context) error {
	q, err := searchQueryParam(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	viewerID, _, authed := principalFromContext(c)
	rows, total, err := s.authsvc.SearchPublicAccounts(ctx, q, viewerID, authed, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]accountSearchView, 0, len(rows))
	for _, r := range rows {
		v := accountSearchView{
			ID:          r.ID.String(),
			Username:    r.Username,
			DisplayName: r.DisplayName,
			Bio:         r.Bio,
		}
		if s.imagesvc != nil {
			has := s.imagesvc.HasUserImage(ctx, r.ID, profileimage.KindAvatar)
			v.HasAvatar = &has
		}
		views = append(views, v)
	}
	return c.JSON(http.StatusOK, accountSearchResponse{Query: q, Accounts: views, pageMeta: page.meta(total)})
}
