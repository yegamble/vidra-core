//go:build integration

package store

import (
	"context"
	"fmt"
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

// Approving a queued request creates the account AND its consent rows in the
// one approval statement, from what the request kept: the documents the
// applicant accepted, at the time they accepted them, not the time of approval.
func TestApprovalRecordsTheApplicantsConsentAtTheTimeTheyGaveIt(t *testing.T) {
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
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE username LIKE $1`, "queued_"+sfx+"%")
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM registration_requests WHERE username LIKE $1`, "queued_"+sfx+"%")
	}()
	termsURL, termsVersion, privacyURL, noLabel := "https://example.test/terms", "2026-11-12", "https://example.test/privacy", ""
	age := int32(16)

	req, err := q.CreateRegistrationRequest(ctx, sqlcgen.CreateRegistrationRequestParams{
		Username: "queued_" + sfx + "a", Email: "queued_" + sfx + "a@example.test", PasswordHash: "x",
		ConsentRecorded: true, ConsentTermsUrl: &termsURL, ConsentTermsVersion: &termsVersion,
		ConsentPrivacyUrl: &privacyURL, ConsentPrivacyVersion: &noLabel, ConsentMinimumAge: &age,
	})
	if err != nil {
		t.Fatalf("CreateRegistrationRequest: %v", err)
	}
	// The applicant accepted three days before the admin got to the queue.
	ticked := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Second)
	if _, err := st.Pool.Exec(ctx, `UPDATE registration_requests SET consent_accepted_at = $2 WHERE id = $1`, req.ID, ticked); err != nil {
		t.Fatalf("backdate the request: %v", err)
	}
	user, err := q.ApproveRegistrationRequest(ctx, sqlcgen.ApproveRegistrationRequestParams{ID: req.ID, HistoryEnabled: true})
	if err != nil {
		t.Fatalf("ApproveRegistrationRequest: %v", err)
	}
	rows, err := st.Pool.Query(ctx, `SELECT document, version, url, COALESCE(minimum_age, 0), source, accepted_at
		FROM user_consents WHERE user_id = $1 ORDER BY document`, user.ID)
	if err != nil {
		t.Fatalf("read consents: %v", err)
	}
	var got []string
	for rows.Next() {
		var document, version, url, source string
		var minimumAge int
		var acceptedAt time.Time
		if err := rows.Scan(&document, &version, &url, &minimumAge, &source, &acceptedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if source != "approval" || !acceptedAt.Equal(ticked) {
			t.Errorf("%s: source %q at %v, want approval at %v", document, source, acceptedAt, ticked)
		}
		got = append(got, fmt.Sprintf("%s|%s|%s|%d", document, version, url, minimumAge))
	}
	rows.Close()
	want := []string{"age|||16", "privacy||https://example.test/privacy|0", "terms|2026-11-12|https://example.test/terms|0"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("consent rows = %v, want %v", got, want)
	}

	// A request that kept nothing (filed before the columns existed, or with no
	// documents configured) approves into an account with no consent rows.
	bare, err := q.CreateRegistrationRequest(ctx, sqlcgen.CreateRegistrationRequestParams{
		Username: "queued_" + sfx + "b", Email: "queued_" + sfx + "b@example.test", PasswordHash: "x",
	})
	if err != nil {
		t.Fatalf("CreateRegistrationRequest (bare): %v", err)
	}
	bareUser, err := q.ApproveRegistrationRequest(ctx, sqlcgen.ApproveRegistrationRequestParams{ID: bare.ID, HistoryEnabled: true})
	if err != nil {
		t.Fatalf("ApproveRegistrationRequest (bare): %v", err)
	}
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM user_consents WHERE user_id = $1`, bareUser.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("consent rows for a request that kept nothing = %d, %v; want 0", n, err)
	}
}
