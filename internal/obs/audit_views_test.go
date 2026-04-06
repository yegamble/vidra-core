package obs

import (
	"testing"

	"github.com/google/uuid"

	"vidra-core/internal/domain"
)

func TestVideoAuditView_ToLogKeys(t *testing.T) {
	v := &domain.Video{
		ID:    "v-123",
		Title: "Test Video",
	}
	view := NewVideoAuditView(v)
	keys := view.ToLogKeys()

	if keys["video-id"] != "v-123" {
		t.Errorf("expected video-id=v-123, got %v", keys["video-id"])
	}
	if keys["video-name"] != "Test Video" {
		t.Errorf("expected video-name=Test Video, got %v", keys["video-name"])
	}
	// Must NOT expose any token or sensitive field
	for k := range keys {
		if k == "password" || k == "token" || k == "secret" {
			t.Errorf("unexpected sensitive field %q in VideoAuditView", k)
		}
	}
}

func TestUserAuditView_ToLogKeys(t *testing.T) {
	u := &domain.User{
		ID:       "u-456",
		Username: "alice",
		Email:    "alice@example.com",
	}
	view := NewUserAuditView(u)
	keys := view.ToLogKeys()

	if keys["user-id"] != "u-456" {
		t.Errorf("expected user-id=u-456, got %v", keys["user-id"])
	}
	if keys["user-username"] != "alice" {
		t.Errorf("expected user-username=alice, got %v", keys["user-username"])
	}
	// Email is intentionally absent (PII — GDPR compliance)
	if _, present := keys["user-email"]; present {
		t.Error("user-email should be absent from UserAuditView — it is PII")
	}
	// Must NOT expose password or tokens
	for k := range keys {
		if k == "password" || k == "token" || k == "twofa_secret" {
			t.Errorf("unexpected sensitive field %q in UserAuditView", k)
		}
	}
}

func TestChannelAuditView_ToLogKeys(t *testing.T) {
	c := &domain.Channel{
		ID:          uuid.New(),
		Handle:      "testchannel",
		DisplayName: "Test Channel",
		IsLocal:     true,
	}
	view := NewChannelAuditView(c)
	keys := view.ToLogKeys()

	if keys["channel-handle"] != "testchannel" {
		t.Errorf("expected channel-handle=testchannel, got %v", keys["channel-handle"])
	}
	if keys["channel-name"] != "Test Channel" {
		t.Errorf("expected channel-name=Test Channel, got %v", keys["channel-name"])
	}
}

func TestCommentAuditView_ToLogKeys(t *testing.T) {
	c := &domain.Comment{
		ID:      uuid.New(),
		VideoID: uuid.New(),
		UserID:  uuid.New(),
		Body:    "Hello world",
	}
	view := NewCommentAuditView(c)
	keys := view.ToLogKeys()

	// comment-text is intentionally absent (full body is user-generated PII)
	if _, present := keys["comment-text"]; present {
		t.Error("comment-text should be absent from CommentAuditView — it may contain PII")
	}
	// Essential identifying fields should be present
	if keys["comment-id"] == "" {
		t.Error("expected comment-id to be present")
	}
	if keys["comment-user-id"] == "" {
		t.Error("expected comment-user-id to be present")
	}
}

func TestConfigAuditView_ToLogKeys(t *testing.T) {
	view := NewConfigAuditView(map[string]interface{}{
		"instance-name": "My Vidra",
		"signup":        true,
	})
	keys := view.ToLogKeys()

	if keys["config-instance-name"] != "My Vidra" {
		t.Errorf("expected config-instance-name, got %v", keys["config-instance-name"])
	}
	if keys["config-signup"] != true {
		t.Errorf("expected config-signup=true, got %v", keys["config-signup"])
	}
}

func TestAbuseAuditView_ToLogKeys(t *testing.T) {
	a := &domain.AbuseReport{
		ID:         "abuse-1",
		Reason:     "spam",
		ReporterID: "u-789",
	}
	view := NewAbuseAuditView(a)
	keys := view.ToLogKeys()

	if keys["abuse-id"] != "abuse-1" {
		t.Errorf("expected abuse-id=abuse-1, got %v", keys["abuse-id"])
	}
	if keys["abuse-reason"] != "spam" {
		t.Errorf("expected abuse-reason=spam, got %v", keys["abuse-reason"])
	}
}

func TestVideoImportAuditView_ToLogKeys(t *testing.T) {
	vi := &domain.VideoImport{
		ID:        "import-1",
		SourceURL: "https://youtube.com/watch?v=xyz",
		UserID:    "u-123",
	}
	view := NewVideoImportAuditView(vi)
	keys := view.ToLogKeys()

	if keys["video-import-id"] != "import-1" {
		t.Errorf("expected video-import-id=import-1, got %v", keys["video-import-id"])
	}
	if keys["video-import-source-url"] != "https://youtube.com/watch?v=xyz" {
		t.Errorf("expected video-import-source-url, got %v", keys["video-import-source-url"])
	}
}

func TestChannelSyncAuditView_ToLogKeys(t *testing.T) {
	cs := &domain.ChannelSync{
		ID:                 1,
		ChannelID:          "ch-123",
		ExternalChannelURL: "https://youtube.com/@channel",
	}
	view := NewChannelSyncAuditView(cs)
	keys := view.ToLogKeys()

	if keys["channel-sync-channel-id"] != "ch-123" {
		t.Errorf("expected channel-sync-channel-id, got %v", keys["channel-sync-channel-id"])
	}
	if keys["channel-sync-external-channel-url"] != "https://youtube.com/@channel" {
		t.Errorf("expected channel-sync-external-channel-url, got %v", keys["channel-sync-external-channel-url"])
	}
}
