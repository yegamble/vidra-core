package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Status reports receiver state, not proof that it matches a retained source
// manifest. Callers must verify that durable source fence before using reads.
type Status struct {
	ProtocolVersion           int        `json:"protocol_version"`
	SupportedScopes           []string   `json:"supported_scopes"`
	SupportedEventTypes       []string   `json:"supported_event_types"`
	Scope                     string     `json:"scope"`
	Epoch                     *uuid.UUID `json:"epoch"`
	Ready                     bool       `json:"ready"`
	HighestObservedGeneration string     `json:"highest_observed_generation"`
	CompletedGeneration       string     `json:"completed_generation"`
	CompletedAt               *time.Time `json:"completed_at"`
	CompletedManifest         *Manifest  `json:"completed_manifest"`
	StagedSnapshots           int32      `json:"staged_snapshots"`
	OldestStagedAt            *time.Time `json:"oldest_staged_at"`
}

func ParseStatus(raw []byte) (Status, error) {
	var s Status
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return Status{}, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Status{}, fmt.Errorf("catalog status has trailing content")
	}
	fields, err := requireStatusFields(raw, []string{"protocol_version", "supported_scopes", "supported_event_types", "scope", "epoch", "ready", "highest_observed_generation", "completed_generation", "completed_at", "completed_manifest", "staged_snapshots", "oldest_staged_at"}, []string{"epoch", "completed_at", "completed_manifest", "oldest_staged_at"})
	if err != nil {
		return Status{}, err
	}
	if s.Epoch != nil {
		var epoch string
		if json.Unmarshal(fields["epoch"], &epoch) != nil || s.Epoch.String() != epoch || *s.Epoch == uuid.Nil {
			return Status{}, fmt.Errorf("catalog status has a noncanonical epoch")
		}
	}
	if s.CompletedManifest != nil {
		if _, err := requireStatusFields(fields["completed_manifest"], []string{"page_count", "document_count", "membership_sha256", "pages_sha256"}, nil); err != nil {
			return Status{}, err
		}
	}
	if err := s.validate(); err != nil {
		return Status{}, err
	}
	return s, nil
}

func (s Status) validate() error {
	if s.ProtocolVersion != ProtocolVersion || s.Scope != ScopeLocal || len(s.SupportedScopes) != 1 || s.SupportedScopes[0] != ScopeLocal || len(s.SupportedEventTypes) != 2 || !slices.Contains(s.SupportedEventTypes, TypeSnapshotPage) || !slices.Contains(s.SupportedEventTypes, TypeSnapshotEnd) {
		return fmt.Errorf("catalog status lacks supported full-only capabilities")
	}
	highest, e1 := statusCounter(s.HighestObservedGeneration)
	completed, e2 := statusCounter(s.CompletedGeneration)
	if e1 != nil || e2 != nil || highest < completed || s.Ready != (completed > 0) {
		return fmt.Errorf("catalog status has inconsistent generation fences")
	}
	if s.Epoch == nil && (highest != 0 || completed != 0 || s.StagedSnapshots != 0) {
		return fmt.Errorf("uninitialized catalog status has observation state")
	}
	if (completed == 0) != (s.CompletedAt == nil && s.CompletedManifest == nil) || (s.CompletedAt == nil) != (s.CompletedManifest == nil) {
		return fmt.Errorf("catalog status has incomplete completion evidence")
	}
	if s.CompletedManifest != nil {
		if err := s.CompletedManifest.Validate(); err != nil {
			return err
		}
		if s.CompletedAt.IsZero() {
			return fmt.Errorf("catalog status has an empty completion time")
		}
	}
	if s.StagedSnapshots < 0 || s.StagedSnapshots > 1 || (s.StagedSnapshots == 0) != (s.OldestStagedAt == nil) || (s.StagedSnapshots == 1 && (highest <= completed || s.OldestStagedAt.IsZero())) {
		return fmt.Errorf("catalog status has inconsistent staging evidence")
	}
	return nil
}

func requireStatusFields(raw []byte, keys, nullable []string) (map[string]json.RawMessage, error) {
	// Struct decoding folds key case and silently takes the last duplicate. The
	// wire contract requires exact, unique names at each authority object level.
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("catalog status requires an object")
	}
	fields := make(map[string]json.RawMessage, len(keys))
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || !slices.Contains(keys, key) || fields[key] != nil {
			return nil, fmt.Errorf("catalog status has an unknown or duplicate field")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("catalog status has an incomplete object")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("catalog status has trailing content")
	}
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || (bytes.Equal(bytes.TrimSpace(value), []byte("null")) && !slices.Contains(nullable, key)) {
			return nil, fmt.Errorf("catalog status requires field %s", key)
		}
	}
	return fields, nil
}

func statusCounter(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != value {
		return 0, fmt.Errorf("catalog status counter must be a canonical nonnegative int64")
	}
	return n, nil
}
