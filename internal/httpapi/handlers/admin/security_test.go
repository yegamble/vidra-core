package admin

import (
	"athena/internal/domain"
	"athena/internal/repository"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Mock repositories
type MockVideoRepo struct {
	Video *domain.Video
}

func (m *MockVideoRepo) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	if m.Video != nil && m.Video.ID == id {
		return m.Video, nil
	}
	return nil, domain.NewDomainError("NOT_FOUND", "Video not found")
}

// Implement other methods as stubs to satisfy interface
func (m *MockVideoRepo) Create(ctx context.Context, video *domain.Video) error { return nil }
func (m *MockVideoRepo) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepo) Update(ctx context.Context, video *domain.Video) error      { return nil }
func (m *MockVideoRepo) Delete(ctx context.Context, id string, userID string) error { return nil }
func (m *MockVideoRepo) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepo) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepo) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return nil
}
func (m *MockVideoRepo) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return nil
}
func (m *MockVideoRepo) Count(ctx context.Context) (int64, error) { return 0, nil }
func (m *MockVideoRepo) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepo) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepo) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	return nil
}
func (m *MockVideoRepo) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	return nil, nil
}

type MockUserRepo struct {
	User *domain.User
}

func (m *MockUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	if m.User != nil && m.User.ID == id {
		return m.User, nil
	}
	return nil, domain.NewDomainError("NOT_FOUND", "User not found")
}

// Implement other methods as stubs
func (m *MockUserRepo) Create(ctx context.Context, user *domain.User, password string) error {
	return nil
}
func (m *MockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return nil, nil
}
func (m *MockUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	return nil, nil
}
func (m *MockUserRepo) Update(ctx context.Context, user *domain.User) error { return nil }
func (m *MockUserRepo) Delete(ctx context.Context, id string) error         { return nil }
func (m *MockUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	return nil, nil
}
func (m *MockUserRepo) Count(ctx context.Context) (int64, error) { return 0, nil }
func (m *MockUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	return "", nil
}
func (m *MockUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return nil
}
func (m *MockUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return nil
}
func (m *MockUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error { return nil }

func TestOEmbed_XMLInjection(t *testing.T) {
	// Setup mocks
	maliciousTitle := "Test Video</title><script>alert('XSS')</script><title>"
	videoID := "v123"
	userID := "u123"

	mockVideoRepo := &MockVideoRepo{
		Video: &domain.Video{
			ID:       videoID,
			UserID:   userID,
			Title:    maliciousTitle,
			Privacy:  domain.PrivacyPublic,
			Duration: 120,
		},
	}

	mockUserRepo := &MockUserRepo{
		User: &domain.User{
			ID:          userID,
			DisplayName: "Test User",
		},
	}

	h := NewInstanceHandlers(&repository.ModerationRepository{}, mockUserRepo, mockVideoRepo)

	// Create request
	req, err := http.NewRequest("GET", "/oembed?url=http://example.com/videos/"+videoID+"&format=xml", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(h.OEmbed)

	handler.ServeHTTP(rr, req)

	// Check response code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	// Check response body for injection
	// The injection should be escaped now, so it should NOT contain the raw script tag
	rawInjection := "<script>alert('XSS')</script>"
	if strings.Contains(rr.Body.String(), rawInjection) {
		t.Errorf("Vulnerability still exists! Response contains raw injection: %s", rawInjection)
	}

	// It SHOULD contain the escaped version.
	// encoding/xml escapes < as &lt; and > as &gt;.
	// We check for the starting tag at least.
	if !strings.Contains(rr.Body.String(), "&lt;script&gt;") {
		t.Errorf("Expected escaped script tag not found. Response: %s", rr.Body.String())
	} else {
		t.Logf("Security fix verified! Response contains escaped characters: %s", rr.Body.String())
	}
}
