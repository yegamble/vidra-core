package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// Shared query-filter parsing for list endpoints. parseJobFilter
// (admin_jobs.go) is the model: parse once, validate up front, return a 400
// rather than silently ignoring a value the caller meant. These helpers exist
// because the alternative — `c.QueryParam("status") == "open"` inline in the
// handler — is how ?status=resolved came to mean "everything".

// scopeAll/scopeLocal/scopeRemote are the values of the shared ?scope filter,
// which selects local rows, federated rows, or both.
const (
	scopeAll    = "all"
	scopeLocal  = "local"
	scopeRemote = "remote"
)

// parseEnumParam reads a query param constrained to a fixed set of values.
// Absent or empty yields def; anything else must be in allowed or the request
// is rejected. This is the antidote to the `== "open"` idiom, where every
// unrecognised value silently collapsed into the "no filter" branch.
func parseEnumParam(c echo.Context, name string, allowed []string, def string) (string, error) {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return def, nil
	}
	for _, a := range allowed {
		if raw == a {
			return raw, nil
		}
	}
	return def, echo.NewHTTPError(http.StatusBadRequest, name+" must be one of "+strings.Join(allowed, ", "))
}

// parseScopeParam reads the shared ?scope=local|remote|all filter, defaulting
// to all.
func parseScopeParam(c echo.Context) (string, error) {
	return parseEnumParam(c, "scope", []string{scopeLocal, scopeRemote, scopeAll}, scopeAll)
}

// parseBoolParam reads an optional tri-state boolean filter: nil when absent
// (no filter), otherwise the parsed value. A malformed value is an error rather
// than a silent "false", which would filter rows the caller never asked to
// exclude.
func parseBoolParam(c echo.Context, name string) (*bool, error) {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, name+" must be true or false")
	}
	return &v, nil
}

// parseTimeParam reads an optional RFC3339 timestamp filter.
func parseTimeParam(c echo.Context, name string) (*time.Time, error) {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return nil, nil
	}
	v, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, name+" must be RFC3339")
	}
	return &v, nil
}

// parseTimeRangeParams reads an inclusive [after, before] window and rejects an
// inverted one, which is always a client bug and would otherwise return an
// empty page that looks like "no data".
func parseTimeRangeParams(c echo.Context, afterName, beforeName string) (after, before *time.Time, err error) {
	if after, err = parseTimeParam(c, afterName); err != nil {
		return nil, nil, err
	}
	if before, err = parseTimeParam(c, beforeName); err != nil {
		return nil, nil, err
	}
	if after != nil && before != nil && after.After(*before) {
		return nil, nil, echo.NewHTTPError(http.StatusBadRequest, afterName+" must not be after "+beforeName)
	}
	return after, before, nil
}

// parseCSVParam reads a repeatable and/or comma-separated multi-value filter
// (?tag=a&tag=b and ?tag=a,b are equivalent) with no constrained value set.
// Blanks are dropped and duplicates collapse; nil means "no filter".
func parseCSVParam(c echo.Context, name string) []string {
	values := c.QueryParams()[name]
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, group := range values {
		for _, part := range strings.Split(group, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

// parseCSVEnumParam is parseCSVParam constrained to a fixed value set: every
// value must be in allowed, or the request is rejected rather than silently
// narrowed.
func parseCSVEnumParam(c echo.Context, name string, allowed []string) ([]string, error) {
	out := parseCSVParam(c, name)
	for _, v := range out {
		ok := false
		for _, a := range allowed {
			if v == a {
				ok = true
				break
			}
		}
		if !ok {
			return nil, echo.NewHTTPError(http.StatusBadRequest, name+" must be one of "+strings.Join(allowed, ", "))
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// parseInt32Param reads an optional non-negative integer filter. nil when
// absent; a malformed or negative value is an error rather than a silent zero,
// which for a lower bound would be a no-op and for an upper bound would filter
// away every row.
func parseInt32Param(c echo.Context, name string) (*int32, error) {
	raw := strings.TrimSpace(c.QueryParam(name))
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 0 {
		return nil, echo.NewHTTPError(http.StatusBadRequest, name+" must be a non-negative integer")
	}
	v := int32(n)
	return &v, nil
}

// parseInt32RangeParams reads an inclusive [min, max] window and rejects an
// inverted one. Like parseTimeRangeParams, an inverted range is always a client
// bug and would otherwise return an empty page indistinguishable from "no data".
func parseInt32RangeParams(c echo.Context, minName, maxName string) (lo, hi *int32, err error) {
	if lo, err = parseInt32Param(c, minName); err != nil {
		return nil, nil, err
	}
	if hi, err = parseInt32Param(c, maxName); err != nil {
		return nil, nil, err
	}
	if lo != nil && hi != nil && *lo > *hi {
		return nil, nil, echo.NewHTTPError(http.StatusBadRequest, minName+" must not be greater than "+maxName)
	}
	return lo, hi, nil
}

// parseSortParam reads a ?sort= key constrained to the endpoint's supported
// orderings. Unlike a free-form sort this can never reach the SQL layer as
// anything but one of `allowed`, which is what lets the query use a
// CASE-over-a-bound-parameter ORDER BY instead of string concatenation.
func parseSortParam(c echo.Context, allowed []string, def string) (string, error) {
	return parseEnumParam(c, "sort", allowed, def)
}
