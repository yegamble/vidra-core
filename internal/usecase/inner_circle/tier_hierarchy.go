// Package inner_circle implements the Inner Circle creator-membership domain.
//
// The tier hierarchy is the single source of truth for ordering Inner Circle
// tiers — every callsite that compares "does this member's tier reach this
// required tier?" routes through HasAccess (or, equivalently, TierRank). The
// SQL CASE used in T7 is asserted equivalent in tests; drift fails the suite.
package inner_circle

// TierID is one of the canonical Inner Circle tier identifiers.
type TierID string

const (
	TierSupporter TierID = "supporter"
	TierVIP       TierID = "vip"
	TierElite     TierID = "elite"
)

// TierRank returns the ordinal rank of a tier ID. Empty string and unknown
// values return 0 (no membership / public content). Higher rank = higher tier.
func TierRank(tier string) int {
	switch tier {
	case string(TierElite):
		return 3
	case string(TierVIP):
		return 2
	case string(TierSupporter):
		return 1
	default:
		return 0
	}
}

// HasAccess reports whether a viewer holding memberTier can access content
// gated at requiredTier. Empty requiredTier means public content (always
// accessible). The empty memberTier represents non-members.
func HasAccess(memberTier, requiredTier string) bool {
	return TierRank(memberTier) >= TierRank(requiredTier)
}

// ValidTierID reports whether s is one of the canonical tier IDs. Used by
// handlers and validators to reject unknown tier values at the API boundary.
func ValidTierID(s string) bool {
	switch s {
	case string(TierSupporter), string(TierVIP), string(TierElite):
		return true
	default:
		return false
	}
}
