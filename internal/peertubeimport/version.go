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
// unless a human passes --force.
//
// The verified range below is pinned in .ralph/specs/peertube-reference.md. It
// covers PeerTube's 5.x–6.x schema line (approximate — operators should confirm
// against that ledger). The bounds are deliberately conservative: importing from
// an unverified schema risks reading columns that were added/renamed/removed, so
// it is a HARD STOP requiring operator sign-off — never an autonomous decision.
const (
	// MinSupportedSchemaVersion is the lowest PeerTube migrationVersion the
	// importer has been verified against.
	MinSupportedSchemaVersion = 700
	// MaxSupportedSchemaVersion is the highest verified migrationVersion. A source
	// newer than this is refused without --force (its schema may have diverged).
	MaxSupportedSchemaVersion = 900
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

// VersionError describes why a detected version was refused. It is a safe,
// operator-facing message (carries only the integer version, no source detail).
func VersionError(v int) error {
	switch ClassifyVersion(v) {
	case VersionSupported:
		return nil
	case VersionUnknown:
		return fmt.Errorf("peertubeimport: could not detect the source PeerTube schema version (empty or missing application table) — pass --force only if you have verified compatibility by hand")
	case VersionTooOld:
		return fmt.Errorf("peertubeimport: source schema version %d is older than the verified range [%d, %d] — pass --force only after verifying compatibility", v, MinSupportedSchemaVersion, MaxSupportedSchemaVersion)
	default: // VersionTooNew
		return fmt.Errorf("peertubeimport: source schema version %d is newer than the verified range [%d, %d] — pass --force only after verifying compatibility", v, MinSupportedSchemaVersion, MaxSupportedSchemaVersion)
	}
}
