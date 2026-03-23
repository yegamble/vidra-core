package activitypub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"athena/internal/domain"
)

func (s *Service) BuildVideoObject(ctx context.Context, video *domain.Video) (*domain.VideoObject, error) {
	if video == nil {
		return nil, fmt.Errorf("video is nil")
	}

	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video owner: %w", err)
	}

	videoID := fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, video.ID)
	actorID := s.buildActorID(owner.Username)

	videoObj := &domain.VideoObject{
		Context:      []interface{}{domain.ActivityStreamsContext, domain.PeerTubeContext},
		Type:         domain.ObjectTypeVideo,
		ID:           videoID,
		Name:         video.Title,
		UUID:         video.ID,
		Published:    &video.UploadDate,
		Updated:      &video.UpdatedAt,
		AttributedTo: []string{actorID},
		State:        1,
	}

	s.populateVideoDescription(videoObj, video)
	s.populateVideoDuration(videoObj, video)
	s.populateVideoFlags(videoObj, video)
	s.populateVideoMetadata(videoObj, video)
	s.populateVideoURLs(videoObj, video)
	s.populateVideoThumbnail(videoObj, video)
	s.populateVideoAudience(videoObj, video, actorID)

	videoObj.Likes = videoID + "/likes"
	videoObj.Dislikes = videoID + "/dislikes"
	videoObj.Shares = videoID + "/shares"
	videoObj.Comments = videoID + "/comments"

	return videoObj, nil
}

// populateVideoDescription sets the content and summary from the video description.
func (s *Service) populateVideoDescription(obj *domain.VideoObject, video *domain.Video) {
	if video.Description != "" {
		obj.Content = video.Description
		obj.Summary = video.Description
	}
}

// populateVideoDuration formats the video duration as an ISO 8601 duration string.
func (s *Service) populateVideoDuration(obj *domain.VideoObject, video *domain.Video) {
	if video.Duration <= 0 {
		return
	}
	hours := video.Duration / 3600
	minutes := (video.Duration % 3600) / 60
	seconds := video.Duration % 60
	if hours > 0 {
		obj.Duration = fmt.Sprintf("PT%dH%dM%dS", hours, minutes, seconds)
	} else if minutes > 0 {
		obj.Duration = fmt.Sprintf("PT%dM%dS", minutes, seconds)
	} else {
		obj.Duration = fmt.Sprintf("PT%dS", seconds)
	}
}

// populateVideoFlags sets boolean flags and view count on the video object.
func (s *Service) populateVideoFlags(obj *domain.VideoObject, video *domain.Video) {
	obj.CommentsEnabled = true
	obj.DownloadEnabled = true
	obj.Sensitive = video.Privacy == domain.PrivacyPrivate
	obj.WaitTranscoding = video.Status == domain.StatusProcessing
	obj.Views = int(video.Views)
}

// populateVideoMetadata sets tags, category, and language on the video object.
func (s *Service) populateVideoMetadata(obj *domain.VideoObject, video *domain.Video) {
	if len(video.Tags) > 0 {
		obj.Tag = make([]domain.APTag, len(video.Tags))
		for i, tag := range video.Tags {
			obj.Tag[i] = domain.APTag{
				Type: "Hashtag",
				Name: "#" + tag,
			}
		}
	}

	if video.Category != nil {
		obj.Category = &domain.APCategory{
			Identifier: video.Category.ID.String(),
			Name:       video.Category.Name,
		}
	}

	if video.Language != "" {
		obj.Language = &domain.APLanguage{
			Identifier: video.Language,
		}
	}
}

// populateVideoURLs builds streaming URLs (MP4, HLS, and per-quality variants) from output paths.
func (s *Service) populateVideoURLs(obj *domain.VideoObject, video *domain.Video) {
	if len(video.OutputPaths) == 0 {
		return
	}

	obj.URL = append(obj.URL, domain.APUrl{
		Type:      "Link",
		MediaType: "video/mp4",
		Href:      fmt.Sprintf("%s/videos/%s/stream", s.cfg.PublicBaseURL, video.ID),
		Height:    video.Metadata.Height,
		Width:     video.Metadata.Width,
	})

	obj.URL = append(obj.URL, domain.APUrl{
		Type:      "Link",
		MediaType: "application/x-mpegURL",
		Href:      fmt.Sprintf("%s/videos/%s/master.m3u8", s.cfg.PublicBaseURL, video.ID),
	})

	for quality, path := range video.OutputPaths {
		variantURL := domain.APUrl{
			Type:      "Link",
			MediaType: "application/x-mpegURL",
			Href:      fmt.Sprintf("%s%s", s.cfg.PublicBaseURL, path),
		}
		var height int
		if _, err := fmt.Sscanf(quality, "%dp", &height); err == nil {
			variantURL.Height = height
			if video.Metadata.Width > 0 && video.Metadata.Height > 0 {
				variantURL.Width = (height * video.Metadata.Width) / video.Metadata.Height
			} else {
				variantURL.Width = (height * 16) / 9
			}
		}
		obj.URL = append(obj.URL, variantURL)
	}
}

// populateVideoThumbnail sets the icon/thumbnail on the video object.
func (s *Service) populateVideoThumbnail(obj *domain.VideoObject, video *domain.Video) {
	if video.ThumbnailPath == "" {
		return
	}
	thumbnailURL := video.ThumbnailPath
	if !strings.HasPrefix(thumbnailURL, "http") {
		thumbnailURL = strings.TrimPrefix(thumbnailURL, "/")
		thumbnailURL = fmt.Sprintf("%s/%s", s.cfg.PublicBaseURL, thumbnailURL)
	}
	obj.Icon = []domain.Image{{
		Type:      "Image",
		URL:       thumbnailURL,
		MediaType: "image/jpeg",
	}}
}

// populateVideoAudience sets the To/Cc audience fields based on video privacy.
func (s *Service) populateVideoAudience(obj *domain.VideoObject, video *domain.Video, actorID string) {
	switch video.Privacy {
	case domain.PrivacyPublic:
		obj.To = []string{ActivityPubPublic}
		obj.Cc = []string{actorID + "/followers"}
	case domain.PrivacyUnlisted:
		obj.To = []string{actorID + "/followers"}
		obj.Cc = []string{ActivityPubPublic}
	case domain.PrivacyPrivate:
		obj.To = []string{actorID + "/followers"}
	}
}

func (s *Service) CreateVideoActivity(ctx context.Context, video *domain.Video) (*domain.Activity, error) {
	videoObj, err := s.BuildVideoObject(ctx, video)
	if err != nil {
		return nil, fmt.Errorf("failed to build video object: %w", err)
	}

	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video owner: %w", err)
	}

	actorID := s.buildActorID(owner.Username)
	activityID := fmt.Sprintf("%s/activities/create-%s", s.cfg.PublicBaseURL, video.ID)

	now := time.Now()

	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeCreate,
		ID:        activityID,
		Actor:     actorID,
		Object:    videoObj,
		Published: &now,
		To:        videoObj.To,
		Cc:        videoObj.Cc,
	}

	return activity, nil
}

func (s *Service) PublishVideo(ctx context.Context, videoID string) error {
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found")
	}

	if video.Status != domain.StatusCompleted {
		return fmt.Errorf("video not completed")
	}

	activity, err := s.CreateVideoActivity(ctx, video)
	if err != nil {
		return fmt.Errorf("failed to create activity: %w", err)
	}

	if err := s.enqueueFollowerDeliveries(ctx, video.UserID, activity, "video "+videoID); err != nil {
		return err
	}

	return s.storeOutboxActivity(ctx, activity)
}

func (s *Service) UpdateVideo(ctx context.Context, videoID string) error {
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found")
	}

	if video.Privacy == "private" {
		return nil
	}

	videoObj, err := s.BuildVideoObject(ctx, video)
	if err != nil {
		return fmt.Errorf("failed to build video object: %w", err)
	}

	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return fmt.Errorf("failed to get video owner: %w", err)
	}

	actorID := s.buildActorID(owner.Username)
	activityID := fmt.Sprintf("%s/videos/%s/activity/update-%d", s.cfg.PublicBaseURL, video.ID, time.Now().Unix())

	now := time.Now()
	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeUpdate,
		ID:        activityID,
		Actor:     actorID,
		Object:    videoObj,
		Published: &now,
		To:        videoObj.To,
		Cc:        videoObj.Cc,
	}

	if err := s.enqueueFollowerDeliveries(ctx, video.UserID, activity, "video "+videoID); err != nil {
		return err
	}

	return s.storeOutboxActivity(ctx, activity)
}

func (s *Service) DeleteVideo(ctx context.Context, videoID string) error {
	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}
	if video == nil {
		return fmt.Errorf("video not found")
	}

	owner, err := s.userRepo.GetByID(ctx, video.UserID)
	if err != nil {
		return fmt.Errorf("failed to get video owner: %w", err)
	}

	actorID := s.buildActorID(owner.Username)
	videoObjectID := fmt.Sprintf("%s/videos/%s", s.cfg.PublicBaseURL, video.ID)
	activityID := fmt.Sprintf("%s/videos/%s/activity/delete-%d", s.cfg.PublicBaseURL, video.ID, time.Now().Unix())

	now := time.Now()
	activity := &domain.Activity{
		Context:   []interface{}{domain.ActivityStreamsContext},
		Type:      domain.ActivityTypeDelete,
		ID:        activityID,
		Actor:     actorID,
		Object:    videoObjectID,
		Published: &now,
		To:        []string{ActivityPubPublic},
		Cc:        []string{actorID + "/followers"},
	}

	if err := s.enqueueFollowerDeliveries(ctx, video.UserID, activity, "video "+videoID); err != nil {
		return err
	}

	return s.storeOutboxActivity(ctx, activity)
}
