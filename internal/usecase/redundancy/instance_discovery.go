package redundancy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vidra-core/internal/domain"
)

type InstanceDiscovery struct {
	httpClient HTTPDoer
}

func NewInstanceDiscovery(httpClient HTTPDoer) *InstanceDiscovery {
	return &InstanceDiscovery{
		httpClient: httpClient,
	}
}

type NodeInfo struct {
	Version  string `json:"version"`
	Software struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"software"`
	Protocols []string `json:"protocols"`
	Services  struct {
		Inbound  []string `json:"inbound"`
		Outbound []string `json:"outbound"`
	} `json:"services"`
	Usage struct {
		Users struct {
			Total int `json:"total"`
		} `json:"users"`
		LocalPosts int `json:"localPosts"`
	} `json:"usage"`
	Metadata map[string]interface{} `json:"metadata"`
}

type WebFingerResponse struct {
	Subject string `json:"subject"`
	Links   []struct {
		Rel  string `json:"rel"`
		Type string `json:"type"`
		Href string `json:"href"`
	} `json:"links"`
}

type ActivityPubActor struct {
	Context           interface{} `json:"@context"`
	ID                string      `json:"id"`
	Type              string      `json:"type"`
	PreferredUsername string      `json:"preferredUsername"`
	Inbox             string      `json:"inbox"`
	Outbox            string      `json:"outbox"`
	SharedInbox       string      `json:"endpoints,omitempty"`
	PublicKey         struct {
		ID           string `json:"id"`
		Owner        string `json:"owner"`
		PublicKeyPem string `json:"publicKeyPem"`
	} `json:"publicKey"`
}

func (d *InstanceDiscovery) DiscoverInstance(ctx context.Context, instanceURL string) (*domain.InstancePeer, error) {
	parsedURL, err := url.Parse(instanceURL)
	if err != nil {
		return nil, fmt.Errorf("invalid instance URL: %w", err)
	}

	if err := domain.ValidateURLWithSSRFCheck(instanceURL); err != nil {
		return nil, fmt.Errorf("invalid or unsafe instance URL: %w", err)
	}

	peer := &domain.InstancePeer{
		InstanceURL:          instanceURL,
		InstanceHost:         parsedURL.Host,
		AutoAcceptRedundancy: false,
		AcceptsNewRedundancy: true,
		IsActive:             true,
	}

	nodeInfo, err := d.fetchNodeInfo(ctx, instanceURL)
	if err == nil {
		peer.Software = nodeInfo.Software.Name
		peer.Version = nodeInfo.Software.Version
		peer.InstanceName = extractInstanceName(nodeInfo)
	}

	actor, err := d.fetchInstanceActor(ctx, instanceURL)
	if err == nil {
		peer.ActorURL = actor.ID
		peer.InboxURL = actor.Inbox
		if actor.SharedInbox != "" {
			peer.SharedInboxURL = actor.SharedInbox
		}
		peer.PublicKey = actor.PublicKey.PublicKeyPem
	}

	return peer, nil
}

func (d *InstanceDiscovery) fetchNodeInfo(ctx context.Context, instanceURL string) (*NodeInfo, error) {
	wellKnownURL := instanceURL + "/.well-known/nodeinfo"

	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	wkResp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch NodeInfo well-known: %w", err)
	}
	defer func() { _ = wkResp.Body.Close() }()

	if wkResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", wkResp.StatusCode)
	}

	var wellKnown struct {
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}

	if err := json.NewDecoder(wkResp.Body).Decode(&wellKnown); err != nil {
		return nil, fmt.Errorf("failed to decode well-known: %w", err)
	}

	var nodeInfoURL string
	for _, link := range wellKnown.Links {
		if strings.Contains(link.Rel, "nodeinfo/2.0") {
			nodeInfoURL = link.Href
			break
		}
	}

	if nodeInfoURL == "" {
		return nil, fmt.Errorf("NodeInfo 2.0 not found")
	}

	req, err = http.NewRequestWithContext(ctx, "GET", nodeInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch NodeInfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var nodeInfo NodeInfo
	if err := json.NewDecoder(resp.Body).Decode(&nodeInfo); err != nil {
		return nil, fmt.Errorf("failed to decode NodeInfo: %w", err)
	}

	return &nodeInfo, nil
}

func (d *InstanceDiscovery) fetchInstanceActor(ctx context.Context, instanceURL string) (*ActivityPubActor, error) {
	actorURLs := []string{
		instanceURL + "/actor",
		instanceURL + "/users/instance",
		instanceURL + "/instance/actor",
	}

	var lastErr error
	for _, actorURL := range actorURLs {
		actor, err := d.fetchActor(ctx, actorURL)
		if err == nil {
			return actor, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("failed to fetch instance actor: %w", lastErr)
}

func (d *InstanceDiscovery) fetchActor(ctx context.Context, actorURL string) (*ActivityPubActor, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", actorURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/activity+json, application/ld+json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var actor ActivityPubActor
	if err := json.NewDecoder(resp.Body).Decode(&actor); err != nil {
		return nil, fmt.Errorf("failed to decode actor: %w", err)
	}

	return &actor, nil
}

func (d *InstanceDiscovery) NegotiateRedundancy(ctx context.Context, peer *domain.InstancePeer, videoID string, videoSize int64) (bool, error) {
	if !peer.HasCapacity(videoSize) {
		return false, domain.ErrInsufficientStorage
	}

	requestURL := peer.InboxURL
	if requestURL == "" {
		requestURL = peer.InstanceURL + "/api/v1/redundancy/request"
	}

	request := map[string]interface{}{
		"type":      "RedundancyRequest",
		"videoId":   videoID,
		"videoSize": videoSize,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", requestURL, strings.NewReader(string(requestBody)))
	if err != nil {
		return false, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusOK:
		return true, nil
	case http.StatusForbidden, http.StatusUnauthorized:
		return false, domain.ErrInstanceRefusedRedundancy
	case http.StatusInsufficientStorage, http.StatusConflict:
		return false, domain.ErrInsufficientStorage
	default:
		return false, fmt.Errorf("unexpected response: %d - %s", resp.StatusCode, string(body))
	}
}

func (d *InstanceDiscovery) CheckInstanceHealth(ctx context.Context, instanceURL string) (bool, error) {
	healthURLs := []string{
		instanceURL + "/health",
		instanceURL + "/api/v1/health",
		instanceURL + "/.well-known/nodeinfo",
	}

	for _, healthURL := range healthURLs {
		req, err := http.NewRequestWithContext(ctx, "HEAD", healthURL, nil)
		if err != nil {
			continue
		}

		resp, err := d.httpClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return true, nil
		}
	}

	return false, fmt.Errorf("instance appears to be down")
}

func (d *InstanceDiscovery) FetchRedundancyCapabilities(ctx context.Context, instanceURL string) (map[string]interface{}, error) {
	capabilitiesURL := instanceURL + "/api/v1/redundancy/capabilities"

	req, err := http.NewRequestWithContext(ctx, "GET", capabilitiesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch capabilities: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var capabilities map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&capabilities); err != nil {
		return nil, fmt.Errorf("failed to decode capabilities: %w", err)
	}

	return capabilities, nil
}

func extractInstanceName(nodeInfo *NodeInfo) string {
	if name, ok := nodeInfo.Metadata["nodeName"].(string); ok {
		return name
	}
	if name, ok := nodeInfo.Metadata["name"].(string); ok {
		return name
	}
	return nodeInfo.Software.Name
}

func (d *InstanceDiscovery) DiscoverInstancesFromKnownPeers(ctx context.Context, knownPeers []*domain.InstancePeer) ([]*domain.InstancePeer, error) {
	var discovered []*domain.InstancePeer

	for _, peer := range knownPeers {
		peersURL := peer.InstanceURL + "/api/v1/server/followers/instances"

		req, err := http.NewRequestWithContext(ctx, "GET", peersURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")

		resp, err := d.httpClient.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			continue
		}

		var peerList []struct {
			Host string `json:"host"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&peerList); err != nil {
			_ = resp.Body.Close()
			continue
		}
		_ = resp.Body.Close()

		for _, p := range peerList {
			instanceURL := "https://" + p.Host
			newPeer, err := d.DiscoverInstance(ctx, instanceURL)
			if err != nil {
				continue
			}
			discovered = append(discovered, newPeer)
		}
	}

	return discovered, nil
}
