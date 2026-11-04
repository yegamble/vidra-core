package searchclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/catalog"
	"github.com/vidra/vidra-core/internal/searchevents"
)

type frozenCatalogBody []byte

// SetCatalogEpoch returns transport success only. GET and verify exact status
// independently; a 204 is not evidence that a source manifest matches.
func (c *Client) SetCatalogEpoch(ctx context.Context, epoch uuid.UUID, expectedEpoch *uuid.UUID) error {
	if epoch == uuid.Nil || expectedEpoch != nil && *expectedEpoch == uuid.Nil {
		return fmt.Errorf("catalog epoch must be nonzero")
	}
	body := struct {
		Scope         string     `json:"scope"`
		Epoch         uuid.UUID  `json:"epoch"`
		ExpectedEpoch *uuid.UUID `json:"expected_epoch"`
	}{catalog.ScopeLocal, epoch, expectedEpoch}
	path := "/internal/v2/catalog/epoch"
	return c.doPaths(ctx, groupCatalog, http.MethodPut, path, path, nil, body, nil, http.StatusNoContent, catalog.Identity{})
}

// SendCatalogBody sends frozen bytes verbatim. Receipt counts never establish
// completion: the caller must GET and compare its retained exact full manifest.
func (c *Client) SendCatalogBody(ctx context.Context, frozen []byte) (searchevents.EventBatchResult, error) {
	if len(frozen) == 0 || len(frozen) > catalog.MaxRequestBytes {
		return searchevents.EventBatchResult{}, fmt.Errorf("catalog request exceeds byte budget or is empty")
	}
	var batch searchevents.EventBatch
	if err := json.Unmarshal(frozen, &batch); err != nil || len(batch.Events) == 0 || len(batch.Events) > 500 {
		return searchevents.EventBatchResult{}, fmt.Errorf("catalog frozen batch is invalid")
	}
	ids := make([]uuid.UUID, len(batch.Events))
	for i, ev := range batch.Events {
		ids[i] = ev.EventID
	}
	if _, err := catalog.MembershipDigest(ids); err != nil {
		return searchevents.EventBatchResult{}, err
	}
	var raw json.RawMessage
	path := "/internal/v2/catalog/events"
	if err := c.doPaths(ctx, groupCatalog, http.MethodPost, path, path, nil, frozenCatalogBody(frozen), &raw, http.StatusOK, catalog.Identity{}); err != nil {
		return searchevents.EventBatchResult{}, err
	}
	return catalog.ParseEventReceipt(raw, ids)
}
