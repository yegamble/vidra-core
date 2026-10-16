package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/moderation"
)

const (
	maxFlagObjects   = 20   // objects past this are ignored, not rejected
	maxFlagReasonLen = 2000 // the local report form's limit; longer is truncated
	maxFlagIDLen     = 2048 // bytes; a longer Flag id is refused before any insert
)

// WithFlagReporting files inbound Flags as moderation reports and fans out
// new_report to staff, as for a local report. Unset = Flags are ignored.
func WithFlagReporting(file func(context.Context, moderation.RemoteReport) (uuid.UUID, error),
	notify func(context.Context, uuid.UUID) (int64, error)) Option {
	return func(s *Service) { s.fileFlagReport, s.notifyNewReport = file, notify }
}

// WithFollowerRequestHook registers a best-effort callback fired with the new
// remote_follows id when a follow lands in the approval queue — the seam the
// admin push notification uses (migration 0156).
func WithFollowerRequestHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onFollowerRequest = fn }
}

// WithHeldCommentHook registers a best-effort callback fired with the comment id
// whenever an inbound Note leaves a comment HELD (0161) — the comment_held seam.
func WithHeldCommentHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onHeldComment = fn }
}

// handleFlag (T07): a fediverse user reported OUR video, comment or account —
// PeerTube sends one object, Mastodon [account, statuses...]. One report per
// LOCAL object; an admin-blocked actor is refused like a blocked instance. A
// store error fails the activity so the remote retries (idempotently).
func (s *Service) handleFlag(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	// Bind the Flag id to the signer's host, like every other inbound id.
	if len(act.ID) > maxFlagIDLen || !sameHost(act.ID, signerActorURL) {
		return ErrBadResource
	}
	if blocked, err := s.repo.IsRemoteActorBlockedInstanceWide(ctx, signerActorURL); err != nil {
		return err
	} else if blocked {
		return ErrSenderBlocked
	}
	if s.fileFlagReport == nil {
		return nil
	}
	reason, predefined := flagReason(act.Content, act.Tag)
	for _, uri := range flagObjectIDs(act.Object) {
		targetType, targetID, ok, err := s.resolveFlagTarget(ctx, uri)
		if err != nil {
			return err
		} else if !ok {
			continue // not ours: no reports about third-party content through us
		}
		reportID, err := s.fileFlagReport(ctx, moderation.RemoteReport{
			FlagActivityID: act.ID, ActorURL: signerActorURL, Domain: hostOf(signerActorURL),
			TargetType: targetType, TargetID: targetID, Reason: reason, PredefinedReasons: predefined,
		})
		if errors.Is(err, moderation.ErrInvalidTarget) {
			continue
		} else if err != nil {
			return err
		}
		// uuid.Nil = already filed by an earlier delivery, which told staff.
		if reportID != uuid.Nil && s.notifyNewReport != nil {
			if _, err := s.notifyNewReport(ctx, reportID); err != nil && s.logger != nil {
				s.logger.Warn("federation: new_report staff notification failed", "report_id", reportID, "error", err)
			}
		}
	}
	return nil
}

// resolveFlagTarget maps an object url to a LOCAL video, comment or account (a
// channel actor reports its owner). Anything else is not ours to report.
func (s *Service) resolveFlagTarget(ctx context.Context, uri string) (string, uuid.UUID, bool, error) {
	if id, ok, err := s.localVideoID(ctx, uri); err != nil || ok {
		return moderation.TargetVideo, id, ok, err
	}
	if id, ok := s.localCommentID(uri); ok {
		return moderation.TargetComment, id, true, nil
	}
	if name, ok := strings.CutPrefix(uri, s.baseURL+"/accounts/"); ok && name != "" && !strings.ContainsAny(name, "/#?") {
		u, err := s.repo.GetUserActorByUsername(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", uuid.Nil, false, nil
		}
		return moderation.TargetAccount, u.ID, err == nil, err
	}
	if handle, ok := s.localChannelHandle(uri); ok {
		ch, err := s.resolveLocalChannel(ctx, handle)
		if errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return "", uuid.Nil, false, nil
		}
		return moderation.TargetAccount, ch.OwnerID, err == nil, err
	}
	return "", uuid.Nil, false, nil
}

// flagObjectIDs returns the ids a Flag's object names (a url, an object, or an
// array of either), capped at maxFlagObjects.
func flagObjectIDs(raw json.RawMessage) []string {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		items = []json.RawMessage{raw}
	}
	ids := make([]string, 0, min(len(items), maxFlagObjects))
	for _, it := range items[:min(len(items), maxFlagObjects)] {
		if id := objectID(it); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// flagReason is the content as plain text, prefixed with the known PeerTube
// predefined reasons (unknown tags ignored), truncated to maxFlagReasonLen; the
// reasons are returned too, for the structured column (0170). The prefix stays
// so a client that predates the column still sees them.
func flagReason(contentRaw, tagRaw json.RawMessage) (string, []string) {
	var content string
	_ = json.Unmarshal(contentRaw, &content)
	var tags []struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(tagRaw, &tags)
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	reasons := moderation.NormalizeReasons(names)
	reason := stripHTMLTags(content)
	if len(reasons) > 0 {
		reason = strings.TrimSpace("[" + strings.Join(reasons, ", ") + "] " + reason)
	}
	return truncate(reason, maxFlagReasonLen), reasons
}
