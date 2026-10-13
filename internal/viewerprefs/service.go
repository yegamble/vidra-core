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
	UpsertUserSensitiveFlagPolicies(ctx context.Context, arg sqlcgen.UpsertUserSensitiveFlagPoliciesParams) (sqlcgen.UserContentPref, error)
}

// ValidationError is a rejected value; the HTTP layer maps it to 400.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// Prefs is a viewer's saved preferences.
type Prefs struct {
	// SensitiveFlagPolicies maps a flag (violent|shocking|sex) to
	// hide|warn|blur|display. An absent flag inherits the viewer's base policy.
	SensitiveFlagPolicies map[string]string
}

// Service reads and writes viewer preferences.
type Service struct{ q Querier }

// NewService builds a Service.
func NewService(q Querier) *Service { return &Service{q: q} }

func decode(row sqlcgen.UserContentPref) Prefs {
	m := map[string]string{}
	_ = json.Unmarshal(row.SensitiveFlagPolicies, &m)
	return Prefs{SensitiveFlagPolicies: m}
}

// Get returns the viewer's preferences (empty for a user who never saved).
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Prefs, error) {
	row, err := s.q.GetUserContentPrefs(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Prefs{SensitiveFlagPolicies: map[string]string{}}, nil
	}
	if err != nil {
		return Prefs{}, err
	}
	return decode(row), nil
}

// SetSensitiveFlagPolicies REPLACES the viewer's overrides. An empty value
// for a flag means inherit and is not stored; an unknown flag or policy is a
// ValidationError.
func (s *Service) SetSensitiveFlagPolicies(ctx context.Context, userID uuid.UUID, in map[string]string) (Prefs, error) {
	clean := map[string]string{}
	for flag, policy := range in {
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
	raw, err := json.Marshal(clean)
	if err != nil {
		return Prefs{}, err
	}
	row, err := s.q.UpsertUserSensitiveFlagPolicies(ctx, sqlcgen.UpsertUserSensitiveFlagPoliciesParams{UserID: userID, SensitiveFlagPolicies: raw})
	if err != nil {
		return Prefs{}, err
	}
	return decode(row), nil
}
