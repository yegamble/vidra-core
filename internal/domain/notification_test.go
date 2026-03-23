package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNotificationType_Constants(t *testing.T) {
	tests := []struct {
		name      string
		notifType NotificationType
		expected  string
	}{
		{"new_video", NotificationNewVideo, "new_video"},
		{"video_processed", NotificationVideoProcessed, "video_processed"},
		{"video_failed", NotificationVideoFailed, "video_failed"},
		{"new_subscriber", NotificationNewSubscriber, "new_subscriber"},
		{"comment", NotificationComment, "comment"},
		{"mention", NotificationMention, "mention"},
		{"system", NotificationSystem, "system"},
		{"new_message", NotificationNewMessage, "new_message"},
		{"message_read", NotificationMessageRead, "message_read"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.notifType) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, string(tt.notifType))
			}
		})
	}
}

func TestNotification_Struct(t *testing.T) {
	userID := uuid.New()
	notifID := uuid.New()
	now := time.Now()

	notif := Notification{
		ID:        notifID,
		UserID:    userID,
		Type:      NotificationNewVideo,
		Title:     "New Video",
		Message:   "A new video was uploaded",
		Data:      map[string]interface{}{"video_id": "video-123"},
		Read:      false,
		CreatedAt: now,
		ReadAt:    nil,
	}

	if notif.ID != notifID {
		t.Errorf("Expected ID %s, got %s", notifID, notif.ID)
	}

	if notif.UserID != userID {
		t.Errorf("Expected UserID %s, got %s", userID, notif.UserID)
	}

	if notif.Type != NotificationNewVideo {
		t.Errorf("Expected type %s, got %s", NotificationNewVideo, notif.Type)
	}

	if notif.Title != "New Video" {
		t.Errorf("Expected title 'New Video', got %s", notif.Title)
	}

	if notif.Read {
		t.Error("Expected Read to be false")
	}

	if notif.ReadAt != nil {
		t.Error("Expected ReadAt to be nil")
	}
}

func TestNotification_JSONMarshaling(t *testing.T) {
	userID := uuid.New()
	notifID := uuid.New()
	videoID := uuid.New()

	notif := Notification{
		ID:      notifID,
		UserID:  userID,
		Type:    NotificationNewVideo,
		Title:   "Test Notification",
		Message: "Test message",
		Data: map[string]interface{}{
			"video_id":     videoID.String(),
			"channel_name": "Test Channel",
		},
		Read:      false,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		ReadAt:    nil,
	}

	data, err := json.Marshal(notif)
	if err != nil {
		t.Fatalf("Failed to marshal notification: %v", err)
	}

	var decoded Notification
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal notification: %v", err)
	}

	if decoded.ID != notif.ID {
		t.Errorf("ID mismatch: expected %s, got %s", notif.ID, decoded.ID)
	}

	if decoded.Type != notif.Type {
		t.Errorf("Type mismatch: expected %s, got %s", notif.Type, decoded.Type)
	}

	if decoded.Title != notif.Title {
		t.Errorf("Title mismatch: expected %s, got %s", notif.Title, decoded.Title)
	}
}

func TestNotificationData_Struct(t *testing.T) {
	videoID := uuid.New()
	channelID := uuid.New()
	senderID := uuid.New()

	data := NotificationData{
		VideoID:        &videoID,
		ChannelID:      &channelID,
		ChannelName:    "Test Channel",
		VideoTitle:     "Test Video",
		ThumbnailCID:   "QmThumbnail123",
		SenderID:       &senderID,
		SenderName:     "Test Sender",
		MessagePreview: "Hello, world!",
	}

	if data.VideoID == nil || *data.VideoID != videoID {
		t.Error("VideoID mismatch")
	}

	if data.ChannelID == nil || *data.ChannelID != channelID {
		t.Error("ChannelID mismatch")
	}

	if data.ChannelName != "Test Channel" {
		t.Errorf("Expected channel name 'Test Channel', got %s", data.ChannelName)
	}

	if data.VideoTitle != "Test Video" {
		t.Errorf("Expected video title 'Test Video', got %s", data.VideoTitle)
	}

	if data.ThumbnailCID != "QmThumbnail123" {
		t.Errorf("Expected thumbnail CID 'QmThumbnail123', got %s", data.ThumbnailCID)
	}
}

func TestNotificationData_JSONMarshaling(t *testing.T) {
	videoID := uuid.New()
	channelID := uuid.New()

	original := NotificationData{
		VideoID:      &videoID,
		ChannelID:    &channelID,
		ChannelName:  "Test Channel",
		VideoTitle:   "Test Video",
		ThumbnailCID: "QmTest",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded NotificationData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.VideoID == nil || *decoded.VideoID != videoID {
		t.Error("VideoID mismatch after round trip")
	}

	if decoded.ChannelName != original.ChannelName {
		t.Error("ChannelName mismatch after round trip")
	}
}

func TestNotificationFilter_Struct(t *testing.T) {
	userID := uuid.New()
	unread := true
	startDate := time.Now().Add(-24 * time.Hour)
	endDate := time.Now()

	filter := NotificationFilter{
		UserID:    userID,
		Unread:    &unread,
		Types:     []NotificationType{NotificationNewVideo, NotificationComment},
		Limit:     20,
		Offset:    0,
		StartDate: &startDate,
		EndDate:   &endDate,
	}

	if filter.UserID != userID {
		t.Errorf("UserID mismatch")
	}

	if filter.Unread == nil || *filter.Unread != true {
		t.Error("Expected Unread to be true")
	}

	if len(filter.Types) != 2 {
		t.Errorf("Expected 2 types, got %d", len(filter.Types))
	}

	if filter.Limit != 20 {
		t.Errorf("Expected limit 20, got %d", filter.Limit)
	}

	if filter.StartDate == nil {
		t.Error("Expected StartDate to be set")
	}

	if filter.EndDate == nil {
		t.Error("Expected EndDate to be set")
	}
}

func TestNotificationFilter_EmptyTypes(t *testing.T) {
	filter := NotificationFilter{
		UserID: uuid.New(),
		Types:  []NotificationType{},
		Limit:  10,
	}

	if filter.Types == nil {
		t.Error("Expected Types to be non-nil empty slice")
	}

	if len(filter.Types) != 0 {
		t.Errorf("Expected 0 types, got %d", len(filter.Types))
	}
}

func TestNotificationStats_Struct(t *testing.T) {
	stats := NotificationStats{
		TotalCount:  100,
		UnreadCount: 25,
		ByType: map[NotificationType]int{
			NotificationNewVideo:   50,
			NotificationComment:    30,
			NotificationNewMessage: 20,
		},
	}

	if stats.TotalCount != 100 {
		t.Errorf("Expected TotalCount 100, got %d", stats.TotalCount)
	}

	if stats.UnreadCount != 25 {
		t.Errorf("Expected UnreadCount 25, got %d", stats.UnreadCount)
	}

	if len(stats.ByType) != 3 {
		t.Errorf("Expected 3 types in ByType, got %d", len(stats.ByType))
	}

	if stats.ByType[NotificationNewVideo] != 50 {
		t.Errorf("Expected 50 new video notifications, got %d", stats.ByType[NotificationNewVideo])
	}
}

func TestNotificationStats_JSONMarshaling(t *testing.T) {
	original := NotificationStats{
		TotalCount:  100,
		UnreadCount: 25,
		ByType: map[NotificationType]int{
			NotificationNewVideo: 50,
			NotificationComment:  30,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded NotificationStats
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.TotalCount != original.TotalCount {
		t.Error("TotalCount mismatch after round trip")
	}

	if decoded.UnreadCount != original.UnreadCount {
		t.Error("UnreadCount mismatch after round trip")
	}

	if len(decoded.ByType) != len(original.ByType) {
		t.Error("ByType length mismatch after round trip")
	}
}

func TestNotification_ReadStatus(t *testing.T) {
	notif := Notification{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		Type:      NotificationComment,
		Title:     "New Comment",
		Message:   "Someone commented on your video",
		Read:      false,
		CreatedAt: time.Now(),
		ReadAt:    nil,
	}

	if notif.Read {
		t.Error("New notification should be unread")
	}

	if notif.ReadAt != nil {
		t.Error("New notification should have nil ReadAt")
	}

	// Simulate marking as read
	readTime := time.Now()
	notif.Read = true
	notif.ReadAt = &readTime

	if !notif.Read {
		t.Error("Notification should be marked as read")
	}

	if notif.ReadAt == nil {
		t.Error("ReadAt should be set")
	}

	if !notif.ReadAt.Equal(readTime) {
		t.Error("ReadAt timestamp mismatch")
	}
}

func TestNotificationData_OptionalFields(t *testing.T) {
	// Test with minimal fields
	data := NotificationData{
		ChannelName: "Test Channel",
	}

	if data.VideoID != nil {
		t.Error("Expected VideoID to be nil")
	}

	if data.ChannelID != nil {
		t.Error("Expected ChannelID to be nil")
	}

	if data.ChannelName != "Test Channel" {
		t.Error("ChannelName should be set")
	}

	// Test JSON marshaling with nil fields
	jsonData, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded NotificationData
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.VideoID != nil {
		t.Error("VideoID should remain nil after round trip")
	}
}
