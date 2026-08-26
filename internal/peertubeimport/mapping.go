package peertubeimport

import (
	"encoding/json"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/video"
)

// This file is the pure PeerTube→Vidra value mapping: enum translations and
// small helpers with no I/O, so they are exhaustively unit-testable.

// mapRole maps a PeerTube UserRole (0 ADMINISTRATOR, 1 MODERATOR, 2 USER) to a
// Vidra role. Unknown values fall back to the least-privileged "user".
func mapRole(ptRole int) string {
	switch ptRole {
	case 0:
		return "admin"
	case 1:
		return "moderator"
	default:
		return "user"
	}
}

// mapPrivacy maps a PeerTube VideoPrivacy (1 PUBLIC, 2 UNLISTED, 3 PRIVATE,
// 4 INTERNAL) to a Vidra privacy. INTERNAL has no Vidra equivalent and maps to
// the safest option, private. Unknown values default to private.
func mapPrivacy(ptPrivacy int) string {
	switch ptPrivacy {
	case 1:
		return "public"
	case 2:
		return "unlisted"
	default: // 3 PRIVATE, 4 INTERNAL, anything else
		return "private"
	}
}

// mapPlaylistPrivacy maps a PeerTube VideoPlaylistPrivacy (1 PUBLIC, 2 UNLISTED,
// 3 PRIVATE) to a Vidra playlist visibility.
func mapPlaylistPrivacy(ptPrivacy int) string {
	switch ptPrivacy {
	case 1:
		return "public"
	case 2:
		return "unlisted"
	default:
		return "private"
	}
}

// mapVideoState maps a PeerTube VideoState to a Vidra state. Only PeerTube's
// PUBLISHED (1) becomes 'published'; every other state (to-transcode, to-import,
// waiting-for-live, failed, …) becomes 'draft' — the video imports but is not
// exposed until the operator re-processes it.
func mapVideoState(ptState int) string {
	if ptState == 1 {
		return "published"
	}
	return "draft"
}

// intPtrToText renders an optional PeerTube numeric taxonomy id (category /
// licence) as the text form Vidra stores (migration 0025: "PeerTube-compatible
// numeric ids, as text"). nil stays nil (unset).
func intPtrToText(v *int) *string {
	if v == nil {
		return nil
	}
	s := strconv.Itoa(*v)
	return &s
}

// optTimestamptz wraps an optional source timestamp as the nullable column value
// Vidra stores. nil stays NULL, which is the ANSWER and not a missing one: for
// originally_published_at (migration 0119) it says the video was first published
// on the source itself, and coercing it to created_at or a zero time would claim
// an elsewhere that never existed.
func optTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// mapRating maps a PeerTube accountVideoRate.type to Vidra's video_ratings.rating
// CHECK. PeerTube writes 'like' and 'dislike'; anything else (a 'none' row left
// behind when a user cleared their rating, or a value from a schema this tool
// has not seen) is NOT a rating and is reported as unsupported rather than
// coerced into a like.
func mapRating(ptType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(ptType)) {
	case "like":
		return "like", true
	case "dislike":
		return "dislike", true
	default:
		return "", false
	}
}

// normalizeChapterTitle fits a source chapter title to video_chapters' CHECK
// (1..120 characters). It trims, then truncates by RUNE — the constraint counts
// characters, so a byte-slice would both cut a multi-byte title short of the
// limit and risk splitting a rune. "" means the source title was blank, which is
// not a chapter Vidra can store.
func normalizeChapterTitle(title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		return ""
	}
	const maxChapterTitle = 120
	if runes := []rune(t); len(runes) > maxChapterTitle {
		t = strings.TrimSpace(string(runes[:maxChapterTitle]))
	}
	return t
}

// mapActorImageKind maps PeerTube's ActorImageType (1 AVATAR, 2 BANNER) to the
// Vidra image kind. Anything else is a type this tool has not seen and is
// reported as unsupported rather than guessed into an avatar — a banner stored
// in the avatar slot is a visibly broken profile, not a near-miss.
func mapActorImageKind(ptType int) (string, bool) {
	switch ptType {
	case 1:
		return profileimage.KindAvatar, true
	case 2:
		return profileimage.KindBanner, true
	default:
		return "", false
	}
}

// deriveSourceOrigin picks the source instance's public origin out of its own
// actors' canonical URLs (https://host/accounts/alice → https://host).
//
// It takes the MAJORITY origin rather than the first one. Every local actor on a
// PeerTube instance carries the same origin by construction, so a disagreeing
// row is a leftover from a rename or a hand-edited database — and since this
// origin is the single host the import will make ~1,400 requests against, one
// odd row must not be able to point them all somewhere else. Ties break on the
// origin seen first, so the result is deterministic. "" means no local actor
// carried a usable absolute URL, which is the caller's signal that this source
// cannot be asked for its images.
func deriveSourceOrigin(urls []string) string {
	counts := map[string]int{}
	var order []string
	for _, raw := range urls {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" {
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		origin := u.Scheme + "://" + u.Host
		if counts[origin] == 0 {
			order = append(order, origin)
		}
		counts[origin]++
	}
	best, bestN := "", 0
	for _, origin := range order {
		if counts[origin] > bestN {
			best, bestN = origin, counts[origin]
		}
	}
	return best
}

// imageExtForSniffedType maps a SNIFFED content type onto the file extension the
// profile-image store accepts. It is deliberately narrower than "any image":
// Vidra's own avatar upload accepts JPEG, PNG and WebP only, so a source GIF or
// SVG is reported as unsupported instead of being stored as something the rest
// of the instance cannot serve. "" means "not an image Vidra stores".
func imageExtForSniffedType(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

// renditionHeight is the rung's pixel height: the height the source recorded for
// the stored file when it has one, otherwise the rung's resolution label, which
// for every non-audio PeerTube rung IS its height ("720" = 720 lines).
func renditionHeight(resolution, sourceHeight int) int {
	if sourceHeight > 0 {
		return sourceHeight
	}
	return resolution
}

// renditionWidth is the rung's pixel width, in descending order of how much the
// source actually told us:
//
//  1. the width recorded against the stored file, when the source records one;
//  2. height x the video's recorded aspect ratio, when the source records that;
//  3. height x 16:9.
//
// Only (3) is a guess, and it is a guess the schema forces: video_renditions
// CHECKs width > 0, so there is no "unknown" to store. It is confined to the
// width — the height, which is what a quality menu labels a rung with and what
// a player picks on, is never guessed. A 4:3 video imported from a source that
// records neither dimension gets a too-wide width and a correct 480p label.
func renditionWidth(height, sourceWidth int, aspectRatio float64) int {
	if sourceWidth > 0 {
		return sourceWidth
	}
	if height <= 0 {
		return 0
	}
	ratio := aspectRatio
	if ratio <= 0 {
		ratio = 16.0 / 9.0
	}
	w := int(math.Round(float64(height) * ratio))
	if w%2 != 0 {
		w++
	}
	if w < 2 {
		w = 2
	}
	return w
}

// ── the instance's category taxonomy ──

// pluginCategorySettingKeys are the settings keys the categories plugin stores
// its taxonomy under, most current first. The plugin's own name for it is
// json-categories-as-text; the older spelling is tried after it so a source that
// has not been through a plugin upgrade still reads.
var pluginCategorySettingKeys = []string{"json-categories-as-text", "json-categories"}

// maxCategoryLabel bounds a carried label. Nothing in the source constrains it
// and nothing in the setting's validator does either, so a pathological label
// would otherwise travel from a source database into a dropdown. 100 runes is
// far past any real category name.
const maxCategoryLabel = 100

// parsePluginCategories decodes a categories-plugin settings blob into the
// taxonomy it defines. ok=false means the blob is not one — unparseable, or
// carrying none of the keys the plugin uses — which reads as "this source has no
// custom taxonomy", never as a failure.
//
// The value under the key is JSON encoded TWICE: the settings column is JSON,
// and the taxonomy is stored in it as a STRING whose contents are themselves
// JSON. So it is decoded, then decoded again. A value that is already an object
// (a hand-edited row, a future plugin version) is accepted too, because
// insisting on the double encoding would be insisting on an implementation
// detail rather than on the data.
func parsePluginCategories(settings string) (SourceCategoryTaxonomy, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(settings), &top); err != nil {
		return SourceCategoryTaxonomy{}, false
	}
	for _, key := range pluginCategorySettingKeys {
		raw, ok := top[key]
		if !ok {
			continue
		}
		// The double encoding: unwrap one layer when the value is a JSON string.
		var inner string
		if err := json.Unmarshal(raw, &inner); err == nil {
			raw = json.RawMessage(inner)
		}
		var doc struct {
			Add []struct {
				Key   flexInt `json:"key"`
				Label string  `json:"label"`
			} `json:"add"`
			Delete []flexInt `json:"delete"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}
		var tax SourceCategoryTaxonomy
		for _, a := range doc.Add {
			tax.Add = append(tax.Add, SourceCategory{ID: int(a.Key), Label: a.Label})
		}
		for _, d := range doc.Delete {
			tax.Delete = append(tax.Delete, int(d))
		}
		if len(tax.Add) == 0 && len(tax.Delete) == 0 {
			continue // the key is there but says nothing; keep looking
		}
		return tax, true
	}
	return SourceCategoryTaxonomy{}, false
}

// flexInt reads a JSON id a plugin setting may hold either as a number (51) or
// as a string ("51"). It never fails: a value it cannot read becomes 0, which
// foldCategories drops. Erroring instead would cost the operator the WHOLE
// taxonomy over one malformed entry, which is the opposite of what this pass is
// for.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), `"`))
	if n, err := strconv.Atoi(s); err == nil {
		*f = flexInt(n)
	}
	return nil
}

// foldCategories folds a source taxonomy onto the built-in list and returns what
// the instance actually offers, ordered by id.
//
// Both halves matter. `delete` withdraws stock ids: an instance that deleted all
// eighteen and added its own offers ONLY its own, and carrying the additions
// without the deletions would leave the stock list showing alongside them.
// `add` defines the instance's own, and wins outright over a stock id it
// repeats — an add on a surviving id is that instance renaming it, which is a
// thing the plugin is used for.
//
// Entries with a non-positive id or an empty label are dropped: they cannot be
// stored (the setting's own validator refuses them), and a category with no name
// is not a category. Labels are collapsed to one line and bounded, because they
// end up in a dropdown.
func foldCategories(builtin []video.ConfigOption, tax SourceCategoryTaxonomy) []video.ConfigOption {
	deleted := make(map[int]bool, len(tax.Delete))
	for _, d := range tax.Delete {
		deleted[d] = true
	}
	labels := make(map[int]string, len(builtin)+len(tax.Add))
	for _, o := range builtin {
		id, err := strconv.Atoi(o.ID)
		if err != nil || deleted[id] {
			continue
		}
		labels[id] = o.Label
	}
	for _, a := range tax.Add {
		label := normalizeCategoryLabel(a.Label)
		if a.ID <= 0 || label == "" {
			continue
		}
		labels[a.ID] = label
	}
	ids := make([]int, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]video.ConfigOption, 0, len(ids))
	for _, id := range ids {
		out = append(out, video.ConfigOption{ID: strconv.Itoa(id), Label: labels[id]})
	}
	return out
}

// normalizeCategoryLabel fits a source label to a one-line dropdown entry:
// whitespace (including a newline a hand-edited plugin setting may carry)
// collapses to single spaces, and the result is truncated by RUNE so a
// multi-byte label is never split mid-character.
func normalizeCategoryLabel(label string) string {
	l := strings.Join(strings.Fields(label), " ")
	if runes := []rune(l); len(runes) > maxCategoryLabel {
		l = strings.TrimSpace(string(runes[:maxCategoryLabel]))
	}
	return l
}

// categoryEntries renders a taxonomy as the "<id>:<label>" entries the
// instance_custom_categories setting stores.
func categoryEntries(opts []video.ConfigOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.ID+":"+o.Label)
	}
	return out
}

// sameCategories reports whether two taxonomies are the same offer — same ids,
// same labels, same order. It is what decides that a source whose plugin ends up
// describing exactly the built-in list needs no override written at all.
func sameCategories(a, b []video.ConfigOption) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
