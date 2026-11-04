package catalog

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/searchevents"
)

// ValidateFrozenEnd checks the complete immutable request against retained proof,
// never completed authority. The caller separately checks its canonical-byte
// ledger against the snapshot limit; END does not carry that ledger.
func ValidateFrozenEnd(body []byte, expected FullProof, limits Limits) (uuid.UUID, error) {
	if err := expected.Identity.Validate(); err != nil {
		return uuid.Nil, err
	}
	if err := expected.Manifest.Validate(); err != nil {
		return uuid.Nil, err
	}
	limits = limits.Effective()
	if limits.MaxRequestBytes <= 0 || limits.MaxSnapshotBytes <= 0 || limits.MaxDocuments <= 0 || limits.MaxPages <= 0 ||
		int64(len(body)) > limits.MaxRequestBytes || int64(expected.Manifest.PageCount) > limits.MaxPages || expected.Manifest.DocumentCount > limits.MaxDocuments {
		return uuid.Nil, ErrQuotaExceeded
	}
	batch, err := requireStatusFields(body, []string{"events"}, nil)
	if err != nil {
		return uuid.Nil, err
	}
	var events []json.RawMessage
	if err := json.Unmarshal(batch["events"], &events); err != nil || len(events) != 1 {
		return uuid.Nil, fmt.Errorf("catalog frozen end requires exactly one event")
	}
	fields, err := requireStatusFields(events[0], []string{"event_id", "type", "occurred_at", "schema_version", "payload"}, nil)
	if err != nil {
		return uuid.Nil, err
	}
	var envelope searchevents.Envelope
	if err := json.Unmarshal(events[0], &envelope); err != nil {
		return uuid.Nil, err
	}
	canonicalUUID := func(raw json.RawMessage, id uuid.UUID) bool {
		var text string
		return id != uuid.Nil && json.Unmarshal(raw, &text) == nil && text == id.String()
	}
	if envelope.Type != TypeSnapshotEnd || envelope.SchemaVersion != ProtocolVersion || envelope.OccurredAt.IsZero() || !canonicalUUID(fields["event_id"], envelope.EventID) {
		return uuid.Nil, fmt.Errorf("catalog frozen end has invalid envelope identity, type or time")
	}
	payload, err := requireStatusFields(envelope.Payload, []string{"scope", "epoch", "generation", "page_count", "document_count", "membership_sha256", "pages_sha256"}, nil)
	if err != nil {
		return uuid.Nil, err
	}
	var end struct {
		Identity
		Manifest
	}
	if err := json.Unmarshal(envelope.Payload, &end); err != nil {
		return uuid.Nil, err
	}
	if end.Identity != expected.Identity || end.Manifest != expected.Manifest || !canonicalUUID(payload["epoch"], end.Epoch) {
		return uuid.Nil, fmt.Errorf("catalog frozen end differs from retained full proof")
	}
	return envelope.EventID, nil
}
