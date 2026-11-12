//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// user_consents (0189) is evidence: one row per acceptance, never rewritten,
// gone only with the account. These are the store's own guarantees, which no
// application code can walk past.
func TestUserConsentsAreAppendOnlyAndGoWithTheAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	userID, _, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	insert := func(document, source string, minimumAge *int) error {
		_, err := st.Pool.Exec(ctx, `INSERT INTO user_consents (user_id, document, version, url, minimum_age, source)
			VALUES ($1, $2, '2026-11-12', 'https://example.test/doc', $3, $4)`, userID, document, minimumAge, source)
		return err
	}
	sixteen := 16
	for _, ok := range []struct {
		document, source string
		age              *int
	}{{"terms", "signup", nil}, {"privacy", "oidc", nil}, {"age", "approval", &sixteen}, {"terms", "reaccept", nil}} {
		if err := insert(ok.document, ok.source, ok.age); err != nil {
			t.Fatalf("insert %s/%s: %v", ok.document, ok.source, err)
		}
	}
	for name, bad := range map[string]struct {
		document, source string
		age              *int
	}{
		"unknown document":       {"cookies", "signup", nil},
		"unknown source":         {"terms", "import", nil},
		"age without a minimum":  {"age", "signup", nil},
		"terms with a minimum":   {"terms", "signup", &sixteen},
		"age with a zero":        {"age", "signup", new(int)},
		"version over the bound": {"terms", "signup", nil},
	} {
		var err error
		if name == "version over the bound" {
			_, err = st.Pool.Exec(ctx, `INSERT INTO user_consents (user_id, document, version, source) VALUES ($1, 'terms', $2, 'signup')`,
				userID, strings.Repeat("v", 65))
		} else {
			err = insert(bad.document, bad.source, bad.age)
		}
		if err == nil {
			t.Errorf("%s: the insert was accepted", name)
		}
	}

	if _, err := st.Pool.Exec(ctx, `UPDATE user_consents SET version = 'rewritten' WHERE user_id = $1`, userID); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Fatalf("an UPDATE of a consent row was not refused: %v", err)
	}
	var kept int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM user_consents WHERE user_id = $1 AND version = '2026-11-12'`, userID).Scan(&kept); err != nil || kept != 4 {
		t.Fatalf("rows after the refused update = %d, %v; want the 4 untouched", kept, err)
	}

	if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete the account: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM user_consents WHERE user_id = $1`, userID).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("consent rows after the account was deleted = %d, %v; want 0", kept, err)
	}
}

// A request held in the approval queue keeps what the applicant accepted and
// when; every column is optional so older requests stay valid.
func TestRegistrationRequestsCarryTheApplicantsConsent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	name := "consent" + uuid.NewString()[:8]
	defer func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM registration_requests WHERE username LIKE $1`, name+"%")
	}()
	if _, err := st.Pool.Exec(ctx, `INSERT INTO registration_requests (username, email, password_hash) VALUES ($1, $2, 'x')`,
		name+"a", name+"a@example.test"); err != nil {
		t.Fatalf("a request with no consent columns: %v", err)
	}
	ticked := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO registration_requests
		(username, email, password_hash, consent_accepted_at, consent_terms_url, consent_terms_version,
		 consent_privacy_url, consent_privacy_version, consent_minimum_age)
		VALUES ($1, $2, 'x', $3, 'https://example.test/terms', 'v1', 'https://example.test/privacy', 'v2', 16)`,
		name+"b", name+"b@example.test", ticked); err != nil {
		t.Fatalf("a request carrying its consent: %v", err)
	}
	var got time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT consent_accepted_at FROM registration_requests WHERE username = $1`, name+"b").Scan(&got); err != nil || !got.Equal(ticked) {
		t.Fatalf("consent_accepted_at = %v, %v; want %v", got, err, ticked)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE registration_requests SET consent_minimum_age = 0 WHERE username = $1`, name+"b"); err == nil {
		t.Error("a zero minimum age was accepted")
	}
}
