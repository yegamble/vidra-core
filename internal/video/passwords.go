package video

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// PrivacyPassword is the CORE-17 privacy tier: the video is reachable by direct
// link only after the viewer unlocks it with a password (POST /videos/{id}/unlock).
// It is excluded from public discovery exactly like "unlisted".
const PrivacyPassword = "password"

// Video-password limits (CORE-17 / W1.C2 contract). A plaintext is 6–100
// characters; a replace-all set is 1–20 entries.
const (
	MinVideoPasswordLen = 6
	MaxVideoPasswordLen = 100
	MaxVideoPasswords   = 20
)

// VideoPassword is one password's PUBLIC projection: its id and creation time.
// The plaintext and the bcrypt hash are write-only and NEVER leave the service —
// no response, log, or error ever carries them.
type VideoPassword struct {
	ID        uuid.UUID
	CreatedAt time.Time
}

// ownedVideo fetches a video and asserts caller ownership. Unknown id →
// ErrNotFound; not the owner → ErrForbidden (the HTTP layer renders both as 404
// so a video's existence is not leaked).
func (s *Service) ownedVideo(ctx context.Context, ownerID, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	}
	if v.OwnerID != ownerID {
		return sqlcgen.GetVideoByIDRow{}, ErrForbidden
	}
	return v, nil
}

// HasPassword reports whether a video has at least one password (used to enforce
// the "privacy=password needs >=1 password" rule on the update path).
func (s *Service) HasPassword(ctx context.Context, videoID uuid.UUID) (bool, error) {
	n, err := s.repo.CountVideoPasswords(ctx, videoID)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListPasswords returns a video's passwords (id + created_at only) for the owner
// listing. Owner-only.
func (s *Service) ListPasswords(ctx context.Context, ownerID, videoID uuid.UUID) ([]VideoPassword, error) {
	if _, err := s.ownedVideo(ctx, ownerID, videoID); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListVideoPasswords(ctx, videoID)
	if err != nil {
		return nil, err
	}
	out := make([]VideoPassword, 0, len(rows))
	for _, r := range rows {
		out = append(out, VideoPassword{ID: r.ID, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// AddPassword stores one new bcrypt-hashed password for a video. Owner-only; the
// plaintext must be 6–100 characters (*PasswordValidationError → 400).
func (s *Service) AddPassword(ctx context.Context, ownerID, videoID uuid.UUID, plain string) (VideoPassword, error) {
	if _, err := s.ownedVideo(ctx, ownerID, videoID); err != nil {
		return VideoPassword{}, err
	}
	if err := validateVideoPasswordLen(plain, -1); err != nil {
		return VideoPassword{}, err
	}
	hash, err := auth.HashPassword(plain)
	if err != nil {
		return VideoPassword{}, err
	}
	row, err := s.repo.InsertVideoPassword(ctx, sqlcgen.InsertVideoPasswordParams{VideoID: videoID, PasswordHash: hash})
	if err != nil {
		return VideoPassword{}, err
	}
	return VideoPassword{ID: row.ID, CreatedAt: row.CreatedAt}, nil
}

// ReplacePasswords replaces a video's whole password set. Owner-only. The set
// must be 1–20 entries, each 6–100 characters. Validation and hashing run in full
// before any write, so a rejected request leaves the stored passwords untouched.
// Returns the stored set (id + created_at) in the GET shape.
func (s *Service) ReplacePasswords(ctx context.Context, ownerID, videoID uuid.UUID, plains []string) ([]VideoPassword, error) {
	if _, err := s.ownedVideo(ctx, ownerID, videoID); err != nil {
		return nil, err
	}
	if len(plains) < 1 || len(plains) > MaxVideoPasswords {
		return nil, &PasswordValidationError{Message: fmt.Sprintf("provide between 1 and %d passwords", MaxVideoPasswords)}
	}
	hashes := make([]string, 0, len(plains))
	for i, p := range plains {
		if err := validateVideoPasswordLen(p, i); err != nil {
			return nil, err
		}
		h, herr := auth.HashPassword(p)
		if herr != nil {
			return nil, herr
		}
		hashes = append(hashes, h)
	}
	if err := s.repo.DeleteVideoPasswords(ctx, videoID); err != nil {
		return nil, err
	}
	if err := s.repo.InsertVideoPasswords(ctx, sqlcgen.InsertVideoPasswordsParams{VideoID: videoID, PasswordHash: hashes}); err != nil {
		return nil, err
	}
	return s.ListPasswords(ctx, ownerID, videoID)
}

// DeletePassword removes one password from a video. Owner-only; an unknown
// passwordId → ErrPasswordNotFound (404); removing the LAST password of a
// privacy=password video → ErrLastPassword (409, so the video never ends up
// password-protected yet unlockable by no one).
func (s *Service) DeletePassword(ctx context.Context, ownerID, videoID, passwordID uuid.UUID) error {
	v, err := s.ownedVideo(ctx, ownerID, videoID)
	if err != nil {
		return err
	}
	exists, err := s.repo.VideoPasswordExists(ctx, sqlcgen.VideoPasswordExistsParams{ID: passwordID, VideoID: videoID})
	if err != nil {
		return err
	}
	if !exists {
		return ErrPasswordNotFound
	}
	if v.Privacy == PrivacyPassword {
		n, cErr := s.repo.CountVideoPasswords(ctx, videoID)
		if cErr != nil {
			return cErr
		}
		if n <= 1 {
			return ErrLastPassword
		}
	}
	if _, err := s.repo.DeleteVideoPassword(ctx, sqlcgen.DeleteVideoPasswordParams{ID: passwordID, VideoID: videoID}); err != nil {
		return err
	}
	return nil
}

// VerifyPassword reports whether plain matches ANY of a video's stored passwords.
// It backs the unlock endpoint and is NOT owner-gated (a viewer unlocking has no
// account relationship to the video). It checks against every stored hash without
// short-circuiting, so the response time does not leak which/how-many passwords
// matched. A video with no passwords verifies nothing (false).
func (s *Service) VerifyPassword(ctx context.Context, videoID uuid.UUID, plain string) (bool, error) {
	hashes, err := s.repo.ListVideoPasswordHashes(ctx, videoID)
	if err != nil {
		return false, err
	}
	matched := false
	for _, h := range hashes {
		if auth.CheckPassword(h, plain) == nil {
			matched = true
		}
	}
	return matched, nil
}

// validateVideoPasswordLen enforces the 6–100 character rule. index >= 0 prefixes
// the message with the entry position (for the replace-all path); index < 0 omits
// it (the single-add path).
func validateVideoPasswordLen(plain string, index int) error {
	if n := len([]rune(plain)); n < MinVideoPasswordLen || n > MaxVideoPasswordLen {
		if index >= 0 {
			return &PasswordValidationError{Message: fmt.Sprintf("password %d must be %d to %d characters", index, MinVideoPasswordLen, MaxVideoPasswordLen)}
		}
		return &PasswordValidationError{Message: fmt.Sprintf("password must be %d to %d characters", MinVideoPasswordLen, MaxVideoPasswordLen)}
	}
	return nil
}
