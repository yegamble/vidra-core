package federation

import (
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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
//   - `icon` is an Image object rather than a bare URL string, so its
//     `mediaType` travels with it — the poster is a png as often as a jpeg, and
//     a consumer that guesses gets it wrong. Width and height are DELIBERATELY
//     absent: vidra records the video's dimensions (video_metadata) but never
//     the poster's, and inventing them would be worse than omitting them.
//   - `duration` is xsd:duration in the whole-seconds form PeerTube emits
//     ("PT123S"), which is what parseISODurationSeconds on the other side reads.
//   - `published` is when the video became public, preferring the creator's
//     declared original date, then the scheduled publication, then the row's
//     creation. `updated` is the row's last edit, which is what lets a follower
//     tell a re-Announce from a real edit.
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
}

// videoObject renders an AS Video object from the gathered facts.
func videoObject(in videoObjectInput) map[string]any {
	obj := map[string]any{
		"id":           in.ObjectID,
		"type":         "Video",
		"name":         in.Title,
		"content":      in.Description,
		"attributedTo": in.ChannelActor,
		"url":          videoURLLinks(in.HLSURL, in.WatchURL),
		"to":           []string{publicAudience},
	}
	if d := isoDuration(in.DurationSeconds); d != "" {
		obj["duration"] = d
	}
	if in.IconURL != "" {
		icon := map[string]any{"type": "Image", "url": in.IconURL}
		if in.IconMediaTyp != "" {
			icon["mediaType"] = in.IconMediaTyp
		}
		obj["icon"] = icon
	}
	if !in.Published.IsZero() {
		obj["published"] = in.Published.UTC().Format(time.RFC3339)
	}
	if !in.Updated.IsZero() {
		obj["updated"] = in.Updated.UTC().Format(time.RFC3339)
	}
	return obj
}

// videoURLLinks builds the `url` array: the playable HLS master first (when
// there is one), then the human watch page. Always an array, even with one
// entry, so the shape a consumer parses does not change with the transcode
// state of one video.
func videoURLLinks(hlsURL, watchURL string) []map[string]any {
	links := make([]map[string]any, 0, 2)
	if hlsURL != "" {
		links = append(links, map[string]any{
			"type":      "Link",
			"href":      hlsURL,
			"mediaType": "application/x-mpegURL",
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

// videoObjectFromDetail gathers the AS Video facts from a GetVideoByID row.
func (s *Service) videoObjectFromDetail(channelActor string, v sqlcgen.GetVideoByIDRow) map[string]any {
	in := videoObjectInput{
		ChannelActor:    channelActor,
		ObjectID:        s.baseURL + "/videos/" + v.ID.String(),
		WatchURL:        s.videoWatchURL(v.ID, v.ShortCode),
		Title:           v.Title,
		Description:     v.Description,
		DurationSeconds: v.MetadataDurationSeconds,
		Published:       publishedAt(v.OriginallyPublishedAt, v.PublishAt, v.CreatedAt),
		Updated:         v.UpdatedAt,
	}
	if v.HasHls {
		in.HLSURL = s.videoHLSURL(v.ID)
	}
	if v.HasThumbnail {
		in.IconURL = s.videoThumbnailURL(v.ID)
		in.IconMediaTyp = v.ThumbnailContentType
	}
	return videoObject(in)
}

// videoObjectFromOutbox gathers the same facts from an outbox page row.
func (s *Service) videoObjectFromOutbox(channelActor string, r sqlcgen.ListChannelOutboxVideosRow) map[string]any {
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
	if r.HasHls {
		in.HLSURL = s.videoHLSURL(r.ID)
	}
	if r.HasThumbnail {
		in.IconURL = s.videoThumbnailURL(r.ID)
		in.IconMediaTyp = r.ThumbnailContentType
	}
	return videoObject(in)
}
