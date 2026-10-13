// Package viewerprefs holds a viewer's content preferences: today the per-flag
// sensitive-content policy overrides (migration 0173), layered on the user's
// own sensitive_content_policy. A user who never saved has no row and no
// overrides. HTTP-agnostic.
package viewerprefs

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// Querier is the slice of the sqlc queries the service needs.
type Querier interface {
	GetUserContentPrefs(ctx context.Context, userID uuid.UUID) (sqlcgen.UserContentPref, error)
	UpsertUserContentPrefs(ctx context.Context, arg sqlcgen.UpsertUserContentPrefsParams) (sqlcgen.UserContentPref, error)
}

// ValidationError is a rejected value; the HTTP layer maps it to 400.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// Prefs is a viewer's saved preferences.
type Prefs struct {
	// SensitiveFlagPolicies maps a flag (violent|shocking|sex) to
	// hide|warn|blur|display. An absent flag inherits the viewer's base policy.
	SensitiveFlagPolicies map[string]string
	// PreferredLanguages are the video language ids the viewer wants in their
	// feeds (empty = no filter). The client sends them as the feed's ?language.
	PreferredLanguages []string
}

// Patch is a partial update: nil leaves a field as stored.
type Patch struct {
	// SensitiveFlagPolicies REPLACES the overrides when non-nil.
	SensitiveFlagPolicies map[string]string
	// PreferredLanguages REPLACES the language list when non-nil ([] clears).
	PreferredLanguages *[]string
}

// MaxPreferredLanguages bounds the list; the feed filter is an ANY() over it.
const MaxPreferredLanguages = 10

// Service reads and writes viewer preferences.
type Service struct{ q Querier }

// NewService builds a Service.
func NewService(q Querier) *Service { return &Service{q: q} }

func decode(row sqlcgen.UserContentPref) Prefs {
	m := map[string]string{}
	_ = json.Unmarshal(row.SensitiveFlagPolicies, &m)
	langs := row.PreferredLanguages
	if langs == nil {
		langs = []string{}
	}
	return Prefs{SensitiveFlagPolicies: m, PreferredLanguages: langs}
}

// Get returns the viewer's preferences (empty for a user who never saved).
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Prefs, error) {
	row, err := s.q.GetUserContentPrefs(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Prefs{SensitiveFlagPolicies: map[string]string{}, PreferredLanguages: []string{}}, nil
	}
	if err != nil {
		return Prefs{}, err
	}
	return decode(row), nil
}

// Update applies a Patch (read-modify-write). In the flag map an empty value
// means inherit and is not stored; an unknown flag, policy or language is a
// ValidationError and nothing is written.
func (s *Service) Update(ctx context.Context, userID uuid.UUID, in Patch) (Prefs, error) {
	cur, err := s.Get(ctx, userID)
	if err != nil {
		return Prefs{}, err
	}
	if in.SensitiveFlagPolicies != nil {
		clean := map[string]string{}
		for flag, policy := range in.SensitiveFlagPolicies {
			if !video.IsSensitiveFlag(flag) {
				return Prefs{}, &ValidationError{Message: "unknown sensitive flag " + flag}
			}
			if policy == "" {
				continue
			}
			if !video.IsSensitivePolicy(policy) {
				return Prefs{}, &ValidationError{Message: "policy must be one of hide, warn, blur, display, or empty to inherit"}
			}
			clean[flag] = policy
		}
		cur.SensitiveFlagPolicies = clean
	}
	if in.PreferredLanguages != nil {
		seen := map[string]bool{}
		langs := []string{}
		for _, l := range *in.PreferredLanguages {
			if !video.IsLanguage(l) {
				return Prefs{}, &ValidationError{Message: "unknown language " + l}
			}
			if !seen[l] {
				seen[l] = true
				langs = append(langs, l)
			}
		}
		if len(langs) > MaxPreferredLanguages {
			return Prefs{}, &ValidationError{Message: "at most 10 preferred languages"}
		}
		cur.PreferredLanguages = langs
	}
	raw, err := json.Marshal(cur.SensitiveFlagPolicies)
	if err != nil {
		return Prefs{}, err
	}
	row, err := s.q.UpsertUserContentPrefs(ctx, sqlcgen.UpsertUserContentPrefsParams{
		UserID: userID, SensitiveFlagPolicies: raw, PreferredLanguages: cur.PreferredLanguages,
	})
	if err != nil {
		return Prefs{}, err
	}
	return decode(row), nil
}
