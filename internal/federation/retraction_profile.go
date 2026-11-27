package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// A previously public actor can withdraw an object from an inbox that actually
// received it. Rebuild its retraction from identifiers, excluding all profile
// text, content, and audience extensions stored in the queued snapshot.
func (s *Service) privateRetraction(ctx context.Context, row sqlcgen.ClaimDueDeliveriesRow) ([]byte, error) {
	var activity struct {
		Type, ID, Actor string
		Object          json.RawMessage
	}
	if err := json.Unmarshal(row.Payload, &activity); err != nil {
		return nil, err
	}
	actor := s.baseURL + "/accounts/" + row.SigningUsername
	if activity.Actor != actor || (activity.Type != "Delete" && activity.Type != "Undo") {
		return nil, ErrNotFound
	}
	prefix := actor + "/activities/" + strings.ToLower(activity.Type) + "/"
	nonce, ok := strings.CutPrefix(activity.ID, prefix)
	if !ok && activity.Type == "Undo" {
		nonce, ok = strings.CutPrefix(activity.ID, actor+"/undo/")
	}
	if _, err := uuid.Parse(nonce); !ok || err != nil {
		return nil, ErrNotFound
	}
	proof := sqlcgen.KnownAccountRetractionActivityParams{AccountID: uuid.UUID(row.SigningUserID.Bytes), Actor: actor, Inbox: row.InboxUrl, Kind: activity.Type}
	var object any
	if activity.Type == "Delete" {
		if err := json.Unmarshal(activity.Object, &proof.Target); err != nil || proof.Target == "" {
			return nil, ErrNotFound
		}
		object = proof.Target
	} else {
		var previous struct{ Type, ID, Actor, Object string }
		if err := json.Unmarshal(activity.Object, &previous); err != nil || previous.Actor != actor || previous.ID == "" || previous.Object == "" {
			return nil, ErrNotFound
		}
		switch previous.Type {
		case "Follow", "Like", "Dislike":
		default:
			return nil, ErrNotFound
		}
		proof.Kind, proof.PreviousID, proof.Target = previous.Type, previous.ID, previous.Object
		object = map[string]string{"type": previous.Type, "id": previous.ID, "actor": actor, "object": previous.Object}
	}
	if _, err := s.repo.KnownAccountRetractionActivity(ctx, proof); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "type": activity.Type, "id": activity.ID, "actor": actor, "object": object})
}
