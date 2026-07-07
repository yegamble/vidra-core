// Package playersettings implements per-user player defaults (PLAY-07, backport
// wave W1.C3). A signed-in user has one set of playback preferences — autoplay
// of the next video, playback speed, default quality, and whether captions /
// theater mode start on — that the custom player in vidra-user reads on load and
// persists here (replacing the interim localStorage store). It is HTTP-agnostic
// and testable without a server.
//
// A user who never saved has no stored row: Get returns the built-in Defaults,
// and the first Update upserts the row. Update is a MERGE (a partial patch keeps
// the stored value for any omitted field), done via read-modify-write so the
// upsert always writes the full effective object.
//
// Scope is PER-USER ONLY — the archived openapi_player_settings.yaml scoped
// settings per-video/per-channel; PLAY-07 is per-user defaults, so
// per-video/per-channel overrides are deliberately not ported.
package playersettings

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// PlaybackRates is the shared speed ladder: the exact set a saved default_speed
// must belong to. It is kept in lockstep with the frontend's PLAYBACK_RATES
// (vidra-user). Values are all multiples of 0.25, so they store exactly.
var PlaybackRates = []float64{0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3, 3.5, 4}

// qualityPattern matches a concrete rendition height like "720p" (2–4 digits +
// "p"), the same shape as the HLS rendition path segment. The sentinel "auto"
// (adaptive) is accepted separately.
var qualityPattern = regexp.MustCompile(`^[0-9]{2,4}p$`)

// Settings is a user's full, effective player configuration. Every field is
// always present (a fresh user gets Defaults), so the API response is complete.
type Settings struct {
	AutoplayNext    bool
	DefaultSpeed    float64
	DefaultQuality  string
	CaptionsDefault bool
	TheaterDefault  bool
}

// Defaults returns the built-in player settings for a user who never saved.
// These mirror the column DEFAULTs in migration 0075.
func Defaults() Settings {
	return Settings{
		AutoplayNext:    true,
		DefaultSpeed:    1,
		DefaultQuality:  "auto",
		CaptionsDefault: false,
		TheaterDefault:  false,
	}
}

// Patch is a partial player-settings update (merge-PUT). A nil field is absent
// from the request body and leaves the stored value unchanged; a non-nil field
// replaces it.
type Patch struct {
	AutoplayNext    *bool
	DefaultSpeed    *float64
	DefaultQuality  *string
	CaptionsDefault *bool
	TheaterDefault  *bool
}

// ValidationError describes a rejected player-settings value. The HTTP layer maps
// it to 400 bad_request (not the 422 field-error path) — matching the chapters
// contract for player-scoped validation.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// validate checks the supplied (non-nil) fields of a patch. Omitted fields are
// not validated. Booleans are always valid (JSON binding enforces the type), so
// only speed and quality have rules.
func (p Patch) validate() error {
	if p.DefaultSpeed != nil && !validSpeed(*p.DefaultSpeed) {
		return &ValidationError{Message: fmt.Sprintf("default_speed must be one of the supported playback rates %v", PlaybackRates)}
	}
	if p.DefaultQuality != nil && !validQuality(*p.DefaultQuality) {
		return &ValidationError{Message: `default_quality must be "auto" or a rendition height like "720p"`}
	}
	return nil
}

// validSpeed reports whether v is one of the shared PlaybackRates.
func validSpeed(v float64) bool {
	for _, r := range PlaybackRates {
		if v == r {
			return true
		}
	}
	return false
}

// validQuality reports whether q is "auto" or a "<height>p" rendition label.
func validQuality(q string) bool {
	return q == "auto" || qualityPattern.MatchString(q)
}

// apply returns a copy of s with the patch's non-nil fields overlaid.
func (s Settings) apply(p Patch) Settings {
	if p.AutoplayNext != nil {
		s.AutoplayNext = *p.AutoplayNext
	}
	if p.DefaultSpeed != nil {
		s.DefaultSpeed = *p.DefaultSpeed
	}
	if p.DefaultQuality != nil {
		s.DefaultQuality = *p.DefaultQuality
	}
	if p.CaptionsDefault != nil {
		s.CaptionsDefault = *p.CaptionsDefault
	}
	if p.TheaterDefault != nil {
		s.TheaterDefault = *p.TheaterDefault
	}
	return s
}

// Repository is the data access the player-settings service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	GetUserPlayerSettings(ctx context.Context, userID uuid.UUID) (sqlcgen.GetUserPlayerSettingsRow, error)
	UpsertUserPlayerSettings(ctx context.Context, arg sqlcgen.UpsertUserPlayerSettingsParams) error
}

// Service holds the player-settings application logic.
type Service struct {
	repo Repository
}

// NewService builds the player-settings service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Get returns the caller's effective player settings. A user who never saved
// gets Defaults (never an error, never a 404).
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Settings, error) {
	row, err := s.repo.GetUserPlayerSettings(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Defaults(), nil
		}
		return Settings{}, err
	}
	return Settings{
		AutoplayNext:    row.AutoplayNext,
		DefaultSpeed:    row.DefaultSpeed,
		DefaultQuality:  row.DefaultQuality,
		CaptionsDefault: row.CaptionsDefault,
		TheaterDefault:  row.TheaterDefault,
	}, nil
}

// Update applies a partial patch (merge-PUT) and returns the full effective
// settings. Any invalid supplied field returns a *ValidationError and nothing is
// written. Omitted fields keep their stored value (or the default, for a user who
// never saved).
func (s *Service) Update(ctx context.Context, userID uuid.UUID, patch Patch) (Settings, error) {
	if err := patch.validate(); err != nil {
		return Settings{}, err
	}
	current, err := s.Get(ctx, userID)
	if err != nil {
		return Settings{}, err
	}
	merged := current.apply(patch)
	if err := s.repo.UpsertUserPlayerSettings(ctx, sqlcgen.UpsertUserPlayerSettingsParams{
		UserID:          userID,
		AutoplayNext:    merged.AutoplayNext,
		DefaultSpeed:    merged.DefaultSpeed,
		DefaultQuality:  merged.DefaultQuality,
		CaptionsDefault: merged.CaptionsDefault,
		TheaterDefault:  merged.TheaterDefault,
	}); err != nil {
		return Settings{}, err
	}
	return merged, nil
}
