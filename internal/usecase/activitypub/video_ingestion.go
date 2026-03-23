package activitypub

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
)

func (s *Service) ingestRemoteVideo(ctx context.Context, videoObj map[string]interface{}, remoteActor *domain.APRemoteActor, activityID string) error {
	videoURI, ok := videoObj["id"].(string)
	if !ok {
		return fmt.Errorf("missing video id")
	}

	existingVideo, err := s.videoRepo.GetByRemoteURI(ctx, videoURI)
	if err == nil && existingVideo != nil {
		return s.updateRemoteVideo(ctx, videoObj, existingVideo, remoteActor)
	}

	title, _ := videoObj["name"].(string)
	if title == "" {
		title = "Untitled Remote Video"
	}

	description, _ := videoObj["content"].(string)
	if description == "" {
		description, _ = videoObj["summary"].(string)
	}

	duration := 0
	if durationStr, ok := videoObj["duration"].(string); ok {
		duration = parseDuration(durationStr)
	}

	videoURL := extractVideoURL(videoObj)
	if videoURL == "" {
		return fmt.Errorf("no video URL found in remote video object")
	}

	thumbnailURL := extractThumbnailURL(videoObj)

	instanceDomain := extractDomain(videoURI)

	privacy := determinePrivacy(videoObj)

	uploadDate := time.Now()
	if published, ok := videoObj["published"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, published); err == nil {
			uploadDate = parsed
		}
	}

	language := "en"
	if lang, ok := videoObj["language"].(map[string]interface{}); ok {
		if identifier, ok := lang["identifier"].(string); ok {
			language = identifier
		}
	} else if langStr, ok := videoObj["language"].(string); ok {
		language = langStr
	}

	tags := extractTags(videoObj)

	now := time.Now()
	video := &domain.Video{
		ID:                   uuid.New().String(),
		Title:                title,
		Description:          description,
		Duration:             duration,
		Privacy:              privacy,
		Status:               domain.StatusCompleted,
		UploadDate:           uploadDate,
		Tags:                 tags,
		Language:             language,
		IsRemote:             true,
		RemoteURI:            &videoURI,
		RemoteActorURI:       &remoteActor.ActorURI,
		RemoteVideoURL:       &videoURL,
		RemoteInstanceDomain: &instanceDomain,
		RemoteThumbnailURL:   &thumbnailURL,
		RemoteLastSyncedAt:   &now,
		CreatedAt:            uploadDate,
		UpdatedAt:            now,
	}

	if err := s.videoRepo.CreateRemoteVideo(ctx, video); err != nil {
		return fmt.Errorf("failed to create remote video: %w", err)
	}

	return nil
}

func (s *Service) updateRemoteVideo(ctx context.Context, videoObj map[string]interface{}, existingVideo *domain.Video, remoteActor *domain.APRemoteActor) error {
	if title, ok := videoObj["name"].(string); ok && title != "" {
		existingVideo.Title = title
	}

	if description, ok := videoObj["content"].(string); ok {
		existingVideo.Description = description
	} else if summary, ok := videoObj["summary"].(string); ok {
		existingVideo.Description = summary
	}

	if durationStr, ok := videoObj["duration"].(string); ok {
		existingVideo.Duration = parseDuration(durationStr)
	}

	if videoURL := extractVideoURL(videoObj); videoURL != "" {
		existingVideo.RemoteVideoURL = &videoURL
	}

	if thumbnailURL := extractThumbnailURL(videoObj); thumbnailURL != "" {
		existingVideo.RemoteThumbnailURL = &thumbnailURL
	}

	existingVideo.Privacy = determinePrivacy(videoObj)

	existingVideo.Tags = extractTags(videoObj)

	now := time.Now()
	existingVideo.RemoteLastSyncedAt = &now
	existingVideo.UpdatedAt = now

	if err := s.videoRepo.Update(ctx, existingVideo); err != nil {
		return fmt.Errorf("failed to update remote video: %w", err)
	}

	return nil
}
