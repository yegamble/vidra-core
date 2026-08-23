package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/qoe"
)

// GET /api/v1/admin/qoe/playback-health — the backend half of the admin
// playback-health page (phase-4 delivery item 4).
//
// It answers the phase-4 exit criterion directly: "an admin can see TTFF/rebuffer
// percentiles per source for the last 24h". The default window IS the last 24h,
// so the criterion is the zero-argument call.
//
// Shape and paging follow the admin-jobs endpoints: admin-only, limit/offset with
// a clamped maximum, a total alongside the page. The one deliberate difference is
// that `sources` is NOT paged — it is a summary over the whole window, and a page
// of a summary is not a summary. Only `buckets`, the hourly detail behind it, is.
//
// The admin PAGE is frontend work and is not in this change.

const (
	defaultQoEBucketLimit = 200
	maxQoEBucketLimit     = 1000
)

// qoeHealthProvider is the read seam. *qoe.Service satisfies it; tests fake it.
type qoeHealthProvider interface {
	PlaybackHealth(ctx context.Context, start, end time.Time, limit, offset int) (qoe.PlaybackHealth, error)
}

// handleQoEPlaybackHealth returns the rollup window.
//
// `since`/`until` are RFC3339 and are snapped to hour boundaries by the service,
// because that is the resolution the rollups exist at. Omitting both asks for the
// last 24 hours; omitting `since` alone asks for the 24 hours before `until`.
func (s *Server) handleQoEPlaybackHealth(c echo.Context) error {
	until, err := queryTime(c, "until", time.Now().UTC())
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "until", Message: "must be an RFC3339 timestamp"}}}
	}
	// The window end is exclusive and hour-aligned, so it rounds UP. Rounding
	// down would make a request at 14:05 silently exclude the 13:00 rollup,
	// which is the most recent one there is and the one an operator watching an
	// incident is waiting for.
	//
	// The rounding happens BEFORE the default start is derived from it, so
	// "the last 24h" is exactly twenty-four hour buckets rather than
	// twenty-four hours and whatever is left of the current one.
	end := ceilHour(until)
	since, err := queryTime(c, "since", end.Add(-qoe.DefaultWindow))
	if err != nil {
		return &ValidationError{Fields: []FieldError{{Field: "since", Message: "must be an RFC3339 timestamp"}}}
	}

	limit := clampInt(queryInt(c, "limit", defaultQoEBucketLimit), 1, maxQoEBucketLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	health, err := s.qoeHealthSvc.PlaybackHealth(c.Request().Context(), since, end, limit, offset)
	switch {
	case errors.Is(err, qoe.ErrWindowTooWide):
		return &ValidationError{Fields: []FieldError{
			{Field: "since", Message: "window is too wide; request at most 7 days, or fewer hours at a time"},
		}}
	case errors.Is(err, qoe.ErrUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "playback telemetry is not available")
	case err != nil:
		return err
	}
	return c.JSON(http.StatusOK, health)
}

// queryTime parses an optional RFC3339 query parameter.
func queryTime(c echo.Context, name string, def time.Time) (time.Time, error) {
	raw := c.QueryParam(name)
	if raw == "" {
		return def, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// ceilHour rounds t up to the next hour boundary unless it is already on one.
func ceilHour(t time.Time) time.Time {
	trunc := t.UTC().Truncate(time.Hour)
	if trunc.Equal(t.UTC()) {
		return trunc
	}
	return trunc.Add(time.Hour)
}
