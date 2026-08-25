package httpapi

import (
	"strconv"

	"github.com/labstack/echo/v4"
)

// Pagination lives here rather than being re-derived in every list handler.
// Before this file the same `clampInt(queryInt(c, "limit", def), 1, max)` pair
// was copy-pasted into two dozen handlers, which is how the API ended up with
// pages that were silently clamped to 100 and no way to tell a caller that more
// rows existed. One helper means one place to change the semantics.

// pageParams is the parsed ?limit/?offset pair shared by every list endpoint.
type pageParams struct {
	Limit  int
	Offset int
}

// pageMeta is the pagination envelope every list response carries. Total is how
// many rows match the SAME filters as the page, ignoring limit/offset — without
// it a client cannot tell "last page" from "there is more", which is exactly how
// the admin videos page came to report "100 videos" on an instance with tens of
// thousands.
//
// It is EMBEDDED (anonymously) in each list response so the JSON stays flat and
// the pre-existing `limit`/`offset` field names are preserved byte-for-byte;
// only `total` is added to the wire format.
type pageMeta struct {
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// meta pairs the request's page bounds with the matching row count.
func (p pageParams) meta(total int64) pageMeta {
	return pageMeta{Total: total, Limit: p.Limit, Offset: p.Offset}
}

// parsePage reads ?limit and ?offset using the endpoint's own default and
// ceiling.
//
// The semantics are deliberately forgiving and must stay that way: an absent,
// malformed, or out-of-range value is CLAMPED, never rejected. Turning these
// into 422s would break every existing client that sends limit=500 today and
// quietly receives the first `max` rows. The accepted limit is the whole range
// [1, max] — not a fixed set of options. The UI happens to offer 5/10/20/50/100
// but the contract is a range, so a caller asking for 37 gets 37.
func parsePage(c echo.Context, def, max int) pageParams {
	return parsePageNamed(c, "limit", "offset", def, max)
}

// parsePageNamed is parsePage for the handful of endpoints whose page params
// are not literally named limit/offset (the job-run detail view pages its
// events with events_limit/events_offset alongside the run's own paging).
func parsePageNamed(c echo.Context, limitParam, offsetParam string, def, max int) pageParams {
	offset := queryInt(c, offsetParam, 0)
	if offset < 0 {
		offset = 0
	}
	return pageParams{Limit: parseLimitNamed(c, limitParam, def, max), Offset: offset}
}

// parseLimit reads ?limit for endpoints that cap a result set without offset
// paging (typeahead and suggestion routes).
func parseLimit(c echo.Context, def, max int) int {
	return parseLimitNamed(c, "limit", def, max)
}

func parseLimitNamed(c echo.Context, param string, def, max int) int {
	return clampInt(queryInt(c, param, def), 1, max)
}

// Limit32/Offset32 are the int32 forms the sqlc-generated params want. Having
// them here keeps the `int32(limit)` casts out of the handlers.
func (p pageParams) Limit32() int32  { return int32(p.Limit) }
func (p pageParams) Offset32() int32 { return int32(p.Offset) }

// queryInt reads an integer query param, returning def when absent or malformed.
func queryInt(c echo.Context, name string, def int) int {
	raw := c.QueryParam(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// clampInt bounds v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
