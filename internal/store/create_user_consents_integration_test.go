//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// CreateUser writes the account and the record of what its holder accepted in
// ONE statement. These are the properties that rest on: both land, the time
// defaults to now and can be supplied, an account can be created with nothing
// to record, and a record the table refuses takes the account down with it.
func TestCreateUserWritesTheAccountAndItsConsentsTogether(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	sfx := uuid.NewString()[:8]
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE username LIKE $1`, "consent_"+sfx+"%")
	}()
	arg := func(name string) sqlcgen.CreateUserParams {
		return sqlcgen.CreateUserParams{Username: "consent_" + sfx + name, Email: "consent_" + sfx + name + "@example.test", PasswordHash: "x", Role: "user", HistoryEnabled: true}
	}
	type row struct {
		document, version, url, source string
		minimumAge                     *int32
		acceptedAt                     time.Time
	}
	consents := func(userID uuid.UUID) []row {
		t.Helper()
		rows, err := st.Pool.Query(ctx, `SELECT document, version, url, source, minimum_age, accepted_at
			FROM user_consents WHERE user_id = $1 ORDER BY document`, userID)
		if err != nil {
			t.Fatalf("read consents: %v", err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.document, &r.version, &r.url, &r.source, &r.minimumAge, &r.acceptedAt); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, r)
		}
		return out
	}

	a := arg("a")
	a.ConsentSource = "signup"
	a.ConsentDocuments = []string{"terms", "privacy", "age"}
	a.ConsentVersions = []string{"2026-11-12", "v3", ""}
	a.ConsentUrls = []string{"https://example.test/terms", "https://example.test/privacy", ""}
	a.ConsentMinimumAges = []int32{0, 0, 16}
	before := time.Now().Add(-time.Minute)
	ua, err := q.CreateUser(ctx, a)
	if err != nil {
		t.Fatalf("CreateUser with consents: %v", err)
	}
	got := consents(ua.ID)
	if len(got) != 3 {
		t.Fatalf("consent rows = %+v, want 3", got)
	}
	// ORDER BY document: age, privacy, terms.
	if got[0].document != "age" || got[0].minimumAge == nil || *got[0].minimumAge != 16 ||
		got[1].document != "privacy" || got[1].version != "v3" || got[1].url != "https://example.test/privacy" || got[1].minimumAge != nil ||
		got[2].document != "terms" || got[2].version != "2026-11-12" || got[2].url != "https://example.test/terms" {
		t.Errorf("consent rows = %+v", got)
	}
	for _, r := range got {
		if r.source != "signup" || r.acceptedAt.Before(before) || r.acceptedAt.After(time.Now().Add(time.Minute)) {
			t.Errorf("row %+v: want source signup and accepted_at about now", r)
		}
	}

	// A supplied time is the time recorded (the approval queue's case).
	ticked := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	b := arg("b")
	b.ConsentSource, b.ConsentAcceptedAt = "approval", pgtype.Timestamptz{Time: ticked, Valid: true}
	b.ConsentDocuments, b.ConsentVersions, b.ConsentUrls, b.ConsentMinimumAges = []string{"terms"}, []string{""}, []string{"https://example.test/terms"}, []int32{0}
	ub, err := q.CreateUser(ctx, b)
	if err != nil {
		t.Fatalf("CreateUser with a supplied time: %v", err)
	}
	if got := consents(ub.ID); len(got) != 1 || !got[0].acceptedAt.Equal(ticked) || got[0].source != "approval" {
		t.Errorf("consent rows = %+v, want one at %v from approval", got, ticked)
	}

	// Nothing to record: the account alone.
	uc, err := q.CreateUser(ctx, arg("c"))
	if err != nil {
		t.Fatalf("CreateUser with no consents: %v", err)
	}
	if got := consents(uc.ID); len(got) != 0 {
		t.Errorf("consent rows for an account that accepted nothing = %+v", got)
	}

	// A record the table refuses: no account either.
	d := arg("d")
	d.ConsentSource = "signup"
	d.ConsentDocuments, d.ConsentVersions, d.ConsentUrls, d.ConsentMinimumAges = []string{"cookies"}, []string{""}, []string{""}, []int32{0}
	if _, err := q.CreateUser(ctx, d); err == nil {
		t.Fatal("CreateUser accepted a consent row the table refuses")
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE username = $1`, d.Username).Scan(&n); err != nil || n != 0 {
		t.Fatalf("accounts left behind by the refused statement = %d, %v; want 0", n, err)
	}
}
