package catalog

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/searchevents"
)

// ParseEventReceipt validates intake accounting, never completed authority.
func ParseEventReceipt(raw []byte, ids []uuid.UUID) (searchevents.EventBatchResult, error) {
	var result searchevents.EventBatchResult
	fields, err := requireStatusFields(raw, []string{"accepted", "duplicates", "failed"}, nil)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return searchevents.EventBatchResult{}, err
	}
	n := len(ids)
	if n == 0 || n > 500 || result.Accepted < 0 || result.Duplicates < 0 || result.Accepted > n || result.Duplicates > n || result.Accepted+result.Duplicates+len(result.Failed) != n {
		return searchevents.EventBatchResult{}, fmt.Errorf("catalog receipt accounting mismatch")
	}
	expected := make(map[uuid.UUID]bool, n)
	for _, id := range ids {
		if id == uuid.Nil || expected[id] {
			return searchevents.EventBatchResult{}, fmt.Errorf("catalog receipt needs distinct event identities")
		}
		expected[id] = true
	}
	var failed []json.RawMessage
	if err := json.Unmarshal(fields["failed"], &failed); err != nil {
		return searchevents.EventBatchResult{}, err
	}
	for i, item := range failed {
		fields, err := requireStatusFields(item, []string{"event_id", "code", "message"}, nil)
		if err != nil {
			return searchevents.EventBatchResult{}, err
		}
		f := result.Failed[i]
		var text string
		if json.Unmarshal(fields["event_id"], &text) != nil || text != f.EventID.String() || !expected[f.EventID] || f.Code == "" {
			return searchevents.EventBatchResult{}, fmt.Errorf("catalog receipt has invalid failure identity or code")
		}
		delete(expected, f.EventID)
	}
	return result, nil
}
