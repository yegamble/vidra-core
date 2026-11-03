package catalog

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/searchevents"
)

type FrozenEnd struct {
	EventID        uuid.UUID
	Manifest       Manifest
	CanonicalBytes int64
	RequestBody    []byte
}

// FreezeEnd bounds the declared full observation, not proof of its membership.
// Capture derives the manifest and canonical-byte sum from its frozen pages.
func FreezeEnd(id Identity, manifest Manifest, canonicalBytes int64, eventID uuid.UUID, at time.Time, limits Limits) (FrozenEnd, error) {
	if err := id.Validate(); err != nil {
		return FrozenEnd{}, err
	}
	if err := manifest.Validate(); err != nil {
		return FrozenEnd{}, err
	}
	if eventID == uuid.Nil || at.IsZero() {
		return FrozenEnd{}, fmt.Errorf("catalog end needs immutable event identity and time")
	}
	limits = limits.Effective()
	if limits.MaxRequestBytes <= 0 || limits.MaxSnapshotBytes <= 0 || limits.MaxDocuments <= 0 || limits.MaxPages <= 0 || int64(manifest.PageCount) > limits.MaxPages || manifest.DocumentCount > limits.MaxDocuments || canonicalBytes < 0 || canonicalBytes > limits.MaxSnapshotBytes || (manifest.DocumentCount == 0) != (canonicalBytes == 0) {
		return FrozenEnd{}, ErrQuotaExceeded
	}
	payload, err := json.Marshal(struct {
		Identity
		Manifest
	}{id, manifest})
	if err != nil {
		return FrozenEnd{}, err
	}
	body, err := json.Marshal(searchevents.EventBatch{Events: []searchevents.Envelope{{
		EventID: eventID, Type: TypeSnapshotEnd, OccurredAt: at, SchemaVersion: ProtocolVersion, Payload: payload,
	}}})
	if err != nil {
		return FrozenEnd{}, err
	}
	if int64(len(body)) > limits.MaxRequestBytes {
		return FrozenEnd{}, ErrQuotaExceeded
	}
	return FrozenEnd{eventID, manifest, canonicalBytes, body}, nil
}
