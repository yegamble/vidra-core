// Package qoe measures what playback actually felt like: time to first frame,
// rebuffering, bitrate switches and playback errors, sliced by the delivery
// source that served the bytes.
//
// It is phase-4 delivery item 4 (docs/productionization/interfaces.md §9) and
// exists to answer one operator question — "an admin can see TTFF/rebuffer
// percentiles per source for the last 24h" — without which item 5 (IPFS
// delivery health/failover) has no falsifiable premise at all.
//
// # Shape: raw -> rollup -> prune
//
// §9 originally pointed at the searchevents outbox. It does not fit unmodified:
// search_outbox is an egress queue to an external service and prunes NOTHING —
// there is no DELETE against it anywhere in the tree. That is survivable at
// search volume and is not at playback volume. So:
//
//   - qoe_events holds individual measurements for 7 days. It is the incident
//     table: when a rollup shows a rebuffer spike, this still has the detail.
//   - qoe_rollups holds one row per (hour, delivery_source, engine,
//     packaging_format) for 90 days, with counts, precomputed p50/p95/p99 and
//     the histograms those came from.
//   - Prune enforces both windows in 10k-row batches, leader-elected.
//
// Percentiles do not merge, which is why the histograms are stored too: a p95
// over "the last 24h for source X" spans 24 hourly rows times every engine and
// packaging format, and averaging their p95s would be a number with no meaning.
// Summing their histograms is exact up to bucket resolution. See histogram.go.
//
// # Bounded cardinality is structural, not a convention
//
// Every dimension is a CLOSED vocabulary, validated at the API edge and CHECKed
// in the schema. An unknown value is REJECTED, not stored — including delivery
// source, which the server derives itself by classifying the origin the client
// reports (classify.go) so that an unrecognised host collapses into one 'other'
// bucket instead of becoming a value of its own. That is what caps a rollup hour
// at 6 x 4 x 3 = 72 rows no matter what arrives. None of this ever becomes a
// Prometheus label.
//
// # What is deliberately unknowable
//
// Two dimensions have no faithful value and are modelled as such rather than as
// nulls that look like bugs:
//
//   - The selected rendition is PERMANENTLY unknown on native HLS. The browser
//     owns variant selection through the manifest's SCORE attribute; the engine
//     adapter can neither read nor set the active variant. That is why engine is
//     part of the rollup key — an engine='native-hls' row with no bitrate
//     switches is correct, not missing — and why RenditionReportingSupported
//     exists to say so out loud in the admin view.
//   - A session id is only sometimes attestable. core#74 mints session ids and
//     records none, so on a public video the id in a beacon is CLIENT-ASSERTED.
//     On a password video (and a private live stream) it rides inside the
//     HMAC-signed playback token and IS verifiable. Every event records which it
//     was, and every rollup carries verified_count, so an admin can see what
//     fraction of a number is attested instead of being asked to assume all of
//     it is.
package qoe

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EventType is what a beacon reports. Closed set: an unknown type is a 400 at
// the edge, never a stored row.
type EventType string

const (
	// EventStart is the first frame rendering. It carries TTFFMs and is the
	// event a "playback" is counted by.
	EventStart EventType = "playback.start"
	// EventRebuffer is one stall that has ENDED, carrying how long it lasted. A
	// stall that is still in progress has no duration to report, so nothing is
	// sent until it finishes.
	EventRebuffer EventType = "playback.rebuffer"
	// EventBitrateSwitch is one ABR rung change. It never arrives from
	// EngineNativeHLS, by construction.
	EventBitrateSwitch EventType = "playback.bitrate_switch"
	// EventError is a playback failure, classed into the closed ErrorClass set.
	EventError EventType = "playback.error"
)

// EventTypes is the ingest allowlist, in schema order.
var EventTypes = []EventType{EventStart, EventRebuffer, EventBitrateSwitch, EventError}

// ValidEventType reports membership of the allowlist.
func ValidEventType(v EventType) bool {
	switch v {
	case EventStart, EventRebuffer, EventBitrateSwitch, EventError:
		return true
	}
	return false
}

// DeliverySource names which of the delivery chain's sources served the bytes.
// It mirrors delivery.SourceKind and adds the two members that chain has no
// reason to know about.
//
// This type is a STRING COPY of delivery.SourceKind's values rather than an
// import of it, deliberately: internal/delivery's import purity is load-bearing
// (it names no vendor and depends on no HTTP layer), and a telemetry package
// reaching into it to borrow four constants would be the first crack in that.
// The values are asserted equal to delivery's in qoe_test.go, so the copy cannot
// drift silently.
type DeliverySource string

const (
	// SourceAPIProxy is the authoritative path: bytes streamed through the Go
	// API. It is also what a same-origin or origin-relative URL classifies to.
	SourceAPIProxy DeliverySource = "api-proxy"
	// SourcePresigned is a signed object-store URL the viewer fetched directly.
	SourcePresigned DeliverySource = "presigned"
	// SourceCDN is the operator's configured edge.
	SourceCDN DeliverySource = "cdn"
	// SourceIPFSGateway is the configured IPFS gateway. Measuring THIS is item
	// 5's first deliverable: nobody has ever measured gateway TTFB for a
	// segment, and item 5's whole premise (health, priority, failover are worth
	// building) is unfalsifiable until somebody has.
	SourceIPFSGateway DeliverySource = "ipfs-gateway"
	// SourceOriginLive is the live plane's ephemeral segment path. RESERVED by
	// phase-4 item 7 and not yet produced by anything: live segments never enter
	// storage.Backend, have no ObjectKey and never reach the resolver, so no
	// origin classification can yield this value today. It is in the vocabulary
	// so that adding the live beacon later is a client change, not a migration.
	SourceOriginLive DeliverySource = "origin-live"
	// SourceOther is every origin the server does not recognise, collapsed into
	// ONE bucket. This member is the bounded-cardinality rule: without it, an
	// unrecognised host would either become a dimension value of its own
	// (unbounded) or be silently dropped (a blind spot). "Some of your traffic
	// came from somewhere I cannot name" is a fact worth having.
	SourceOther DeliverySource = "other"
)

// DeliverySources is the closed vocabulary, in schema order.
var DeliverySources = []DeliverySource{
	SourceAPIProxy, SourcePresigned, SourceCDN, SourceIPFSGateway, SourceOriginLive, SourceOther,
}

// ValidDeliverySource reports membership. Note that no client ever supplies one
// of these: the server derives it from the origin the client reports (see
// Classifier). This validates the derivation, not an input.
func ValidDeliverySource(v DeliverySource) bool {
	switch v {
	case SourceAPIProxy, SourcePresigned, SourceCDN, SourceIPFSGateway, SourceOriginLive, SourceOther:
		return true
	}
	return false
}

// Engine names which playback engine produced the measurement.
type Engine string

const (
	// EngineHLSJS is hls.js over MSE — the default VOD path, and the only one
	// that can report a selected rendition.
	EngineHLSJS Engine = "hls-js"
	// EngineNativeHLS is the browser's own HLS implementation (Safari, iOS).
	// Rendition identity has NO faithful value here; see the package comment.
	EngineNativeHLS Engine = "native-hls"
	// EngineProgressive is a plain <video src> against the original file: no
	// manifest, no ladder, one bitrate for the whole playback.
	EngineProgressive Engine = "progressive"
	// EngineShaka is RESERVED for item 3c's second engine (DASH/EME). Reserving
	// it costs one CHECK member and means the day it ships is not a migration.
	EngineShaka Engine = "shaka"
)

// Engines is the closed vocabulary, in schema order.
var Engines = []Engine{EngineHLSJS, EngineNativeHLS, EngineProgressive, EngineShaka}

// ValidEngine reports membership.
func ValidEngine(v Engine) bool {
	switch v {
	case EngineHLSJS, EngineNativeHLS, EngineProgressive, EngineShaka:
		return true
	}
	return false
}

// RenditionReportingSupported reports whether this engine can name the variant
// it is playing. False for native HLS, permanently — the browser owns variant
// selection via the manifest SCORE attribute and exposes no hook. The admin view
// reads this so that "no bitrate switches on Safari" renders as unsupported
// rather than as a suspiciously perfect number.
func RenditionReportingSupported(e Engine) bool {
	return e != EngineNativeHLS
}

// PackagingFormat is the manifest shape that was played. The first two match the
// values the transcode pipeline stores and the playback session advertises.
type PackagingFormat string

const (
	// FormatHLSTS is an HLS ladder over MPEG-TS segments.
	FormatHLSTS PackagingFormat = "hls-ts"
	// FormatCMAF is fMP4/CMAF segments, addressed by both an HLS playlist and a
	// DASH MPD.
	FormatCMAF PackagingFormat = "cmaf"
	// FormatProgressive is the no-manifest path: the original file, served
	// whole. It is not a packager output, which is exactly why it needs its own
	// member — folding it into hls-ts would attribute progressive playback's
	// (very different) TTFF to the HLS ladder.
	FormatProgressive PackagingFormat = "progressive"
)

// PackagingFormats is the closed vocabulary, in schema order.
var PackagingFormats = []PackagingFormat{FormatHLSTS, FormatCMAF, FormatProgressive}

// ValidPackagingFormat reports membership.
func ValidPackagingFormat(v PackagingFormat) bool {
	switch v {
	case FormatHLSTS, FormatCMAF, FormatProgressive:
		return true
	}
	return false
}

// ErrorClass buckets a playback failure. It is a CLASS and never a message: an
// engine's error string is unbounded, frequently carries a URL, and would be the
// one free-form dimension that undid every other bound in this package.
type ErrorClass string

const (
	// ErrorNetwork is a transport failure fetching a segment or manifest.
	ErrorNetwork ErrorClass = "network"
	// ErrorMedia is a decode/append failure — the bytes arrived and could not be
	// played.
	ErrorMedia ErrorClass = "media"
	// ErrorManifest is a manifest that could not be parsed or was inconsistent.
	ErrorManifest ErrorClass = "manifest"
	// ErrorDecrypt is a key/licence failure. Nothing produces it yet; it is
	// reserved for phase-5 DRM so that the day a licence request fails, the
	// failure has somewhere to be counted.
	ErrorDecrypt ErrorClass = "decrypt"
	// ErrorTimeout is a stall that never recovered.
	ErrorTimeout ErrorClass = "timeout"
	// ErrorOther is everything else, in one bucket, for the same reason
	// SourceOther exists.
	ErrorOther ErrorClass = "other"
)

// ErrorClasses is the closed vocabulary, in schema order.
var ErrorClasses = []ErrorClass{
	ErrorNetwork, ErrorMedia, ErrorManifest, ErrorDecrypt, ErrorTimeout, ErrorOther,
}

// ValidErrorClass reports membership.
func ValidErrorClass(v ErrorClass) bool {
	switch v {
	case ErrorNetwork, ErrorMedia, ErrorManifest, ErrorDecrypt, ErrorTimeout, ErrorOther:
		return true
	}
	return false
}

// maxMeasurementMs caps a reported duration at one hour. A TTFF or a rebuffer
// longer than that is not a measurement, it is a client that slept, a tab that
// was backgrounded, or a forged beacon — and admitting it would drag a p99 into
// meaninglessness. Rejecting is better than clamping: a clamped value is
// indistinguishable from a real 3,600,000 ms.
const maxMeasurementMs = 60 * 60 * 1000

// ErrInvalid is returned by Event.Validate for any vocabulary or range
// violation. The API edge turns it into a 400 naming the field.
var ErrInvalid = errors.New("qoe: invalid event")

// Event is one validated, server-enriched measurement, ready to be recorded.
//
// Identity fields (ViewerDigest, SessionVerified, DeliverySource) are set by the
// server and are NOT settable by a client: a beacon reports the URL origin it
// fetched from and the server classifies it, and a beacon never reports who it
// is at all.
type Event struct {
	Type            EventType
	DeliverySource  DeliverySource
	Engine          Engine
	PackagingFormat PackagingFormat

	// VideoID or LiveStreamID names the subject; at most one is set.
	VideoID      uuid.UUID
	LiveStreamID uuid.UUID

	// SessionID correlates a playback. SessionVerified is true only when the
	// server checked it against a signed playback token carrying the same id.
	SessionID       uuid.UUID
	SessionVerified bool

	// ViewerDigest is the keyed, day-scoped digest from digest.go. Never an IP.
	ViewerDigest string

	// TTFFMs is set on EventStart only. RebufferMs on EventRebuffer only.
	TTFFMs     *int32
	RebufferMs *int32

	// RenditionHeight is the rung in play, when the engine can name one. Always
	// nil for EngineNativeHLS.
	RenditionHeight *int32

	// ErrorClass is set on EventError only.
	ErrorClass *ErrorClass

	// Metadata is the allowlisted, size-capped extras map. Nil is normal.
	Metadata map[string]any
}

// Validate enforces every closed vocabulary and the per-type field contract. It
// is the single gate: the schema's CHECK constraints repeat it as defence in
// depth, but a constraint violation surfaces as a swallowed write in a
// best-effort recorder, which is no feedback at all. This runs at the edge so a
// client learns its value was rejected.
func (e Event) Validate() error {
	if !ValidEventType(e.Type) {
		return fmt.Errorf("%w: unsupported event type", ErrInvalid)
	}
	if !ValidDeliverySource(e.DeliverySource) {
		return fmt.Errorf("%w: unsupported delivery source", ErrInvalid)
	}
	if !ValidEngine(e.Engine) {
		return fmt.Errorf("%w: unsupported engine", ErrInvalid)
	}
	if !ValidPackagingFormat(e.PackagingFormat) {
		return fmt.Errorf("%w: unsupported packaging format", ErrInvalid)
	}
	if e.VideoID == uuid.Nil && e.LiveStreamID == uuid.Nil {
		return fmt.Errorf("%w: one of video_id or live_stream_id is required", ErrInvalid)
	}
	if e.VideoID != uuid.Nil && e.LiveStreamID != uuid.Nil {
		return fmt.Errorf("%w: video_id and live_stream_id are mutually exclusive", ErrInvalid)
	}
	if err := validMeasurement("ttff_ms", e.TTFFMs, e.Type == EventStart); err != nil {
		return err
	}
	if err := validMeasurement("rebuffer_ms", e.RebufferMs, e.Type == EventRebuffer); err != nil {
		return err
	}
	if e.RenditionHeight != nil {
		if *e.RenditionHeight <= 0 || *e.RenditionHeight > 8640 {
			return fmt.Errorf("%w: rendition_height out of range", ErrInvalid)
		}
		// Not an error to omit it — it is structurally absent on native HLS —
		// but a native-HLS beacon that CLAIMS one is reporting something the
		// browser does not expose, so it is not trustworthy.
		if e.Engine == EngineNativeHLS {
			return fmt.Errorf("%w: native-hls cannot report a rendition", ErrInvalid)
		}
	}
	if e.Type == EventError {
		if e.ErrorClass == nil || !ValidErrorClass(*e.ErrorClass) {
			return fmt.Errorf("%w: error_class is required and must be a known class", ErrInvalid)
		}
	} else if e.ErrorClass != nil {
		return fmt.Errorf("%w: error_class belongs only on playback.error", ErrInvalid)
	}
	return nil
}

// validMeasurement enforces "present exactly on the type that measures it, and
// inside the plausible range".
func validMeasurement(field string, v *int32, required bool) error {
	if required {
		if v == nil {
			return fmt.Errorf("%w: %s is required for this event type", ErrInvalid, field)
		}
		if *v < 0 || *v > maxMeasurementMs {
			return fmt.Errorf("%w: %s out of range", ErrInvalid, field)
		}
		return nil
	}
	if v != nil {
		return fmt.Errorf("%w: %s does not belong on this event type", ErrInvalid, field)
	}
	return nil
}

// HourBucket truncates t to the UTC hour a rollup is keyed by. UTC and not the
// server's local zone: a rollup key that shifted twice a year with daylight
// saving would produce a 23-hour and a 25-hour day, and the 25-hour one would
// have two rows claiming the same wall-clock hour.
func HourBucket(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }
