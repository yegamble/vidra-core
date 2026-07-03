package atproto

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const (
	// maxPostAttempts is how many times a post is retried before dead-lettering.
	maxPostAttempts = 6
	// postBaseBackoff is the first retry delay; it doubles each attempt.
	postBaseBackoff = 30 * time.Second
	// maxPostBackoff caps the exponential backoff.
	maxPostBackoff = 6 * time.Hour
	// maxPostErrorLen bounds the stored (safe) error string.
	maxPostErrorLen = 300
	// maxThumbBytes caps the embed thumbnail blob (spec: ≤1 MiB).
	maxThumbBytes = 1 << 20
)

// permanentError marks a failure that must NOT be retried: the video is gone or
// no longer public, the owner unlinked, auto_post was turned off, or a
// configuration problem (unopenable password / missing base URL). It dead-letters
// immediately instead of burning six attempts.
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

func permanentf(format string, args ...any) error {
	return &permanentError{msg: fmt.Sprintf(format, args...)}
}

func isPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// DrainPosts claims up to limit due auto-posts and attempts each: on success the
// resulting AT-URI is recorded (state 'posted'); a transient failure reschedules
// with exponential backoff, or dead-letters (state 'failed') after
// maxPostAttempts; a permanent failure dead-letters immediately. Returns the
// number posted. Only the claim-query error is returned — per-post outcomes are
// persisted. Intended to be called on a ticker by a single worker. A no-op when
// the extension is disabled.
func (s *Service) DrainPosts(ctx context.Context, limit int) (int, error) {
	if !s.enabled {
		return 0, nil
	}
	rows, err := s.repo.ClaimDueATProtoPosts(ctx, int32(limit))
	if err != nil {
		return 0, err
	}
	posted := 0
	for _, row := range rows {
		if err := s.runPost(ctx, row); err != nil {
			s.recordPostFailure(ctx, row, err)
			continue
		}
		posted++
	}
	return posted, nil
}

// runPost performs one auto-post: re-validate the video is still public, resolve
// the owner's linked account, create a fresh session, optionally upload the
// thumbnail, and create the feed post — recording the AT-URI on success.
func (s *Service) runPost(ctx context.Context, row sqlcgen.ClaimDueATProtoPostsRow) error {
	info, err := s.repo.GetATProtoPostVideo(ctx, row.VideoID)
	if err != nil {
		return permanentf("the video no longer exists")
	}
	if info.Privacy != "public" {
		return permanentf("the video is no longer public")
	}
	acct, err := s.repo.GetATProtoAccount(ctx, info.OwnerID)
	if err != nil {
		return permanentf("the owner has no linked Bluesky account")
	}
	if !acct.AutoPost {
		return permanentf("auto-post is disabled for the owner")
	}
	if s.baseURL == "" {
		return permanentf("the instance public base URL is not configured")
	}
	password, err := s.openPassword(acct.AppPasswordSealed)
	if err != nil {
		return permanentf("the stored app password could not be opened")
	}

	sess, err := s.pds.CreateSession(ctx, acct.PdsUrl, acct.Handle, password)
	if err != nil {
		return err // transient (network / 5xx / 429) — retried with backoff
	}

	watchURL := s.baseURL + "/videos/watch/" + info.ID.String()
	thumb := s.uploadThumbnail(ctx, sess, acct.PdsUrl, row.VideoID)
	record := buildPostRecord(info.Title, watchURL, s.now(), thumb)

	uri, err := s.pds.CreateRecord(ctx, acct.PdsUrl, sess, feedPostCollection, record)
	if err != nil {
		return err
	}
	if err := s.repo.MarkATProtoPostDone(ctx, sqlcgen.MarkATProtoPostDoneParams{ID: row.ID, PostUri: uri}); err != nil {
		return err
	}
	_ = s.repo.TouchATProtoAccountPosted(ctx, info.OwnerID)
	return nil
}

// uploadThumbnail best-effort uploads a video's poster image (≤1 MiB) as a blob
// for the embed card. It returns nil — and the post proceeds without a card
// image — when there is no thumbnail source, no thumbnail, an oversize image, or
// an upload error.
func (s *Service) uploadThumbnail(ctx context.Context, sess Session, pdsURL string, videoID uuid.UUID) Blob {
	if s.thumbs == nil {
		return nil
	}
	data, mime, ok, err := s.thumbs.ReadThumbnail(ctx, videoID)
	if err != nil || !ok || len(data) == 0 || len(data) > maxThumbBytes {
		return nil
	}
	if mime == "" {
		mime = "image/jpeg"
	}
	blob, err := s.pds.UploadBlob(ctx, pdsURL, sess, mime, data)
	if err != nil {
		s.logger.Warn("atproto thumbnail upload failed; posting without a card image",
			"video_id", videoID.String(), "error", err)
		return nil
	}
	return blob
}

// recordPostFailure dead-letters a permanent failure (or one that exhausted the
// attempt budget), else reschedules with backoff. The stored error is always the
// safe message from runPost (XRPCError / permanentError — never a raw URL or
// credential).
func (s *Service) recordPostFailure(ctx context.Context, row sqlcgen.ClaimDueATProtoPostsRow, cause error) {
	msg := cause.Error()
	if len(msg) > maxPostErrorLen {
		msg = msg[:maxPostErrorLen]
	}
	attempts := int(row.Attempts) + 1
	if isPermanent(cause) || attempts >= maxPostAttempts {
		_ = s.repo.FailATProtoPost(ctx, sqlcgen.FailATProtoPostParams{ID: row.ID, Error: msg})
		return
	}
	_ = s.repo.RescheduleATProtoPost(ctx, sqlcgen.RescheduleATProtoPostParams{
		ID:            row.ID,
		NextAttemptAt: s.now().UTC().Add(postBackoff(attempts)),
		Error:         msg,
	})
}

// postBackoff is postBaseBackoff * 2^(attempts-1), capped at maxPostBackoff.
func postBackoff(attempts int) time.Duration {
	d := postBaseBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= maxPostBackoff {
			return maxPostBackoff
		}
	}
	return d
}
