package redundancy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFetchNodeInfo_Success(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	wellKnown := map[string]interface{}{
		"links": []map[string]string{
			{"rel": "http://nodeinfo/2.0", "href": "https://peer.example.com/nodeinfo/2.0"},
		},
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/.well-known/nodeinfo"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(wellKnown),
	}, nil).Once()

	nodeInfo := NodeInfo{
		Version: "2.0",
		Software: struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}{Name: "peertube", Version: "6.0"},
		Protocols: []string{"activitypub"},
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/nodeinfo/2.0"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(nodeInfo),
	}, nil).Once()

	result, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "peertube", result.Software.Name)
	assert.Equal(t, "6.0", result.Software.Version)
	httpDoer.AssertExpectations(t)
}

func TestFetchNodeInfo_WellKnownHTTPError(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.Anything).Return(nil, errors.New("connection refused")).Once()

	_, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch NodeInfo well-known")
}

func TestFetchNodeInfo_WellKnownBadStatus(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusNotFound,
		Body:       plainBody("not found"),
	}, nil).Once()

	_, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")
}

func TestFetchNodeInfo_NoNodeInfo2Link(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	wellKnown := map[string]interface{}{
		"links": []map[string]string{
			{"rel": "http://nodeinfo/1.0", "href": "https://peer.example.com/nodeinfo/1.0"},
		},
	}
	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(wellKnown),
	}, nil).Once()

	_, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NodeInfo 2.0 not found")
}

func TestFetchNodeInfo_NodeInfoHTTPError(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	wellKnown := map[string]interface{}{
		"links": []map[string]string{
			{"rel": "http://nodeinfo/2.0", "href": "https://peer.example.com/nodeinfo/2.0"},
		},
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/.well-known/nodeinfo"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(wellKnown),
	}, nil).Once()

	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/nodeinfo/2.0"
	})).Return(nil, errors.New("timeout")).Once()

	_, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch NodeInfo")
}

func TestFetchNodeInfo_NodeInfoBadStatus(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	wellKnown := map[string]interface{}{
		"links": []map[string]string{
			{"rel": "http://nodeinfo/2.0", "href": "https://peer.example.com/nodeinfo/2.0"},
		},
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/.well-known/nodeinfo"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(wellKnown),
	}, nil).Once()

	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/nodeinfo/2.0"
	})).Return(&http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       plainBody("error"),
	}, nil).Once()

	_, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")
}

func TestFetchNodeInfo_InvalidNodeInfoJSON(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	wellKnown := map[string]interface{}{
		"links": []map[string]string{
			{"rel": "http://nodeinfo/2.0", "href": "https://peer.example.com/nodeinfo/2.0"},
		},
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/.well-known/nodeinfo"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(wellKnown),
	}, nil).Once()

	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/nodeinfo/2.0"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       plainBody("not json"),
	}, nil).Once()

	_, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode NodeInfo")
}

func TestFetchActor_Success(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	actor := ActivityPubActor{
		ID:                "https://peer.example.com/actor",
		Type:              "Application",
		PreferredUsername: "instance",
		Inbox:             "https://peer.example.com/inbox",
		Outbox:            "https://peer.example.com/outbox",
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/actor" &&
			req.Header.Get("Accept") == "application/activity+json, application/ld+json"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(actor),
	}, nil).Once()

	result, err := discovery.fetchActor(context.Background(), "https://peer.example.com/actor")

	require.NoError(t, err)
	assert.Equal(t, "https://peer.example.com/actor", result.ID)
	assert.Equal(t, "Application", result.Type)
	assert.Equal(t, "https://peer.example.com/inbox", result.Inbox)
	httpDoer.AssertExpectations(t)
}

func TestFetchActor_HTTPError(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.Anything).Return(nil, errors.New("refused")).Once()

	_, err := discovery.fetchActor(context.Background(), "https://peer.example.com/actor")
	require.Error(t, err)
}

func TestFetchActor_BadStatus(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusNotFound,
		Body:       plainBody("not found"),
	}, nil).Once()

	_, err := discovery.fetchActor(context.Background(), "https://peer.example.com/actor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")
}

func TestFetchActor_InvalidJSON(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       plainBody("{bad json"),
	}, nil).Once()

	_, err := discovery.fetchActor(context.Background(), "https://peer.example.com/actor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode actor")
}

func TestFetchInstanceActor_SuccessOnFirstEndpoint(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	actor := ActivityPubActor{
		ID:    "https://peer.example.com/actor",
		Type:  "Application",
		Inbox: "https://peer.example.com/inbox",
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/actor"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(actor),
	}, nil).Once()

	result, err := discovery.fetchInstanceActor(context.Background(), "https://peer.example.com")

	require.NoError(t, err)
	assert.Equal(t, "https://peer.example.com/actor", result.ID)
	httpDoer.AssertExpectations(t)
}

func TestFetchInstanceActor_SuccessOnSecondEndpoint(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/actor"
	})).Return(&http.Response{
		StatusCode: http.StatusNotFound,
		Body:       plainBody("not found"),
	}, nil).Once()

	actor := ActivityPubActor{
		ID:    "https://peer.example.com/users/instance",
		Type:  "Application",
		Inbox: "https://peer.example.com/inbox",
	}
	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://peer.example.com/users/instance"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       jsonBody(actor),
	}, nil).Once()

	result, err := discovery.fetchInstanceActor(context.Background(), "https://peer.example.com")

	require.NoError(t, err)
	assert.Equal(t, "https://peer.example.com/users/instance", result.ID)
	httpDoer.AssertExpectations(t)
}

func TestFetchInstanceActor_AllEndpointsFail(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusNotFound,
		Body:       plainBody(""),
	}, nil)

	_, err := discovery.fetchInstanceActor(context.Background(), "https://peer.example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch instance actor")
}

func TestDiscoverInstancesFromKnownPeers_EmptyPeers(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	peers, err := discovery.DiscoverInstancesFromKnownPeers(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, peers)
}

func TestDiscoverInstancesFromKnownPeers_PeerListHTTPError(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	knownPeers := []*domain.InstancePeer{
		{InstanceURL: "https://known.example.com"},
	}

	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://known.example.com/api/v1/server/followers/instances"
	})).Return(nil, errors.New("timeout")).Once()

	peers, err := discovery.DiscoverInstancesFromKnownPeers(context.Background(), knownPeers)
	require.NoError(t, err)
	assert.Empty(t, peers)
}

func TestDiscoverInstancesFromKnownPeers_PeerListBadStatus(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	knownPeers := []*domain.InstancePeer{
		{InstanceURL: "https://known.example.com"},
	}

	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://known.example.com/api/v1/server/followers/instances"
	})).Return(&http.Response{
		StatusCode: http.StatusForbidden,
		Body:       plainBody("forbidden"),
	}, nil).Once()

	peers, err := discovery.DiscoverInstancesFromKnownPeers(context.Background(), knownPeers)
	require.NoError(t, err)
	assert.Empty(t, peers)
}

func TestDiscoverInstancesFromKnownPeers_InvalidPeerListJSON(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	knownPeers := []*domain.InstancePeer{
		{InstanceURL: "https://known.example.com"},
	}

	httpDoer.On("Do", mock.MatchedBy(func(req *http.Request) bool {
		return req.URL.String() == "https://known.example.com/api/v1/server/followers/instances"
	})).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       plainBody("not json"),
	}, nil).Once()

	peers, err := discovery.DiscoverInstancesFromKnownPeers(context.Background(), knownPeers)
	require.NoError(t, err)
	assert.Empty(t, peers)
}

func TestNegotiateRedundancy_Unauthorized(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	peer := &domain.InstancePeer{
		ID:                   "peer-1",
		InstanceURL:          "https://peer.example.com",
		InboxURL:             "https://peer.example.com/inbox",
		AcceptsNewRedundancy: true,
		MaxRedundancySizeGB:  100,
		TotalStorageBytes:    0,
		IsActive:             true,
	}

	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       plainBody(`{"error":"unauthorized"}`),
	}, nil).Once()

	ok, err := discovery.NegotiateRedundancy(context.Background(), peer, "v1", 1024*1024)

	require.Error(t, err)
	require.False(t, ok)
	assert.ErrorIs(t, err, domain.ErrInstanceRefusedRedundancy)
}

func TestNegotiateRedundancy_Conflict(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	peer := &domain.InstancePeer{
		ID:                   "peer-1",
		InstanceURL:          "https://peer.example.com",
		InboxURL:             "https://peer.example.com/inbox",
		AcceptsNewRedundancy: true,
		MaxRedundancySizeGB:  100,
		TotalStorageBytes:    0,
		IsActive:             true,
	}

	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusConflict,
		Body:       plainBody(`{"error":"conflict"}`),
	}, nil).Once()

	ok, err := discovery.NegotiateRedundancy(context.Background(), peer, "v1", 1024*1024)

	require.Error(t, err)
	require.False(t, ok)
	assert.ErrorIs(t, err, domain.ErrInsufficientStorage)
}

func TestFetchNodeInfo_WellKnownInvalidJSON(t *testing.T) {
	httpDoer := new(MockHTTPDoer)
	discovery := NewInstanceDiscovery(httpDoer)

	httpDoer.On("Do", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       plainBody("not valid json"),
	}, nil).Once()

	_, err := discovery.fetchNodeInfo(context.Background(), "https://peer.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode well-known")
}

func TestJsonBodyHelper(t *testing.T) {
	data := map[string]string{"key": "value"}
	body := jsonBody(data)
	defer func() { _ = body.Close() }()

	var result map[string]string
	err := json.NewDecoder(body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}
