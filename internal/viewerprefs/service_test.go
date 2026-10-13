package viewerprefs

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeQ struct {
	rows map[uuid.UUID]sqlcgen.UserContentPref
}

func (f *fakeQ) GetUserContentPrefs(_ context.Context, id uuid.UUID) (sqlcgen.UserContentPref, error) {
	r, ok := f.rows[id]
	if !ok {
		return sqlcgen.UserContentPref{}, pgx.ErrNoRows
	}
	return r, nil
}

func (f *fakeQ) UpsertUserSensitiveFlagPolicies(_ context.Context, a sqlcgen.UpsertUserSensitiveFlagPoliciesParams) (sqlcgen.UserContentPref, error) {
	r := sqlcgen.UserContentPref{UserID: a.UserID, SensitiveFlagPolicies: a.SensitiveFlagPolicies}
	f.rows[a.UserID] = r
	return r, nil
}

func TestSetAndGetFlagPolicies(t *testing.T) {
	s, id := NewService(&fakeQ{rows: map[uuid.UUID]sqlcgen.UserContentPref{}}), uuid.New()
	p, err := s.Get(context.Background(), id)
	if err != nil || len(p.SensitiveFlagPolicies) != 0 {
		t.Fatalf("fresh user: %v %v, want empty", p, err)
	}
	// Empty value = inherit: not stored.
	if _, err := s.SetSensitiveFlagPolicies(context.Background(), id, map[string]string{"violent": "blur", "sex": ""}); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get(context.Background(), id)
	if len(p.SensitiveFlagPolicies) != 1 || p.SensitiveFlagPolicies["violent"] != "blur" {
		t.Errorf("got %v, want only violent=blur", p.SensitiveFlagPolicies)
	}
	// Replace, not merge.
	if _, err := s.SetSensitiveFlagPolicies(context.Background(), id, map[string]string{"sex": "hide"}); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get(context.Background(), id)
	if _, kept := p.SensitiveFlagPolicies["violent"]; kept || p.SensitiveFlagPolicies["sex"] != "hide" {
		t.Errorf("got %v, want a replacement holding only sex=hide", p.SensitiveFlagPolicies)
	}
}

func TestSetRejectsBadInput(t *testing.T) {
	s := NewService(&fakeQ{rows: map[uuid.UUID]sqlcgen.UserContentPref{}})
	for name, in := range map[string]map[string]string{
		"unknown flag":   {"gore": "hide"},
		"unknown policy": {"sex": "nuke"},
	} {
		_, err := s.SetSensitiveFlagPolicies(context.Background(), uuid.New(), in)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want ValidationError", name, err)
		}
	}
}
