package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Usage mistakes are refused before the config or database is touched.
func TestOwnerRecoveryUsageErrors(t *testing.T) {
	open := func(context.Context) (ownerRecoveryDeps, func(), error) {
		t.Fatal("a usage mistake reached config/database")
		return ownerRecoveryDeps{}, nil, nil
	}
	for _, tc := range []struct {
		args   []string
		errHas string
	}{
		{[]string{"--fix"}, "flag provided but not defined"},
		{[]string{"mona"}, "takes no positional arguments"},
	} {
		var out, errBuf bytes.Buffer
		if code := runOwnerRecovery(tc.args, &out, &errBuf, open); code != 1 {
			t.Fatalf("%v: exit = %d, want 1", tc.args, code)
		}
		if !strings.Contains(errBuf.String(), tc.errHas) || out.Len() != 0 {
			t.Errorf("%v: stderr = %q, stdout = %q", tc.args, errBuf.String(), out.String())
		}
	}
}

func recoveryDeps(rec auth.OwnerRecovery, issueErr, recordErr error, seen *[]audit.Event, gotMFA *bool) ownerRecoveryDeps {
	return ownerRecoveryDeps{
		baseURL: "https://vidra.example/",
		issue: func(_ context.Context, removeMFA bool) (auth.OwnerRecovery, error) {
			if gotMFA != nil {
				*gotMFA = removeMFA
			}
			return rec, issueErr
		},
		record: func(_ context.Context, ev audit.Event) error {
			*seen = append(*seen, ev)
			return recordErr
		},
	}
}

// stdout is ONE line, the link, token query-escaped; every human sentence is on
// stderr. The host verb parses stdout, so this split is the contract.
func TestOwnerRecoveryPrintsOnlyTheLinkOnStdout(t *testing.T) {
	var seen []audit.Event
	var gotMFA bool
	rec := auth.OwnerRecovery{Token: "a+b/c=d", OwnerID: uuid.New(), ExpiresAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), MFARemoved: true}
	var out, errBuf bytes.Buffer
	if code := execOwnerRecovery(context.Background(), true, recoveryDeps(rec, nil, nil, &seen, &gotMFA), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errBuf.String())
	}
	if !gotMFA {
		t.Error("--remove-mfa did not reach the service")
	}
	if want := "https://vidra.example/reset-password/confirm?token=" + url.QueryEscape("a+b/c=d") + "\n"; out.String() != want {
		t.Errorf("stdout = %q, want exactly %q", out.String(), want)
	}
	for _, s := range []string{"2026-10-01T12:00:00Z", "Single-use", "REMOVED"} {
		if !strings.Contains(errBuf.String(), s) {
			t.Errorf("stderr = %q, want %q", errBuf.String(), s)
		}
	}
	if strings.Contains(errBuf.String(), "a+b/c=d") {
		t.Error("the raw token leaked to stderr")
	}
	if len(seen) != 1 || seen[0].Action != "auth.owner_recovery" || seen[0].ResourceID != rec.OwnerID.String() ||
		seen[0].Actor.Kind != "system" || seen[0].Reason != "host operator (CLI)" || len(seen[0].Changes) != 1 {
		t.Fatalf("audit events = %+v", seen)
	}
}

func TestOwnerRecoveryFailuresAreOneLineAndEmptyStdout(t *testing.T) {
	empty := ""
	for _, tc := range []struct {
		name               string
		issueErr, auditErr error
		baseURL            *string
		want               string
	}{
		{name: "no owner", issueErr: auth.ErrNoInstanceOwner, want: "vidra claim"},
		{name: "other error", issueErr: errors.New("db down"), want: "db down"},
		{name: "audit write fails", auditErr: errors.New("insert failed"), want: "link is withheld"},
		{name: "no public base url", baseURL: &empty, want: "PUBLIC_BASE_URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []audit.Event
			deps := recoveryDeps(auth.OwnerRecovery{Token: "tok", OwnerID: uuid.New()}, tc.issueErr, tc.auditErr, &seen, nil)
			if tc.baseURL != nil {
				deps.baseURL = *tc.baseURL
			}
			var out, errBuf bytes.Buffer
			if code := execOwnerRecovery(context.Background(), false, deps, &out, &errBuf); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if out.Len() != 0 || !strings.Contains(errBuf.String(), tc.want) || strings.Count(strings.TrimSpace(errBuf.String()), "\n") != 0 {
				t.Errorf("stdout = %q, stderr = %q; want empty stdout and one line mentioning %q", out.String(), errBuf.String(), tc.want)
			}
		})
	}
}

// auditSink stands in for audit_log; the embedded interface panics on any
// method this test must not reach.
type auditSink struct {
	audit.Repository
	rows []sqlcgen.InsertAuditLogParams
}

func (a *auditSink) InsertAuditLog(_ context.Context, p sqlcgen.InsertAuditLogParams) error {
	a.rows = append(a.rows, p)
	return nil
}

// The event must survive the REAL envelope validation (allowlisted metadata key
// and change field, system actor with no id) — otherwise a typo here would
// withhold every link in production — and must not carry the token.
func TestOwnerRecoveryEventPassesAuditEnvelope(t *testing.T) {
	sink := &auditSink{}
	deps := recoveryDeps(auth.OwnerRecovery{Token: "tok", OwnerID: uuid.New(), MFARemoved: true}, nil, nil, new([]audit.Event), nil)
	deps.record = audit.NewService(sink).Record
	var out, errBuf bytes.Buffer
	if code := execOwnerRecovery(context.Background(), true, deps, &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errBuf.String())
	}
	if len(sink.rows) != 1 || sink.rows[0].Action != "auth.owner_recovery" || sink.rows[0].ActorKind != "system" {
		t.Fatalf("rows = %+v", sink.rows)
	}
	if strings.Contains(string(sink.rows[0].Metadata)+string(sink.rows[0].Changes)+sink.rows[0].Reason, "tok") {
		t.Error("token reached the audit row")
	}
}
