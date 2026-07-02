package federation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// publicAudience is the ActivityStreams magic collection marking an activity as
// public (delivered to followers and visible to anyone).
const publicAudience = "https://www.w3.org/ns/activitystreams#Public"

// AnnounceVideo fans a newly-published video out to the publishing channel's
// remote followers: it builds a `Create{Video}` activity (attributed to the
// channel) and enqueues one signed delivery per distinct follower inbox. It is a
// no-op unless the video is public + published and the channel has remote
// followers, so it is safe to call on every publish. Errors are returned so the
// caller can log them; a failure never rolls back the publish.
func (s *Service) AnnounceVideo(ctx context.Context, videoID uuid.UUID) error {
	v, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	// Only public, published videos ever federate.
	if v.Privacy != "public" || v.State != "published" {
		return nil
	}
	ch, err := s.repo.GetChannelByID(ctx, v.ChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	inboxes, err := s.repo.ListRemoteFollowerInboxes(ctx, v.ChannelID)
	if err != nil {
		return err
	}
	if len(inboxes) == 0 {
		return nil
	}
	payload, err := s.buildCreateVideo(ch.Handle, v)
	if err != nil {
		return err
	}
	for _, inbox := range inboxes {
		if inbox == "" {
			continue
		}
		if err := s.enqueueChannelDelivery(ctx, ch.ID, ch.Handle, inbox, payload); err != nil {
			return err
		}
	}
	return nil
}

// buildCreateVideo renders a Create activity wrapping the video as an AS Video
// object, attributed to the channel actor and addressed to the public.
func (s *Service) buildCreateVideo(channelHandle string, v sqlcgen.GetVideoByIDRow) ([]byte, error) {
	channelActor := s.baseURL + "/video-channels/" + channelHandle
	videoURL := s.baseURL + "/videos/watch/" + v.ID.String()
	create := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       channelActor + "/activities/create/" + uuid.NewString(),
		"type":     "Create",
		"actor":    channelActor,
		"to":       []string{publicAudience},
		"object": map[string]any{
			"id":           videoURL,
			"type":         "Video",
			"name":         v.Title,
			"content":      v.Description,
			"attributedTo": channelActor,
			"url":          videoURL,
			"to":           []string{publicAudience},
		},
	}
	return json.Marshal(create)
}
