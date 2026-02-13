package redundancy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// ==================== Helper Functions ====================

func jsonBody(v interface{}) io.ReadCloser {
	b, _ := json.Marshal(v)
	return io.NopCloser(strings.NewReader(string(b)))
}

func plainBody(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

// ==================== DiscoverInstance Tests ====================

// Note: DiscoverInstance calls domain.ValidateURLWithSSRFCheck which performs
// real DNS resolution, so tests with fake hostnames will fail. We test the
// invalid URL path (which fails before DNS) and test the sub-functions
// (fetchNodeInfo, fetchActor) indirectly via other public methods.

func TestDiscoverInstance_InvalidURL(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	_, err := discovery.DiscoverInstance(context.Background(), "://invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestDiscoverInstance_EmptyURL(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	_, err := discovery.DiscoverInstance(context.Background(), "")
	assert.Error(t, err)
}

func TestDiscoverInstance_PrivateIP(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	// SSRF check should block private IPs
	_, err := discovery.DiscoverInstance(context.Background(), "https://127.0.0.1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private")
}

// ==================== CheckInstanceHealth Tests ====================

func TestInstanceDiscovery_CheckInstanceHealth(t *testing.T) {
	tests := []struct {
		name        string
		instanceURL string
		setupMock   func(*MockHTTPDoer)
		wantHealthy bool
		wantErr     bool
	}{
		{
			name:        "healthy on first endpoint",
			instanceURL: "https://peer.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/health" && req.Method == "HEAD"
				})).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       plainBody(""),
				}, nil).Once()
			},
			wantHealthy: true,
			wantErr:     false,
		},
		{
			name:        "healthy on second endpoint after first fails",
			instanceURL: "https://peer.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				// First endpoint fails
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/health"
				})).Return(&http.Response{
					StatusCode: http.StatusNotFound,
					Body:       plainBody(""),
				}, nil).Once()
				// Second endpoint succeeds
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/api/v1/health"
				})).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       plainBody(""),
				}, nil).Once()
			},
			wantHealthy: true,
			wantErr:     false,
		},
		{
			name:        "healthy on third endpoint after first two fail",
			instanceURL: "https://peer.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				// First two endpoints fail
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/health"
				})).Return(&http.Response{
					StatusCode: http.StatusNotFound,
					Body:       plainBody(""),
				}, nil).Once()
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/api/v1/health"
				})).Return(&http.Response{
					StatusCode: http.StatusNotFound,
					Body:       plainBody(""),
				}, nil).Once()
				// Third endpoint succeeds
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/.well-known/nodeinfo"
				})).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       plainBody(""),
				}, nil).Once()
			},
			wantHealthy: true,
			wantErr:     false,
		},
		{
			name:        "unhealthy when all endpoints fail",
			instanceURL: "https://down.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://down.example.com/health"
				})).Return(nil, errors.New("connection refused")).Once()
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://down.example.com/api/v1/health"
				})).Return(nil, errors.New("connection refused")).Once()
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://down.example.com/.well-known/nodeinfo"
				})).Return(nil, errors.New("connection refused")).Once()
			},
			wantHealthy: false,
			wantErr:     true,
		},
		{
			name:        "unhealthy when all return server error",
			instanceURL: "https://error.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://error.example.com/health"
				})).Return(&http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       plainBody(""),
				}, nil).Once()
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://error.example.com/api/v1/health"
				})).Return(&http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       plainBody(""),
				}, nil).Once()
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://error.example.com/.well-known/nodeinfo"
				})).Return(&http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       plainBody(""),
				}, nil).Once()
			},
			wantHealthy: false,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(httpDoer)

			discovery := NewInstanceDiscovery(httpDoer)
			healthy, err := discovery.CheckInstanceHealth(context.Background(), tt.instanceURL)

			if tt.wantErr {
				assert.Error(t, err)
				assert.False(t, healthy)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantHealthy, healthy)
			}
			httpDoer.AssertExpectations(t)
		})
	}
}

// ==================== NegotiateRedundancy Tests ====================

func TestNegotiateRedundancy(t *testing.T) {
	tests := []struct {
		name      string
		peer      *domain.InstancePeer
		videoID   string
		videoSize int64
		setupMock func(*MockHTTPDoer)
		wantOK    bool
		wantErr   bool
		errTarget error
	}{
		{
			name: "accepted via 202",
			peer: &domain.InstancePeer{
				ID:                   "peer-1",
				InstanceURL:          "https://peer.example.com",
				InboxURL:             "https://peer.example.com/inbox",
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    0,
				IsActive:             true,
			},
			videoID:   "video-1",
			videoSize: 1024 * 1024, // 1 MB
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/inbox" && req.Method == "POST"
				})).Return(&http.Response{
					StatusCode: http.StatusAccepted,
					Body:       plainBody(`{"status":"accepted"}`),
				}, nil).Once()
			},
			wantOK:  true,
			wantErr: false,
		},
		{
			name: "accepted via 200",
			peer: &domain.InstancePeer{
				ID:                   "peer-1",
				InstanceURL:          "https://peer.example.com",
				InboxURL:             "",
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    0,
				IsActive:             true,
			},
			videoID:   "video-1",
			videoSize: 1024 * 1024,
			setupMock: func(httpDoer *MockHTTPDoer) {
				// No InboxURL, falls back to /api/v1/redundancy/request
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/api/v1/redundancy/request" && req.Method == "POST"
				})).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       plainBody(`{"status":"ok"}`),
				}, nil).Once()
			},
			wantOK:  true,
			wantErr: false,
		},
		{
			name: "rejected via 403",
			peer: &domain.InstancePeer{
				ID:                   "peer-1",
				InstanceURL:          "https://peer.example.com",
				InboxURL:             "https://peer.example.com/inbox",
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    0,
				IsActive:             true,
			},
			videoID:   "video-1",
			videoSize: 1024 * 1024,
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/inbox"
				})).Return(&http.Response{
					StatusCode: http.StatusForbidden,
					Body:       plainBody(`{"error":"forbidden"}`),
				}, nil).Once()
			},
			wantOK:    false,
			wantErr:   true,
			errTarget: domain.ErrInstanceRefusedRedundancy,
		},
		{
			name: "insufficient storage via 507",
			peer: &domain.InstancePeer{
				ID:                   "peer-1",
				InstanceURL:          "https://peer.example.com",
				InboxURL:             "https://peer.example.com/inbox",
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    0,
				IsActive:             true,
			},
			videoID:   "video-1",
			videoSize: 1024 * 1024,
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/inbox"
				})).Return(&http.Response{
					StatusCode: http.StatusInsufficientStorage,
					Body:       plainBody(`{"error":"no space"}`),
				}, nil).Once()
			},
			wantOK:    false,
			wantErr:   true,
			errTarget: domain.ErrInsufficientStorage,
		},
		{
			name: "local capacity check fails before HTTP",
			peer: &domain.InstancePeer{
				ID:                   "peer-1",
				InstanceURL:          "https://peer.example.com",
				InboxURL:             "https://peer.example.com/inbox",
				AcceptsNewRedundancy: false, // No capacity
				MaxRedundancySizeGB:  1,
				TotalStorageBytes:    0,
				IsActive:             true,
			},
			videoID:   "video-1",
			videoSize: 1024 * 1024,
			setupMock: func(httpDoer *MockHTTPDoer) {
				// No HTTP calls expected - fails at capacity check
			},
			wantOK:    false,
			wantErr:   true,
			errTarget: domain.ErrInsufficientStorage,
		},
		{
			name: "HTTP error propagated",
			peer: &domain.InstancePeer{
				ID:                   "peer-1",
				InstanceURL:          "https://peer.example.com",
				InboxURL:             "https://peer.example.com/inbox",
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    0,
				IsActive:             true,
			},
			videoID:   "video-1",
			videoSize: 1024 * 1024,
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.Anything).Return(nil, errors.New("connection refused")).Once()
			},
			wantOK:  false,
			wantErr: true,
		},
		{
			name: "unexpected status code",
			peer: &domain.InstancePeer{
				ID:                   "peer-1",
				InstanceURL:          "https://peer.example.com",
				InboxURL:             "https://peer.example.com/inbox",
				AcceptsNewRedundancy: true,
				MaxRedundancySizeGB:  100,
				TotalStorageBytes:    0,
				IsActive:             true,
			},
			videoID:   "video-1",
			videoSize: 1024 * 1024,
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.Anything).Return(&http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       plainBody(`{"error":"bad request"}`),
				}, nil).Once()
			},
			wantOK:  false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(httpDoer)

			discovery := NewInstanceDiscovery(httpDoer)
			ok, err := discovery.NegotiateRedundancy(context.Background(), tt.peer, tt.videoID, tt.videoSize)

			if tt.wantErr {
				assert.Error(t, err)
				assert.False(t, ok)
				if tt.errTarget != nil {
					assert.ErrorIs(t, err, tt.errTarget)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantOK, ok)
			}
			httpDoer.AssertExpectations(t)
		})
	}
}

// ==================== FetchRedundancyCapabilities Tests ====================

func TestFetchRedundancyCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		instanceURL string
		setupMock   func(*MockHTTPDoer)
		wantKeys    []string
		wantErr     bool
	}{
		{
			name:        "success",
			instanceURL: "https://peer.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				capabilities := map[string]interface{}{
					"maxStorageGB":   float64(500),
					"acceptsVideo":   true,
					"protocols":      []interface{}{"activitypub"},
					"softwareName":   "peertube",
					"redundancyMode": "automatic",
				}
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/api/v1/redundancy/capabilities" &&
						req.Method == "GET"
				})).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       jsonBody(capabilities),
				}, nil).Once()
			},
			wantKeys: []string{"maxStorageGB", "acceptsVideo", "protocols", "softwareName", "redundancyMode"},
			wantErr:  false,
		},
		{
			name:        "not found",
			instanceURL: "https://peer.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/api/v1/redundancy/capabilities"
				})).Return(&http.Response{
					StatusCode: http.StatusNotFound,
					Body:       plainBody("not found"),
				}, nil).Once()
			},
			wantErr: true,
		},
		{
			name:        "HTTP error",
			instanceURL: "https://peer.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.Anything).Return(nil, errors.New("timeout")).Once()
			},
			wantErr: true,
		},
		{
			name:        "invalid JSON response",
			instanceURL: "https://peer.example.com",
			setupMock: func(httpDoer *MockHTTPDoer) {
				httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
					return req.URL.String() == "https://peer.example.com/api/v1/redundancy/capabilities"
				})).Return(&http.Response{
					StatusCode: http.StatusOK,
					Body:       plainBody("not json"),
				}, nil).Once()
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpDoer := new(MockHTTPDoer)
			tt.setupMock(httpDoer)

			discovery := NewInstanceDiscovery(httpDoer)
			capabilities, err := discovery.FetchRedundancyCapabilities(context.Background(), tt.instanceURL)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, capabilities)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, capabilities)
				for _, key := range tt.wantKeys {
					assert.Contains(t, capabilities, key)
				}
			}
			httpDoer.AssertExpectations(t)
		})
	}
}

// ==================== extractInstanceName Tests ====================

func TestExtractInstanceName(t *testing.T) {
	tests := []struct {
		name     string
		nodeInfo *NodeInfo
		want     string
	}{
		{
			name: "extracts nodeName from metadata",
			nodeInfo: &NodeInfo{
				Software: struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				}{Name: "peertube", Version: "6.0"},
				Metadata: map[string]interface{}{
					"nodeName": "My PeerTube Instance",
				},
			},
			want: "My PeerTube Instance",
		},
		{
			name: "extracts name from metadata when nodeName missing",
			nodeInfo: &NodeInfo{
				Software: struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				}{Name: "peertube", Version: "6.0"},
				Metadata: map[string]interface{}{
					"name": "Another Instance",
				},
			},
			want: "Another Instance",
		},
		{
			name: "falls back to software name",
			nodeInfo: &NodeInfo{
				Software: struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				}{Name: "peertube", Version: "6.0"},
				Metadata: map[string]interface{}{},
			},
			want: "peertube",
		},
		{
			name: "falls back to software name with nil metadata values",
			nodeInfo: &NodeInfo{
				Software: struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				}{Name: "mastodon", Version: "4.0"},
				Metadata: map[string]interface{}{
					"nodeName": 12345, // Not a string
				},
			},
			want: "mastodon",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractInstanceName(tt.nodeInfo)
			assert.Equal(t, tt.want, result)
		})
	}
}
