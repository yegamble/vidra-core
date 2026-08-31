package pseudonym

import (
	"testing"
	"time"
)

var (
	secretA = []byte("secret-a-secret-a-secret-a-secret-a")
	domainA = "vidra/test-domain-a/v1"
	domainB = "vidra/test-domain-b/v1"
	day     = time.Date(2026, 8, 23, 11, 0, 0, 0, time.UTC)
)

// TestSaltRotatesAtTheUTCDayBoundary is the privacy property: the day is inside
// the MAC'd bytes, so one second past midnight UTC the same principal is a new,
// unlinkable subject. The clock is a parameter — this never sleeps.
func TestSaltRotatesAtTheUTCDayBoundary(t *testing.T) {
	d := New(secretA, domainA)
	before := d.Of(time.Date(2026, 8, 23, 23, 59, 59, 0, time.UTC), "ip:203.0.113.7")
	after := d.Of(time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), "ip:203.0.113.7")
	if before == "" || after == "" {
		t.Fatal("pseudonym empty for a configured key")
	}
	if before == after {
		t.Error("the same principal produced the same pseudonym on two UTC days — cross-day linkage must be impossible")
	}
	// A non-UTC clock must not shift the boundary: 23:59 UTC seen as tomorrow in
	// a +02:00 zone is still today's salt.
	east := time.FixedZone("east", 2*60*60)
	if got := d.Of(time.Date(2026, 8, 24, 1, 59, 59, 0, east), "ip:203.0.113.7"); got != before {
		t.Error("the day salt followed the caller's zone; it must follow UTC")
	}
}

// TestDomainSeparation: the same secret and principal must yield unrelated
// pseudonyms in two domains, so a value from one dataset can never be joined
// against another's.
func TestDomainSeparation(t *testing.T) {
	a := New(secretA, domainA).Of(day, "ip:203.0.113.7")
	b := New(secretA, domainB).Of(day, "ip:203.0.113.7")
	if a == "" || b == "" || a == b {
		t.Errorf("domains produced %q and %q; they must differ and be non-empty", a, b)
	}
}

// TestDegradesToEmpty: a missing secret, a missing domain or a blank principal
// must produce NO value rather than an unkeyed or ambiguous hash.
func TestDegradesToEmpty(t *testing.T) {
	if New(nil, domainA) != nil {
		t.Error("New with no secret returned a digester; an absent key must yield none at all")
	}
	if New(secretA, "") != nil {
		t.Error("New with no domain returned a digester; an unlabelled key is not domain-separated")
	}
	var nilD *Digester
	if got := nilD.Of(day, "ip:203.0.113.7"); got != "" {
		t.Errorf("nil digester produced %q, want empty", got)
	}
	if got := New(secretA, domainA).Of(day, "  "); got != "" {
		t.Errorf("blank principal produced %q, want empty", got)
	}
}
