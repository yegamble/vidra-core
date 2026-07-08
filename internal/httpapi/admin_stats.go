package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// adminStatsResponse is the instance-wide overview the admin dashboard renders as
// cards. Every field is a live, trivially-real COUNT/SUM over a committed column.
// There are deliberately NO period-over-period deltas — no time-series store
// exists to compute them (that is a W3 analytics dependency). No secrets/PII.
type adminStatsResponse struct {
	// Users is the total number of accounts (active or not).
	Users int64 `json:"users"`
	// PublishedVideos is the count of public, published videos (the same total
	// NodeInfo advertises as local posts).
	PublishedVideos int64 `json:"published_videos"`
	// MediaStoredBytes is the total stored bytes of every video file across all
	// accounts (originals, renditions, thumbnails).
	MediaStoredBytes int64 `json:"media_stored_bytes"`
	// FederatedPeers is the number of distinct remote instances we have cached
	// actors from.
	FederatedPeers int64 `json:"federated_peers"`
	// Comments is the total number of local comments.
	Comments int64 `json:"comments"`
}

// handleAdminStats returns the instance-wide overview counts for the admin
// dashboard (the vidra-user admin overview cards bind to this). Behind
// requireRole(admin). A read-only aggregate — like the other admin GET snapshots
// (system status, jobs) it is not audited.
func (s *Server) handleAdminStats(c echo.Context) error {
	st, err := s.adminsvc.Stats(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, adminStatsResponse{
		Users:            st.Users,
		PublishedVideos:  st.PublishedVideos,
		MediaStoredBytes: st.MediaStoredBytes,
		FederatedPeers:   st.FederatedPeers,
		Comments:         st.Comments,
	})
}
