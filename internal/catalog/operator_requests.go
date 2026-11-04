package catalog

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var ErrOperatorRequest = errors.New("invalid catalog operator request")

type BootstrapRequest struct {
	Scope                 string     `json:"scope"`
	Epoch                 uuid.UUID  `json:"epoch"`
	ExpectedEpoch         *uuid.UUID `json:"expected_epoch"`
	ExpectedReceiverEpoch *uuid.UUID `json:"expected_receiver_epoch"`
}

func (r BootstrapRequest) Validate() error {
	if r.Scope != ScopeLocal || r.Epoch == uuid.Nil ||
		(r.ExpectedEpoch != nil && *r.ExpectedEpoch == uuid.Nil) ||
		(r.ExpectedReceiverEpoch != nil && *r.ExpectedReceiverEpoch == uuid.Nil) {
		return ErrOperatorRequest
	}
	return nil
}

type FullRequest struct {
	Scope                       string    `json:"scope"`
	ExpectedEpoch               uuid.UUID `json:"expected_epoch"`
	ExpectedAllocatedGeneration int64     `json:"expected_allocated_generation,string"`
}

func (r FullRequest) Validate() error {
	if r.Scope != ScopeLocal || r.ExpectedEpoch == uuid.Nil || r.ExpectedAllocatedGeneration < 0 {
		return ErrOperatorRequest
	}
	return nil
}

// ParseBootstrapRequest preserves explicit null predecessors. CAS relationships
// (including inconsistent retries/retired epochs) are controller conflicts, not
// shape errors; this parser never resets or observes source/receiver state.
func ParseBootstrapRequest(raw []byte) (BootstrapRequest, error) {
	fields, err := requireStatusFields(raw,
		[]string{"scope", "epoch", "expected_epoch", "expected_receiver_epoch"},
		[]string{"expected_epoch", "expected_receiver_epoch"})
	var r BootstrapRequest
	if err != nil || json.Unmarshal(raw, &r) != nil || r.Validate() != nil ||
		!requestEpoch(&r.Epoch, fields["epoch"]) ||
		!requestEpoch(r.ExpectedEpoch, fields["expected_epoch"]) ||
		!requestEpoch(r.ExpectedReceiverEpoch, fields["expected_receiver_epoch"]) {
		return BootstrapRequest{}, ErrOperatorRequest
	}
	return r, nil
}

// ParseFullRequest allows allocator zero and retains exact int64 strings rather
// than passing through floating point. A FULL identity itself stays positive.
func ParseFullRequest(raw []byte) (FullRequest, error) {
	fields, err := requireStatusFields(raw,
		[]string{"scope", "expected_epoch", "expected_allocated_generation"}, nil)
	var r FullRequest
	if err != nil || json.Unmarshal(raw, &r) != nil || r.Validate() != nil ||
		!requestEpoch(&r.ExpectedEpoch, fields["expected_epoch"]) {
		return FullRequest{}, ErrOperatorRequest
	}
	var text string
	if json.Unmarshal(fields["expected_allocated_generation"], &text) != nil {
		return FullRequest{}, ErrOperatorRequest
	}
	n, err := statusCounter(text)
	if err != nil || n != r.ExpectedAllocatedGeneration {
		return FullRequest{}, ErrOperatorRequest
	}
	return r, nil
}

func requestEpoch(value *uuid.UUID, raw json.RawMessage) bool {
	if value == nil {
		return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
	}
	var text string
	return *value != uuid.Nil && json.Unmarshal(raw, &text) == nil && value.String() == text
}
