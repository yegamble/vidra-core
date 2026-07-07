package video

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Embed-privacy tiers (CORE-17 / W1.C2). Enforcement lives at the embed page in
// vidra-user (referrer / ancestor-origin check); the server stores and serves the
// policy only. Server-side Referer enforcement is a non-goal.
const (
	EmbedEnabled   = "enabled"
	EmbedDisabled  = "disabled"
	EmbedWhitelist = "whitelist"
)

// MaxEmbedAllowedDomains caps the whitelist size.
const MaxEmbedAllowedDomains = 50

// EmbedPrivacy is a video's embed policy: the status tier and, for the
// "whitelist" tier, the allow-listed hostnames (empty for the other tiers).
type EmbedPrivacy struct {
	Status         string
	AllowedDomains []string
}

// GetEmbedPrivacy returns a video's embed-privacy policy. It performs no
// visibility check — the HTTP layer gates the read. An unknown id → ErrNotFound.
func (s *Service) GetEmbedPrivacy(ctx context.Context, videoID uuid.UUID) (EmbedPrivacy, error) {
	row, err := s.repo.GetVideoEmbedPrivacy(ctx, videoID)
	if err != nil {
		return EmbedPrivacy{}, ErrNotFound
	}
	domains := row.EmbedAllowedDomains
	if domains == nil {
		domains = []string{}
	}
	return EmbedPrivacy{Status: row.EmbedPrivacy, AllowedDomains: domains}, nil
}

// SetEmbedPrivacy replaces a video's embed-privacy policy. Owner-only (non-owner
// or unknown id → ErrForbidden/ErrNotFound). The input is validated in full
// (*EmbedValidationError → 400) before any write; for a non-whitelist status the
// stored domain list is always empty. Returns the stored policy (GET shape).
func (s *Service) SetEmbedPrivacy(ctx context.Context, ownerID, videoID uuid.UUID, in EmbedPrivacy) (EmbedPrivacy, error) {
	if _, err := s.ownedVideo(ctx, ownerID, videoID); err != nil {
		return EmbedPrivacy{}, err
	}
	normalized, err := validateEmbedPrivacy(in)
	if err != nil {
		return EmbedPrivacy{}, err
	}
	if err := s.repo.SetVideoEmbedPrivacy(ctx, sqlcgen.SetVideoEmbedPrivacyParams{
		ID:                  videoID,
		EmbedPrivacy:        normalized.Status,
		EmbedAllowedDomains: normalized.AllowedDomains,
	}); err != nil {
		return EmbedPrivacy{}, err
	}
	return normalized, nil
}

// validateEmbedPrivacy enforces the CORE-17 embed contract and returns the
// normalized policy (lowercased, de-duplicated domains; empty list off-whitelist).
func validateEmbedPrivacy(in EmbedPrivacy) (EmbedPrivacy, error) {
	switch in.Status {
	case EmbedEnabled, EmbedDisabled:
		if len(in.AllowedDomains) > 0 {
			return EmbedPrivacy{}, &EmbedValidationError{Message: `allowed_domains is only valid with status "whitelist"`}
		}
		return EmbedPrivacy{Status: in.Status, AllowedDomains: []string{}}, nil
	case EmbedWhitelist:
		domains, err := normalizeEmbedDomains(in.AllowedDomains)
		if err != nil {
			return EmbedPrivacy{}, err
		}
		return EmbedPrivacy{Status: EmbedWhitelist, AllowedDomains: domains}, nil
	default:
		return EmbedPrivacy{}, &EmbedValidationError{Message: "status must be one of enabled, disabled, whitelist"}
	}
}

// normalizeEmbedDomains lowercases/trims, drops empties and duplicates (first-seen
// order), and requires 1..MaxEmbedAllowedDomains valid bare hostnames.
func normalizeEmbedDomains(raw []string) ([]string, error) {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, d := range raw {
		host := strings.ToLower(strings.TrimSpace(d))
		if host == "" {
			continue
		}
		if !validHostname(host) {
			return nil, &EmbedValidationError{Message: fmt.Sprintf("invalid domain %q: hostnames only (no scheme, port, or path)", d)}
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	if len(out) == 0 {
		return nil, &EmbedValidationError{Message: `status "whitelist" requires at least one domain`}
	}
	if len(out) > MaxEmbedAllowedDomains {
		return nil, &EmbedValidationError{Message: fmt.Sprintf("at most %d domains are allowed", MaxEmbedAllowedDomains)}
	}
	return out, nil
}

// validHostname accepts a bare DNS hostname: dot-separated labels of [a-z0-9-]
// (not starting/ending with '-'), with no scheme, port, path, query, or spaces.
func validHostname(h string) bool {
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	if strings.ContainsAny(h, "/:@ \t\r\n?#") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}
