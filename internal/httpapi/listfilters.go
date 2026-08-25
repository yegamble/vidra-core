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

// parseCSVEnumParam reads a repeatable and/or comma-separated multi-value
// filter (?state=draft&state=failed and ?state=draft,failed are equivalent).
// Every value must be in allowed. Duplicates collapse; an empty result means
// "no filter".
func parseCSVEnumParam(c echo.Context, name string, allowed []string) ([]string, error) {
	values := c.QueryParams()[name]
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(allowed))
	out := make([]string, 0, len(allowed))
	for _, group := range values {
		for _, part := range strings.Split(group, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			ok := false
			for _, a := range allowed {
				if part == a {
					ok = true
					break
				}
			}
			if !ok {
				return nil, echo.NewHTTPError(http.StatusBadRequest, name+" must be one of "+strings.Join(allowed, ", "))
			}
			if !seen[part] {
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// parseSortParam reads a ?sort= key constrained to the endpoint's supported
// orderings. Unlike a free-form sort this can never reach the SQL layer as
// anything but one of `allowed`, which is what lets the query use a
// CASE-over-a-bound-parameter ORDER BY instead of string concatenation.
func parseSortParam(c echo.Context, allowed []string, def string) (string, error) {
	return parseEnumParam(c, "sort", allowed, def)
}
