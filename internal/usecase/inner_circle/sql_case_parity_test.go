package inner_circle

import "testing"

// TestSqlCaseMatchesTierHierarchy is a string-equivalence guard. The CASE
// expression in repository/comment_repository.go (and any other call site
// that mirrors the tier ordering in SQL) must produce the same ranking as
// inner_circle.TierRank. This test encodes the SQL CASE rules in Go and
// asserts equality against TierRank for every canonical tier value plus
// null/unknown — drift here means the comment-list join would surface
// stale or incorrect badges, which is silent visual lying.
func TestSqlCaseMatchesTierHierarchy(t *testing.T) {
	// Replicates the SQL CASE used in ListByVideo and ListByChannel:
	//   CASE m.tier_id WHEN 'elite' THEN 3 WHEN 'vip' THEN 2
	//                  WHEN 'supporter' THEN 1 ELSE 0 END
	sqlCase := func(tier string) int {
		switch tier {
		case "elite":
			return 3
		case "vip":
			return 2
		case "supporter":
			return 1
		default:
			return 0
		}
	}
	cases := []string{"", "supporter", "vip", "elite", "unknown", "Elite", "diamond"}
	for _, tier := range cases {
		t.Run(tier, func(t *testing.T) {
			if got, want := sqlCase(tier), TierRank(tier); got != want {
				t.Fatalf("SQL CASE rank for %q = %d, but TierRank = %d — drift!", tier, got, want)
			}
		})
	}
}
