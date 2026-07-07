package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// anonymizeAttempts bounds the retry loop should a generated deleted-<suffix>
// username/email sentinel collide with an existing row (astronomically
// unlikely, but the unique indexes make it a hard error).
const anonymizeAttempts = 3

// Delete performs the irreversible §1 hard delete of an account
// (product-decisions.md §1). Password/permission checks are the caller's job
// (the HTTP layer re-confirms the password for self-service deletes and
// enforces admin + self-guard for the admin variant).
//
// Retention policy, in order:
//   - Owned channels and their videos are HARD-deleted. Each video is removed
//     through the video service so the registered delete hooks fire (the
//     federation Delete fan-out for previously-public videos); recorded media
//     blobs (originals, thumbnails, captions, the HLS ladder) are deleted from
//     storage best-effort.
//   - The account's comments become tombstones: body emptied + deleted_at
//     stamped; the rows — and so reply threads under them — are preserved and
//     views render "[deleted]".
//   - Per-user rows are hard-deleted: ratings, saved videos, watch history,
//     playlists, follows (local + remote), mutes (accounts + instances, both
//     directions), blocks (both directions), OAuth identities, MFA settings +
//     recovery codes, notifications + prefs, reset/verification tokens, the
//     ActivityPub account actor key, profile images, and any export archives.
//   - DM messages remain (the counterpart's copy of a conversation is their
//     data); the sender resolves to the anonymised placeholder.
//   - All sessions are revoked.
//   - Finally the users row is ANONYMISED, not removed: username →
//     deleted-<8char> (unique), email → a unique sentinel, password hash
//     cleared, profile wiped, is_active=false, deleted_at stamped.
//
// An unknown or already-deleted account is ErrNotFound.
func (s *Service) Delete(ctx context.Context, userID uuid.UUID) error {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return ErrNotFound
	}
	if user.DeletedAt.Valid {
		return ErrNotFound
	}

	// 1. Owned channels + videos (existing DB cascades clean the rows hanging
	// off each video; the explicit per-video delete fires the federation hook).
	channels, err := s.repo.ListChannelsByOwner(ctx, userID)
	if err != nil {
		return fmt.Errorf("account: list channels: %w", err)
	}
	for _, ch := range channels {
		videos, err := s.repo.ListVideosByChannel(ctx, ch.ID)
		if err != nil {
			return fmt.Errorf("account: list channel videos: %w", err)
		}
		for _, v := range videos {
			// Gather the video's recorded blob keys BEFORE the rows cascade away.
			keys := s.videoBlobKeys(ctx, v.ID)
			if s.videos != nil {
				if err := s.videos.Delete(ctx, userID, v.ID); err != nil {
					return fmt.Errorf("account: delete video %s: %w", v.ID, err)
				}
			}
			// Best-effort media cleanup: a missing/failing blob never aborts the
			// account deletion (the rows are already gone).
			for _, key := range keys {
				s.blobDelete(ctx, key)
			}
			s.blobDeletePrefix(ctx, media.HLSKeyPrefix(v.ID))
		}
		// Channel avatar/banner blobs (their rows cascade with the channel).
		for _, kind := range []string{"avatar", "banner"} {
			if img, err := s.repo.GetChannelImage(ctx, sqlcgen.GetChannelImageParams{ChannelID: ch.ID, Kind: kind}); err == nil {
				s.blobDelete(ctx, img.StorageKey)
			}
		}
		if err := s.repo.DeleteChannel(ctx, ch.ID); err != nil {
			return fmt.Errorf("account: delete channel %s: %w", ch.ID, err)
		}
	}

	// 2. Profile images (account avatar/banner): blob + row.
	for _, kind := range []string{"avatar", "banner"} {
		if img, err := s.repo.GetUserImage(ctx, sqlcgen.GetUserImageParams{UserID: userID, Kind: kind}); err == nil {
			s.blobDelete(ctx, img.StorageKey)
		}
		if _, err := s.repo.DeleteUserImage(ctx, sqlcgen.DeleteUserImageParams{UserID: userID, Kind: kind}); err != nil {
			return fmt.Errorf("account: delete %s: %w", kind, err)
		}
	}

	// 3. Comment tombstones (threads preserved; views render "[deleted]").
	if err := s.repo.TombstoneUserComments(ctx, pgtype.UUID{Bytes: userID, Valid: true}); err != nil {
		return fmt.Errorf("account: tombstone comments: %w", err)
	}

	// 4. Per-user hard deletes. The users row is anonymised rather than
	// removed, so its ON DELETE CASCADE FKs never fire — purge explicitly.
	purges := []struct {
		name string
		fn   func(context.Context, uuid.UUID) error
	}{
		{"ratings", s.repo.DeleteVideoRatingsByUser},
		{"saved videos", s.repo.DeleteSavedVideosByUser},
		{"watch history", s.repo.ClearWatchHistory},
		{"playlists", s.repo.DeletePlaylistsByOwner},
		{"follows", s.repo.DeleteChannelFollowsByFollower},
		{"remote follows", s.repo.DeleteRemoteChannelFollowsByUser},
		{"account mutes", s.repo.DeleteMutedAccountsByUser},
		{"instance mutes", s.repo.DeleteMutedInstancesByUser},
		{"blocks", s.repo.DeleteUserBlocksByUser},
		{"oauth identities", s.repo.DeleteOAuthIdentitiesByUser},
		{"recovery codes", s.repo.DeleteRecoveryCodes},
		{"notifications", s.repo.DeleteNotificationsByUser},
		{"notification prefs", s.repo.DeleteNotificationPrefsByUser},
		{"password reset tokens", s.repo.DeletePasswordResetTokensByUser},
		{"email verification tokens", s.repo.DeleteEmailVerificationTokensByUser},
		{"actor key", s.repo.DeleteAccountActorKey},
	}
	for _, p := range purges {
		if err := p.fn(ctx, userID); err != nil {
			return fmt.Errorf("account: delete %s: %w", p.name, err)
		}
	}
	if _, err := s.repo.DeleteUserMFA(ctx, userID); err != nil {
		return fmt.Errorf("account: delete mfa: %w", err)
	}
	// Export rows + their archive blobs (the archive contains account data —
	// it must not outlive the account).
	exportKeys, err := s.repo.DeleteAccountExportsByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("account: delete exports: %w", err)
	}
	for _, key := range exportKeys {
		if key != "" {
			s.blobDelete(ctx, key)
		}
	}

	// 5. Sign out everywhere.
	if err := s.repo.RevokeAllUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("account: revoke sessions: %w", err)
	}

	// 6. Anonymise the users row (kept for audit/actor integrity). Retried
	// with a fresh suffix on the (theoretical) unique-sentinel collision.
	var lastErr error
	for i := 0; i < anonymizeAttempts; i++ {
		suffix, err := randomSuffix()
		if err != nil {
			return err
		}
		rows, err := s.repo.AnonymizeDeletedUser(ctx, sqlcgen.AnonymizeDeletedUserParams{
			ID:       userID,
			Username: "deleted-" + suffix,
			Email:    "deleted-" + suffix + "@deleted.invalid",
		})
		if err != nil {
			lastErr = err
			continue
		}
		if rows == 0 {
			// Deleted concurrently between the initial check and here.
			return ErrNotFound
		}
		return nil
	}
	return fmt.Errorf("account: anonymize user: %w", lastErr)
}

// videoBlobKeys collects every DB-recorded blob key for a video (original +
// thumbnail files, caption tracks). All reads are best-effort — the keys are
// only used for best-effort blob deletion.
func (s *Service) videoBlobKeys(ctx context.Context, videoID uuid.UUID) []string {
	var keys []string
	if files, err := s.repo.ListVideoFiles(ctx, videoID); err == nil {
		for _, f := range files {
			keys = append(keys, f.StorageKey)
		}
	}
	if captions, err := s.repo.ListCaptionsByVideo(ctx, videoID); err == nil {
		for _, c := range captions {
			keys = append(keys, c.StorageKey)
		}
	}
	return keys
}

// blobDelete removes one object best-effort; failures are swallowed (the
// backend's Delete is idempotent and cleanup must never abort the delete).
func (s *Service) blobDelete(ctx context.Context, key string) {
	if s.blobs == nil || key == "" {
		return
	}
	if s.mirror != nil {
		_ = s.mirror.EnqueueUnpin(ctx, key) // best-effort: pull it off the mirror too
	}
	_ = s.blobs.Delete(ctx, key)
}

// blobDeletePrefix removes a whole key prefix best-effort when the backend
// supports it (both first-party backends do). Used for the HLS ladder, whose
// segment objects are not individually recorded in the database.
func (s *Service) blobDeletePrefix(ctx context.Context, prefix string) {
	pd, ok := s.blobs.(storage.PrefixDeleter)
	if !ok || prefix == "" {
		return
	}
	_ = pd.DeletePrefix(ctx, prefix)
}

// randomSuffix returns 8 lowercase hex chars from crypto/rand — the unique
// part of the deleted-account username/email sentinels.
func randomSuffix() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("account: random suffix: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
