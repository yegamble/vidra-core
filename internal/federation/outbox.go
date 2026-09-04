package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// publicAudience is the ActivityStreams magic collection marking an activity as
// public (delivered to followers and visible to anyone).
const publicAudience = "https://www.w3.org/ns/activitystreams#Public"

// AnnounceVideo fans a newly-published video out to the publishing channel's
// remote followers as a Create{Video}. No-op unless the video is public+published.
func (s *Service) AnnounceVideo(ctx context.Context, videoID uuid.UUID) error {
	v, ch, ok, err := s.loadVideoAndChannel(ctx, videoID)
	if err != nil || !ok {
		return err
	}
	if !ch.ActivitypubEnabled {
		return nil // channel opted out of ActivityPub (migration 0096)
	}
	if v.Privacy != "public" || v.State != "published" {
		return nil
	}
	payload, err := s.buildVideoActivity("Create", ch.Handle, v)
	if err != nil {
		return err
	}
	return s.fanOutToFollowers(ctx, ch.ID, ch.Handle, payload)
}

// UpdateVideo propagates an edit to remote followers: an Update{Video} while the
// video is still public+published, or a Delete (unfederate) if it is no longer
// public+published (e.g. went private). No-op if the video is gone.
func (s *Service) UpdateVideo(ctx context.Context, videoID uuid.UUID) error {
	v, ch, ok, err := s.loadVideoAndChannel(ctx, videoID)
	if err != nil || !ok {
		return err
	}
	if !ch.ActivitypubEnabled {
		return nil // channel opted out of ActivityPub (migration 0096)
	}
	var payload []byte
	if v.Privacy == "public" && v.State == "published" {
		payload, err = s.buildVideoActivity("Update", ch.Handle, v)
	} else {
		payload, err = s.buildDeleteVideo(ch.Handle, v.ID)
	}
	if err != nil {
		return err
	}
	return s.fanOutToFollowers(ctx, ch.ID, ch.Handle, payload)
}

// DeleteVideo propagates a deletion to remote followers as a Delete. The video row
// is already gone, so the caller passes its channel id and whether it had been
// public (only public videos were ever federated). No-op otherwise.
func (s *Service) DeleteVideo(ctx context.Context, videoID, channelID uuid.UUID, wasPublic bool) error {
	if !wasPublic {
		return nil
	}
	ch, err := s.repo.GetChannelByID(ctx, channelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !ch.ActivitypubEnabled {
		return nil // channel opted out of ActivityPub (migration 0096)
	}
	payload, err := s.buildDeleteVideo(ch.Handle, videoID)
	if err != nil {
		return err
	}
	return s.fanOutToFollowers(ctx, channelID, ch.Handle, payload)
}

// loadVideoAndChannel loads a video and its channel; ok is false (nil error) when
// either is absent (a no-op for federation).
func (s *Service) loadVideoAndChannel(ctx context.Context, videoID uuid.UUID) (sqlcgen.GetVideoByIDRow, sqlcgen.Channel, bool, error) {
	v, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, sqlcgen.Channel{}, false, nil
		}
		return v, sqlcgen.Channel{}, false, err
	}
	ch, err := s.repo.GetChannelByID(ctx, v.ChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v, ch, false, nil
		}
		return v, ch, false, err
	}
	return v, ch, true, nil
}

// fanOutToFollowers enqueues one signed delivery of payload per distinct remote
// follower inbox of the channel.
func (s *Service) fanOutToFollowers(ctx context.Context, channelID uuid.UUID, channelHandle string, payload []byte) error {
	inboxes, err := s.repo.ListRemoteFollowerInboxes(ctx, channelID)
	if err != nil {
		return err
	}
	for _, inbox := range inboxes {
		if inbox == "" {
			continue
		}
		if err := s.enqueueChannelDelivery(ctx, channelID, channelHandle, inbox, payload); err != nil {
			return err
		}
	}
	return nil
}

// buildVideoActivity renders a Create or Update activity wrapping the video as an
// AS Video object, attributed to the channel actor and addressed to the public.
func (s *Service) buildVideoActivity(activityType, channelHandle string, v sqlcgen.GetVideoByIDRow) ([]byte, error) {
	channelActor := s.baseURL + "/video-channels/" + channelHandle
	objectID := s.baseURL + "/videos/" + v.ID.String()
	watchURL := objectID
	if v.ShortCode != "" {
		watchURL = s.baseURL + "/v/" + v.ShortCode
	}
	activity := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       channelActor + "/activities/" + strings.ToLower(activityType) + "/" + uuid.NewString(),
		"type":     activityType,
		"actor":    channelActor,
		"to":       []string{publicAudience},
		"object":   videoObject(channelActor, objectID, watchURL, v.Title, v.Description),
	}
	return json.Marshal(activity)
}

// videoObject renders a video as an AS Video object.
//
// DELIBERATELY OMITTED (config-parity W9): PeerTube's per-video policy fields
// (pt:commentsPolicy / pt:downloadEnabled — vidra's comments_policy and
// download_enabled columns). Vidra's outbound Video is plain ActivityStreams
// with no PeerTube JSON-LD context; emitting those fields alone would be
// piecemeal PT-vocabulary adoption without the @context that names it, and no
// consumer reads them from vidra today — remote viewers of a federated card
// click through to the origin watch page, where the HTTP API enforces both
// policies. Revisit if/when the outbound representation adopts the PT context
// wholesale (duration, views, category would come with it); the contract
// golden fixtures pin today's shape.
// videoObject renders the AS Video. objectID and watchURL are DIFFERENT things
// and the split is deliberate:
//
//   - `id` is the object's identity across the fediverse. Remote servers store
//     it, address Update/Delete to it, and thread replies against it. It stays
//     the /videos/{uuid} form FOREVER; changing it re-identifies the video
//     everywhere and orphans every existing reply.
//   - `url` is the human landing page, and moves to /v/{code}. ActivityPub
//     draws exactly this distinction, and remote copies pick the new value up
//     on the next Update we send — there is no mass re-send.
func videoObject(channelActor, objectID, watchURL, title, description string) map[string]any {
	return map[string]any{
		"id":           objectID,
		"type":         "Video",
		"name":         title,
		"content":      description,
		"attributedTo": channelActor,
		"url":          watchURL,
		"to":           []string{publicAudience},
	}
}

// buildDeleteVideo renders a Delete activity for a video (object = its AP id).
func (s *Service) buildDeleteVideo(channelHandle string, videoID uuid.UUID) ([]byte, error) {
	channelActor := s.baseURL + "/video-channels/" + channelHandle
	del := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       channelActor + "/activities/delete/" + uuid.NewString(),
		"type":     "Delete",
		"actor":    channelActor,
		"to":       []string{publicAudience},
		"object":   s.baseURL + "/videos/" + videoID.String(),
	}
	return json.Marshal(del)
}
