package peertubeimport

import "fmt"

// Entity kinds — the stable ledger entity_kind values and the report's per-kind
// keys. They also name what a --dry-run plan counts.
const (
	// KindCategoryTaxonomy is the INSTANCE's category list, not an entity of the
	// catalogue: at most one per run, and 0 for the great majority of sources,
	// which offer the stock taxonomy and need no override written.
	KindCategoryTaxonomy = "category_taxonomy"
	KindUser             = "user"
	KindChannel          = "channel"
	KindVideo            = "video"
	KindVideoFile        = "video_file"
	KindHLSPlaylist      = "hls_playlist"
	// KindThumbnail / KindStoryboard count VIDEOS whose poster and seek-preview
	// sprite sheet were carried — one source row each per video, which is what
	// the source reads already reduce to. They are two kinds rather than one for
	// the same reason avatars and banners are: they answer different questions
	// ("did the posters come across?" is the one an operator asks first) and a
	// source with no storyboards at all must not dilute the poster count.
	KindThumbnail    = "thumbnail"
	KindStoryboard   = "storyboard"
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
	// KindActorAvatar / KindActorBanner count the source's actorImage rows,
	// split by which slot they fill. They are two kinds rather than one so the
	// report answers the question an operator actually asks after a migration —
	// "did the avatars come across?" — without the banners diluting the count.
	KindActorAvatar = "actor_avatar"
	KindActorBanner = "actor_banner"
)

// orderedKinds is the stable order entities are imported and reported in
// (parents before children).
var orderedKinds = []string{
	KindCategoryTaxonomy,
	KindUser, KindChannel, KindActorAvatar, KindActorBanner, KindVideo, KindVideoFile,
	KindHLSPlaylist, KindThumbnail, KindStoryboard, KindCaption, KindTag, KindViewCount,
	KindChapter, KindRating, KindRendition, KindComment, KindPlaylist, KindPlaylistItem,
	KindFollow,
}

// Counts tallies one entity kind's outcome. Planned is what a dry-run found;
// Imported/Updated/Skipped/Failed/Unsupported are the run's per-row results.
type Counts struct {
	Planned  int `json:"planned"`
	Imported int `json:"imported"`
	// Updated counts rows that ALREADY EXISTED and were changed to match the
	// source — only ever non-zero under --source-authoritative, and the number an
	// operator running that mode actually needs: "imported" answers what came
	// across, this answers what this run altered on an instance that was already
	// live. It is deliberately a separate counter and not folded into Imported,
	// because "0 imported, 4,113 updated" and "4,113 imported" describe very
	// different runs.
	Updated     int `json:"updated"`
	Skipped     int `json:"skipped"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}

// Report is the machine-readable summary of a plan (dry-run) or a run. It is the
// JSON persisted to peertube_import_runs.progress and returned by the admin API.
// It carries NO secrets — only counts, the detected version, and safe conflict
// notes.
type Report struct {
	SourceVersion  int    `json:"source_version"`
	DryRun         bool   `json:"dry_run"`
	ConflictPolicy string `json:"conflict_policy"`
	// SourceAuthoritative records which side won on this run. It is on the report
	// rather than only on the run row because the report is what gets read months
	// later when somebody asks why a title changed.
	SourceAuthoritative bool               `json:"source_authoritative"`
	Entities            map[string]*Counts `json:"entities"`
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
func NewReport(dryRun bool, policy ConflictPolicy, sourceAuthoritative bool) *Report {
	r := &Report{
		DryRun:              dryRun,
		ConflictPolicy:      policy.String(),
		SourceAuthoritative: sourceAuthoritative,
		Entities:            make(map[string]*Counts, len(orderedKinds)),
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
		t.Updated += c.Updated
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
	if r.SourceAuthoritative {
		kind += " source-authoritative"
	}
	return fmt.Sprintf("%s v%d policy=%s planned=%d imported=%d updated=%d skipped=%d failed=%d",
		kind, r.SourceVersion, r.ConflictPolicy, t.Planned, t.Imported, t.Updated, t.Skipped, t.Failed)
}

// addConflict records a safe conflict note (capped to keep the JSON bounded).
func (r *Report) addConflict(note string) {
	const maxConflicts = 500
	if len(r.Conflicts) < maxConflicts {
		r.Conflicts = append(r.Conflicts, note)
	}
}
