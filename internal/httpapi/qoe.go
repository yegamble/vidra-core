package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/playback"
	"github.com/vidra/vidra-core/internal/qoe"
)

// POST /api/v1/qoe/events — the playback quality beacon (phase-4 delivery item
// 4, docs/productionization/interfaces.md §9).
//
// This is deliberately the same shape as POST /search/events, which already
// solved this problem once: a batched POST under optionalAuth, a hard cap on
// events per request, an all-or-nothing type allowlist, server-side enrichment
// of everything identity-shaped, and 202 Accepted. Anything the server can
// derive, the server derives — a client is never trusted to say who it is, and
// (the addition here) is never trusted to say where its bytes came from either.
//
// # Nothing here can fail a playback
//
// The write is best-effort all the way down: qoe.Service.Record logs, meters and
// swallows. A batch that is well-formed gets 202 whether or not a single row
// landed. That is not laziness — the alternative is a viewer whose player
// retries, backs off, or surfaces an error because a metrics row could not be
// written, which would make the measurement of quality a cause of bad quality.
//
// # What the client reports, and what it does not
//
// The client reports the FINAL URL it fetched from. It does not report a
// delivery source: the server classifies that origin against its own configured
// CDN base, IPFS gateway and public origin (qoe.Classifier), so an unrecognised
// host collapses into one 'other' bucket and a client cannot invent a new
// dimension value by naming a new host. The URL itself is never stored.
//
// # Live is not accepted yet
//
// The schema and qoe.Event both carry live_stream_id, and 'origin-live' is in
// the delivery-source vocabulary, because phase-4 item 7 will need them. Neither
// is reachable from this endpoint yet: live segments never enter storage.Backend
// and never reach the delivery resolver, so no origin classification can honestly
// produce 'origin-live' today. Reserving the schema means item 7 is a client
// change and a handler change, not a migration.

// maxQoEEventsPerRequest caps one beacon batch. It matches the search beacon's
// cap for the same reason: a batch is a network optimisation, not a bulk-import
// channel.
const maxQoEEventsPerRequest = 20

// maxSourceURLLength bounds the reported origin before it is parsed. The value
// is never stored — it exists only long enough to be classified — but an
// unbounded string still costs parse time, so it is fenced at the edge.
const maxSourceURLLength = 2048

// qoeEventsRequest is the POST body: a batch of measurements.
type qoeEventsRequest struct {
	Events []qoeEventInput `json:"events"`
}

// qoeEventInput is one reported measurement. Every field is client-supplied and
// therefore suspect; see handleQoEEvents for what happens to each.
type qoeEventInput struct {
	Type    string `json:"type"`
	VideoID string `json:"video_id"`

	// SessionID is the id from the playback session. On a public video it is
	// CLIENT-ASSERTED — core#74 mints session ids and records none — and the
	// stored row says so. PlaybackToken, when present, is what makes it
	// attestable: the id is inside the token's signed payload.
	SessionID     string `json:"session_id"`
	PlaybackToken string `json:"playback_token"`

	// SourceURL is the final URL the client actually fetched media from. It is
	// classified and discarded.
	SourceURL string `json:"source_url"`

	Engine          string `json:"engine"`
	PackagingFormat string `json:"packaging_format"`

	TTFFMs          *int32 `json:"ttff_ms"`
	RebufferMs      *int32 `json:"rebuffer_ms"`
	RenditionHeight *int32 `json:"rendition_height"`
	ErrorClass      string `json:"error_class"`

	Metadata map[string]any `json:"metadata"`
}

// handleQoEEvents accepts a batch of playback measurements.
//
// Validation is ALL-OR-NOTHING, exactly as the search beacon's is: every event
// is checked before any is recorded, and one bad event rejects the batch. A
// partial accept would mean a client whose vocabulary has drifted keeps sending
// forever while silently losing half its data, which is worse than a 400 it can
// see in a console.
func (s *Server) handleQoEEvents(c echo.Context) error {
	var in qoeEventsRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "malformed or invalid request body")
	}
	if len(in.Events) == 0 {
		return c.NoContent(http.StatusAccepted)
	}
	if len(in.Events) > maxQoEEventsPerRequest {
		return &ValidationError{Fields: []FieldError{
			{Field: "events", Message: "at most 20 events per request"},
		}}
	}

	// The runtime kill switch is checked BEFORE validation so that turning
	// collection off during an incident stops the work rather than merely
	// stopping the writes — and answers 202 either way, so a client cannot tell
	// (and therefore cannot behave differently) when an operator flips it.
	if !s.qoeCollectionEnabled() {
		return c.NoContent(http.StatusAccepted)
	}

	events := make([]qoe.Event, len(in.Events))
	var fes []FieldError
	for i, ev := range in.Events {
		built, err := s.buildQoEEvent(ev)
		if err != nil {
			fes = append(fes, FieldError{Field: "events", Message: qoeFieldMessage(err)})
			continue
		}
		events[i] = built
	}
	if len(fes) > 0 {
		return &ValidationError{Fields: fes}
	}

	// Identity is enriched here, once, from the request — never from the body.
	// A beacon that claimed a user id would be believed by nobody, so it is not
	// even read.
	viewerID, _, authed := principalFromContext(c)
	digest := s.qoeDigester.Viewer(time.Now(), authed, viewerID, c.RealIP())

	ctx := c.Request().Context()
	for i := range events {
		events[i].ViewerDigest = digest
		s.qoesvc.Record(ctx, events[i])
	}
	return c.NoContent(http.StatusAccepted)
}

// buildQoEEvent turns one client-supplied object into a validated event.
func (s *Server) buildQoEEvent(in qoeEventInput) (qoe.Event, error) {
	videoID, err := uuid.Parse(strings.TrimSpace(in.VideoID))
	if err != nil {
		return qoe.Event{}, errors.New("video_id must be a uuid")
	}
	if len(in.SourceURL) > maxSourceURLLength {
		return qoe.Event{}, errors.New("source_url too long")
	}

	ev := qoe.Event{
		Type: qoe.EventType(strings.TrimSpace(in.Type)),
		// The client says where it fetched from; the server says what that is.
		DeliverySource:  s.qoeClassifier.Classify(in.SourceURL),
		Engine:          qoe.Engine(strings.TrimSpace(in.Engine)),
		PackagingFormat: qoe.PackagingFormat(strings.TrimSpace(in.PackagingFormat)),
		VideoID:         videoID,
		TTFFMs:          in.TTFFMs,
		RebufferMs:      in.RebufferMs,
		RenditionHeight: in.RenditionHeight,
		Metadata:        in.Metadata,
	}
	if class := strings.TrimSpace(in.ErrorClass); class != "" {
		ec := qoe.ErrorClass(class)
		ev.ErrorClass = &ec
	}
	if raw := strings.TrimSpace(in.SessionID); raw != "" {
		sid, err := uuid.Parse(raw)
		if err != nil {
			return qoe.Event{}, errors.New("session_id must be a uuid")
		}
		ev.SessionID = sid
		ev.SessionVerified = s.verifyQoESession(in.PlaybackToken, videoID, sid)
	}
	if err := ev.Validate(); err != nil {
		return qoe.Event{}, err
	}
	return ev, nil
}

// verifyQoESession reports whether the beacon proved its session id.
//
// It can, for exactly the subjects that have a playback token at all: the token
// is HMAC-signed, scoped to this video, and carries the session id inside the
// signed payload, so a matching pair is attestation. Everything else — which is
// every public video, because core#74 deliberately mints no token for one — is
// client-asserted, and the row records that rather than pretending otherwise.
//
// A token that does not verify is not an error. The beacon is still recorded,
// marked unverified: rejecting it would turn an expired token (six hours is
// shorter than plenty of watches) into lost telemetry.
func (s *Server) verifyQoESession(token string, videoID, sessionID uuid.UUID) bool {
	if s.playbackSigner == nil || strings.TrimSpace(token) == "" {
		return false
	}
	claims, ok := s.playbackSigner.VerifyClaims(token, videoID, playback.ScopePlayback)
	return ok && claims.SessionID == sessionID
}

// qoeFieldMessage keeps a rejection informative without echoing client input
// back into a response body.
func qoeFieldMessage(err error) string {
	if errors.Is(err, qoe.ErrInvalid) {
		// qoe.ErrInvalid's messages are authored in that package and name a
		// field or a vocabulary, never a value.
		return strings.TrimPrefix(err.Error(), "qoe: invalid event: ")
	}
	return err.Error()
}

// qoeCollectionEnabled reports the runtime collection toggle. Default ON: unlike
// presign and CDN delivery, this involves no third party, no egress and no cost,
// and an install that measures nothing has no way to answer the one question
// this feature exists for. The switch exists because a switch that only an
// upgrade can flip is not a switch.
func (s *Server) qoeCollectionEnabled() bool {
	return s.settingBool(instancesettings.KeyQoECollectionEnabled, true)
}

// qoeRecorder is the ingest seam. *qoe.Service satisfies it; tests fake it.
type qoeRecorder interface {
	Record(ctx context.Context, e qoe.Event)
}
