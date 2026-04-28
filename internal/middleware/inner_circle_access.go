package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"vidra-core/internal/usecase/inner_circle"

	"github.com/google/uuid"
)

// VideoTierLookup returns the inner_circle_tier and channel_id for a video, or
// empty values if the video is not gated.
type VideoTierLookup interface {
	// LookupVideoTier returns (channelID, tierGate, found). When tierGate is
	// "", the video is public; non-empty values gate the video to that tier.
	// found=false signals video not found (the middleware passes through —
	// the underlying handler will produce 404).
	LookupVideoTier(ctx context.Context, videoID string) (channelID uuid.UUID, tier string, found bool, err error)
}

// MembershipTierLookup returns the caller's active tier on the given channel,
// or empty string if none.
type MembershipTierLookup interface {
	GetActiveTier(ctx context.Context, userID, channelID uuid.UUID) (string, error)
}

// InnerCircleAccessConfig wires the middleware.
type InnerCircleAccessConfig struct {
	Videos      VideoTierLookup
	Memberships MembershipTierLookup
	// PathPrefixes is a list of URL prefixes to recover the video ID from.
	// If a request path doesn't start with any prefix, the middleware
	// passes through without checking. Default: ["/api/v1/hls/"].
	// Phase 9 must protect all HLS-serving routes, including
	// /static/streaming-playlists/hls/ and /static/streaming-playlists/hls/private/.
	PathPrefixes []string
	// PathPrefix is the legacy single-prefix form, kept for backward compat
	// with existing callers/tests. When set, it is appended to PathPrefixes.
	PathPrefix string
}

// RequireInnerCircleAccess returns a chi-compatible middleware that enforces
// the per-video Inner Circle tier gate on streaming routes. When the video is
// public (no tier set), the request passes through. When the video is gated
// and the caller's active membership tier does not satisfy the requirement,
// the handler returns 403 with a structured error body.
//
// The middleware reads UserIDKey from the request context. Callers without
// a UserIDKey are treated as anonymous (no membership tier).
func RequireInnerCircleAccess(cfg InnerCircleAccessConfig) func(http.Handler) http.Handler {
	prefixes := append([]string{}, cfg.PathPrefixes...)
	if cfg.PathPrefix != "" {
		prefixes = append(prefixes, cfg.PathPrefix)
	}
	if len(prefixes) == 0 {
		prefixes = []string{"/api/v1/hls/"}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			videoID := extractVideoIDFromPaths(r.URL.Path, prefixes)
			if videoID == "" {
				next.ServeHTTP(w, r)
				return
			}

			channelID, requiredTier, found, err := cfg.Videos.LookupVideoTier(r.Context(), videoID)
			if err != nil || !found {
				// Pass through — the downstream handler will produce 404 (or its
				// own error). The middleware does not invent failure modes.
				next.ServeHTTP(w, r)
				return
			}
			if requiredTier == "" {
				next.ServeHTTP(w, r)
				return
			}

			memberTier := ""
			if userIDStr, ok := r.Context().Value(UserIDKey).(string); ok && userIDStr != "" {
				if userID, parseErr := uuid.Parse(userIDStr); parseErr == nil {
					memberTier, _ = cfg.Memberships.GetActiveTier(r.Context(), userID, channelID)
				}
			}

			if inner_circle.HasAccess(memberTier, requiredTier) {
				next.ServeHTTP(w, r)
				return
			}

			writeInnerCircleForbidden(w, requiredTier, channelID.String())
		})
	}
}

func extractVideoIDFromPath(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rel := strings.TrimPrefix(path, prefix)
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// extractVideoIDFromPaths tries each prefix in order and returns the video ID
// from the first one that matches. Returns "" when no prefix matches.
func extractVideoIDFromPaths(path string, prefixes []string) string {
	for _, p := range prefixes {
		if id := extractVideoIDFromPath(path, p); id != "" {
			return id
		}
	}
	return ""
}

func writeInnerCircleForbidden(w http.ResponseWriter, requiredTier, channelID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":          "inner_circle_tier_required",
			"message":       "this video requires an active Inner Circle membership",
			"tier_required": requiredTier,
			"channel_id":    channelID,
		},
	})
}
