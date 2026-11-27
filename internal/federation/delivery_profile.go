package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Delivery rows are snapshots and may outlive a profile opt-in. An account
// activity needs a current Person to sign it. Optional owner and Mention
// entries can be removed while delivering the channel or author's activity.
func (s *Service) currentProfilePayload(ctx context.Context, row sqlcgen.ClaimDueDeliveriesRow) ([]byte, error) {
	if row.SigningUserID.Valid {
		user, err := s.repo.GetUserActorByID(ctx, uuid.UUID(row.SigningUserID.Bytes))
		if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Username != row.SigningUsername {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	// UseNumber preserves exact integral media counters if an optional reference
	// changes. When nothing changes, sign the original bytes.
	var envelope map[string]any
	decoder := json.NewDecoder(bytes.NewReader(row.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, err
	}
	// Only an eligible Group may respond to or initiate a new relationship.
	// Undo and Delete refer to previously known relationships or content.
	if row.SigningChannelID.Valid && (envelope["type"] == "Accept" || envelope["type"] == "Reject" || envelope["type"] == "Follow") {
		ch, err := s.resolveChannel(ctx, row.SigningChannelHandle, s.repo.GetFederatedChannelByHandle)
		if err != nil {
			return nil, err
		}
		if ch.ID != uuid.UUID(row.SigningChannelID.Bytes) {
			return nil, ErrNotFound
		}
	}
	obj, _ := envelope["object"].(map[string]any)
	var field, kind, link string
	switch obj["type"] {
	case "Video":
		field, kind, link = "attributedTo", "Person", "id"
	case "Note":
		field, kind, link = "tag", "Mention", "href"
	default:
		return row.Payload, nil
	}
	refs, _ := obj[field].([]any)
	kept := make([]any, 0, len(refs))
	for _, item := range refs {
		ref, _ := item.(map[string]any)
		uri, _ := ref[link].(string)
		name, local := strings.CutPrefix(uri, s.baseURL+"/accounts/")
		if local && ref["type"] == kind {
			_, err := s.repo.GetUserActorByUsername(ctx, name)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		kept = append(kept, item)
	}
	if len(kept) == len(refs) {
		return row.Payload, nil
	}
	if len(kept) == 0 {
		if field == "attributedTo" {
			return nil, ErrNotFound
		}
		delete(obj, field)
	} else {
		obj[field] = kept
	}
	return json.Marshal(envelope)
}
