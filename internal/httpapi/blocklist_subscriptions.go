package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/blocklistsub"
	"github.com/vidra/vidra-core/internal/instancemod"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sync-now fetches an admin-supplied URL, so it carries a per-admin budget.
const (
	blocklistSyncRateLimit  = 6
	blocklistSyncRateWindow = time.Minute
)

// WithBlocklistSubscriptionService mounts the admin blocklist-subscription and
// allowlist routes. When unset the routes are not registered.
func WithBlocklistSubscriptionService(svc *blocklistsub.Service) Option {
	return func(s *Server) { s.blocklistsvc = svc }
}

type blocklistSubscriptionView struct {
	ID           string     `json:"id"`
	URL          string     `json:"url"`
	Label        string     `json:"label"`
	Format       string     `json:"format"`
	Enabled      bool       `json:"enabled"`
	LastStatus   string     `json:"last_status"`
	LastError    string     `json:"last_error,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastAttempt  *time.Time `json:"last_attempt_at,omitempty"`
	LastAdded    int32      `json:"last_added"`
	LastRemoved  int32      `json:"last_removed"`
	EntryCount   int32      `json:"entry_count"`
	BlockCount   int64      `json:"block_count"`
	CreatedAt    time.Time  `json:"created_at"`
}

func subView(s sqlcgen.BlocklistSubscription, blocks int64) blocklistSubscriptionView {
	v := blocklistSubscriptionView{
		ID: s.ID.String(), URL: s.Url, Label: s.Label, Format: s.Format, Enabled: s.Enabled,
		LastStatus: s.LastStatus, LastError: s.LastError, LastAdded: s.LastAdded,
		LastRemoved: s.LastRemoved, EntryCount: s.EntryCount, BlockCount: blocks, CreatedAt: s.CreatedAt,
	}
	if s.LastSyncedAt.Valid {
		t := s.LastSyncedAt.Time
		v.LastSyncedAt = &t
	}
	if s.LastAttemptAt.Valid {
		t := s.LastAttemptAt.Time
		v.LastAttempt = &t
	}
	return v
}

type blocklistSubscriptionListResponse struct {
	Subscriptions []blocklistSubscriptionView `json:"subscriptions"`
	pageMeta
}

type createBlocklistSubscriptionRequest struct {
	URL     string `json:"url"`
	Label   string `json:"label"`
	Format  string `json:"format"`
	Enabled *bool  `json:"enabled"`
}

func (r createBlocklistSubscriptionRequest) Validate() []FieldError {
	var errs []FieldError
	if strings.TrimSpace(r.URL) == "" {
		errs = append(errs, FieldError{Field: "url", Message: "is required"})
	}
	if !blocklistsub.ValidFormat(r.Format) {
		errs = append(errs, FieldError{Field: "format", Message: "must be mastodon_csv, domain_list or json"})
	}
	if len([]rune(r.Label)) > 200 {
		errs = append(errs, FieldError{Field: "label", Message: "must be at most 200 characters"})
	}
	return errs
}

type updateBlocklistSubscriptionRequest struct {
	Label   *string `json:"label"`
	Enabled *bool   `json:"enabled"`
}

func (r updateBlocklistSubscriptionRequest) Validate() []FieldError {
	if r.Label != nil && len([]rune(*r.Label)) > 200 {
		return []FieldError{{Field: "label", Message: "must be at most 200 characters"}}
	}
	return nil
}

type blocklistSyncResultView struct {
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	Added           int    `json:"added"`
	Removed         int    `json:"removed"`
	Entries         int    `json:"entries"`
	IgnoredSeverity int    `json:"ignored_severity"`
	Invalid         int    `json:"invalid"`
}

type blocklistAllowView struct {
	Domain  string    `json:"domain"`
	AddedAt time.Time `json:"added_at"`
}

type blocklistAllowListResponse struct {
	Domains []blocklistAllowView `json:"domains"`
	pageMeta
}

type addBlocklistAllowRequest struct {
	Domain string `json:"domain"`
}

func (r addBlocklistAllowRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Domain) == "" {
		return []FieldError{{Field: "domain", Message: "is required"}}
	}
	return nil
}

func subID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid subscription id")
	}
	return id, nil
}

func (s *Server) handleListBlocklistSubscriptions(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	rows, total, err := s.blocklistsvc.List(c.Request().Context(), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]blocklistSubscriptionView, 0, len(rows))
	for _, r := range rows {
		views = append(views, subView(sqlcgen.BlocklistSubscription{
			ID: r.ID, Url: r.Url, Label: r.Label, Format: r.Format, Enabled: r.Enabled, Etag: r.Etag,
			LastAttemptAt: r.LastAttemptAt, LastSyncedAt: r.LastSyncedAt, LastStatus: r.LastStatus,
			LastError: r.LastError, LastAdded: r.LastAdded, LastRemoved: r.LastRemoved,
			EntryCount: r.EntryCount, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		}, r.BlockCount))
	}
	return c.JSON(http.StatusOK, blocklistSubscriptionListResponse{Subscriptions: views, pageMeta: page.meta(total)})
}

func (s *Server) handleCreateBlocklistSubscription(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in createBlocklistSubscriptionRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	enabled := in.Enabled == nil || *in.Enabled
	sub, err := s.blocklistsvc.Create(c.Request().Context(), userID, in.URL, in.Label, in.Format, enabled)
	switch {
	case errors.Is(err, blocklistsub.ErrInvalidURL):
		return &ValidationError{Fields: []FieldError{{Field: "url", Message: "must be a public http(s) URL"}}}
	case errors.Is(err, blocklistsub.ErrDuplicate):
		return echo.NewHTTPError(http.StatusConflict, "already subscribed to that URL")
	case err != nil:
		return err
	}
	// The resource is the opaque subscription id; the URL is operator-supplied
	// and may carry a token, so it never enters the audit trail.
	s.auditEvent(c, audit.Event{
		Action: observability.ActionBlocklistSubscriptionAdd, Result: observability.ResultSuccess,
		ActorID: userID.String(), ResourceType: "blocklist_subscription", ResourceID: sub.ID.String(),
	})
	return c.JSON(http.StatusCreated, subView(sub, 0))
}

func (s *Server) handleUpdateBlocklistSubscription(c echo.Context) error {
	id, err := subID(c)
	if err != nil {
		return err
	}
	var in updateBlocklistSubscriptionRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	cur, err := s.blocklistsvc.Get(c.Request().Context(), id)
	if errors.Is(err, blocklistsub.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "subscription not found")
	}
	if err != nil {
		return err
	}
	label, enabled := cur.Label, cur.Enabled
	if in.Label != nil {
		label = *in.Label
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	sub, err := s.blocklistsvc.Update(c.Request().Context(), id, label, enabled)
	if errors.Is(err, blocklistsub.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "subscription not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, subView(sub, 0))
}

func (s *Server) handleDeleteBlocklistSubscription(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := subID(c)
	if err != nil {
		return err
	}
	removed, err := s.blocklistsvc.Delete(c.Request().Context(), id)
	if errors.Is(err, blocklistsub.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "subscription not found")
	}
	if err != nil {
		return err
	}
	s.auditEvent(c, audit.Event{
		Action: observability.ActionBlocklistSubscriptionRemove, Result: observability.ResultSuccess,
		ActorID: userID.String(), ResourceType: "blocklist_subscription", ResourceID: id.String(),
		Metadata: []audit.MetadataField{{Key: "removed", Value: strconv.Itoa(removed)}},
	})
	return c.NoContent(http.StatusNoContent)
}

// handleSyncBlocklistSubscription syncs one subscription now. Admin-only and
// rate-limited per admin: it makes the server fetch an admin-supplied URL.
func (s *Server) handleSyncBlocklistSubscription(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := subID(c)
	if err != nil {
		return err
	}
	res, err := s.blocklistSyncLimit.Allow(c.Request().Context(), "blocklist-sync:"+userID.String())
	if err != nil {
		s.logger.Warn("blocklist sync rate limiter unavailable, failing closed", "error", err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "temporarily unavailable")
	}
	if !res.Allowed {
		retry := max(int(res.RetryAfter.Seconds()), 1)
		c.Response().Header().Set("Retry-After", strconv.Itoa(retry))
		return echo.NewHTTPError(http.StatusTooManyRequests, "too many sync requests")
	}
	out, err := s.blocklistsvc.SyncNow(c.Request().Context(), id, userID)
	if errors.Is(err, blocklistsub.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "subscription not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, blocklistSyncResultView{
		Status: out.Status, Error: out.Error, Added: out.Added, Removed: out.Removed,
		Entries: out.Entries, IgnoredSeverity: out.IgnoredSeverity, Invalid: out.Invalid,
	})
}

func (s *Server) handleListBlocklistAllow(c echo.Context) error {
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	rows, total, err := s.blocklistsvc.ListAllow(c.Request().Context(), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]blocklistAllowView, 0, len(rows))
	for _, r := range rows {
		views = append(views, blocklistAllowView{Domain: r.Domain, AddedAt: r.CreatedAt})
	}
	return c.JSON(http.StatusOK, blocklistAllowListResponse{Domains: views, pageMeta: page.meta(total)})
}

func (s *Server) handleAddBlocklistAllow(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in addBlocklistAllowRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	if err := s.blocklistsvc.AddAllow(c.Request().Context(), userID, in.Domain); err != nil {
		if errors.Is(err, instancemod.ErrInvalidDomain) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid instance domain")
		}
		return err
	}
	s.audit(c, observability.ActionBlocklistAllow, observability.ResultSuccess, userID.String(), "domain="+strings.ToLower(strings.TrimSpace(in.Domain)))
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) handleRemoveBlocklistAllow(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	domain := domainParam(c)
	if err := s.blocklistsvc.RemoveAllow(c.Request().Context(), domain); err != nil {
		if errors.Is(err, instancemod.ErrInvalidDomain) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid instance domain")
		}
		return err
	}
	s.audit(c, observability.ActionBlocklistDisallow, observability.ResultSuccess, userID.String(), "domain="+strings.ToLower(strings.TrimSpace(domain)))
	return c.NoContent(http.StatusNoContent)
}
