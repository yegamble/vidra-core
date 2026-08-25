// Package peertubeimport implements the one-way PeerTube → Vidra migration tool
// (fix_plan P18, .ralph/specs/peertube-import.md): it reads an existing PeerTube
// instance's PostgreSQL database and media storage (READ-ONLY on the source) and
// maps them into Vidra. The import is idempotent and resumable (a durable ledger
// maps each source row to its Vidra id), dry-runnable, and audited. Source
// credentials are secrets and are never logged.
//
// PeerTube is treated as a data source to READ, never as code to copy.
package peertubeimport

import "fmt"

// PeerTube records its schema version as an integer in the `application` table's
// `migrationVersion` column (bumped once per Sequelize migration). Preflight
// reads it and refuses to run against a version outside the verified range
// unless a human signs off — on the CLI with --force, through the admin API by
// acknowledging the DETECTED version on the launch request (AcknowledgesVersion).
//
// The verified range below is pinned in .ralph/specs/peertube-reference.md. It
// covers PeerTube's 5.x–8.x schema line (approximate — operators should confirm
// against that ledger). The bounds are deliberately conservative: importing from
// an unverified schema risks reading columns that were added/renamed/removed, so
// it is a HARD STOP requiring operator sign-off — never an autonomous decision.
const (
	// MinSupportedSchemaVersion is the lowest PeerTube migrationVersion the
	// importer has been verified against.
	MinSupportedSchemaVersion = 700
	// MaxSupportedSchemaVersion is the highest verified migrationVersion. A source
	// newer than this is refused without --force (its schema may have diverged).
	MaxSupportedSchemaVersion = 1000
)

// VersionSupport classifies a detected source schema version.
type VersionSupport int

const (
	// VersionSupported means the version is within the verified range.
	VersionSupported VersionSupport = iota
	// VersionTooOld means the version predates the verified range.
	VersionTooOld
	// VersionTooNew means the version is newer than the verified range.
	VersionTooNew
	// VersionUnknown means no version could be detected (empty application table).
	VersionUnknown
)

// ClassifyVersion reports whether a detected migrationVersion is supported. A
// zero or negative value is treated as unknown (no version row).
func ClassifyVersion(v int) VersionSupport {
	switch {
	case v <= 0:
		return VersionUnknown
	case v < MinSupportedSchemaVersion:
		return VersionTooOld
	case v > MaxSupportedSchemaVersion:
		return VersionTooNew
	default:
		return VersionSupported
	}
}

// IsSupported is a convenience predicate: true only for a version inside the
// verified range.
func IsSupported(v int) bool { return ClassifyVersion(v) == VersionSupported }

// Stable snake_case classes for a version refusal, in the same vocabulary as the
// error envelope's `code` and operational_job_runs.error_code. They exist so a
// CLIENT can tell the one refusal an administrator is allowed to overrule from
// every other reason a run can stop. Prose cannot carry that distinction: the
// admin UI was left string-matching English or, in practice, doing nothing.
const (
	// CodeUnverifiedSchema — a version WAS detected and sits outside the verified
	// range. An administrator may acknowledge it (see AcknowledgesVersion).
	CodeUnverifiedSchema = "unverified_schema"
	// CodeUndetectableSchema — no version could be read at all (empty or missing
	// application table). There is no number to put in front of an administrator,
	// so this one is NOT acknowledgeable through the API; it needs the CLI and a
	// human who has inspected the source by hand.
	CodeUndetectableSchema = "undetectable_schema"
)

// UnverifiedSchemaError is the version gate's refusal. It carries the detected
// version and a stable code alongside the operator-facing prose, and nothing
// about the source but that integer — the same safety property the message
// always had, now in a shape a client can branch on.
type UnverifiedSchemaError struct {
	// Version is the detected migrationVersion; <= 0 when none could be read.
	Version int
	// Support is how Version was classified against the verified range.
	Support VersionSupport
	msg     string
}

// Error renders the safe, operator-facing message.
func (e *UnverifiedSchemaError) Error() string { return e.msg }

// Code returns the stable snake_case class of this refusal.
func (e *UnverifiedSchemaError) Code() string {
	if e.Support == VersionUnknown {
		return CodeUndetectableSchema
	}
	return CodeUnverifiedSchema
}

// Acknowledgeable reports whether an administrator can sign this refusal off
// through the API. Only a refusal that names a version can be: an acknowledgement
// is a statement ABOUT a specific number, and there is no number here otherwise.
func (e *UnverifiedSchemaError) Acknowledgeable() bool { return e.Support != VersionUnknown }

// VersionError describes why a detected version was refused. It is a safe,
// operator-facing message (carries only the integer version, no source detail),
// and always an *UnverifiedSchemaError when non-nil.
func VersionError(v int) error {
	support := ClassifyVersion(v)
	var msg string
	switch support {
	case VersionSupported:
		return nil
	case VersionUnknown:
		msg = "peertubeimport: could not detect the source PeerTube schema version (empty or missing application table) — verify compatibility by hand and re-run the CLI with --force"
	case VersionTooOld:
		msg = fmt.Sprintf("peertubeimport: source schema version %d is older than the verified range [%d, %d] — the importer has not been verified against it and may read columns that no longer exist", v, MinSupportedSchemaVersion, MaxSupportedSchemaVersion)
	default: // VersionTooNew
		msg = fmt.Sprintf("peertubeimport: source schema version %d is newer than the verified range [%d, %d] — the importer has not been verified against it and may read columns that were renamed or removed", v, MinSupportedSchemaVersion, MaxSupportedSchemaVersion)
	}
	return &UnverifiedSchemaError{Version: v, Support: support, msg: msg}
}

// AcknowledgesVersion reports whether a per-run operator acknowledgement covers
// the version preflight actually detected.
//
// The acknowledgement is a VERSION, not a boolean, and this equality is the
// entire reason. A boolean can be set by a caller that never looked at the
// source, carried forward from a previous run, or defaulted on by a config key;
// a number cannot be produced without having seen the refusal that named it. It
// also expires by itself: an operator who signs off on 1040 and then upgrades the
// source to 1055 mid-migration gets the gate back, rather than a sign-off
// silently applying to a schema nobody looked at.
//
// detected <= 0 never matches, so a source whose version could not be read at all
// can never be acknowledged this way — see CodeUndetectableSchema.
func AcknowledgesVersion(acknowledged, detected int) bool {
	return detected > 0 && acknowledged == detected
}
