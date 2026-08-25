package setup

import (
	"strconv"
	"strings"

	"github.com/vidra/vidra-core/internal/config"
)

// The PeerTube-source block: the one answer set in this engine that configures
// something the deployment READS FROM rather than something it runs.
//
// WHAT SETUP CAN AND CANNOT DO HERE, because the ordering is not negotiable.
// install.sh ends by running `vidra setup` and deliberately never runs
// `docker compose up`, so at the moment these questions are asked THE STACK DOES
// NOT EXIST. The import itself runs inside the api container, launched from
// POST /api/v1/admin/peertube-import. Setup therefore COLLECTS and WRITES these
// values and nothing else: every answer is shape-checked with the api's own
// validators (CheckPeerTube* in internal/config, called from here so there is no
// second opinion), and NOTHING dials the source database. A wizard that tried to
// prove the DSN works would be a wizard that hangs for a TCP timeout on a host
// whose firewall has not been opened yet, on the one question an operator cannot
// skip.
//
// The handoff is a URL, printed once the file is written: <origin>/admin/import-peertube.

// PeerTubeAnswers is the source instance a migration reads from. Enabled is the
// gate — it is PEERTUBE_IMPORT_ENABLED, which mounts the admin import surface —
// and everything else is only consulted when it is true.
//
// A nil *PeerTubeAnswers on Answers means UNANSWERED and leaves every key as it
// is, exactly like a nil Registration: a re-run about the domain must not
// reconfigure a migration in flight.
type PeerTubeAnswers struct {
	Enabled bool

	// DatabaseURL is the read-only DSN of the SOURCE PeerTube database. It
	// carries a password, so it is a secret everywhere it is handled: hidden at
	// the prompt, write-only across the wizard's wire, never printed by report().
	DatabaseURL string

	// StorageBackend is where the source instance's media lives: "local" (a
	// filesystem copy mounted into the api container) or "s3". "" keeps whatever
	// the file already says.
	StorageBackend string
	// LocalRoot is the directory that copy is mounted at, for the local backend.
	LocalRoot string
	// S3 addresses the source's object store, read-only. Same five fields as the
	// instance's own STORAGE_S3_* block and the same rules: the endpoint is a
	// HOST with no scheme, and the secret key is a secret.
	S3 S3Answers

	// MediaMode is copy|reference|none and ConflictPolicy is
	// skip|rename|merge|fail. Both are DEFAULTS for a run the operator launches
	// later from the admin UI, which may override either per import.
	MediaMode      string
	ConflictPolicy string
}

// peerTubeKeys are the keys the PeerTube answer owns, in the order they are
// appended to a template that does not define them. They are part of
// ManagedKeys() for the same reason the component keys are: they are this
// engine's own output, so a deployment carrying one must not be reported as
// having drifted from a template that predates it.
var peerTubeKeys = []string{
	peerTubeEnabledKey,
	"PEERTUBE_SOURCE_DATABASE_URL",
	"PEERTUBE_SOURCE_STORAGE_BACKEND",
	"PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT",
	"PEERTUBE_SOURCE_S3_ENDPOINT",
	"PEERTUBE_SOURCE_S3_BUCKET",
	"PEERTUBE_SOURCE_S3_ACCESS_KEY",
	"PEERTUBE_SOURCE_S3_SECRET_KEY",
	"PEERTUBE_SOURCE_S3_REGION",
	"PEERTUBE_IMPORT_MEDIA_MODE",
	"PEERTUBE_IMPORT_CONFLICT_POLICY",
}

// peerTubeEnabledKey is the gate, spelled once. PEERTUBE_SOURCE_S3_USE_SSL and
// PEERTUBE_SOURCE_S3_FORCE_PATH_STYLE are deliberately NOT answered by this
// engine — they are transport details of somebody else's object store with
// correct defaults (true / false, matching the primary STORAGE_S3_* pair), and
// the template documents them for the MinIO-shaped source that needs the other
// answer. Asking about them would add two questions to every migration to
// change nothing in almost all of them.
const peerTubeEnabledKey = "PEERTUBE_IMPORT_ENABLED"

// peerTubeAnswerValues is the answer set as concrete key/value pairs. Empty
// means unanswered throughout, as everywhere else in this package.
//
// TURNING THE IMPORT OFF DOES NOT ERASE THE SOURCE. Enabled=false writes the
// gate and stops there: the DSN and the source credentials stay in the file,
// exactly as applyStorageRule keeps real S3 credentials when an operator moves
// to local storage. A migration is run more than once — the schedule up to
// cutover is the documented shape — and a wizard that blanked the source every
// time the operator said "not right now" would make the second run a retyping
// exercise, with a password that exists nowhere else.
func peerTubeAnswerValues(p *PeerTubeAnswers, out map[string]string) {
	if p == nil {
		return
	}
	out[peerTubeEnabledKey] = strconv.FormatBool(p.Enabled)
	if !p.Enabled {
		return
	}
	for k, v := range map[string]string{
		"PEERTUBE_SOURCE_DATABASE_URL":       p.DatabaseURL,
		"PEERTUBE_SOURCE_STORAGE_BACKEND":    p.StorageBackend,
		"PEERTUBE_SOURCE_STORAGE_LOCAL_ROOT": p.LocalRoot,
		"PEERTUBE_SOURCE_S3_ENDPOINT":        p.S3.Endpoint,
		"PEERTUBE_SOURCE_S3_BUCKET":          p.S3.Bucket,
		"PEERTUBE_SOURCE_S3_ACCESS_KEY":      p.S3.AccessKey,
		"PEERTUBE_SOURCE_S3_SECRET_KEY":      p.S3.SecretKey,
		"PEERTUBE_SOURCE_S3_REGION":          p.S3.Region,
		"PEERTUBE_IMPORT_MEDIA_MODE":         p.MediaMode,
		"PEERTUBE_IMPORT_CONFLICT_POLICY":    p.ConflictPolicy,
	} {
		if v != "" {
			out[k] = v
		}
	}
}

// applyPeerTubeRule writes the answered source keys a template does not define,
// and returns the ones it appended.
//
// The appending is applyComponentRule's rule applied to the same failure. These
// thirteen keys reached the CONTAINER long before any template mentioned them —
// vidra-core's compose anchor has always carried them — so an operator wanting
// to migrate had to hand-edit a file `vidra setup` regenerates and `vidra setup
// --check` cannot validate. The template defines them now; a deployment tree
// still on an older one gets them written into the managed block rather than
// having its answers silently dropped, which is the failure this whole change
// is about.
func applyPeerTubeRule(req Request, answers map[string]string, res *Result) []string {
	p := req.Answers.PeerTube
	if p == nil {
		return nil
	}
	// "No, I am not migrating", asked against a template that never mentioned the
	// import and a file that does not either, is not a value worth writing down:
	// appending PEERTUBE_IMPORT_ENABLED=false under a "Managed by vidra setup"
	// header would be the engine announcing a default to every install that has
	// nothing to do with PeerTube.
	if !p.Enabled && !mentionsPeerTube(req.Existing) {
		return nil
	}
	var added []string
	for _, key := range peerTubeKeys {
		if _, defined := res.Values[key]; defined {
			continue
		}
		v := answers[key]
		if v == "" {
			if ev, ok := existingValue(req.Existing, key); ok {
				v = ev
			}
		}
		if strings.TrimSpace(v) == "" {
			continue
		}
		res.Values[key] = v
		added = append(added, key)
	}
	return added
}

// mentionsPeerTube reports whether a previous configuration says anything at
// all about a PeerTube source — the test for "this deployment has been here
// before", as opposed to one being asked the question for the first time.
func mentionsPeerTube(existing *EnvFile) bool {
	if existing == nil {
		return false
	}
	for _, key := range peerTubeKeys {
		if _, ok := existingValue(existing, key); ok {
			return true
		}
	}
	return false
}

// The per-answer validators, for a front end that has to reject a value AT THE
// PROMPT rather than after every other question has been answered. Each one is
// a wrapper on the api's OWN rule (internal/config), for the reason
// NormalizeOrigin states from the other side: a second implementation of
// "usable answer" is exactly how a wizard starts accepting what Generate then
// refuses.

// CheckPeerTubeSourceDatabaseURL validates the SHAPE of the source DSN and
// dials nothing — see the note at the top of this file for why it cannot.
func CheckPeerTubeSourceDatabaseURL(v string) error {
	return config.CheckPeerTubeSourceDatabaseURL(v)
}

// CheckPeerTubeSourceStorageBackend accepts local|s3 (and "" for unanswered).
func CheckPeerTubeSourceStorageBackend(v string) error {
	return config.CheckPeerTubeSourceStorageBackend(normalizePeerTubeAnswer(v))
}

// CheckPeerTubeSourceS3Endpoint rejects a value with a scheme in it, exactly as
// the instance's own STORAGE_S3_ENDPOINT is rejected.
func CheckPeerTubeSourceS3Endpoint(v string) error {
	return config.CheckPeerTubeSourceS3Endpoint(strings.TrimSpace(v))
}

// CheckPeerTubeMediaMode accepts copy|reference|none (and "" for unanswered).
func CheckPeerTubeMediaMode(v string) error {
	return config.CheckPeerTubeMediaMode(normalizePeerTubeAnswer(v))
}

// CheckPeerTubeConflictPolicy accepts skip|rename|merge|fail (and "" for
// unanswered).
func CheckPeerTubeConflictPolicy(v string) error {
	return config.CheckPeerTubeConflictPolicy(normalizePeerTubeAnswer(v))
}

// normalizePeerTubeAnswer applies config.Load's own normalisation — trim and
// lowercase — before the rule is asked. Without it a prompt would refuse `Copy`
// while the api accepts it, which is the same disagreement in the other
// direction.
func normalizePeerTubeAnswer(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}
