package peertubeimport

import "fmt"

// Entity kinds — the stable ledger entity_kind values and the report's per-kind
// keys. They also name what a --dry-run plan counts.
const (
	KindUser         = "user"
	KindChannel      = "channel"
	KindVideo        = "video"
	KindVideoFile    = "video_file"
	KindHLSPlaylist  = "hls_playlist"
	KindThumbnail    = "thumbnail"
	KindCaption      = "caption"
	KindTag          = "tag"
	KindComment      = "comment"
	KindPlaylist     = "playlist"
	KindPlaylistItem = "playlist_item"
	KindFollow       = "follow"
	// KindViewCount counts VIDEOS whose view total was carried, not views. One
	// source video contributes at most one to it per run, and contributes nothing
	// on a run where its total has not moved.
	KindViewCount = "view_count"
	KindChapter   = "chapter"
	KindRating    = "rating"
	KindRendition = "rendition"
)

// orderedKinds is the stable order entities are imported and reported in
// (parents before children).
var orderedKinds = []string{
	KindUser, KindChannel, KindVideo, KindVideoFile, KindHLSPlaylist, KindThumbnail,
	KindCaption, KindTag, KindViewCount, KindChapter, KindRating, KindRendition,
	KindComment, KindPlaylist, KindPlaylistItem, KindFollow,
}

// Counts tallies one entity kind's outcome. Planned is what a dry-run found;
// Imported/Skipped/Failed/Unsupported are the run's per-row results.
type Counts struct {
	Planned     int `json:"planned"`
	Imported    int `json:"imported"`
	Skipped     int `json:"skipped"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}

// Report is the machine-readable summary of a plan (dry-run) or a run. It is the
// JSON persisted to peertube_import_runs.progress and returned by the admin API.
// It carries NO secrets — only counts, the detected version, and safe conflict
// notes.
type Report struct {
	SourceVersion  int                `json:"source_version"`
	DryRun         bool               `json:"dry_run"`
	ConflictPolicy string             `json:"conflict_policy"`
	Entities       map[string]*Counts `json:"entities"`
	// Deferred lists entity families the importer intentionally does not migrate
	// in this version (e.g. HLS renditions, moderation state), so the operator
	// knows what to reconcile by hand.
	Deferred []string `json:"deferred,omitempty"`
	// Conflicts holds safe, human-readable notes about collisions the policy
	// resolved (renamed/skipped/merged). Never PII beyond the identifier involved.
	Conflicts []string `json:"conflicts,omitempty"`
}

// NewReport builds an empty report with every entity kind initialised (so the
// JSON shape is stable for the frontend contract).
func NewReport(dryRun bool, policy ConflictPolicy) *Report {
	r := &Report{
		DryRun:         dryRun,
		ConflictPolicy: policy.String(),
		Entities:       make(map[string]*Counts, len(orderedKinds)),
	}
	for _, k := range orderedKinds {
		r.Entities[k] = &Counts{}
	}
	return r
}

// count returns the counter for a kind, creating it if absent.
func (r *Report) count(kind string) *Counts {
	c := r.Entities[kind]
	if c == nil {
		c = &Counts{}
		r.Entities[kind] = c
	}
	return c
}

// Total sums a field across all entity kinds.
func (r *Report) totals() Counts {
	var t Counts
	for _, c := range r.Entities {
		t.Planned += c.Planned
		t.Imported += c.Imported
		t.Skipped += c.Skipped
		t.Failed += c.Failed
		t.Unsupported += c.Unsupported
	}
	return t
}

// Summary renders a SAFE one-line tally for audit events + logs (no secrets,
// only counts). Suitable as an audit reason.
func (r *Report) Summary() string {
	t := r.totals()
	kind := "run"
	if r.DryRun {
		kind = "dry-run"
	}
	return fmt.Sprintf("%s v%d policy=%s planned=%d imported=%d skipped=%d failed=%d",
		kind, r.SourceVersion, r.ConflictPolicy, t.Planned, t.Imported, t.Skipped, t.Failed)
}

// addConflict records a safe conflict note (capped to keep the JSON bounded).
func (r *Report) addConflict(note string) {
	const maxConflicts = 500
	if len(r.Conflicts) < maxConflicts {
		r.Conflicts = append(r.Conflicts, note)
	}
}
