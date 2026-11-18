package federation

// PeerTube v8.3.1 inbound-Video conformance (T05).
//
// A PeerTube instance that follows a vidra channel creates a remote video by
// FETCHING the object id (lib/activitypub/videos/get.ts →
// shared/url-to-object.ts:12-14) and running it through
// sanitizeAndCheckVideoTorrentObject. Anything that fails a clause is dropped
// with a debug log and nothing else, so "PeerTube ingests none of our videos"
// is invisible from both sides. peertubeCheck below ports that validator clause
// by clause — each comment names the PeerTube v8.3.1 file:line it mirrors — and
// the tests run it over what VideoObject() actually serves.
//
// This is conformance BY TEST against a port, not interop proven against a
// live PeerTube. validator.js semantics are reproduced for the inputs vidra
// emits; where the port narrows one (ISO 8601 → RFC 3339, isURL → the
// http(s)+host+TLD subset) the comment says so.

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ptVerdict is what PeerTube makes of one object.
type ptVerdict struct {
	// Reject is the first failing clause ("" = accepted).
	Reject string
	// Object is the object AFTER PeerTube's in-place sanitizers ran: a url, an
	// icon or a caption they filtered out is simply gone, which is how a video
	// can be "accepted" and still unplayable.
	Object map[string]any
	// Defaulted lists the fields PeerTube overwrote with its own default
	// because ours was absent or invalid (videos.ts:63-78).
	Defaulted []string
}

// peertubeCheck ports sanitizeAndCheckVideoTorrentObject (videos.ts:45-109).
func peertubeCheck(t *testing.T, obj map[string]any) ptVerdict {
	t.Helper()
	// Round-trip through JSON so every value has the type PeerTube sees.
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	out := ptVerdict{Object: v}
	fail := func(clause string) ptVerdict { out.Reject = clause; return out }

	if v["type"] != "Video" { // videos.ts:46
		return fail("type")
	}
	ptSetValidRemoteTags(v)            // videos.ts:53 (always true)
	if !ptSetValidRemoteVideoUrls(v) { // videos.ts:54
		return fail("urls")
	}
	if c, ok := v["content"].(string); ok && utf8.RuneCountInString(c) > 10000 { // videos.ts:55 → 252-258
		v["content"] = string([]rune(c)[:10000])
	}
	ptSetValidAttributedTo(v)         // videos.ts:56 → misc.ts:39-46
	if !ptSetValidRemoteCaptions(v) { // videos.ts:57
		return fail("captions")
	}
	ptSetValidRemoteIcon(v)                             // videos.ts:58 → misc.ts:48-72
	if icons, _ := v["icon"].([]any); len(icons) == 0 { // videos.ts:58 `video.icon.length === 0`
		return fail("icons")
	}
	if p, ok := v["preview"]; ok && p != nil { // videos.ts:59 → 273-280
		if _, isArr := p.([]any); !isArr {
			return fail("preview (storyboard)")
		}
	}
	if !ptSetValidLicence(v) { // videos.ts:60 → 260-271
		return fail("licence")
	}

	// Default attributes (videos.ts:63-78): never a rejection, but a value we
	// emitted and PeerTube discarded is a silent loss the tests must see.
	if !ptIsVideoStateValid(v["state"]) { // videos.ts:63
		out.Defaulted = append(out.Defaulted, "state")
	}
	for _, k := range []string{"waitTranscoding", "downloadEnabled", "isLiveBroadcast", "liveSaveReplay", "permanentLive", "sensitive"} { // videos.ts:64-69
		if !ptIsBooleanValid(v[k]) {
			out.Defaulted = append(out.Defaulted, k)
		}
	}
	if cp := v["commentsPolicy"]; !ptTruthy(cp) || !ptIsCommentsPolicyValid(cp) { // videos.ts:72-78
		out.Defaulted = append(out.Defaulted, "commentsPolicy")
	}

	if !ptIsAPURL(v["id"]) { // videos.ts:80
		return fail("id")
	}
	if !ptIsLength(v["name"], 3, 120) { // videos.ts:81 → videos.ts(validators):58-60, constants.ts:519
		return fail("name")
	}
	d, _ := v["duration"].(string)
	if !strings.HasPrefix(d, "PT") || !strings.HasSuffix(d, "S") { // videos.ts:83 → misc.ts:74-80
		return fail("duration format")
	}
	if !ptIsInt(ptDurationFromActivityStream(d), 0) { // videos.ts:84
		return fail("duration")
	}
	if !ptIsUUIDv4(v["uuid"]) { // videos.ts:89 → misc.ts:65-67
		return fail("uuid")
	}
	if c, ok := v["category"]; ok && c != nil && !ptIdentifierIsInt(c) { // videos.ts:91 → 215-217
		return fail("category")
	}
	if l, ok := v["language"]; ok && l != nil { // videos.ts:92 → 219-221
		if m, _ := l.(map[string]any); m == nil || !ptIsString(m["identifier"]) {
			return fail("language")
		}
	}
	if !ptIsInt(v["views"], 0) { // videos.ts:94 → videos.ts(validators):78-80
		return fail("views")
	}
	if !ptIsDate(v["published"]) { // videos.ts:95
		return fail("published")
	}
	if !ptIsDate(v["updated"]) { // videos.ts:96
		return fail("updated")
	}
	for _, k := range []string{"originallyPublishedAt", "uploadDate"} { // videos.ts:98-99
		if x, ok := v[k]; ok && x != nil && !ptIsDate(x) {
			return fail(k)
		}
	}
	if c, ok := v["content"]; ok && c != nil { // videos.ts:100 → 223-225
		mt := v["mediaType"]
		if (mt != "text/markdown" && mt != "text/html") || !ptIsLength(c, 3, 10000) {
			return fail("mediaType/content")
		}
	}
	for _, k := range []string{"audience", "embedUrl"} { // videos.ts:102-104
		if x, ok := v[k]; ok && x != nil && !ptIsAPURL(x) {
			return fail(k)
		}
	}
	if at, _ := v["attributedTo"].([]any); len(at) == 0 { // videos.ts:106
		return fail("attributedTo")
	}
	return out
}

// ptDurationFromActivityStream ports activity.ts:17-19,
// parseInt(duration.replace(/[^\d]+/, ”)): a NON-global replace removes only
// the FIRST non-digit run, then parseInt takes the leading digits (NaN → nil).
func ptDurationFromActivityStream(d string) any {
	if loc := regexp.MustCompile(`[^\d]+`).FindStringIndex(d); loc != nil {
		d = d[:loc[0]] + d[loc[1]:]
	}
	s := d
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == 0 {
		return nil
	}
	n, err := strconv.ParseFloat(s[:j], 64)
	if err != nil {
		return nil
	}
	return n
}

// --- sanitizers ------------------------------------------------------------

// videos.ts:170-185
func ptSetValidRemoteTags(v map[string]any) {
	tags, ok := v["tag"].([]any)
	if !ok {
		tags = []any{}
	}
	if len(tags) > 100 { // constants.ts:992 MAX_VIDEO_TAGS
		tags = tags[:100]
	}
	kept := []any{}
	for _, raw := range tags {
		tg, _ := raw.(map[string]any)
		if tg == nil {
			continue
		}
		if (tg["type"] == "Hashtag" && ptIsLength(tg["name"], 2, 30)) || // constants.ts:535 TAG
			(tg["type"] == "SensitiveTag" && ptTruthy(tg["name"])) {
			kept = append(kept, tg)
		}
	}
	v["tag"] = kept
}

// videos.ts:227-250
func ptSetValidRemoteVideoUrls(v map[string]any) bool {
	urls, ok := v["url"].([]any)
	if !ok {
		return false
	}
	if len(urls) > 100 { // constants.ts:991 MAX_VIDEO_URLS
		urls = urls[:100]
	}
	kept := []any{}
	for _, raw := range urls {
		if u, _ := raw.(map[string]any); u != nil && ptIsRemoteVideoURLValid(u) {
			kept = append(kept, u)
		}
	}
	v["url"] = kept
	return true
}

// videos.ts:111-141. JS precedence: `type === 'Link' && (file)` binds tighter
// than the `||` chain, so only the video-FILE arm requires type Link.
func ptIsRemoteVideoURLValid(u map[string]any) bool {
	mt, _ := u["mediaType"].(string)
	apVideo := map[string]bool{"video/mp4": true, "video/ogg": true, "video/webm": true, "audio/mp4": true} // constants.ts:930-937
	file := apVideo[mt] && ptIsAPURL(u["href"]) && ptIsInt(u["height"], 0) && ptIsInt(u["size"], 0) &&
		(!ptTruthy(u["fps"]) || ptIsInt(u["fps"], -1))
	torrent := mt == "application/x-bittorrent" && ptIsAPURL(u["href"]) && ptIsInt(u["height"], 0)
	magnet := mt == "application/x-bittorrent;x-scheme-handler/magnet" && ptIsLength(u["href"], 5, math.MaxInt) && ptIsInt(u["height"], 0)
	hlsType := mt
	if hlsType == "" {
		hlsType, _ = u["mimeType"].(string)
	}
	_, tagIsArray := u["tag"].([]any)
	hls := hlsType == "application/x-mpegURL" && ptIsAPURL(u["href"]) && tagIsArray // videos.ts:133-138
	rel, _ := u["rel"].([]any)
	metadata := u["type"] == "Link" && mt == "application/json" && ptContains(rel, "metadata") // videos.ts:143-147
	tracker := ptContains(rel, "tracker") && ptIsAPURL(u["href"])                              // videos.ts:149-153 (http(s) subset)
	return (u["type"] == "Link" && file) || torrent || magnet || hls || tracker || metadata
}

// misc.ts:39-46
func ptSetValidAttributedTo(v map[string]any) {
	kept := []any{}
	for _, a := range ptArrayify(v["attributedTo"]) {
		if ptIsAPURL(a) {
			kept = append(kept, a)
			continue
		}
		if m, _ := a.(map[string]any); m != nil && (m["type"] == "Group" || m["type"] == "Person") && ptIsAPURL(m["id"]) {
			kept = append(kept, a)
		}
	}
	v["attributedTo"] = kept
}

// videos.ts:187-213, with setAPCaptionUrlObject (videos.ts:155-164).
func ptSetValidRemoteCaptions(v map[string]any) bool {
	sl := v["subtitleLanguage"]
	if !ptTruthy(sl) {
		sl = []any{}
	}
	caps, ok := sl.([]any)
	if !ok {
		return false
	}
	kept := []any{}
	for _, raw := range caps {
		c, _ := raw.(map[string]any)
		if c == nil {
			continue
		}
		var links []any
		if s, isStr := c["url"].(string); isStr {
			if ptIsAPURL(s) {
				links = []any{map[string]any{"type": "Link", "href": s, "mediaType": "text/vtt"}}
			}
		} else {
			for _, l := range ptArrayify(c["url"]) {
				lm, _ := l.(map[string]any)
				if lm == nil || lm["type"] != "Link" || !ptIsAPURL(lm["href"]) {
					continue
				}
				if !ptTruthy(lm["mediaType"]) && strings.HasSuffix(lm["href"].(string), ".vtt") {
					lm["mediaType"] = "text/vtt"
				}
				if lm["mediaType"] == "text/vtt" || lm["mediaType"] == "application/x-mpegURL" {
					links = append(links, lm)
				}
			}
		}
		c["url"] = links
		if len(links) > 0 && ptIsString(c["identifier"]) {
			kept = append(kept, c)
		}
	}
	v["subtitleLanguage"] = kept
	return true
}

// misc.ts:48-72
func ptSetValidRemoteIcon(v map[string]any) {
	icons := ptArrayify(v["icon"])
	imageTypes := map[string]bool{"image/png": true, "image/gif": true, "image/webp": true, "image/jpg": true, "image/jpeg": true} // constants.ts:898-906
	seen := map[string]bool{}
	kept := []any{}
	for _, raw := range icons {
		ic, _ := raw.(map[string]any)
		if ic == nil {
			continue
		}
		mt, _ := ic["mediaType"].(string)
		if ic["type"] != "Image" || !ptIsAPURL(ic["url"]) || !imageTypes[mt] || !ptIsInt(ic["width"], 0) || !ptIsInt(ic["height"], 0) {
			continue
		}
		size := ptJSString(ic["width"]) + "x" + ptJSString(ic["height"])
		if seen[size] {
			continue
		}
		seen[size] = true
		kept = append(kept, ic)
	}
	v["icon"] = kept
}

// videos.ts:260-271. validator.isInt THROWS on a non-string (validator.js
// assertString), so a numeric identifier is reported as a rejection.
func ptSetValidLicence(v map[string]any) bool {
	l, ok := v["licence"]
	if !ok || l == nil {
		return true
	}
	m, _ := l.(map[string]any)
	if m == nil || !ptIsString(m["identifier"]) {
		return false
	}
	if ptIsInt(m["identifier"], math.MinInt64) {
		return ptIdentifierIsInt(m)
	}
	return true // SPDX → mapped or cleared, never a rejection
}

// --- validator.js / misc.ts primitives -------------------------------------

func ptTruthy(x any) bool {
	switch y := x.(type) {
	case nil:
		return false
	case bool:
		return y
	case string:
		return y != ""
	case float64:
		return y != 0 && !math.IsNaN(y)
	default:
		return true
	}
}

func ptIsString(x any) bool { _, ok := x.(string); return ok }

func ptArrayify(x any) []any {
	if x == nil {
		return nil
	}
	if a, ok := x.([]any); ok {
		return a
	}
	return []any{x}
}

func ptContains(a []any, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

// ptJSString is JS `” + x` for the JSON scalar types.
func ptJSString(x any) string {
	switch y := x.(type) {
	case string:
		return y
	case float64:
		return strconv.FormatFloat(y, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(y)
	case nil:
		return "null"
	default:
		return "[object Object]"
	}
}

var ptIntRe = regexp.MustCompile(`^[-+]?[0-9]+$`) // validator.js isInt, allow_leading_zeroes

// ptIsInt is validator.isInt(” + x, { min }).
func ptIsInt(x any, min int64) bool {
	s := ptJSString(x)
	if !ptIntRe.MatchString(s) {
		return false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n >= min
}

// ptIdentifierIsInt is isRemoteNumberIdentifierValid (videos.ts:215-217):
// validator.isInt(data.identifier, { min: 0 }) with NO stringification.
func ptIdentifierIsInt(x any) bool {
	m, _ := x.(map[string]any)
	if m == nil || !ptIsString(m["identifier"]) {
		return false
	}
	return ptIsInt(m["identifier"], 0)
}

// ptIsLength is exists(x) && validator.isLength(x, {min,max}): code points,
// less the variation selectors U+FE0E and U+FE0F. Run against the validator
// PeerTube v8.3.2 ships (meta docs/evidence/peertube-interop-2026-11-17-gaps.md):
// "ab" followed by U+FE0F has length 2, and two emoji have length 2.
func ptIsLength(x any, min, max int) bool {
	s, ok := x.(string)
	if !ok {
		return false
	}
	n := utf8.RuneCountInString(s) - strings.Count(s, string(rune(0xFE0E))) - strings.Count(s, string(rune(0xFE0F)))
	return n >= min && n <= max
}

var ptUUIDv4 = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`) // validator.js isUUID(…, 4)

func ptIsUUIDv4(x any) bool { s, ok := x.(string); return ok && ptUUIDv4.MatchString(s) }

// ptIsDate is misc.ts:57-59 narrowed to RFC 3339 (a strict subset of the
// ISO 8601 validator.isISO8601 accepts — enough to prove OUR dates pass).
func ptIsDate(x any) bool {
	s, ok := x.(string)
	if !ok {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

// ptIsAPURL is isActivityPubUrlValid (misc.ts:8-10 → urls.ts:15-38):
// http(s), a host with a TLD (require_tld), length 3..2000 (constants.ts:561).
func ptIsAPURL(x any) bool {
	s, ok := x.(string)
	if !ok || !ptIsLength(s, 3, 2000) {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return false
	}
	h := u.Hostname()
	i := strings.LastIndex(h, ".")
	return i > 0 && len(h)-i-1 >= 2 && strings.Trim(h[i+1:], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") == ""
}

// videos.ts(validators):29-31 + constants.ts:730-743 (VIDEO_STATES keys 1..12).
func ptIsVideoStateValid(x any) bool {
	s := ptJSString(x)
	n, err := strconv.Atoi(s)
	return x != nil && err == nil && strconv.Itoa(n) == s && n >= 1 && n <= 12
}

// misc.ts:77-79 (validator.isBoolean, strict: true/false/1/0).
func ptIsBooleanValid(x any) bool {
	switch y := x.(type) {
	case bool:
		return true
	case string:
		return y == "true" || y == "false" || y == "1" || y == "0"
	}
	return false
}

// videos.ts(validators):50-52 + constants.ts:810-814 (1 enabled, 2 disabled, 3 requires approval).
func ptIsCommentsPolicyValid(x any) bool {
	s := ptJSString(x)
	return x == nil || s == "1" || s == "2" || s == "3"
}

// --- the world ---------------------------------------------------------------

// ptPoster writes a real w×h JPEG at the video's poster key, so the icon's
// width/height are read from bytes, not asserted by the test.
func ptPoster(t *testing.T, blobs storage.Backend, key string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode poster: %v", err)
	}
	if _, err := blobs.Put(t.Context(), key, &buf); err != nil {
		t.Fatalf("put poster: %v", err)
	}
}

var (
	ptNoThumbID   = uuid.MustParse("9c3f1a64-7d5e-4b0a-8f2e-1e0d9c8b7a60")
	ptSensitiveID = uuid.MustParse("ad4f2b75-8e6f-4c1b-9a3f-2f1e0d9c8b71")
	ptCaptionedID = uuid.MustParse("be503c86-9f70-4d2c-8b40-3a2f1e0d9c82")
	ptShortNameID = uuid.MustParse("cf614d97-a081-4e3d-9c51-4b3a2f1e0d93")
	ptUnsizedID   = uuid.MustParse("d0725ea8-b192-4f4e-8d62-5c4b3a2f1e04")
	ptNoProbeID   = uuid.MustParse("e1836fb9-c2a3-4a5f-9e73-6d5c4b3a2f15")
	ptOctetID     = uuid.MustParse("f2947aca-d3b4-4b60-8f84-7e6d5c4b3a26")
	ptGIFID       = uuid.MustParse("03a58bdb-e4c5-4c71-9095-8f7e6d5c4b37")
	ptFatJPEGID   = uuid.MustParse("14b69cec-f5d6-4d82-a1a6-908f7e6d5c48")
	ptAVIFID      = uuid.MustParse("25c7adfd-06e7-4e93-b2b7-a1908f7e6d59")
	ptAnimWebPID  = uuid.MustParse("36d8be0e-17f8-4fa4-83c8-b2a1908f7e6a")
)

// ptPosterBytes stores raw poster bytes for a copy of base under id, with the
// content type the row claims.
func ptPosterBytes(t *testing.T, repo fakeRepo, blobs storage.Backend, base sqlcgen.GetVideoByIDRow, id uuid.UUID, contentType string, b []byte) {
	t.Helper()
	v := base
	v.ID, v.Title, v.ThumbnailContentType = id, "Poster "+id.String()[:8], contentType
	repo.videosByID[id] = v
	if _, err := blobs.Put(t.Context(), media.VideoThumbnailKey(id), bytes.NewReader(b)); err != nil {
		t.Fatalf("put poster: %v", err)
	}
}

// ptJPEGBytes is a w×h JPEG with pad bytes of COM segments before its frame
// header, as a camera's EXIF/ICC block puts APP data there.
func ptJPEGBytes(t *testing.T, w, h, pad int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := []byte{0xFF, 0xD8}
	for ; pad > 0; pad -= 65533 {
		n := min(pad, 65533)
		out = append(out, 0xFF, 0xFE, byte((n+2)>>8), byte(n+2))
		out = append(out, bytes.Repeat([]byte{'x'}, n)...)
	}
	return append(out, buf.Bytes()[2:]...)
}

// ptWorld is the contract world plus one video per conformance case.
func ptWorld(t *testing.T) (*Service, fakeRepo) {
	t.Helper()
	repo := newContractRepo()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	finished := repo.videosByID[ctVideoID]
	finished.Category, finished.Language, finished.License = ptStr("2"), ptStr("en"), ptStr("1")
	finished.DownloadEnabled, finished.CommentsPolicy = true, "enabled"
	repo.videosByID[ctVideoID] = finished
	ptPoster(t, blobs, media.VideoThumbnailKey(ctVideoID), 640, 360)

	noThumb := finished
	noThumb.ID, noThumb.Title, noThumb.HasThumbnail, noThumb.ThumbnailContentType = ptNoThumbID, "Poster never generated", false, ""
	repo.videosByID[ptNoThumbID] = noThumb

	sensitive := finished
	sensitive.ID, sensitive.Title, sensitive.IsSensitive = ptSensitiveID, "Surgery, unedited", true
	sensitive.SensitiveReason, sensitive.CommentsPolicy = "Graphic medical footage.", "disabled"
	repo.videosByID[ptSensitiveID] = sensitive
	ptPoster(t, blobs, media.VideoThumbnailKey(ptSensitiveID), 640, 480)

	// A poster row whose bytes are not an image this instance can size.
	unsized := finished
	unsized.ID, unsized.Title = ptUnsizedID, "Poster that cannot be sized"
	repo.videosByID[ptUnsizedID] = unsized
	if _, err := blobs.Put(t.Context(), media.VideoThumbnailKey(ptUnsizedID), strings.NewReader("not an image")); err != nil {
		t.Fatalf("put unsized poster: %v", err)
	}

	// A source whose container states no duration: a WebM written to a pipe,
	// as a browser recording is. The probe reads none.
	noProbe := finished
	noProbe.ID, noProbe.Title, noProbe.MetadataDurationSeconds = ptNoProbeID, "Recorded in a browser", nil
	repo.videosByID[ptNoProbeID] = noProbe
	ptPoster(t, blobs, media.VideoThumbnailKey(ptNoProbeID), 640, 360)

	// G4: posters PeerTube refused although vidra could size them, or could not
	// size although they are ordinary images. An imported poster with an
	// unknown extension is stored as application/octet-stream.
	ptPosterBytes(t, repo, blobs, finished, ptOctetID, "application/octet-stream", ptJPEGBytes(t, 640, 360, 0))
	// A 480×270 GIF header, hand-written: importing image/gif here would
	// register the decoder for the test binary and hide a missing registration.
	ptPosterBytes(t, repo, blobs, finished, ptGIFID, "image/gif", []byte("GIF89a\xe0\x01\x0e\x01\x00\x00\x00;"))
	ptPosterBytes(t, repo, blobs, finished, ptFatJPEGID, "image/jpeg", ptJPEGBytes(t, 1280, 720, 300<<10))
	// An ANIMATED WebP (VP8X with the animation bit, then ANIM/ANMF): the
	// decoder reads the canvas size from VP8X without looking at the frames.
	anim := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x02\x00\x00\x00\x8f\x01\x00\x2b\x01\x00ANIM\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	anim[4] = byte(len(anim) - 8)
	ptPosterBytes(t, repo, blobs, finished, ptAnimWebPID, "image/webp", anim)
	// An AVIF saved as .jpg: SetThumbnail trusts the extension, nothing decodes it.
	ptPosterBytes(t, repo, blobs, finished, ptAVIFID, "image/jpeg", append([]byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), make([]byte, 64)...))

	shortName := finished
	shortName.ID, shortName.Title = ptShortNameID, "ab"
	repo.videosByID[ptShortNameID] = shortName
	ptPoster(t, blobs, media.VideoThumbnailKey(ptShortNameID), 640, 360)

	captioned := finished
	// 130 characters, multi-byte: vidra allows 200 bytes, PeerTube 120 chars.
	captioned.ID, captioned.Title = ptCaptionedID, "Interview with subtitles — "+strings.Repeat("ü", 103)
	captioned.OriginallyPublishedAt = pgtype.Timestamptz{}
	repo.videosByID[ptCaptionedID] = captioned
	// An importer reference-mode poster (entities_videoimages.go:149) keeps its
	// SOURCE filename as the key, not media.VideoThumbnailKey.
	ptPoster(t, blobs, "imports/peertube/thumbnails/source-name.jpg", 640, 272)

	repo.videoFacts = map[uuid.UUID]sqlcgen.GetVideoFederationFactsRow{
		ctVideoID: {Views: 1234, Tags: []string{"fjord", "kayak"}},
		// vidra stores captions under any BCP-47-ish tag (video/captions.go:21),
		// sorted by language as the SQL returns them.
		ptCaptionedID: {
			Views: 7, CaptionLanguages: []string{"en", "en-US", "fr-CA", "pt-br", "qaa", "zh-Hans"},
			ThumbnailFileID: "c0ffee00-0000-4000-8000-000000000001", ThumbnailStorageKey: "imports/peertube/thumbnails/source-name.jpg",
		},
	}

	return NewService(repo, WithBaseURL(contractBase), WithMediaStorage(blobs)), repo
}

func ptStr(s string) *string { return &s }

// TestVideoObjectPassesPeerTubeValidator runs every case through the port.
func TestVideoObjectPassesPeerTubeValidator(t *testing.T) {
	svc, _ := ptWorld(t)
	cases := []struct {
		name string
		id   uuid.UUID
		// wantReject pins a case PeerTube MUST reject, with the clause.
		wantReject string
		check      func(t *testing.T, v ptVerdict)
	}{
		{name: "published transcoded video", id: ctVideoID, check: func(t *testing.T, v ptVerdict) {
			ptAssertIcon(t, v, 640, 360)
			if v.Object["views"] != float64(1234) || v.Object["uuid"] != ctVideoID.String() {
				t.Errorf("views/uuid = %v/%v", v.Object["views"], v.Object["uuid"])
			}
			// Both hashtags survive setValidRemoteTags (videos.ts:170-185).
			if tags := v.Object["tag"].([]any); len(tags) != 2 {
				t.Errorf("surviving hashtags = %v, want fjord + kayak", tags)
			}
			// Integer identifiers pass videos.ts:91 and 260-271 unchanged.
			if c := v.Object["category"].(map[string]any); c["identifier"] != "2" {
				t.Errorf("category = %v", c)
			}
		}},
		// PeerTube requires at least one icon with an integer width and height
		// (videos.ts "icons", misc.ts setValidRemoteIcon) and refuses the whole
		// video without one. The interop lab measured it both ways: an
		// audio-only upload, which has no poster, and a video whose poster this
		// instance could not size, were each refused with "Video field is not
		// valid to PeerTube: icons" and never stored. Such a video names the
		// instance's fallback poster instead, a real image of that real size.
		{name: "video with no thumbnail", id: ptNoThumbID, check: ptAssertFallbackIcon},
		{name: "video whose poster cannot be sized", id: ptUnsizedID, check: ptAssertFallbackIcon},
		{name: "AVIF poster saved as .jpg", id: ptAVIFID, check: ptAssertFallbackIcon},
		// The icon's mediaType is what the bytes ARE: PeerTube keeps only
		// png/jpeg/webp/gif icons, and an octet-stream-typed JPEG was refused
		// ("icons") although its size was read.
		{name: "JPEG poster stored as application/octet-stream", id: ptOctetID, check: func(t *testing.T, v ptVerdict) {
			ptAssertIconType(t, v, 640, 360, "image/jpeg")
		}},
		// PeerTube keeps an image/webp icon; whether it can render the
		// animation is its business, and the size is the real canvas.
		{name: "animated WebP poster", id: ptAnimWebPID, check: func(t *testing.T, v ptVerdict) { ptAssertIconType(t, v, 400, 300, "image/webp") }},
		{name: "GIF poster", id: ptGIFID, check: func(t *testing.T, v ptVerdict) { ptAssertIconType(t, v, 480, 270, "image/gif") }},
		{name: "JPEG with 300 KiB of metadata before its frame header", id: ptFatJPEGID, check: func(t *testing.T, v ptVerdict) {
			ptAssertIconType(t, v, 1280, 720, "image/jpeg")
		}},
		// PeerTube requires a duration (videos.ts "duration format") and refuses
		// the whole video without one: the interop lab uploaded a WebM that
		// states none, and PeerTube logged "Video field is not valid to
		// PeerTube: duration format" and stored nothing. An unknown duration is
		// PT0S, which is what PeerTube itself sends when it does not know one.
		{name: "video with no probed duration", id: ptNoProbeID, check: func(t *testing.T, v ptVerdict) {
			if v.Object["duration"] != "PT0S" {
				t.Errorf("duration = %v, want PT0S", v.Object["duration"])
			}
		}},
		// PeerTube's name floor is 3 characters (constants.ts VIDEOS.NAME), and
		// vidra allows 1: the interop lab's video titled "ab" was refused with
		// "Video field is not valid to PeerTube: name". The object pads it, and
		// the padding is nothing a trim does not remove.
		{name: "video with a 2-character title", id: ptShortNameID, check: func(t *testing.T, v ptVerdict) {
			if name := v.Object["name"].(string); strings.TrimRight(name, " ") != "ab" {
				t.Errorf("name = %q, want the title ab with nothing but trailing spaces added", name)
			}
		}},
		{name: "sensitive video", id: ptSensitiveID, check: func(t *testing.T, v ptVerdict) {
			if v.Object["sensitive"] != true {
				t.Errorf("sensitive = %v, want true", v.Object["sensitive"])
			}
			if v.Object["commentsPolicy"] != float64(2) {
				t.Errorf("commentsPolicy = %v, want 2 (disabled)", v.Object["commentsPolicy"])
			}
			ptAssertIcon(t, v, 640, 480)
		}},
		{name: "video with captions", id: ptCaptionedID, check: func(t *testing.T, v ptVerdict) {
			// The poster was found under its importer key, not the canonical one.
			ptAssertIcon(t, v, 640, 272)
			// Each caption survives setValidRemoteCaptions with the text/vtt link
			// getCaptionAttributesFromObject dereferences
			// (object-to-model-attributes.ts:227-245), its identifier mapped to a
			// PeerTube language KEY: en-US → en is a duplicate of en and dropped,
			// fr-CA → fr, pt-br → pt-BR, qaa has no key and is omitted, and the
			// mixed-case zh-Hans keeps its own regional key (matching is
			// case-insensitive, never a fallback to zh). The href
			// still names vidra's stored tag.
			subs, _ := v.Object["subtitleLanguage"].([]any)
			want := [][2]string{{"en", "en"}, {"fr", "fr-CA"}, {"pt-BR", "pt-br"}, {"zh-Hans", "zh-Hans"}}
			if len(subs) != len(want) {
				t.Fatalf("surviving captions = %v, want %v", subs, want)
			}
			for i, w := range want {
				c := subs[i].(map[string]any)
				link := c["url"].([]any)[0].(map[string]any)
				href := contractBase + "/api/v1/videos/" + ptCaptionedID.String() + "/captions/" + w[1]
				if c["identifier"] != w[0] || link["mediaType"] != "text/vtt" || link["href"] != href {
					t.Errorf("caption %d = %v, want identifier %s → %s", i, c, w[0], href)
				}
			}
			// The 130-character title is cut to PeerTube's 120 (videos.ts:81) on a
			// rune boundary rather than rejected.
			if n := utf8.RuneCountInString(v.Object["name"].(string)); n != 120 || !utf8.ValidString(v.Object["name"].(string)) {
				t.Errorf("name = %d runes (valid UTF-8: %v), want 120", n, utf8.ValidString(v.Object["name"].(string)))
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj, err := svc.VideoObject(t.Context(), tc.id)
			if err != nil {
				t.Fatalf("VideoObject: %v", err)
			}
			v := peertubeCheck(t, obj)
			if v.Reject != tc.wantReject {
				t.Fatalf("PeerTube verdict = reject %q, want reject %q\nobject: %v", v.Reject, tc.wantReject, obj)
			}
			if tc.wantReject != "" {
				return
			}
			if m := peertubeModelCheck(v); m != "" {
				t.Errorf("PeerTube's model layer throws on %s: the creator transaction rolls the video back", m)
			}
			ptAssertPlayableAndOwned(t, v, tc.id)
			for _, f := range []string{"state", "waitTranscoding", "downloadEnabled", "sensitive", "commentsPolicy"} {
				for _, d := range v.Defaulted {
					if d == f {
						t.Errorf("%s was absent or invalid, so PeerTube replaced it with its default (videos.ts:63-78)", f)
					}
				}
			}
			if tc.check != nil {
				tc.check(t, v)
			}
		})
	}
}

// ptAssertPlayableAndOwned checks what PeerTube needs AFTER the validator to
// create a playable remote video in the right channel.
func ptAssertPlayableAndOwned(t *testing.T, v ptVerdict, id uuid.UUID) {
	t.Helper()
	// fetchRemoteVideo: the served id must be on the host it was fetched from
	// (shared/url-to-object.ts:14).
	if v.Object["id"] != contractBase+"/videos/"+id.String() {
		t.Errorf("id = %v", v.Object["id"])
	}
	// The HLS master must SURVIVE setValidRemoteVideoUrls: it is the only url
	// getStreamingPlaylistAttributesFromObject turns into a playlist
	// (object-to-model-attributes.ts:163-165,386-388).
	var hls string
	for _, raw := range v.Object["url"].([]any) {
		if u := raw.(map[string]any); u["mediaType"] == "application/x-mpegURL" {
			hls, _ = u["href"].(string)
		}
	}
	if want := contractBase + "/api/v1/videos/" + id.String() + "/hls/master.m3u8"; hls != want {
		t.Errorf("surviving HLS playlist = %q, want %q (filtered out = PeerTube stores an unplayable video)", hls, want)
	}
	// findOwner walks attributedTo for a same-host GROUP and throws without one
	// (abstract-builder.ts:48-56 → actors/get.ts:108-137). It must also be the
	// FIRST entry: vidra's own Announce/search ingest takes firstAttributedTo
	// as the owner (ingest.go, search.go), and only goldens guarded that.
	at := v.Object["attributedTo"].([]any)
	first, _ := at[0].(map[string]any)
	if first["type"] != "Group" || first["id"] != contractBase+"/video-channels/films" {
		t.Errorf("attributedTo[0] = %v, want the channel Group first", at[0])
	}
}

// ptRegionalLanguageKeys are the non-ISO-639 keys buildLanguages adds
// (constants.ts:1585-1600), in their exact case.
var ptRegionalLanguageKeys = map[string]bool{"pt-BR": true, "pt-PT": true, "es-419": true, "zh-Hans": true, "zh-Hant": true, "ca-valencia": true}

// peertubeModelCheck ports the model-layer @Is checks the creator transaction
// runs AFTER the validator (creator.ts:33-43): a throw there rolls the whole
// video back. VideoCaptionModel.language must be a VIDEO_TEXT_LANGUAGES key
// (video-caption.ts:88 → video-captions.ts:10-12 → constants.ts:1510-1521)
// and (videoId, language) is unique (video-caption.ts:74-76). The key table is
// built at runtime from the iso-639-3 package, which is not vendored here, so
// this checks a NECESSARY condition: a regional key in its exact case, or an
// all-lowercase 2-3 letter ISO 639 code. That is what rejects "pt-br",
// "en-US" and "fr-CA".
func peertubeModelCheck(v ptVerdict) string {
	seen := map[string]bool{}
	subs, _ := v.Object["subtitleLanguage"].([]any)
	for _, raw := range subs {
		id, _ := raw.(map[string]any)["identifier"].(string)
		iso := len(id) >= 2 && len(id) <= 3 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyz") == ""
		if !iso && !ptRegionalLanguageKeys[id] {
			return "caption language " + strconv.Quote(id)
		}
		if seen[id] {
			return "duplicate caption language " + strconv.Quote(id)
		}
		seen[id] = true
	}
	return ""
}

// ptAssertIcon: the poster survived misc.ts:48-72 with its REAL pixel size.
func ptAssertIcon(t *testing.T, v ptVerdict, w, h int) {
	t.Helper()
	icons := v.Object["icon"].([]any)
	ic := icons[0].(map[string]any)
	if ic["width"] != float64(w) || ic["height"] != float64(h) {
		t.Errorf("icon size = %vx%v, want %dx%d (read from the stored poster)", ic["width"], ic["height"], w, h)
	}
}

// ptAssertIconType: the one icon PeerTube kept is the real poster, at its real
// size and with the type its bytes decode as.
func ptAssertIconType(t *testing.T, v ptVerdict, w, h int, mediaType string) {
	t.Helper()
	icons := v.Object["icon"].([]any)
	if ic := icons[0].(map[string]any); len(icons) != 1 || ic["mediaType"] != mediaType || !strings.HasSuffix(ic["url"].(string), "/thumbnail") {
		t.Errorf("icon = %v, want the real poster alone as %s", icons, mediaType)
	}
	ptAssertIcon(t, v, w, h)
}

// ptAssertFallbackIcon: the one icon is the instance's fallback poster, with
// the size and type the route serving it really has (httpapi
// TestFederationFallbackPosterIsTheImageTheObjectNames decodes it).
func ptAssertFallbackIcon(t *testing.T, v ptVerdict) {
	t.Helper()
	icons := v.Object["icon"].([]any)
	ic := icons[0].(map[string]any)
	if len(icons) != 1 || ic["url"] != contractBase+"/api/v1/federation/poster.png" || ic["mediaType"] != "image/png" {
		t.Errorf("icon = %v, want the fallback poster alone", icons)
	}
	ptAssertIcon(t, v, 640, 360)
}

// TestPeerTubeCheckBites proves the port is not vacuous: each mutation of an
// accepted object trips the clause PeerTube would.
func TestPeerTubeCheckBites(t *testing.T) {
	svc, _ := ptWorld(t)
	cases := []struct {
		name, want string
		mutate     func(o map[string]any)
	}{
		{"no uuid", "uuid", func(o map[string]any) { delete(o, "uuid") }},
		{"non-v4 uuid", "uuid", func(o map[string]any) { o["uuid"] = "0190a0b2-7c3d-7e4f-8a5b-6c7d8e9f0a1b" }},
		{"no views", "views", func(o map[string]any) { delete(o, "views") }},
		{"no duration", "duration format", func(o map[string]any) { delete(o, "duration") }},
		{"short name", "name", func(o map[string]any) { o["name"] = "ab" }},
		{"long name", "name", func(o map[string]any) { o["name"] = strings.Repeat("x", 121) }},
		{"short content", "mediaType/content", func(o map[string]any) { o["content"] = "ab" }},
		{"plain-text mediaType", "mediaType/content", func(o map[string]any) { o["mediaType"] = "text/plain" }},
		{"slug category", "category", func(o map[string]any) { o["category"] = map[string]any{"identifier": "music"} }},
		{"numeric licence id", "licence", func(o map[string]any) { o["licence"] = map[string]any{"identifier": 1} }},
		{"icon without size", "icons", func(o map[string]any) {
			o["icon"] = []any{map[string]any{"type": "Image", "url": "https://videos.example/p.jpg", "mediaType": "image/jpeg"}}
		}},
		{"no updated", "updated", func(o map[string]any) { delete(o, "updated") }},
		{"attributedTo unusable", "attributedTo", func(o map[string]any) {
			o["attributedTo"] = []any{map[string]any{"type": "Service", "id": "https://x.example/a"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj, err := svc.VideoObject(t.Context(), ctVideoID)
			if err != nil {
				t.Fatalf("VideoObject: %v", err)
			}
			raw, _ := json.Marshal(obj)
			var o map[string]any
			_ = json.Unmarshal(raw, &o)
			tc.mutate(o)
			if got := peertubeCheck(t, o).Reject; got != tc.want {
				t.Errorf("reject = %q, want %q", got, tc.want)
			}
		})
	}

	for _, bad := range []string{"pt-br", "en-US", "fr-CA"} {
		v := ptVerdict{Object: map[string]any{"subtitleLanguage": []any{map[string]any{"identifier": bad}}}}
		if peertubeModelCheck(v) == "" {
			t.Errorf("model check accepted caption language %q", bad)
		}
	}
	dup := ptVerdict{Object: map[string]any{"subtitleLanguage": []any{map[string]any{"identifier": "en"}, map[string]any{"identifier": "en"}}}}
	if peertubeModelCheck(dup) == "" {
		t.Error("model check accepted a duplicate caption language")
	}

	// An HLS link without `tag` is not a rejection: it is silently filtered,
	// leaving an accepted video with nothing to play (videos.ts:133-138,236).
	obj, err := svc.VideoObject(t.Context(), ctVideoID)
	if err != nil {
		t.Fatalf("VideoObject: %v", err)
	}
	raw, _ := json.Marshal(obj)
	var o map[string]any
	_ = json.Unmarshal(raw, &o)
	delete(o["url"].([]any)[0].(map[string]any), "tag")
	v := peertubeCheck(t, o)
	if v.Reject != "" {
		t.Fatalf("reject = %q, want accepted", v.Reject)
	}
	for _, u := range v.Object["url"].([]any) {
		if u.(map[string]any)["mediaType"] == "application/x-mpegURL" {
			t.Error("an HLS link without tag survived; PeerTube filters it out")
		}
	}
}

// countingBackend counts Opens, i.e. poster reads.
type countingBackend struct {
	storage.Backend
	opens *int
}

func (c countingBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	*c.opens++
	return c.Backend.Open(ctx, key)
}

// TestPosterSizeIsReadOncePerThumbnailRow: an unauthenticated outbox page or
// object fetch must not re-read posters on every request. The size is cached
// per video_files thumbnail row — a replaced poster is a NEW row, so the cache
// can never serve a stale size.
func TestPosterSizeIsReadOncePerThumbnailRow(t *testing.T) {
	repo := newContractRepo()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	ptPoster(t, local, media.VideoThumbnailKey(ctVideoID), 640, 360)
	repo.videoFacts = map[uuid.UUID]sqlcgen.GetVideoFederationFactsRow{ctVideoID: {ThumbnailFileID: "row-1"}}
	opens := 0
	svc := NewService(repo, WithBaseURL(contractBase), WithMediaStorage(countingBackend{local, &opens}))
	for i := 0; i < 3; i++ {
		obj, err := svc.VideoObject(t.Context(), ctVideoID)
		if err != nil {
			t.Fatalf("VideoObject: %v", err)
		}
		if icon := obj["icon"].([]map[string]any)[0]; icon["width"] != 640 {
			t.Fatalf("icon = %v, want the poster's size", icon)
		}
	}
	if opens != 1 {
		t.Errorf("poster opened %d times for 3 emissions, want 1", opens)
	}
	// A replaced poster is a new thumbnail row: read again, never stale.
	ptPoster(t, local, media.VideoThumbnailKey(ctVideoID), 320, 180)
	repo.videoFacts[ctVideoID] = sqlcgen.GetVideoFederationFactsRow{ThumbnailFileID: "row-2"}
	obj, err := svc.VideoObject(t.Context(), ctVideoID)
	if err != nil {
		t.Fatalf("VideoObject: %v", err)
	}
	if icon := obj["icon"].([]map[string]any)[0]; icon["width"] != 320 || opens != 2 {
		t.Errorf("after replacement: icon = %v, opens = %d; want 320 wide from a second read", icon, opens)
	}
	// No row id means nothing could be cached, so nothing is read: up to 2 MiB
	// per emission is not spent on a poster the next emission reads again.
	if f := svc.posterFactsOf(t.Context(), "", media.VideoThumbnailKey(ctVideoID)); f != (posterFacts{}) || opens != 2 {
		t.Errorf("a poster with no row id: facts %+v after %d opens, want none and no read", f, opens)
	}
}

// failingOpenBackend fails every Open, as a storage outage does.
type failingOpenBackend struct {
	storage.Backend
	opens *int
}

func (f failingOpenBackend) Open(context.Context, string) (io.ReadCloser, error) {
	*f.opens++
	return nil, io.ErrUnexpectedEOF
}

// failingReadBackend opens, hands over the first bytes of a real JPEG, then
// fails the read, as a connection dropped mid-object does.
type failingReadBackend struct {
	storage.Backend
	opens *int
}

func (f failingReadBackend) Open(context.Context, string) (io.ReadCloser, error) {
	*f.opens++
	return io.NopCloser(io.MultiReader(bytes.NewReader([]byte{0xFF, 0xD8, 0xFF}), iotest.ErrReader(io.ErrUnexpectedEOF))), nil
}

// TestUndecodablePosterIsReadOnceButAnOutageIsRetried: with a 2 MiB header
// bound, re-reading a poster that will never decode on every outbox page would
// be a cost an anonymous caller can multiply, so that verdict is cached per
// thumbnail row. A storage failure is not a verdict on the bytes: it is retried.
func TestUndecodablePosterIsReadOnceButAnOutageIsRetried(t *testing.T) {
	repo := newContractRepo()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	if _, err := local.Put(t.Context(), media.VideoThumbnailKey(ctVideoID), strings.NewReader("not an image")); err != nil {
		t.Fatalf("put: %v", err)
	}
	repo.videoFacts = map[uuid.UUID]sqlcgen.GetVideoFederationFactsRow{ctVideoID: {ThumbnailFileID: "row-1"}}
	for _, tc := range []struct {
		name    string
		backend func(opens *int) storage.Backend
		want    int
	}{
		{"undecodable bytes", func(o *int) storage.Backend { return countingBackend{local, o} }, 1},
		{"storage outage", func(o *int) storage.Backend { return failingOpenBackend{local, o} }, 3},
		{"read failing mid-object", func(o *int) storage.Backend { return failingReadBackend{local, o} }, 3},
	} {
		opens := 0
		svc := NewService(repo, WithBaseURL(contractBase), WithMediaStorage(tc.backend(&opens)))
		for i := 0; i < 3; i++ {
			obj, err := svc.VideoObject(t.Context(), ctVideoID)
			if err != nil {
				t.Fatalf("%s: VideoObject: %v", tc.name, err)
			}
			if icons := obj["icon"].([]map[string]any); icons[len(icons)-1]["url"] != contractBase+FallbackPosterPath {
				t.Fatalf("%s: icons = %v, want the fallback poster last", tc.name, icons)
			}
		}
		if opens != tc.want {
			t.Errorf("%s: poster opened %d times for 3 emissions, want %d", tc.name, opens, tc.want)
		}
	}
}
