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

func (f *fakeQ) UpsertUserContentPrefs(_ context.Context, a sqlcgen.UpsertUserContentPrefsParams) (sqlcgen.UserContentPref, error) {
	// Mirrors the SQL: a NULL argument keeps the stored value (empty on first save).
	r, ok := f.rows[a.UserID]
	if !ok {
		r = sqlcgen.UserContentPref{UserID: a.UserID, SensitiveFlagPolicies: []byte("{}"), PreferredLanguages: []string{}}
	}
	if a.SensitiveFlagPolicies != nil {
		r.SensitiveFlagPolicies = a.SensitiveFlagPolicies
	}
	if a.PreferredLanguages != nil {
		r.PreferredLanguages = a.PreferredLanguages
	}
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
	if _, err := s.Update(context.Background(), id, Patch{SensitiveFlagPolicies: map[string]string{"violent": "blur", "sex": ""}}); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Get(context.Background(), id)
	if len(p.SensitiveFlagPolicies) != 1 || p.SensitiveFlagPolicies["violent"] != "blur" {
		t.Errorf("got %v, want only violent=blur", p.SensitiveFlagPolicies)
	}
	// Replace, not merge.
	if _, err := s.Update(context.Background(), id, Patch{SensitiveFlagPolicies: map[string]string{"sex": "hide"}}); err != nil {
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
		_, err := s.Update(context.Background(), uuid.New(), Patch{SensitiveFlagPolicies: in})
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want ValidationError", name, err)
		}
	}
}

func TestPreferredLanguages(t *testing.T) {
	s, id := NewService(&fakeQ{rows: map[uuid.UUID]sqlcgen.UserContentPref{}}), uuid.New()
	set := func(l ...string) (Prefs, error) {
		return s.Update(context.Background(), id, Patch{PreferredLanguages: &l})
	}
	p, err := set("fr", "en", "fr")
	if err != nil || len(p.PreferredLanguages) != 2 || p.PreferredLanguages[0] != "fr" || p.PreferredLanguages[1] != "en" {
		t.Fatalf("got %v, %v; want [fr en] deduped in order", p.PreferredLanguages, err)
	}
	// A patch that omits languages keeps them and the flag overrides.
	p, err = s.Update(context.Background(), id, Patch{SensitiveFlagPolicies: map[string]string{"sex": "hide"}})
	if err != nil || len(p.PreferredLanguages) != 2 || p.SensitiveFlagPolicies["sex"] != "hide" {
		t.Errorf("omitted field not kept: %v, %v", p, err)
	}
	if p, _ = set(); len(p.PreferredLanguages) != 0 {
		t.Errorf("[] must clear, got %v", p.PreferredLanguages)
	}
	var ve *ValidationError
	if _, err = set("xx-not-a-language"); !errors.As(err, &ve) {
		t.Errorf("unknown language: err = %v, want ValidationError", err)
	}
	if _, err = set("en", "fr", "de", "es", "it", "pt", "ru", "ja", "zh", "ar", "ko"); !errors.As(err, &ve) {
		t.Errorf("11 languages: err = %v, want ValidationError", err)
	}
}
