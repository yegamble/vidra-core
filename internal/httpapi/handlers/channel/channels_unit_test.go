package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	ucchannel "vidra-core/internal/usecase/channel"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unitChannelRepoStub struct {
	createFn                   func(ctx context.Context, channel *domain.Channel) error
	getByIDFn                  func(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
	getByHandleFn              func(ctx context.Context, handle string) (*domain.Channel, error)
	listFn                     func(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error)
	updateFn                   func(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error)
	deleteFn                   func(ctx context.Context, id uuid.UUID) error
	getChannelsByAccountIDFn   func(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error)
	getDefaultChannelFn        func(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error)
	checkOwnershipFn           func(ctx context.Context, channelID, userID uuid.UUID) (bool, error)
	capturedListParams         *domain.ChannelListParams
	capturedGetChannelsAccount *uuid.UUID
}

func (s *unitChannelRepoStub) Create(ctx context.Context, channel *domain.Channel) error {
	if s.createFn != nil {
		return s.createFn(ctx, channel)
	}
	return nil
}

func (s *unitChannelRepoStub) GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, id)
	}
	return nil, domain.ErrNotFound
}

func (s *unitChannelRepoStub) GetByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	if s.getByHandleFn != nil {
		return s.getByHandleFn(ctx, handle)
	}
	return nil, domain.ErrNotFound
}

func (s *unitChannelRepoStub) List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	s.capturedListParams = &params
	if s.listFn != nil {
		return s.listFn(ctx, params)
	}
	return &domain.ChannelListResponse{Total: 0, Page: 1, PageSize: 20, Data: []domain.Channel{}}, nil
}

func (s *unitChannelRepoStub) Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, id, updates)
	}
	return &domain.Channel{ID: id}, nil
}

func (s *unitChannelRepoStub) Delete(ctx context.Context, id uuid.UUID) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id)
	}
	return nil
}

func (s *unitChannelRepoStub) GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error) {
	s.capturedGetChannelsAccount = &accountID
	if s.getChannelsByAccountIDFn != nil {
		return s.getChannelsByAccountIDFn(ctx, accountID)
	}
	return []domain.Channel{}, nil
}

func (s *unitChannelRepoStub) GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error) {
	if s.getDefaultChannelFn != nil {
		return s.getDefaultChannelFn(ctx, accountID)
	}
	return nil, domain.ErrNotFound
}

func (s *unitChannelRepoStub) CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	if s.checkOwnershipFn != nil {
		return s.checkOwnershipFn(ctx, channelID, userID)
	}
	return false, nil
}

type unitUserRepoStub struct {
	getByIDFn func(ctx context.Context, id string) (*domain.User, error)
}

func (s *unitUserRepoStub) Create(context.Context, *domain.User, string) error { return nil }
func (s *unitUserRepoStub) GetByID(ctx context.Context, id string) (*domain.User, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, id)
	}
	return &domain.User{ID: id, Username: "user", CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}
func (s *unitUserRepoStub) GetByEmail(context.Context, string) (*domain.User, error) { return nil, nil }
func (s *unitUserRepoStub) GetByUsername(context.Context, string) (*domain.User, error) {
	return nil, nil
}
func (s *unitUserRepoStub) Update(context.Context, *domain.User) error              { return nil }
func (s *unitUserRepoStub) Delete(context.Context, string) error                    { return nil }
func (s *unitUserRepoStub) GetPasswordHash(context.Context, string) (string, error) { return "", nil }
func (s *unitUserRepoStub) UpdatePassword(context.Context, string, string) error    { return nil }
func (s *unitUserRepoStub) List(context.Context, int, int) ([]*domain.User, error)  { return nil, nil }
func (s *unitUserRepoStub) Count(context.Context) (int64, error)                    { return 0, nil }
func (s *unitUserRepoStub) SetAvatarFields(context.Context, string, sql.NullString, sql.NullString) error {
	return nil
}
func (s *unitUserRepoStub) MarkEmailAsVerified(context.Context, string) error { return nil }
func (s *unitUserRepoStub) Anonymize(_ context.Context, _ string) error       { return nil }

type unitSubscriptionRepoStub struct {
	subscribeToChannelFn      func(ctx context.Context, subscriberID, channelID uuid.UUID) error
	unsubscribeFromChannelFn  func(ctx context.Context, subscriberID, channelID uuid.UUID) error
	listChannelSubscribersFn  func(ctx context.Context, channelID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error)
	listUserSubscriptionsFn   func(ctx context.Context, subscriberID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error)
	getSubscriptionVideosFn   func(ctx context.Context, subscriberID uuid.UUID, limit, offset int) ([]domain.Video, int, error)
	subscribeLegacyFn         func(ctx context.Context, subscriberID, channelID string) error
	unsubscribeLegacyFn       func(ctx context.Context, subscriberID, channelID string) error
	capturedListLimit         int
	capturedListOffset        int
	capturedSubscribersFilter *uuid.UUID
}

func (s *unitSubscriptionRepoStub) SubscribeToChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	if s.subscribeToChannelFn != nil {
		return s.subscribeToChannelFn(ctx, subscriberID, channelID)
	}
	return nil
}
func (s *unitSubscriptionRepoStub) UnsubscribeFromChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	if s.unsubscribeFromChannelFn != nil {
		return s.unsubscribeFromChannelFn(ctx, subscriberID, channelID)
	}
	return nil
}
func (s *unitSubscriptionRepoStub) IsSubscribed(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (s *unitSubscriptionRepoStub) ListUserSubscriptions(ctx context.Context, subscriberID uuid.UUID, limit, offset int, _ ...string) (*domain.SubscriptionResponse, error) {
	if s.listUserSubscriptionsFn != nil {
		return s.listUserSubscriptionsFn(ctx, subscriberID, limit, offset)
	}
	return &domain.SubscriptionResponse{Total: 0, Data: []domain.Subscription{}}, nil
}
func (s *unitSubscriptionRepoStub) ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error) {
	s.capturedSubscribersFilter = &channelID
	s.capturedListLimit = limit
	s.capturedListOffset = offset
	if s.listChannelSubscribersFn != nil {
		return s.listChannelSubscribersFn(ctx, channelID, limit, offset)
	}
	return &domain.SubscriptionResponse{Total: 0, Data: []domain.Subscription{}}, nil
}
func (s *unitSubscriptionRepoStub) GetSubscriptionVideos(ctx context.Context, subscriberID uuid.UUID, limit, offset int) ([]domain.Video, int, error) {
	if s.getSubscriptionVideosFn != nil {
		return s.getSubscriptionVideosFn(ctx, subscriberID, limit, offset)
	}
	return []domain.Video{}, 0, nil
}
func (s *unitSubscriptionRepoStub) Subscribe(ctx context.Context, subscriberID, channelID string) error {
	if s.subscribeLegacyFn != nil {
		return s.subscribeLegacyFn(ctx, subscriberID, channelID)
	}
	return nil
}
func (s *unitSubscriptionRepoStub) Unsubscribe(ctx context.Context, subscriberID, channelID string) error {
	if s.unsubscribeLegacyFn != nil {
		return s.unsubscribeLegacyFn(ctx, subscriberID, channelID)
	}
	return nil
}
func (s *unitSubscriptionRepoStub) ListSubscriptions(_ context.Context, _ string, _, _ int, _ ...string) ([]*domain.User, int64, error) {
	return nil, 0, nil
}
func (s *unitSubscriptionRepoStub) ListSubscriptionVideos(context.Context, string, int, int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (s *unitSubscriptionRepoStub) CountSubscribers(context.Context, string) (int64, error) {
	return 0, nil
}
func (s *unitSubscriptionRepoStub) GetSubscribers(context.Context, string) ([]*domain.Subscription, error) {
	return nil, nil
}

func decodeChannelResponse(t *testing.T, rr *httptest.ResponseRecorder) Response {
	t.Helper()
	var response Response
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	return response
}

func withChannelParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func newUnitChannelHandlers(channelRepo *unitChannelRepoStub, userRepo *unitUserRepoStub, subRepo *unitSubscriptionRepoStub) *ChannelHandlers {
	if channelRepo == nil {
		channelRepo = &unitChannelRepoStub{}
	}
	if userRepo == nil {
		userRepo = &unitUserRepoStub{}
	}
	if subRepo == nil {
		subRepo = &unitSubscriptionRepoStub{}
	}
	return NewChannelHandlers(ucchannel.NewService(channelRepo, userRepo, nil), subRepo)
}

func TestChannelHandlers_ListAndGet_Unit(t *testing.T) {
	channelID := uuid.New()
	accountID := uuid.New()
	channelRepo := &unitChannelRepoStub{
		listFn: func(context.Context, domain.ChannelListParams) (*domain.ChannelListResponse, error) {
			return &domain.ChannelListResponse{
				Total:    1,
				Page:     2,
				PageSize: 10,
				Data:     []domain.Channel{{ID: channelID, Handle: "alice"}},
			}, nil
		},
		getByIDFn: func(context.Context, uuid.UUID) (*domain.Channel, error) {
			return &domain.Channel{ID: channelID, Handle: "alice"}, nil
		},
		getByHandleFn: func(context.Context, string) (*domain.Channel, error) {
			return &domain.Channel{ID: channelID, Handle: "alice"}, nil
		},
	}
	handlers := newUnitChannelHandlers(channelRepo, nil, nil)

	t.Run("list success parses filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels?page=2&pageSize=10&search=alice&sort=-createdAt&accountId="+accountID.String()+"&isLocal=true", nil)
		rr := httptest.NewRecorder()
		handlers.ListChannels(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, channelRepo.capturedListParams)
		assert.Equal(t, "alice", channelRepo.capturedListParams.Search)
		assert.Equal(t, "-createdAt", channelRepo.capturedListParams.Sort)
		assert.Equal(t, 2, channelRepo.capturedListParams.Page)
		assert.Equal(t, 10, channelRepo.capturedListParams.PageSize)
		require.NotNil(t, channelRepo.capturedListParams.AccountID)
		assert.Equal(t, accountID, *channelRepo.capturedListParams.AccountID)
		require.NotNil(t, channelRepo.capturedListParams.IsLocal)
		assert.True(t, *channelRepo.capturedListParams.IsLocal)
	})

	t.Run("list failure", func(t *testing.T) {
		failingRepo := &unitChannelRepoStub{
			listFn: func(context.Context, domain.ChannelListParams) (*domain.ChannelListResponse, error) {
				return nil, errors.New("db failure")
			},
		}
		handlers := newUnitChannelHandlers(failingRepo, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
		rr := httptest.NewRecorder()
		handlers.ListChannels(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("get by uuid success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/"+channelID.String(), nil)
		req = withChannelParam(req, "id", channelID.String())
		rr := httptest.NewRecorder()
		handlers.GetChannel(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("get by handle success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/alice", nil)
		req = withChannelParam(req, "id", "alice")
		rr := httptest.NewRecorder()
		handlers.GetChannel(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("get not found", func(t *testing.T) {
		notFoundRepo := &unitChannelRepoStub{
			getByHandleFn: func(context.Context, string) (*domain.Channel, error) {
				return nil, domain.ErrNotFound
			},
		}
		handlers := newUnitChannelHandlers(notFoundRepo, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/missing", nil)
		req = withChannelParam(req, "id", "missing")
		rr := httptest.NewRecorder()
		handlers.GetChannel(rr, req)
		require.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestChannelHandlers_CreateUpdateDelete_Unit(t *testing.T) {
	userID := uuid.New()
	channelID := uuid.New()

	userRepo := &unitUserRepoStub{
		getByIDFn: func(_ context.Context, id string) (*domain.User, error) {
			return &domain.User{ID: id, Username: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
		},
	}

	t.Run("create unauthorized", func(t *testing.T) {
		handlers := newUnitChannelHandlers(&unitChannelRepoStub{}, userRepo, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(`{"handle":"owner","displayName":"Owner"}`))
		rr := httptest.NewRecorder()
		handlers.CreateChannel(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("create invalid json", func(t *testing.T) {
		handlers := newUnitChannelHandlers(&unitChannelRepoStub{}, userRepo, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader("{bad"))
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.CreateChannel(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("create invalid input", func(t *testing.T) {
		handlers := newUnitChannelHandlers(&unitChannelRepoStub{}, userRepo, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(`{"handle":"","displayName":""}`))
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.CreateChannel(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("create success", func(t *testing.T) {
		channelRepo := &unitChannelRepoStub{
			createFn: func(_ context.Context, channel *domain.Channel) error {
				channel.ID = channelID
				return nil
			},
		}
		handlers := newUnitChannelHandlers(channelRepo, userRepo, nil)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(`{"handle":"owner_channel","displayName":"Owner Channel"}`))
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.CreateChannel(rr, req)
		require.Equal(t, http.StatusCreated, rr.Code)
	})

	t.Run("update forbidden", func(t *testing.T) {
		channelRepo := &unitChannelRepoStub{
			checkOwnershipFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
				return false, nil
			},
		}
		handlers := newUnitChannelHandlers(channelRepo, userRepo, nil)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/"+channelID.String(), strings.NewReader(`{"displayName":"Updated"}`))
		req = withChannelParam(req, "id", channelID.String())
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.UpdateChannel(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("update success", func(t *testing.T) {
		channelRepo := &unitChannelRepoStub{
			checkOwnershipFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
				return true, nil
			},
			updateFn: func(_ context.Context, id uuid.UUID, req domain.ChannelUpdateRequest) (*domain.Channel, error) {
				return &domain.Channel{ID: id, DisplayName: *req.DisplayName}, nil
			},
		}
		handlers := newUnitChannelHandlers(channelRepo, userRepo, nil)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/"+channelID.String(), strings.NewReader(`{"displayName":"Updated"}`))
		req = withChannelParam(req, "id", channelID.String())
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.UpdateChannel(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("delete success", func(t *testing.T) {
		channelRepo := &unitChannelRepoStub{
			checkOwnershipFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
				return true, nil
			},
			getChannelsByAccountIDFn: func(context.Context, uuid.UUID) ([]domain.Channel, error) {
				return []domain.Channel{{ID: uuid.New()}, {ID: channelID}}, nil
			},
			deleteFn: func(context.Context, uuid.UUID) error { return nil },
		}
		handlers := newUnitChannelHandlers(channelRepo, userRepo, nil)
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/channels/"+channelID.String(), nil)
		req = withChannelParam(req, "id", channelID.String())
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.DeleteChannel(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code)
	})
}

func TestChannelHandlers_ChannelVideosAndMyChannels_Unit(t *testing.T) {
	userID := uuid.New()
	channelID := uuid.New()
	channelRepo := &unitChannelRepoStub{
		getChannelsByAccountIDFn: func(context.Context, uuid.UUID) ([]domain.Channel, error) {
			return []domain.Channel{{ID: channelID, Handle: "owner"}}, nil
		},
		getByHandleFn: func(context.Context, string) (*domain.Channel, error) {
			return &domain.Channel{ID: channelID, Handle: "owner"}, nil
		},
	}
	handlers := newUnitChannelHandlers(channelRepo, nil, nil)

	t.Run("get my channels unauthorized", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/channels", nil)
		rr := httptest.NewRecorder()
		handlers.GetMyChannels(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("get my channels success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/channels", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.GetMyChannels(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeChannelResponse(t, rr)
		require.True(t, response.Success)
	})

	t.Run("get channel videos by handle", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/owner/videos?page=2&pageSize=30", nil)
		req = withChannelParam(req, "id", "owner")
		rr := httptest.NewRecorder()
		handlers.GetChannelVideos(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeChannelResponse(t, rr)
		require.True(t, response.Success)
	})
}

func TestChannelHandlers_Subscriptions_Unit(t *testing.T) {
	userID := uuid.New()
	channelID := uuid.New()
	subRepo := &unitSubscriptionRepoStub{
		listChannelSubscribersFn: func(context.Context, uuid.UUID, int, int) (*domain.SubscriptionResponse, error) {
			return &domain.SubscriptionResponse{
				Total: 1,
				Data:  []domain.Subscription{{SubscriberID: userID, ChannelID: channelID}},
			}, nil
		},
	}
	handlers := newUnitChannelHandlers(&unitChannelRepoStub{}, nil, subRepo)

	t.Run("subscribe invalid channel id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/bad/subscribe", nil)
		req = withChannelParam(req, "id", "bad")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.SubscribeToChannel(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("subscribe own channel error", func(t *testing.T) {
		subRepo.subscribeToChannelFn = func(context.Context, uuid.UUID, uuid.UUID) error {
			return errors.New("cannot subscribe to your own channel")
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+channelID.String()+"/subscribe", nil)
		req = withChannelParam(req, "id", channelID.String())
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.SubscribeToChannel(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		subRepo.subscribeToChannelFn = nil
	})

	t.Run("unsubscribe not found treated as success", func(t *testing.T) {
		subRepo.unsubscribeFromChannelFn = func(context.Context, uuid.UUID, uuid.UUID) error {
			return domain.ErrNotFound
		}
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/channels/"+channelID.String()+"/subscribe", nil)
		req = withChannelParam(req, "id", channelID.String())
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))
		rr := httptest.NewRecorder()
		handlers.UnsubscribeFromChannel(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		subRepo.unsubscribeFromChannelFn = nil
	})

	t.Run("get channel subscribers success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/"+channelID.String()+"/subscribers?page=2&pageSize=10", nil)
		req = withChannelParam(req, "id", channelID.String())
		rr := httptest.NewRecorder()
		handlers.GetChannelSubscribers(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, subRepo.capturedSubscribersFilter)
		assert.Equal(t, channelID, *subRepo.capturedSubscribersFilter)
		assert.Equal(t, 10, subRepo.capturedListLimit)
		assert.Equal(t, 10, subRepo.capturedListOffset)
	})
}

func TestLegacySubscriptionHandlers_Unit(t *testing.T) {
	me := uuid.New().String()
	target := uuid.New().String()

	t.Run("subscribe unauthorized and invalid id", func(t *testing.T) {
		subRepo := &unitSubscriptionRepoStub{}
		userRepo := &unitUserRepoStub{}
		handler := SubscribeToUserHandler(subRepo, userRepo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		rr := httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/users/bad/subscribe", nil)
		req = withChannelParam(req, "id", "bad-uuid")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr = httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("subscribe target not found and internal verification error", func(t *testing.T) {
		subRepo := &unitSubscriptionRepoStub{}
		userRepo := &unitUserRepoStub{
			getByIDFn: func(context.Context, string) (*domain.User, error) {
				return nil, domain.ErrUserNotFound
			},
		}
		handler := SubscribeToUserHandler(subRepo, userRepo)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr := httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusNotFound, rr.Code)

		userRepo.getByIDFn = func(context.Context, string) (*domain.User, error) {
			return nil, errors.New("db error")
		}
		req = httptest.NewRequest(http.MethodPost, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr = httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("subscribe repository failure and success", func(t *testing.T) {
		capturedSubscriber := ""
		capturedTarget := ""
		subRepo := &unitSubscriptionRepoStub{
			subscribeLegacyFn: func(_ context.Context, subscriberID, channelID string) error {
				capturedSubscriber = subscriberID
				capturedTarget = channelID
				return errors.New("insert error")
			},
		}
		userRepo := &unitUserRepoStub{}
		handler := SubscribeToUserHandler(subRepo, userRepo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr := httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, me, capturedSubscriber)
		assert.Equal(t, target, capturedTarget)

		subRepo.subscribeLegacyFn = func(_ context.Context, subscriberID, channelID string) error {
			capturedSubscriber = subscriberID
			capturedTarget = channelID
			return nil
		}
		req = httptest.NewRequest(http.MethodPost, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr = httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code)
		assert.Equal(t, me, capturedSubscriber)
		assert.Equal(t, target, capturedTarget)
	})

	t.Run("unsubscribe unauthorized invalid id error success", func(t *testing.T) {
		capturedSubscriber := ""
		capturedTarget := ""
		subRepo := &unitSubscriptionRepoStub{
			unsubscribeLegacyFn: func(_ context.Context, subscriberID, channelID string) error {
				capturedSubscriber = subscriberID
				capturedTarget = channelID
				return errors.New("delete error")
			},
		}
		handler := UnsubscribeFromUserHandler(subRepo)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		rr := httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)

		req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/bad/subscribe", nil)
		req = withChannelParam(req, "id", "not-uuid")
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr = httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)

		req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr = httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
		assert.Equal(t, me, capturedSubscriber)
		assert.Equal(t, target, capturedTarget)

		subRepo.unsubscribeLegacyFn = func(_ context.Context, subscriberID, channelID string) error {
			capturedSubscriber = subscriberID
			capturedTarget = channelID
			return nil
		}
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+target+"/subscribe", nil)
		req = withChannelParam(req, "id", target)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, me))
		rr = httptest.NewRecorder()
		handler(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code)
		assert.Equal(t, me, capturedSubscriber)
		assert.Equal(t, target, capturedTarget)
	})
}
