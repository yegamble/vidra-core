package searchclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/vidra/vidra-core/internal/catalog"
)

// CatalogStatus requires explicit v2 full-only capability and consistent
// receiver evidence. It does not verify a source manifest or grant read authority.
func (c *Client) CatalogStatus(ctx context.Context) (catalog.Status, error) {
	var raw json.RawMessage
	err := c.do(ctx, groupCatalog, http.MethodGet, "/internal/v2/catalog/status",
		url.Values{"scope": {catalog.ScopeLocal}}, nil, &raw)
	if err != nil {
		return catalog.Status{}, err
	}
	return catalog.ParseStatus(raw)
}
