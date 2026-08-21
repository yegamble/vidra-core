package httpapi

import (
	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// The IPFS gateway redirect itself now lives in internal/delivery as one source
// among several (delivery.SourceIPFSGateway, minted by the mirror provider that
// delivery.go wires): an eligible, already-pinned public asset is redirected to
// the configured gateway, and missing/pending/failed/private pins and ledger
// lookup failures all fall back to authoritative local/S3 serving. IPFS stays an
// optional bandwidth-saving sidecar, never a serving dependency — that
// fail-open shape is now the contract for every delivery source.
//
// What stays here is the part that is about VIDRA's data rather than about
// delivery: which rows are eligible, and how a route's asset maps onto a pin
// ledger media class.

// publicVideoForIPFS is the eligibility fence for redirecting a video's assets
// anywhere off-origin: public AND published. It is also what the delivery
// resolver's Eligible flag means for video routes — a private, unlisted-by-
// password, draft, scheduled, quarantined or blocked video is served by the API
// byte proxy alone.
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
