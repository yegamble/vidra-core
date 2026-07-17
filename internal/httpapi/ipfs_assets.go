package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// redirectPublicIPFS sends an eligible, already-pinned public asset to the
// configured IPFS gateway. Missing/pending/failed/private pins and lookup
// failures fall back to authoritative local/S3 serving: IPFS remains an
// optional bandwidth-saving sidecar, never a serving dependency.
func (s *Server) redirectPublicIPFS(c echo.Context, objectKey string, class ipfsmirror.MediaClass) (bool, error) {
	if !s.ipfsMirrorEnabled() {
		return false, nil
	}
	u, ok, err := s.ipfsmirrorsvc.PublicAssetURL(c.Request().Context(), objectKey, class)
	if err != nil {
		s.logger.WarnContext(c.Request().Context(), "ipfs asset lookup failed; serving authoritative storage",
			"error", err, "media_class", class, "object_key", objectKey)
		return false, nil
	}
	if !ok {
		return false, nil
	}
	// The stable application URL may point at replacement bytes later, so cache
	// this redirect briefly. The gateway target itself is immutable by CID.
	c.Response().Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
	return true, c.Redirect(http.StatusTemporaryRedirect, u)
}

func publicVideoForIPFS(privacy, state string) bool {
	return privacy == "public" && state == "published"
}

func identityIPFSClass(ownerKind, imageKind string) ipfsmirror.MediaClass {
	switch ownerKind + ":" + imageKind {
	case "user:avatar":
		return ipfsmirror.ClassUserAvatar
	case "user:banner":
		return ipfsmirror.ClassUserBanner
	case "channel:avatar":
		return ipfsmirror.ClassChannelAvatar
	case "channel:banner":
		return ipfsmirror.ClassChannelBanner
	default:
		return ""
	}
}
