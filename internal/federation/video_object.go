package federation

import (
	"context"
	"image"
	_ "image/jpeg" // register the JPEG decoder for image.DecodeConfig
	_ "image/png"  // register the PNG decoder for image.DecodeConfig
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	_ "golang.org/x/image/webp" // register the WebP decoder for image.DecodeConfig

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The outbound ActivityStreams Video object (A29 remediation).
//
// A29 measured what a follower instance could do with a federated video: store
// a title, a description and a link, and nothing else. The cause was not the
// ingest — videoLinks(), iconURL() and parseISODurationSeconds() were all
// already there and all already correct — it was that videoObject() emitted a
// bare-string `url` and no `icon` and no `duration`, so a vidra→vidra
// federation could never populate stream_url, thumbnail_key or
// duration_seconds no matter how good the reader was.
//
// What the object carries now, and why each field is the shape it is:
//
//   - `id` is STILL /videos/{uuid} and must never move. Remote servers key on
//     it, address Update and Delete to it, and thread replies against it.
//   - `url` is an ARRAY of Links, PeerTube's convention and the one vidra's own
//     ingest already reads: the HLS master as `application/x-mpegURL`, and the
//     human watch page (/v/{code}) as `text/html`. The follower picks the
//     stream link for its player and the html link for "watch on origin"; a
//     follower that understands neither still has a URL it can open.
//   - `icon` is an ARRAY of Image objects carrying `mediaType`, `width` and
//     `height`: PeerTube drops an icon without integer width/height
//     (misc.ts:48-72) and then rejects a video with no icon (videos.ts:58).
//     vidra records no poster dimensions, so they are read from the stored
//     poster's own header — never invented; unreadable means omitted.
//   - `duration` is xsd:duration in the whole-seconds form PeerTube emits
//     ("PT123S"), which is what parseISODurationSeconds on the other side reads.
//   - `published` is when the video became public, preferring the creator's
//     declared original date, then the scheduled publication, then the row's
//     creation. `updated` is the row's last edit, which is what lets a follower
//     tell a re-Announce from a real edit.
//
// PeerTube v8.3.1 (T05) re-fetches the object id and drops, silently, any
// object failing sanitizeAndCheckVideoTorrentObject; peertubeFields adds what
// that requires. attributedTo names the channel Group FIRST, then the owning
// Person: findOwner wants the Group, and vidra's Announce ingest takes the
// first entry as the owner. Proof is the validator port in
// peertube_conformance_test.go — by test, not against a live PeerTube.
//
// A field is OMITTED rather than emitted empty whenever its underlying fact is
// absent: no ready ladder means no HLS link, no poster means no icon, no probe
// means no duration. An empty `icon` or a `duration` of "PT0S" would be a claim
// the origin cannot back.

// videoObjectInput is everything the AS Video needs, gathered from ONE row.
type videoObjectInput struct {
	ChannelActor string
	ObjectID     string
	WatchURL     string
	Title        string
	Description  string
	// HLSURL is the master playlist URL, or "" when no ready ladder exists.
	HLSURL string
	// IconURL is the poster URL, or "" when the video has no thumbnail.
	IconURL      string
	IconMediaTyp string
	// DurationSeconds is the probed duration, or nil when unknown.
	DurationSeconds *int32
	Published       time.Time
	Updated         time.Time

	// OwnerActor is the owning account's Person actor, or "" when unknown.
	OwnerActor string
	// IconWidth/IconHeight are the poster's pixel size, 0 when unreadable.
	IconWidth, IconHeight int
}

// videoObject renders an AS Video object from the gathered facts.
func videoObject(in videoObjectInput) map[string]any {
	attributedTo := []map[string]any{{"type": "Group", "id": in.ChannelActor}}
	if in.OwnerActor != "" {
		attributedTo = append(attributedTo, map[string]any{"type": "Person", "id": in.OwnerActor})
	}
	obj := map[string]any{
		"id":           in.ObjectID,
		"type":         "Video",
		"name":         truncate(in.Title, peertubeMaxNameLen),
		"attributedTo": attributedTo,
		"url":          videoURLLinks(in.HLSURL, in.WatchURL),
		"to":           []string{publicAudience},
	}
	// PeerTube rejects content shorter than 3 characters (videos.ts:100,223).
	if utf8.RuneCountInString(in.Description) >= 3 {
		obj["content"], obj["mediaType"] = in.Description, "text/markdown"
	}
	if d := isoDuration(in.DurationSeconds); d != "" {
		obj["duration"] = d
	}
	if in.IconURL != "" {
		icon := map[string]any{"type": "Image", "url": in.IconURL}
		if in.IconMediaTyp != "" {
			icon["mediaType"] = in.IconMediaTyp
		}
		if in.IconWidth > 0 && in.IconHeight > 0 {
			icon["width"], icon["height"] = in.IconWidth, in.IconHeight
		}
		obj["icon"] = []map[string]any{icon}
	}
	if !in.Published.IsZero() {
		obj["published"] = in.Published.UTC().Format(time.RFC3339)
	}
	if !in.Updated.IsZero() {
		obj["updated"] = in.Updated.UTC().Format(time.RFC3339)
	}
	return obj
}

// peertubeMaxNameLen is PeerTube's title limit (videos.ts:81, constants.ts:519);
// vidra allows 200 bytes, and a longer name is rejected outright.
const peertubeMaxNameLen = 120

// peertubeCommentsPolicy maps vidra's per-video policy to PeerTube's
// VideoCommentPolicy (1 enabled, 2 disabled; vidra has no approval tier).
var peertubeCommentsPolicy = map[string]int{"enabled": 1, "disabled": 2}

// peertubeFields adds the fields PeerTube's validator requires (uuid, views)
// or would otherwise overwrite with its defaults (videos.ts:63-78), plus the
// identifiers, hashtags and captions it reads. state is 1 (PUBLISHED): only
// published videos federate.
func (s *Service) peertubeFields(obj map[string]any, r sqlcgen.ListChannelOutboxVideosRow) {
	obj["uuid"], obj["views"], obj["state"] = r.ID.String(), r.Views, 1
	obj["sensitive"], obj["downloadEnabled"], obj["waitTranscoding"] = r.IsSensitive, r.DownloadEnabled, r.PublishAfterTranscode
	if p, ok := peertubeCommentsPolicy[r.CommentsPolicy]; ok {
		obj["commentsPolicy"], obj["commentsEnabled"] = p, p == 1
	}
	// A non-integer category/licence id rejects the WHOLE video
	// (videos.ts:91,263), so a custom non-numeric id is omitted.
	if r.Category != nil && isDigits(*r.Category) {
		obj["category"] = map[string]any{"identifier": *r.Category}
	}
	if r.License != nil && isDigits(*r.License) {
		obj["licence"] = map[string]any{"identifier": *r.License}
	}
	if r.Language != nil && *r.Language != "" {
		obj["language"] = map[string]any{"identifier": *r.Language}
	}
	var tags, subs []map[string]any
	for _, t := range r.Tags {
		tags = append(tags, map[string]any{"type": "Hashtag", "name": t})
	}
	seen := map[string]bool{}
	for _, lang := range r.CaptionLanguages {
		// An unknown or duplicate language key throws in PeerTube's caption
		// model (video-caption.ts:74-76,88) and rolls the WHOLE video back.
		key := peertubeCaptionLanguage(lang)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		href := s.baseURL + "/api/v1/videos/" + r.ID.String() + "/captions/" + url.PathEscape(lang)
		subs = append(subs, map[string]any{"identifier": key, "url": []map[string]any{{"type": "Link", "mediaType": "text/vtt", "href": href}}})
	}
	if len(tags) > 0 {
		obj["tag"] = tags
	}
	if len(subs) > 0 {
		obj["subtitleLanguage"] = subs
	}
}

func isDigits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// peertubeTextLanguages is PeerTube's caption-language key set
// (VIDEO_TEXT_LANGUAGES: buildLanguages, constants.ts:1510-1600), keyed by
// lowercase tag. PeerTube builds it at boot from the iso-639-3 package, so
// this copy is CONSERVATIVE: living ISO 639-1 codes whose status is not in
// doubt, the 639-3 additions and the regional keys. A key PeerTube lacks rolls
// the video back; a missing one only drops that caption.
var peertubeTextLanguages = func() map[string]string {
	m := map[string]string{"pt-br": "pt-BR", "pt-pt": "pt-PT", "es-419": "es-419", "zh-hans": "zh-Hans", "zh-hant": "zh-Hant", "ca-valencia": "ca-valencia"}
	for _, c := range strings.Fields(`aa ab af ak am an ar as av ay az ba be bg bi bm bn bo br bs ca ce ch co cr cs cv cy
		da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu ha he hi ho hr ht hu hy hz id ig ii ik is
		it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr ms mt my
		na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pl ps pt qu rm rn ro ru rw sc sd se sg si sk sl sm sn so sq
		sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi wa wo xh yi yo za zh zu
		kab gcf tlh jbo avk gsw tok rcf gcr`) {
		m[c] = c
	}
	return m
}()

// peertubeCaptionLanguage maps a vidra caption tag (any BCP-47-ish case) to a
// PeerTube key: the tag itself (pt-br → pt-BR), else its base language
// (en-US → en), else "" — no key, so the caption is not offered.
func peertubeCaptionLanguage(tag string) string {
	t := strings.ToLower(tag)
	if k, ok := peertubeTextLanguages[t]; ok {
		return k
	}
	base, _, _ := strings.Cut(t, "-")
	return peertubeTextLanguages[base]
}

// videoURLLinks builds the `url` array: the playable HLS master first (when
// there is one), then the human watch page. Always an array, even with one
// entry, so the shape a consumer parses does not change with the transcode
// state of one video.
func videoURLLinks(hlsURL, watchURL string) []map[string]any {
	links := make([]map[string]any, 0, 2)
	if hlsURL != "" {
		// `tag` must be an array or PeerTube filters the link out
		// (videos.ts:133-138). It is empty: vidra publishes no segment
		// hashes and no per-resolution file to list there.
		links = append(links, map[string]any{
			"type":      "Link",
			"href":      hlsURL,
			"mediaType": "application/x-mpegURL",
			"tag":       []any{},
		})
	}
	links = append(links, map[string]any{
		"type":      "Link",
		"href":      watchURL,
		"mediaType": "text/html",
	})
	return links
}

// isoDuration renders whole seconds as xsd:duration, or "" for unknown/zero.
// The whole-seconds form ("PT123S") is what PeerTube emits and what vidra's own
// parseISODurationSeconds reads, so the two halves of the loop agree.
func isoDuration(seconds *int32) string {
	if seconds == nil || *seconds <= 0 {
		return ""
	}
	return "PT" + strconv.FormatInt(int64(*seconds), 10) + "S"
}

// videoMediaURLs builds the two absolute media URLs the object advertises. They
// are the API's own public routes: the same URLs this instance's own player
// uses, which is what makes the CORS rule (public + non-credentialed → `*`) the
// only thing a remote player needs.
func (s *Service) videoHLSURL(videoID uuid.UUID) string {
	return s.baseURL + "/api/v1/videos/" + videoID.String() + "/hls/master.m3u8"
}

func (s *Service) videoThumbnailURL(videoID uuid.UUID) string {
	return s.baseURL + "/api/v1/videos/" + videoID.String() + "/thumbnail"
}

// videoWatchURL is the human landing page: /v/{code} when the video has a short
// code, else the object id itself (which the frontend also routes).
func (s *Service) videoWatchURL(videoID uuid.UUID, shortCode string) string {
	if shortCode != "" {
		return s.baseURL + "/v/" + shortCode
	}
	return s.baseURL + "/videos/" + videoID.String()
}

// publishedAt is the AS `published` value: the creator's declared original
// date, else the scheduled publication, else the row's creation.
func publishedAt(originallyPublished, publishAt pgtype.Timestamptz, createdAt time.Time) time.Time {
	if originallyPublished.Valid {
		return originallyPublished.Time
	}
	if publishAt.Valid {
		return publishAt.Time
	}
	return createdAt
}

// maxPosterHeaderBytes bounds the read that recovers a poster's pixel size;
// maxPosterSizes bounds the in-process cache of the sizes read.
const (
	maxPosterHeaderBytes = 256 << 10
	maxPosterSizes       = 50000
)

// posterSize returns the pixel size of the poster stored at key (the
// video_files thumbnail row /thumbnail serves), 0, 0 when unreadable. Only the
// header is decoded and the reader is closed straight after, and a size is
// cached per thumbnail ROW id: a replaced poster is a new row, so an outbox
// page or object fetch re-reads nothing it has read before.
func (s *Service) posterSize(ctx context.Context, fileID, key string) (int, int) {
	if s.media == nil || key == "" {
		return 0, 0
	}
	s.posterMu.Lock()
	wh, ok := s.posterSizes[fileID]
	s.posterMu.Unlock()
	if ok {
		return wh[0], wh[1]
	}
	rc, err := s.media.Open(ctx, key)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = rc.Close() }()
	cfg, _, err := image.DecodeConfig(io.LimitReader(rc, maxPosterHeaderBytes))
	if err != nil {
		return 0, 0
	}
	if fileID != "" {
		s.posterMu.Lock()
		if s.posterSizes == nil || len(s.posterSizes) >= maxPosterSizes {
			s.posterSizes = map[string][2]int{}
		}
		s.posterSizes[fileID] = [2]int{cfg.Width, cfg.Height}
		s.posterMu.Unlock()
	}
	return cfg.Width, cfg.Height
}

// videoObjectFromDetail gathers the AS Video facts for one video (the pushed
// activity and the dereferenced object id). It reshapes the row into the
// outbox row so both paths go through ONE builder.
func (s *Service) videoObjectFromDetail(ctx context.Context, channelActor string, v sqlcgen.GetVideoByIDRow) (map[string]any, error) {
	f, err := s.repo.GetVideoFederationFacts(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	return s.videoObjectFromOutbox(ctx, channelActor, sqlcgen.ListChannelOutboxVideosRow{
		ID: v.ID, Title: v.Title, Description: v.Description, ShortCode: v.ShortCode,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, PublishAt: v.PublishAt,
		OriginallyPublishedAt: v.OriginallyPublishedAt, DurationSeconds: v.MetadataDurationSeconds,
		HasThumbnail: v.HasThumbnail, ThumbnailContentType: v.ThumbnailContentType, HasHls: v.HasHls,
		Category: v.Category, Language: v.Language, License: v.License,
		IsSensitive: v.IsSensitive, CommentsPolicy: v.CommentsPolicy,
		DownloadEnabled: v.DownloadEnabled, PublishAfterTranscode: v.PublishAfterTranscode,
		OwnerUsername: f.OwnerUsername, Views: f.Views, Tags: f.Tags, CaptionLanguages: f.CaptionLanguages,
		ThumbnailFileID: f.ThumbnailFileID, ThumbnailStorageKey: f.ThumbnailStorageKey,
	}), nil
}

// videoObjectFromOutbox gathers the AS Video facts from an outbox page row.
func (s *Service) videoObjectFromOutbox(ctx context.Context, channelActor string, r sqlcgen.ListChannelOutboxVideosRow) map[string]any {
	in := videoObjectInput{
		ChannelActor:    channelActor,
		ObjectID:        s.baseURL + "/videos/" + r.ID.String(),
		WatchURL:        s.videoWatchURL(r.ID, r.ShortCode),
		Title:           r.Title,
		Description:     r.Description,
		DurationSeconds: r.DurationSeconds,
		Published:       publishedAt(r.OriginallyPublishedAt, r.PublishAt, r.CreatedAt),
		Updated:         r.UpdatedAt,
	}
	if r.OwnerUsername != "" {
		in.OwnerActor = s.baseURL + "/accounts/" + r.OwnerUsername
	}
	if r.HasHls {
		in.HLSURL = s.videoHLSURL(r.ID)
	}
	if r.HasThumbnail {
		in.IconURL = s.videoThumbnailURL(r.ID)
		in.IconMediaTyp = r.ThumbnailContentType
		in.IconWidth, in.IconHeight = s.posterSize(ctx, r.ThumbnailFileID, r.ThumbnailStorageKey)
	}
	obj := videoObject(in)
	s.peertubeFields(obj, r)
	return obj
}
