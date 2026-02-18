package torrent

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTracker() *Tracker {
	return &Tracker{
		config: DefaultTrackerConfig(),
		peers:  make(map[string]*PeerSwarm),
		stats:  &TrackerStats{StartTime: time.Now()},
	}
}

func TestGetOrCreateSwarm_CreatesNew(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("infohash1")
	require.NotNil(t, swarm)
	assert.Equal(t, "infohash1", swarm.InfoHash)
	assert.NotNil(t, swarm.Peers)
}

func TestGetOrCreateSwarm_ReturnsSameInstance(t *testing.T) {
	tr := newTestTracker()
	s1 := tr.getOrCreateSwarm("hash1")
	s2 := tr.getOrCreateSwarm("hash1")
	assert.Same(t, s1, s2, "same swarm should be returned for the same info hash")
}

func TestGetOrCreateSwarm_IncrementsTotalSwarms(t *testing.T) {
	tr := newTestTracker()
	tr.getOrCreateSwarm("h1")
	tr.getOrCreateSwarm("h2")
	tr.getOrCreateSwarm("h1")
	tr.statsMu.RLock()
	defer tr.statsMu.RUnlock()
	assert.Equal(t, int64(2), tr.stats.TotalSwarms)
}

func TestGetOrCreateSwarm_DifferentHashesDifferentSwarms(t *testing.T) {
	tr := newTestTracker()
	s1 := tr.getOrCreateSwarm("hash-A")
	s2 := tr.getOrCreateSwarm("hash-B")
	assert.NotSame(t, s1, s2)
}

func TestGetSwarm_ReturnsNilForMissing(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getSwarm("nonexistent")
	assert.Nil(t, swarm)
}

func TestGetSwarm_ReturnsCreatedSwarm(t *testing.T) {
	tr := newTestTracker()
	tr.getOrCreateSwarm("existing")
	swarm := tr.getSwarm("existing")
	require.NotNil(t, swarm)
	assert.Equal(t, "existing", swarm.InfoHash)
}

func TestUpdatePeer_AddsNewPeer(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("hash1")

	req := &AnnounceRequest{
		PeerID:   "peer-001",
		InfoHash: "hash1",
		Left:     100,
	}
	tr.updatePeer(swarm, req, nil, "192.168.1.1:5000", false)

	swarm.mu.RLock()
	defer swarm.mu.RUnlock()
	peer, ok := swarm.Peers["peer-001"]
	require.True(t, ok)
	assert.Equal(t, "peer-001", peer.PeerID)
	assert.Equal(t, "192.168.1.1", peer.IP)
	assert.False(t, peer.IsSeeder)
}

func TestUpdatePeer_UpdatesExistingPeer(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("hash1")
	req := &AnnounceRequest{PeerID: "peer-001", InfoHash: "hash1", Left: 100, Uploaded: 0}
	tr.updatePeer(swarm, req, nil, "10.0.0.1:9000", false)

	req2 := &AnnounceRequest{PeerID: "peer-001", InfoHash: "hash1", Left: 0, Uploaded: 500, Downloaded: 200}
	tr.updatePeer(swarm, req2, nil, "10.0.0.1:9000", true)

	swarm.mu.RLock()
	defer swarm.mu.RUnlock()
	peer := swarm.Peers["peer-001"]
	require.NotNil(t, peer)
	peer.mu.Lock()
	defer peer.mu.Unlock()
	assert.Equal(t, int64(500), peer.Uploaded)
	assert.Equal(t, int64(200), peer.Downloaded)
	assert.True(t, peer.IsSeeder, "peer should be upgraded to seeder")
}

func TestUpdatePeer_IncrementsTotalPeers(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("hash1")
	before := tr.stats.TotalPeers
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "p1", InfoHash: "hash1"}, nil, "1.2.3.4:1234", false)
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "p2", InfoHash: "hash1"}, nil, "1.2.3.5:1234", false)
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "p1", InfoHash: "hash1"}, nil, "1.2.3.4:1234", false)
	tr.statsMu.RLock()
	defer tr.statsMu.RUnlock()
	assert.Equal(t, before+2, tr.stats.TotalPeers)
}

func TestUpdatePeer_StripPortFromRemoteAddr(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("h")
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "px", InfoHash: "h"}, nil, "203.0.113.5:51413", false)
	swarm.mu.RLock()
	defer swarm.mu.RUnlock()
	assert.Equal(t, "203.0.113.5", swarm.Peers["px"].IP)
}

func TestRemovePeer_RemovesExistingPeer(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("hash1")
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "rm-peer", InfoHash: "hash1"}, nil, "1.1.1.1:1", false)

	tr.removePeer(swarm, "rm-peer")

	swarm.mu.RLock()
	defer swarm.mu.RUnlock()
	_, ok := swarm.Peers["rm-peer"]
	assert.False(t, ok, "removed peer should not be in swarm")
}

func TestRemovePeer_DecrementsTotalPeers(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("hash1")
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "target", InfoHash: "hash1"}, nil, "1.1.1.1:1", false)

	tr.statsMu.RLock()
	before := tr.stats.TotalPeers
	tr.statsMu.RUnlock()

	tr.removePeer(swarm, "target")

	tr.statsMu.RLock()
	defer tr.statsMu.RUnlock()
	assert.Equal(t, before-1, tr.stats.TotalPeers)
}

func TestRemovePeer_NoOpForMissingPeer(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("hash1")
	before := tr.stats.TotalPeers
	tr.removePeer(swarm, "ghost")
	tr.statsMu.RLock()
	defer tr.statsMu.RUnlock()
	assert.Equal(t, before, tr.stats.TotalPeers)
}

func TestGetPeerList_ExcludesRequester(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("h")
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "me"}, nil, "1.1.1.1:1", false)
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "other"}, nil, "2.2.2.2:2", false)

	peers := tr.getPeerList(swarm, "me", 10)
	for _, p := range peers {
		assert.NotEqual(t, "me", p.PeerID, "requesting peer should be excluded")
	}
}

func TestGetPeerList_RespectsNumWant(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("h")
	for i := range 10 {
		tr.updatePeer(swarm, &AnnounceRequest{PeerID: string(rune('a' + i))}, nil, "1.1.1.1:1", false)
	}
	peers := tr.getPeerList(swarm, "requester", 3)
	assert.LessOrEqual(t, len(peers), 3)
}

func TestGetPeerList_EmptySwarm(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("empty")
	peers := tr.getPeerList(swarm, "requester", 10)
	assert.Empty(t, peers)
}

func TestGetPeerList_UsesMaxPeersToReturnWhenNumWantIsZero(t *testing.T) {
	tr := newTestTracker()
	tr.config.MaxPeersToReturn = 5
	swarm := tr.getOrCreateSwarm("h")
	for i := range 20 {
		tr.updatePeer(swarm, &AnnounceRequest{PeerID: string(rune('a' + i))}, nil, "1.1.1.1:1", false)
	}
	peers := tr.getPeerList(swarm, "req", 0)
	assert.LessOrEqual(t, len(peers), 5)
}

func TestCountPeers_Empty(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("h")
	complete, incomplete := tr.countPeers(swarm)
	assert.Equal(t, 0, complete)
	assert.Equal(t, 0, incomplete)
}

func TestCountPeers_SeederVsLeecher(t *testing.T) {
	tr := newTestTracker()
	swarm := tr.getOrCreateSwarm("h")
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "seeder1"}, nil, "1.1.1.1:1", true)
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "seeder2"}, nil, "2.2.2.2:2", true)
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "leecher"}, nil, "3.3.3.3:3", false)

	complete, incomplete := tr.countPeers(swarm)
	assert.Equal(t, 2, complete, "should count 2 seeders")
	assert.Equal(t, 1, incomplete, "should count 1 leecher")
}

func TestDefaultTrackerConfig_HasValidDefaults(t *testing.T) {
	cfg := DefaultTrackerConfig()
	assert.Equal(t, 1000, cfg.MaxPeersPerSwarm)
	assert.Equal(t, 50, cfg.MaxPeersToReturn)
	assert.Greater(t, cfg.AnnounceInterval, time.Duration(0))
	assert.Greater(t, cfg.PeerExpirationTime, time.Duration(0))
	assert.Equal(t, []string{"*"}, cfg.AllowedOrigins)
}

func TestGetStats_ReflectsUpdates(t *testing.T) {
	tr := newTestTracker()
	tr.getOrCreateSwarm("h1")
	tr.getOrCreateSwarm("h2")
	swarm := tr.getOrCreateSwarm("h1")
	tr.updatePeer(swarm, &AnnounceRequest{PeerID: "p1"}, nil, "1.1.1.1:1", false)

	stats := tr.GetStats()
	assert.Equal(t, int64(2), stats.TotalSwarms)
	assert.Equal(t, int64(1), stats.TotalPeers)
}

func TestTracker_ConcurrentSwarmAccess(t *testing.T) {
	tr := newTestTracker()
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			hash := string(rune('a' + n%5))
			swarm := tr.getOrCreateSwarm(hash)
			tr.updatePeer(swarm, &AnnounceRequest{PeerID: string(rune('A' + n))}, nil, "1.1.1.1:1", n%2 == 0)
			tr.getPeerList(swarm, "", 5)
			tr.countPeers(swarm)
		}(i)
	}
	wg.Wait()
}
