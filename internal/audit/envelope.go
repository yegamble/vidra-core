package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// CurrentSchemaVersion identifies the typed audit envelope introduced by
	// migration 0084. Rows written before that migration remain schema v1.
	CurrentSchemaVersion int16 = 2

	maxReasonBytes        = 512
	maxCorrelationIDBytes = 128
	maxResourceIDBytes    = 512
	maxMetadataFields     = 32
	maxMetadataValueBytes = 256
	maxChanges            = 32
	maxChangeValueBytes   = 256
	// Leave headroom below the DB's 8 KiB `jsonb::text` backstop because JSONB's
	// canonical text form may insert insignificant whitespace.
	maxJSONBytes = 7168
)

var (
	// ErrInvalidEvent means the envelope is structurally invalid. Audit writes are
	// best-effort today, so callers log this error without failing the business
	// action; transactional command coupling is a separate migration.
	ErrInvalidEvent = errors.New("audit: invalid event")

	actionPattern   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,159}$`)
	domainPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	resourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	idPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,511}$`)
	traceIDPattern  = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

	// Metadata is a deliberately small vocabulary of non-content operational
	// classifications. Identifiers belong in resource_id/job_id/pipeline_run_id;
	// user prose, URLs, payloads, headers and process output never belong here.
	allowedMetadataKeys = stringSet(
		"attempt", "auth_method", "changed_key", "changed_keys", "count",
		"dry_run", "mode", "network", "outcome", "policy", "provider",
		"reason_code", "reason_provided", "resolver", "source_version", "stage",
	)

	// Safe before/after values are limited to low-sensitivity state/config fields.
	// Content fields (title, description, comment text, email, URLs, CSS/JS, etc.)
	// are intentionally absent.
	allowedChangeFields = stringSet(
		"account_enabled", "bypass_quarantine", "comments_enabled",
		"download_enabled", "email_verified", "imports_enabled", "is_owner",
		"live_enabled", "privacy", "role", "state", "storage_quota_bytes",
		"uploads_enabled",
	)
)

// ActorSnapshot captures only durable, non-PII actor facts at event time. ID is a
// user UUID when Kind=user; Kind may also be anonymous, system, or service. Role
// is optional and restricted to Vidra's stable authorization roles. Never add an
// email, IP address, user-agent, display name, or token here.
type ActorSnapshot struct {
	ID   string
	Kind string
	Role string
}

// MetadataField is one allowlisted scalar audit attribute. The service encodes
// fields as a JSON object after validating both the key vocabulary and bounds.
type MetadataField struct {
	Key   string
	Value string
}

// Change is one allowlisted safe before/after difference. Values are bounded
// scalars, not arbitrary JSON. Empty Before/After represents an absent value.
type Change struct {
	Field  string `json:"field"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Event is the typed security-audit envelope. The legacy ActorID and RequestID
// fields remain so existing call sites keep compiling; Actor.ID takes precedence
// when set. Routine/high-volume content activity must use a separate activity
// stream, while job execution details belong in job_events.
type Event struct {
	ID            uuid.UUID
	SchemaVersion int16
	Domain        string
	Action        string
	Result        string

	Actor   ActorSnapshot
	ActorID string // compatibility alias for Actor.ID

	Reason        string
	RequestID     string
	CorrelationID string
	TraceID       string
	PipelineRunID uuid.UUID
	JobID         uuid.UUID
	ResourceType  string
	ResourceID    string
	Metadata      []MetadataField
	Changes       []Change
}

type normalizedEvent struct {
	Event
	metadataJSON []byte
	changesJSON  []byte
}

func normalizeEvent(ev Event) (normalizedEvent, error) {
	if ev.ID == uuid.Nil {
		ev.ID = uuid.New()
	}
	if ev.SchemaVersion == 0 {
		ev.SchemaVersion = CurrentSchemaVersion
	}
	if ev.SchemaVersion < 1 {
		return normalizedEvent{}, invalid("schema_version must be positive")
	}

	ev.Action = strings.TrimSpace(ev.Action)
	if !actionPattern.MatchString(ev.Action) {
		return normalizedEvent{}, invalid("action is not a stable dot-namespaced identifier")
	}
	if ev.Domain == "" {
		ev.Domain = domainFromAction(ev.Action)
	}
	ev.Domain = strings.TrimSpace(ev.Domain)
	if !domainPattern.MatchString(ev.Domain) {
		return normalizedEvent{}, invalid("domain is invalid")
	}
	if ev.Result != "success" && ev.Result != "failure" {
		return normalizedEvent{}, invalid("result must be success or failure")
	}

	if ev.Actor.ID != "" && ev.ActorID != "" && ev.Actor.ID != ev.ActorID {
		return normalizedEvent{}, invalid("actor snapshot id conflicts with legacy actor_id")
	}
	if ev.Actor.ID == "" {
		ev.Actor.ID = ev.ActorID
	}
	actorID := parseActor(ev.Actor.ID)
	if ev.Actor.Kind == "" {
		if actorID.Valid {
			ev.Actor.Kind = "user"
		} else {
			ev.Actor.Kind = "anonymous"
		}
	}
	if !validActorKind(ev.Actor.Kind) {
		return normalizedEvent{}, invalid("actor kind is invalid")
	}
	if !validActorRole(ev.Actor.Role) {
		return normalizedEvent{}, invalid("actor role is invalid")
	}
	if ev.Actor.Kind == "user" && !actorID.Valid {
		return normalizedEvent{}, invalid("user actor id must be a valid UUID")
	}
	if ev.Actor.Kind != "user" && ev.Actor.ID != "" {
		return normalizedEvent{}, invalid("anonymous, system, and service actors cannot carry a user id")
	}
	if ev.Actor.Kind != "user" && ev.Actor.Role != "" {
		return normalizedEvent{}, invalid("only user actors may have a role")
	}
	// Preserve the legacy alias for repository adapters and old tests.
	ev.ActorID = ev.Actor.ID

	ev.Reason = truncateUTF8(strings.TrimSpace(ev.Reason), maxReasonBytes)
	if err := validateCorrelationID("request_id", ev.RequestID); err != nil {
		return normalizedEvent{}, err
	}
	if err := validateCorrelationID("correlation_id", ev.CorrelationID); err != nil {
		return normalizedEvent{}, err
	}
	if ev.TraceID != "" && !traceIDPattern.MatchString(ev.TraceID) {
		return normalizedEvent{}, invalid("trace_id must be a 32-character hexadecimal trace id")
	}
	if ev.ResourceType == "" && ev.ResourceID != "" {
		return normalizedEvent{}, invalid("resource_type is required when resource_id is set")
	}
	if ev.ResourceType != "" && !resourcePattern.MatchString(ev.ResourceType) {
		return normalizedEvent{}, invalid("resource_type is invalid")
	}
	if len(ev.ResourceID) > maxResourceIDBytes || (ev.ResourceID != "" && !idPattern.MatchString(ev.ResourceID)) {
		return normalizedEvent{}, invalid("resource_id must be a bounded opaque identifier, not a URL or payload")
	}

	metadataJSON, err := encodeMetadata(ev.Metadata)
	if err != nil {
		return normalizedEvent{}, err
	}
	changesJSON, err := encodeChanges(ev.Changes)
	if err != nil {
		return normalizedEvent{}, err
	}
	return normalizedEvent{Event: ev, metadataJSON: metadataJSON, changesJSON: changesJSON}, nil
}

func domainFromAction(action string) string {
	if i := strings.IndexByte(action, '.'); i > 0 {
		return action[:i]
	}
	return "legacy"
}

func validActorKind(v string) bool {
	switch v {
	case "anonymous", "user", "system", "service":
		return true
	default:
		return false
	}
}

func validActorRole(v string) bool {
	switch v {
	case "", "user", "moderator", "admin":
		return true
	default:
		return false
	}
}

func validateCorrelationID(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maxCorrelationIDBytes || !idPattern.MatchString(value) {
		return invalid(name + " must be a bounded URL-safe identifier")
	}
	return nil
}

func encodeMetadata(fields []MetadataField) ([]byte, error) {
	if len(fields) > maxMetadataFields {
		return nil, invalid("too many metadata fields")
	}
	obj := make(map[string]string, len(fields))
	for _, f := range fields {
		if !allowedMetadataKeys[f.Key] {
			return nil, invalid(fmt.Sprintf("metadata key %q is not allowlisted", f.Key))
		}
		if _, duplicate := obj[f.Key]; duplicate {
			return nil, invalid(fmt.Sprintf("metadata key %q is duplicated", f.Key))
		}
		if err := validateSafeScalar("metadata value", f.Value, maxMetadataValueBytes); err != nil {
			return nil, err
		}
		obj[f.Key] = f.Value
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, invalid("metadata cannot be encoded")
	}
	if len(b) > maxJSONBytes {
		return nil, invalid("metadata exceeds the encoded size limit")
	}
	return b, nil
}

func encodeChanges(changes []Change) ([]byte, error) {
	if len(changes) > maxChanges {
		return nil, invalid("too many changes")
	}
	if len(changes) == 0 {
		return []byte("[]"), nil
	}
	seen := make(map[string]bool, len(changes))
	for i := range changes {
		c := &changes[i]
		if !allowedChangeFields[c.Field] {
			return nil, invalid(fmt.Sprintf("change field %q is not allowlisted", c.Field))
		}
		if seen[c.Field] {
			return nil, invalid(fmt.Sprintf("change field %q is duplicated", c.Field))
		}
		seen[c.Field] = true
		if err := validateSafeScalar("change before value", c.Before, maxChangeValueBytes); err != nil {
			return nil, err
		}
		if err := validateSafeScalar("change after value", c.After, maxChangeValueBytes); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(changes)
	if err != nil {
		return nil, invalid("changes cannot be encoded")
	}
	if len(b) > maxJSONBytes {
		return nil, invalid("changes exceed the encoded size limit")
	}
	return b, nil
}

// validateSafeScalar rejects value shapes most likely to contain content, URLs,
// headers, multiline process output, or PII. The key allowlists remain the main
// defense; this is a second boundary for accidental misuse.
func validateSafeScalar(name, value string, maxBytes int) error {
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return invalid(name + " is not valid UTF-8 or exceeds its size limit")
	}
	if strings.ContainsAny(value, "\r\n\t/@?\\{}[]\"") || strings.Contains(strings.ToLower(value), "bearer ") {
		return invalid(name + " must be a classification, count, enum, or opaque id; URLs/content are forbidden")
	}
	return nil
}

func truncateUTF8(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "")
	for len(value) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalidEvent, message) }

func stringSet(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
