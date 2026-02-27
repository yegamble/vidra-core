package redundancy

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// ==================== Mock Definitions ====================

// MockRedundancyRepository is a mock implementation of port.RedundancyRepository
type MockRedundancyRepository struct {
	mock.Mock
}

func (m *MockRedundancyRepository) CreateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	args := m.Called(ctx, peer)
	return args.Error(0)
}

func (m *MockRedundancyRepository) GetInstancePeerByID(ctx context.Context, id string) (*domain.InstancePeer, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.InstancePeer), args.Error(1)
}

func (m *MockRedundancyRepository) ListInstancePeers(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error) {
	args := m.Called(ctx, limit, offset, activeOnly)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.InstancePeer), args.Error(1)
}

func (m *MockRedundancyRepository) UpdateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error {
	args := m.Called(ctx, peer)
	return args.Error(0)
}

func (m *MockRedundancyRepository) UpdateInstancePeerContact(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRedundancyRepository) DeleteInstancePeer(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRedundancyRepository) GetActiveInstancesWithCapacity(ctx context.Context, videoSizeBytes int64) ([]*domain.InstancePeer, error) {
	args := m.Called(ctx, videoSizeBytes)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.InstancePeer), args.Error(1)
}

func (m *MockRedundancyRepository) CreateVideoRedundancy(ctx context.Context, redundancy *domain.VideoRedundancy) error {
	args := m.Called(ctx, redundancy)
	return args.Error(0)
}

func (m *MockRedundancyRepository) GetVideoRedundancyByID(ctx context.Context, id string) (*domain.VideoRedundancy, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoRedundancy), args.Error(1)
}

func (m *MockRedundancyRepository) GetVideoRedundanciesByVideoID(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRedundancy), args.Error(1)
}

func (m *MockRedundancyRepository) GetVideoRedundanciesByVideoIDs(ctx context.Context, videoIDs []string) ([]*domain.VideoRedundancy, error) {
	args := m.Called(ctx, videoIDs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRedundancy), args.Error(1)
}

func (m *MockRedundancyRepository) GetVideoRedundanciesByInstanceID(ctx context.Context, instanceID string) ([]*domain.VideoRedundancy, error) {
	args := m.Called(ctx, instanceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRedundancy), args.Error(1)
}

func (m *MockRedundancyRepository) UpdateVideoRedundancy(ctx context.Context, redundancy *domain.VideoRedundancy) error {
	args := m.Called(ctx, redundancy)
	return args.Error(0)
}

func (m *MockRedundancyRepository) UpdateRedundancyProgress(ctx context.Context, id string, bytesTransferred, speedBPS int64) error {
	args := m.Called(ctx, id, bytesTransferred, speedBPS)
	return args.Error(0)
}

func (m *MockRedundancyRepository) CancelRedundanciesByInstanceID(ctx context.Context, instanceID string) error {
	args := m.Called(ctx, instanceID)
	return args.Error(0)
}

func (m *MockRedundancyRepository) DeleteVideoRedundancy(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRedundancyRepository) ListPendingRedundancies(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRedundancy), args.Error(1)
}

func (m *MockRedundancyRepository) ListFailedRedundancies(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRedundancy), args.Error(1)
}

func (m *MockRedundancyRepository) ListRedundanciesForResync(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRedundancy), args.Error(1)
}

func (m *MockRedundancyRepository) CreateRedundancyPolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	args := m.Called(ctx, policy)
	return args.Error(0)
}

func (m *MockRedundancyRepository) GetRedundancyPolicyByID(ctx context.Context, id string) (*domain.RedundancyPolicy, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.RedundancyPolicy), args.Error(1)
}

func (m *MockRedundancyRepository) ListRedundancyPolicies(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error) {
	args := m.Called(ctx, enabledOnly)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RedundancyPolicy), args.Error(1)
}

func (m *MockRedundancyRepository) ListPoliciesToEvaluate(ctx context.Context) ([]*domain.RedundancyPolicy, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RedundancyPolicy), args.Error(1)
}

func (m *MockRedundancyRepository) UpdateRedundancyPolicy(ctx context.Context, policy *domain.RedundancyPolicy) error {
	args := m.Called(ctx, policy)
	return args.Error(0)
}

func (m *MockRedundancyRepository) UpdatePolicyEvaluationTime(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRedundancyRepository) DeleteRedundancyPolicy(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRedundancyRepository) CreateSyncLog(ctx context.Context, log *domain.RedundancySyncLog) error {
	args := m.Called(ctx, log)
	return args.Error(0)
}

func (m *MockRedundancyRepository) CleanupOldSyncLogs(ctx context.Context) (int, error) {
	args := m.Called(ctx)
	return args.Int(0), args.Error(1)
}

func (m *MockRedundancyRepository) GetRedundancyStats(ctx context.Context) (map[string]interface{}, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]interface{}), args.Error(1)
}

func (m *MockRedundancyRepository) GetVideoRedundancyHealth(ctx context.Context, videoID string) (float64, error) {
	args := m.Called(ctx, videoID)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockRedundancyRepository) CheckInstanceHealth(ctx context.Context) (int, error) {
	args := m.Called(ctx)
	return args.Int(0), args.Error(1)
}

// MockVideoRepository is a mock implementation of port.RedundancyVideoRepository
type MockVideoRepository struct {
	mock.Mock
}

func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) GetVideosForRedundancy(ctx context.Context, strategy domain.RedundancyStrategy, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, strategy, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}

// MockHTTPDoer is a mock implementation of HTTPDoer
type MockHTTPDoer struct {
	mock.Mock
}

func (m *MockHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

// ==================== Helper Functions ====================

func newTestService(redundancyRepo *MockRedundancyRepository, videoRepo *MockVideoRepository, httpDoer *MockHTTPDoer) *Service {
	return NewService(redundancyRepo, videoRepo, httpDoer)
}

func validInstancePeer() *domain.InstancePeer {
	return &domain.InstancePeer{
		ID:                   "peer-1",
		InstanceURL:          "https://example.com",
		InstanceName:         "Test Instance",
		InstanceHost:         "example.com",
		Software:             "peertube",
		Version:              "5.0",
		AutoAcceptRedundancy: true,
		MaxRedundancySizeGB:  100,
		AcceptsNewRedundancy: true,
		IsActive:             true,
		TotalStorageBytes:    0,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
}

func validVideo() *domain.Video {
	return &domain.Video{
		ID:         "video-1",
		Title:      "Test Video",
		FileSize:   1024 * 1024 * 100, // 100 MB
		Views:      1000,
		UploadDate: time.Now().Add(-24 * time.Hour),
	}
}

func validPolicy() *domain.RedundancyPolicy {
	return &domain.RedundancyPolicy{
		ID:                      "policy-1",
		Name:                    "Trending Policy",
		Description:             "Redundancy for trending videos",
		Strategy:                domain.RedundancyStrategyTrending,
		Enabled:                 true,
		MinViews:                100,
		TargetInstanceCount:     2,
		MinInstanceCount:        1,
		EvaluationIntervalHours: 6,
	}
}

func validVideoRedundancy() *domain.VideoRedundancy {
	return &domain.VideoRedundancy{
		ID:               "redundancy-1",
		VideoID:          "video-1",
		TargetInstanceID: "peer-1",
		Strategy:         domain.RedundancyStrategyTrending,
		Status:           domain.RedundancyStatusPending,
		FileSizeBytes:    1024 * 1024 * 100,
		Priority:         10,
		AutoResync:       true,
		MaxSyncAttempts:  5,
	}
}

// ==================== RegisterInstancePeer Tests ====================

func TestRegisterInstancePeer(t *testing.T) {
	tests := []struct {
		name      string
		peer      *domain.InstancePeer
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success with empty actor URL sets default",
			peer: &domain.InstancePeer{
				InstanceURL:          "https://peer.example.com",
				AcceptsNewRedundancy: true,
				IsActive:             true,
			},
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CreateInstancePeer", mock.Anything, mock.MatchedBy(func(p *domain.InstancePeer) bool {
					return p.ActorURL == "https://peer.example.com/actor"
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "success with existing actor URL preserves it",
			peer: &domain.InstancePeer{
				InstanceURL:          "https://peer.example.com",
				ActorURL:             "https://peer.example.com/custom/actor",
				AcceptsNewRedundancy: true,
				IsActive:             true,
			},
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CreateInstancePeer", mock.Anything, mock.MatchedBy(func(p *domain.InstancePeer) bool {
					return p.ActorURL == "https://peer.example.com/custom/actor"
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "validation error on empty URL",
			peer: &domain.InstancePeer{
				InstanceURL: "",
			},
			setupMock: func(repo *MockRedundancyRepository) {},
			wantErr:   true,
			errMsg:    "invalid instance peer",
		},
		{
			name: "repository error propagated",
			peer: &domain.InstancePeer{
				InstanceURL:          "https://peer.example.com",
				AcceptsNewRedundancy: true,
				IsActive:             true,
			},
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CreateInstancePeer", mock.Anything, mock.Anything).Return(errors.New("db error"))
			},
			wantErr: true,
			errMsg:  "db error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.RegisterInstancePeer(context.Background(), tt.peer)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== GetInstancePeer Tests ====================

func TestGetInstancePeer(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(*MockRedundancyRepository)
		wantPeer  bool
		wantErr   bool
	}{
		{
			name: "success",
			id:   "peer-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(validInstancePeer(), nil)
			},
			wantPeer: true,
			wantErr:  false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetInstancePeerByID", mock.Anything, "nonexistent").Return(nil, domain.ErrInstancePeerNotFound)
			},
			wantPeer: false,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			peer, err := svc.GetInstancePeer(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, peer)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, peer)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== ListInstancePeers Tests ====================

func TestListInstancePeers(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		offset     int
		activeOnly bool
		setupMock  func(*MockRedundancyRepository)
		wantCount  int
		wantErr    bool
	}{
		{
			name:       "list all peers",
			limit:      10,
			offset:     0,
			activeOnly: false,
			setupMock: func(repo *MockRedundancyRepository) {
				peers := []*domain.InstancePeer{validInstancePeer(), validInstancePeer()}
				repo.On("ListInstancePeers", mock.Anything, 10, 0, false).Return(peers, nil)
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:       "list active only",
			limit:      10,
			offset:     0,
			activeOnly: true,
			setupMock: func(repo *MockRedundancyRepository) {
				peers := []*domain.InstancePeer{validInstancePeer()}
				repo.On("ListInstancePeers", mock.Anything, 10, 0, true).Return(peers, nil)
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:       "repository error",
			limit:      10,
			offset:     0,
			activeOnly: false,
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("ListInstancePeers", mock.Anything, 10, 0, false).Return(nil, errors.New("db error"))
			},
			wantCount: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			peers, err := svc.ListInstancePeers(context.Background(), tt.limit, tt.offset, tt.activeOnly)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, peers)
			} else {
				assert.NoError(t, err)
				assert.Len(t, peers, tt.wantCount)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== UpdateInstancePeer Tests ====================

func TestUpdateInstancePeer(t *testing.T) {
	tests := []struct {
		name      string
		peer      *domain.InstancePeer
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success",
			peer: validInstancePeer(),
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("UpdateInstancePeer", mock.Anything, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "validation error on empty URL",
			peer: &domain.InstancePeer{
				InstanceURL: "",
			},
			setupMock: func(repo *MockRedundancyRepository) {},
			wantErr:   true,
			errMsg:    "invalid instance peer",
		},
		{
			name: "repository error",
			peer: validInstancePeer(),
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("UpdateInstancePeer", mock.Anything, mock.Anything).Return(errors.New("db error"))
			},
			wantErr: true,
			errMsg:  "db error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.UpdateInstancePeer(context.Background(), tt.peer)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== DeleteInstancePeer Tests ====================

func TestDeleteInstancePeer(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name: "success",
			id:   "peer-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CancelRedundanciesByInstanceID", mock.Anything, "peer-1").Return(nil)
				repo.On("DeleteInstancePeer", mock.Anything, "peer-1").Return(nil)
			},
			wantErr: false,
		},
		{
			name: "error cancelling redundancies propagated",
			id:   "peer-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CancelRedundanciesByInstanceID", mock.Anything, "peer-1").Return(errors.New("db error"))
			},
			wantErr: true,
		},
		{
			name: "error deleting instance peer propagated",
			id:   "peer-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CancelRedundanciesByInstanceID", mock.Anything, "peer-1").Return(nil)
				repo.On("DeleteInstancePeer", mock.Anything, "peer-1").Return(errors.New("delete error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.DeleteInstancePeer(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== CreateRedundancy Tests ====================

func TestCreateRedundancy(t *testing.T) {
	tests := []struct {
		name       string
		videoID    string
		instanceID string
		strategy   domain.RedundancyStrategy
		priority   int
		setupMock  func(*MockRedundancyRepository, *MockVideoRepository)
		wantErr    bool
		errTarget  error
	}{
		{
			name:       "success",
			videoID:    "video-1",
			instanceID: "peer-1",
			strategy:   domain.RedundancyStrategyTrending,
			priority:   10,
			setupMock: func(rRepo *MockRedundancyRepository, vRepo *MockVideoRepository) {
				vRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
				rRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(validInstancePeer(), nil)
				rRepo.On("CreateVideoRedundancy", mock.Anything, mock.MatchedBy(func(r *domain.VideoRedundancy) bool {
					return r.VideoID == "video-1" &&
						r.TargetInstanceID == "peer-1" &&
						r.Status == domain.RedundancyStatusPending &&
						r.AutoResync == true &&
						r.MaxSyncAttempts == 5
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name:       "video not found",
			videoID:    "nonexistent",
			instanceID: "peer-1",
			strategy:   domain.RedundancyStrategyTrending,
			priority:   10,
			setupMock: func(rRepo *MockRedundancyRepository, vRepo *MockVideoRepository) {
				vRepo.On("GetByID", mock.Anything, "nonexistent").Return(nil, errors.New("not found"))
			},
			wantErr: true,
		},
		{
			name:       "instance not found",
			videoID:    "video-1",
			instanceID: "nonexistent",
			strategy:   domain.RedundancyStrategyTrending,
			priority:   10,
			setupMock: func(rRepo *MockRedundancyRepository, vRepo *MockVideoRepository) {
				vRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
				rRepo.On("GetInstancePeerByID", mock.Anything, "nonexistent").Return(nil, errors.New("not found"))
			},
			wantErr: true,
		},
		{
			name:       "instance inactive",
			videoID:    "video-1",
			instanceID: "peer-1",
			strategy:   domain.RedundancyStrategyTrending,
			priority:   10,
			setupMock: func(rRepo *MockRedundancyRepository, vRepo *MockVideoRepository) {
				vRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
				inactivePeer := validInstancePeer()
				inactivePeer.IsActive = false
				rRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(inactivePeer, nil)
			},
			wantErr:   true,
			errTarget: domain.ErrInstancePeerInactive,
		},
		{
			name:       "insufficient storage",
			videoID:    "video-1",
			instanceID: "peer-1",
			strategy:   domain.RedundancyStrategyTrending,
			priority:   10,
			setupMock: func(rRepo *MockRedundancyRepository, vRepo *MockVideoRepository) {
				vRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
				fullPeer := validInstancePeer()
				fullPeer.AcceptsNewRedundancy = false // HasCapacity will return false
				rRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(fullPeer, nil)
			},
			wantErr:   true,
			errTarget: domain.ErrInsufficientStorage,
		},
		{
			name:       "repository create error",
			videoID:    "video-1",
			instanceID: "peer-1",
			strategy:   domain.RedundancyStrategyTrending,
			priority:   10,
			setupMock: func(rRepo *MockRedundancyRepository, vRepo *MockVideoRepository) {
				vRepo.On("GetByID", mock.Anything, "video-1").Return(validVideo(), nil)
				rRepo.On("GetInstancePeerByID", mock.Anything, "peer-1").Return(validInstancePeer(), nil)
				rRepo.On("CreateVideoRedundancy", mock.Anything, mock.Anything).Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo, videoRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			result, err := svc.CreateRedundancy(context.Background(), tt.videoID, tt.instanceID, tt.strategy, tt.priority)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
				if tt.errTarget != nil {
					assert.ErrorIs(t, err, tt.errTarget)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.videoID, result.VideoID)
				assert.Equal(t, tt.instanceID, result.TargetInstanceID)
				assert.Equal(t, domain.RedundancyStatusPending, result.Status)
			}
			redundancyRepo.AssertExpectations(t)
			videoRepo.AssertExpectations(t)
		})
	}
}

// ==================== GetRedundancy Tests ====================

func TestGetRedundancy(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name: "success",
			id:   "redundancy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(validVideoRedundancy(), nil)
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetVideoRedundancyByID", mock.Anything, "nonexistent").Return(nil, domain.ErrRedundancyNotFound)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			result, err := svc.GetRedundancy(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== ListVideoRedundancies Tests ====================

func TestListVideoRedundancies(t *testing.T) {
	tests := []struct {
		name      string
		videoID   string
		setupMock func(*MockRedundancyRepository)
		wantCount int
		wantErr   bool
	}{
		{
			name:    "success returns redundancies",
			videoID: "video-1",
			setupMock: func(repo *MockRedundancyRepository) {
				redundancies := []*domain.VideoRedundancy{validVideoRedundancy(), validVideoRedundancy()}
				repo.On("GetVideoRedundanciesByVideoID", mock.Anything, "video-1").Return(redundancies, nil)
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:    "success returns empty list",
			videoID: "video-2",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetVideoRedundanciesByVideoID", mock.Anything, "video-2").Return([]*domain.VideoRedundancy{}, nil)
			},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name:    "repository error",
			videoID: "video-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetVideoRedundanciesByVideoID", mock.Anything, "video-1").Return(nil, errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			result, err := svc.ListVideoRedundancies(context.Background(), tt.videoID)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, tt.wantCount)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== CancelRedundancy Tests ====================

func TestCancelRedundancy(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success cancels pending redundancy",
			id:   "redundancy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				r := validVideoRedundancy()
				r.Status = domain.RedundancyStatusPending
				repo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(r, nil)
				repo.On("UpdateVideoRedundancy", mock.Anything, mock.MatchedBy(func(r *domain.VideoRedundancy) bool {
					return r.Status == domain.RedundancyStatusCancelled
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "success cancels syncing redundancy",
			id:   "redundancy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				r := validVideoRedundancy()
				r.Status = domain.RedundancyStatusSyncing
				repo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(r, nil)
				repo.On("UpdateVideoRedundancy", mock.Anything, mock.MatchedBy(func(r *domain.VideoRedundancy) bool {
					return r.Status == domain.RedundancyStatusCancelled
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "error cannot cancel synced redundancy",
			id:   "redundancy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				r := validVideoRedundancy()
				r.Status = domain.RedundancyStatusSynced
				repo.On("GetVideoRedundancyByID", mock.Anything, "redundancy-1").Return(r, nil)
			},
			wantErr: true,
			errMsg:  "cannot cancel synced redundancy",
		},
		{
			name: "error redundancy not found",
			id:   "nonexistent",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetVideoRedundancyByID", mock.Anything, "nonexistent").Return(nil, domain.ErrRedundancyNotFound)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.CancelRedundancy(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== DeleteRedundancy Tests ====================

func TestDeleteRedundancy(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name: "success",
			id:   "redundancy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("DeleteVideoRedundancy", mock.Anything, "redundancy-1").Return(nil)
			},
			wantErr: false,
		},
		{
			name: "repository error",
			id:   "redundancy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("DeleteVideoRedundancy", mock.Anything, "redundancy-1").Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.DeleteRedundancy(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== CreatePolicy Tests ====================

func TestCreatePolicy(t *testing.T) {
	tests := []struct {
		name      string
		policy    *domain.RedundancyPolicy
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name:   "success",
			policy: validPolicy(),
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CreateRedundancyPolicy", mock.Anything, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "validation error on empty name",
			policy: &domain.RedundancyPolicy{
				Name:                    "",
				Strategy:                domain.RedundancyStrategyTrending,
				TargetInstanceCount:     2,
				MinInstanceCount:        1,
				EvaluationIntervalHours: 6,
			},
			setupMock: func(repo *MockRedundancyRepository) {},
			wantErr:   true,
		},
		{
			name: "validation error on invalid target count",
			policy: &domain.RedundancyPolicy{
				Name:                    "Test",
				Strategy:                domain.RedundancyStrategyTrending,
				TargetInstanceCount:     0,
				MinInstanceCount:        1,
				EvaluationIntervalHours: 6,
			},
			setupMock: func(repo *MockRedundancyRepository) {},
			wantErr:   true,
		},
		{
			name:   "repository error",
			policy: validPolicy(),
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CreateRedundancyPolicy", mock.Anything, mock.Anything).Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.CreatePolicy(context.Background(), tt.policy)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== GetPolicy Tests ====================

func TestGetPolicy(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name: "success",
			id:   "policy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetRedundancyPolicyByID", mock.Anything, "policy-1").Return(validPolicy(), nil)
			},
			wantErr: false,
		},
		{
			name: "not found",
			id:   "nonexistent",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetRedundancyPolicyByID", mock.Anything, "nonexistent").Return(nil, domain.ErrPolicyNotFound)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			result, err := svc.GetPolicy(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== ListPolicies Tests ====================

func TestListPolicies(t *testing.T) {
	tests := []struct {
		name        string
		enabledOnly bool
		setupMock   func(*MockRedundancyRepository)
		wantCount   int
		wantErr     bool
	}{
		{
			name:        "list all policies",
			enabledOnly: false,
			setupMock: func(repo *MockRedundancyRepository) {
				policies := []*domain.RedundancyPolicy{validPolicy(), validPolicy()}
				repo.On("ListRedundancyPolicies", mock.Anything, false).Return(policies, nil)
			},
			wantCount: 2,
			wantErr:   false,
		},
		{
			name:        "list enabled only",
			enabledOnly: true,
			setupMock: func(repo *MockRedundancyRepository) {
				policies := []*domain.RedundancyPolicy{validPolicy()}
				repo.On("ListRedundancyPolicies", mock.Anything, true).Return(policies, nil)
			},
			wantCount: 1,
			wantErr:   false,
		},
		{
			name:        "repository error",
			enabledOnly: false,
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("ListRedundancyPolicies", mock.Anything, false).Return(nil, errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			result, err := svc.ListPolicies(context.Background(), tt.enabledOnly)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, tt.wantCount)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== UpdatePolicy Tests ====================

func TestUpdatePolicy(t *testing.T) {
	tests := []struct {
		name      string
		policy    *domain.RedundancyPolicy
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name:   "success",
			policy: validPolicy(),
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("UpdateRedundancyPolicy", mock.Anything, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "validation error",
			policy: &domain.RedundancyPolicy{
				Name: "", // Invalid: empty name
			},
			setupMock: func(repo *MockRedundancyRepository) {},
			wantErr:   true,
		},
		{
			name:   "repository error",
			policy: validPolicy(),
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("UpdateRedundancyPolicy", mock.Anything, mock.Anything).Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.UpdatePolicy(context.Background(), tt.policy)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== DeletePolicy Tests ====================

func TestDeletePolicy(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name: "success",
			id:   "policy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("DeleteRedundancyPolicy", mock.Anything, "policy-1").Return(nil)
			},
			wantErr: false,
		},
		{
			name: "repository error",
			id:   "policy-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("DeleteRedundancyPolicy", mock.Anything, "policy-1").Return(errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			err := svc.DeletePolicy(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== GetStats Tests ====================

func TestGetStats(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*MockRedundancyRepository)
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(repo *MockRedundancyRepository) {
				stats := map[string]interface{}{
					"total_redundancies": 10,
					"total_synced":       7,
					"total_pending":      3,
				}
				repo.On("GetRedundancyStats", mock.Anything).Return(stats, nil)
			},
			wantErr: false,
		},
		{
			name: "repository error",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetRedundancyStats", mock.Anything).Return(nil, errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			result, err := svc.GetStats(context.Background())

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== GetVideoHealth Tests ====================

func TestGetVideoHealth(t *testing.T) {
	tests := []struct {
		name      string
		videoID   string
		setupMock func(*MockRedundancyRepository)
		wantScore float64
		wantErr   bool
	}{
		{
			name:    "success",
			videoID: "video-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetVideoRedundancyHealth", mock.Anything, "video-1").Return(0.95, nil)
			},
			wantScore: 0.95,
			wantErr:   false,
		},
		{
			name:    "repository error",
			videoID: "video-1",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("GetVideoRedundancyHealth", mock.Anything, "video-1").Return(0.0, errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			score, err := svc.GetVideoHealth(context.Background(), tt.videoID)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantScore, score)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== CheckInstanceHealth Tests ====================

func TestCheckInstanceHealth(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*MockRedundancyRepository)
		wantCount int
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CheckInstanceHealth", mock.Anything).Return(5, nil)
			},
			wantCount: 5,
			wantErr:   false,
		},
		{
			name: "repository error",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CheckInstanceHealth", mock.Anything).Return(0, errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			count, err := svc.CheckInstanceHealth(context.Background())

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}

// ==================== EvaluatePolicies N+1 Fix Tests ====================

func TestEvaluatePolicies_BatchFetchesRedundancies(t *testing.T) {
	ctx := context.Background()

	video1 := validVideo()
	video1.ID = "video-1"
	video2 := &domain.Video{ID: "video-2", FileSize: 1024 * 1024 * 50}
	peer := validInstancePeer()
	peer.MaxRedundancySizeGB = 1000
	policy := validPolicy()
	policy.TargetInstanceCount = 1

	// video-1 already has synced redundancy on peer-1 → needs none
	// video-2 has no redundancy → service should create one
	existing := &domain.VideoRedundancy{
		ID:               "redundancy-1",
		VideoID:          "video-1",
		TargetInstanceID: "peer-1",
		Status:           domain.RedundancyStatusSynced,
	}

	redundancyRepo := new(MockRedundancyRepository)
	videoRepo := new(MockVideoRepository)
	httpDoer := new(MockHTTPDoer)

	redundancyRepo.On("ListPoliciesToEvaluate", mock.Anything).Return([]*domain.RedundancyPolicy{policy}, nil)
	videoRepo.On("GetVideosForRedundancy", mock.Anything, policy.Strategy, 100).Return([]*domain.Video{video1, video2}, nil)
	redundancyRepo.On("GetActiveInstancesWithCapacity", mock.Anything, int64(0)).Return([]*domain.InstancePeer{peer}, nil)
	// Single batch call for both video IDs — verifies N+1 is eliminated
	redundancyRepo.On("GetVideoRedundanciesByVideoIDs", mock.Anything, []string{"video-1", "video-2"}).
		Return([]*domain.VideoRedundancy{existing}, nil)
	redundancyRepo.On("CreateVideoRedundancy", mock.Anything, mock.AnythingOfType("*domain.VideoRedundancy")).Return(nil)
	redundancyRepo.On("UpdatePolicyEvaluationTime", mock.Anything, policy.ID).Return(nil)

	svc := newTestService(redundancyRepo, videoRepo, httpDoer)
	count, err := svc.EvaluatePolicies(ctx)

	assert.NoError(t, err)
	assert.Equal(t, 1, count) // video-2 needed a new redundancy
	redundancyRepo.AssertExpectations(t)
	// Crucially: GetVideoRedundanciesByVideoID (singular) must NOT have been called
	redundancyRepo.AssertNotCalled(t, "GetVideoRedundanciesByVideoID", mock.Anything, mock.Anything)
}

// ==================== CleanupOldLogs Tests ====================

func TestCleanupOldLogs(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*MockRedundancyRepository)
		wantCount int
		wantErr   bool
	}{
		{
			name: "success",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CleanupOldSyncLogs", mock.Anything).Return(42, nil)
			},
			wantCount: 42,
			wantErr:   false,
		},
		{
			name: "repository error",
			setupMock: func(repo *MockRedundancyRepository) {
				repo.On("CleanupOldSyncLogs", mock.Anything).Return(0, errors.New("db error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redundancyRepo := new(MockRedundancyRepository)
			videoRepo := new(MockVideoRepository)
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(redundancyRepo)

			svc := newTestService(redundancyRepo, videoRepo, httpDoer)
			count, err := svc.CleanupOldLogs(context.Background())

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}
			redundancyRepo.AssertExpectations(t)
		})
	}
}
