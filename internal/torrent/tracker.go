package torrent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

// Tracker implements a WebTorrent-compatible WebSocket tracker
type Tracker struct {
	db          *sqlx.DB
	config      *TrackerConfig
	peers       map[string]*PeerSwarm // keyed by info hash
	mu          sync.RWMutex
	upgrader    websocket.Upgrader
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *logrus.Logger
	stats       *TrackerStats
	peerRepo    *repository.TorrentPeerRepository
	torrentRepo *repository.TorrentRepository
}

// TrackerConfig holds configuration for the tracker
type TrackerConfig struct {
	// Network
	Host string
	Port int

	// Limits
	MaxPeersPerSwarm      int
	MaxPeersToReturn      int
	AnnounceInterval      time.Duration
	PeerExpirationTime    time.Duration
	CleanupInterval       time.Duration
	MaxWebSocketConns     int
	MaxMessageSize        int64
	ReadTimeout           time.Duration
	WriteTimeout          time.Duration
	PingInterval          time.Duration
	PongTimeout           time.Duration
	MaxSwarms             int
	MaxPeersPerInfoHash   int
	EnableIPv6            bool
	RequireAuthentication bool

	// CORS
	AllowedOrigins []string
}

// DefaultTrackerConfig returns a default tracker configuration
func DefaultTrackerConfig() *TrackerConfig {
	return &TrackerConfig{
		Host:                  "0.0.0.0",
		Port:                  8000,
		MaxPeersPerSwarm:      1000,
		MaxPeersToReturn:      50,
		AnnounceInterval:      30 * time.Minute,
		PeerExpirationTime:    1 * time.Hour,
		CleanupInterval:       5 * time.Minute,
		MaxWebSocketConns:     10000,
		MaxMessageSize:        16 * 1024, // 16KB
		ReadTimeout:           60 * time.Second,
		WriteTimeout:          10 * time.Second,
		PingInterval:          30 * time.Second,
		PongTimeout:           10 * time.Second,
		MaxSwarms:             10000,
		MaxPeersPerInfoHash:   1000,
		EnableIPv6:            true,
		RequireAuthentication: false,
		AllowedOrigins:        []string{"*"},
	}
}

// PeerSwarm manages peers for a single info hash
type PeerSwarm struct {
	InfoHash    string
	Peers       map[string]*TrackerPeer // keyed by peer ID
	mu          sync.RWMutex
	LastUpdated time.Time
}

// TrackerPeer represents a peer in the tracker
type TrackerPeer struct {
	PeerID     string
	InfoHash   string
	IP         string
	Port       int
	Conn       *websocket.Conn
	OfferID    string
	LastSeen   time.Time
	Uploaded   int64
	Downloaded int64
	Left       int64
	Event      string // started, stopped, completed
	UserAgent  string
	IsSeeder   bool
	mu         sync.Mutex
}

// TrackerStats tracks tracker statistics
type TrackerStats struct {
	mu                sync.RWMutex
	TotalAnnounces    int64
	TotalScrapes      int64
	ActiveConnections int64
	TotalPeers        int64
	TotalSwarms       int64
	AnnounceErrors    int64
	ConnectionErrors  int64
	StartTime         time.Time
}

// WebTorrent protocol messages
type AnnounceRequest struct {
	Action     string `json:"action"` // "announce"
	InfoHash   string `json:"info_hash"`
	PeerID     string `json:"peer_id"`
	Downloaded int64  `json:"downloaded"`
	Left       int64  `json:"left"`
	Uploaded   int64  `json:"uploaded"`
	Event      string `json:"event"`   // started, stopped, completed
	NumWant    int    `json:"numwant"` // number of peers wanted
	Compact    int    `json:"compact"`
	NoPeerID   int    `json:"no_peer_id"`
	OfferID    string `json:"offer_id,omitempty"`
	Offer      any    `json:"offer,omitempty"` // WebRTC offer
}

type AnnounceResponse struct {
	Action     string           `json:"action"` // "announce"
	InfoHash   string           `json:"info_hash"`
	Complete   int              `json:"complete"`   // seeders
	Incomplete int              `json:"incomplete"` // leechers
	Interval   int              `json:"interval"`   // announce interval in seconds
	Peers      []WebTorrentPeer `json:"peers,omitempty"`
	Offer      any              `json:"offer,omitempty"`
	OfferID    string           `json:"offer_id,omitempty"`
	ToPeerID   string           `json:"to_peer_id,omitempty"`
}

type WebTorrentPeer struct {
	PeerID  string `json:"peer_id"`
	IP      string `json:"ip,omitempty"`
	Port    int    `json:"port,omitempty"`
	OfferID string `json:"offer_id,omitempty"`
}

type ScrapeRequest struct {
	Action   string   `json:"action"` // "scrape"
	InfoHash []string `json:"info_hash"`
}

type ScrapeResponse struct {
	Action string                    `json:"action"` // "scrape"
	Files  map[string]ScrapeFileInfo `json:"files"`
}

type ScrapeFileInfo struct {
	Complete   int `json:"complete"`   // seeders
	Incomplete int `json:"incomplete"` // leechers
	Downloaded int `json:"downloaded"` // completed downloads
}

type ErrorResponse struct {
	Action        string `json:"action"` // "error"
	FailureReason string `json:"failure_reason"`
	InfoHash      string `json:"info_hash,omitempty"`
}

// NewTracker creates a new WebTorrent tracker
func NewTracker(db *sqlx.DB, config *TrackerConfig, logger *logrus.Logger) *Tracker {
	if config == nil {
		config = DefaultTrackerConfig()
	}
	if logger == nil {
		logger = logrus.New()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Tracker{
		db:     db,
		config: config,
		peers:  make(map[string]*PeerSwarm),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// Allow configured origins
				if len(config.AllowedOrigins) == 1 && config.AllowedOrigins[0] == "*" {
					return true
				}
				origin := r.Header.Get("Origin")
				for _, allowed := range config.AllowedOrigins {
					if origin == allowed {
						return true
					}
				}
				return false
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		ctx:         ctx,
		cancel:      cancel,
		logger:      logger,
		stats:       &TrackerStats{StartTime: time.Now()},
		peerRepo:    repository.NewTorrentPeerRepository(db),
		torrentRepo: repository.NewTorrentRepository(db),
	}
}

// Start starts the tracker
func (t *Tracker) Start() error {
	t.logger.Info("Starting WebTorrent tracker")

	// Start cleanup worker
	go t.cleanupWorker()

	// Start stats worker
	go t.statsWorker()

	return nil
}

// Stop gracefully stops the tracker
func (t *Tracker) Stop() error {
	t.logger.Info("Stopping WebTorrent tracker")

	// Cancel context
	t.cancel()

	// Close all peer connections
	t.mu.Lock()
	for _, swarm := range t.peers {
		swarm.mu.Lock()
		for _, peer := range swarm.Peers {
			peer.mu.Lock()
			if peer.Conn != nil {
				if err := peer.Conn.Close(); err != nil {
					t.logger.WithError(err).Debug("Failed to close peer connection")
				}
			}
			peer.mu.Unlock()
		}
		swarm.mu.Unlock()
	}
	t.peers = make(map[string]*PeerSwarm)
	t.mu.Unlock()

	t.logger.Info("WebTorrent tracker stopped")
	return nil
}

// HandleWebSocket handles WebSocket connections from peers
func (t *Tracker) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade connection
	conn, err := t.upgrader.Upgrade(w, r, nil)
	if err != nil {
		t.logger.WithError(err).Error("Failed to upgrade WebSocket connection")
		t.stats.mu.Lock()
		t.stats.ConnectionErrors++
		t.stats.mu.Unlock()
		return
	}

	// Increment active connections
	t.stats.mu.Lock()
	t.stats.ActiveConnections++
	t.stats.mu.Unlock()

	defer func() {
		if err := conn.Close(); err != nil {
			t.logger.WithError(err).Debug("Failed to close WebSocket connection")
		}
		t.stats.mu.Lock()
		t.stats.ActiveConnections--
		t.stats.mu.Unlock()
	}()

	// Set connection limits
	conn.SetReadLimit(t.config.MaxMessageSize)
	if err := conn.SetReadDeadline(time.Now().Add(t.config.ReadTimeout)); err != nil {
		t.logger.WithError(err).Debug("Failed to set read deadline")
	}
	conn.SetPongHandler(func(string) error {
		if err := conn.SetReadDeadline(time.Now().Add(t.config.ReadTimeout)); err != nil {
			t.logger.WithError(err).Debug("Failed to set read deadline in pong handler")
		}
		return nil
	})

	// Start ping ticker
	ticker := time.NewTicker(t.config.PingInterval)
	defer ticker.Stop()

	// Handle messages
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			// Send ping
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(t.config.WriteTimeout)); err != nil {
				return
			}
		default:
			// Read message
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					t.logger.WithError(err).Debug("WebSocket connection closed unexpectedly")
				}
				return
			}

			// Parse and handle message
			t.handleMessage(conn, message, r.RemoteAddr)

			// Reset read deadline
			if err := conn.SetReadDeadline(time.Now().Add(t.config.ReadTimeout)); err != nil {
				t.logger.WithError(err).Debug("Failed to reset read deadline")
			}
		}
	}
}

// handleMessage processes incoming WebSocket messages
func (t *Tracker) handleMessage(conn *websocket.Conn, message []byte, remoteAddr string) {
	// Parse message to determine action
	var baseMsg struct {
		Action string `json:"action"`
	}

	if err := json.Unmarshal(message, &baseMsg); err != nil {
		t.sendError(conn, "", "invalid message format")
		return
	}

	switch baseMsg.Action {
	case "announce":
		t.handleAnnounce(conn, message, remoteAddr)
	case "scrape":
		t.handleScrape(conn, message)
	default:
		t.sendError(conn, "", fmt.Sprintf("unknown action: %s", baseMsg.Action))
	}
}

// handleAnnounce processes announce requests
func (t *Tracker) handleAnnounce(conn *websocket.Conn, message []byte, remoteAddr string) {
	var req AnnounceRequest
	if err := json.Unmarshal(message, &req); err != nil {
		t.sendError(conn, "", "invalid announce message")
		return
	}

	// Validate required fields
	if req.InfoHash == "" || req.PeerID == "" {
		t.sendError(conn, req.InfoHash, "missing required fields")
		return
	}

	t.stats.mu.Lock()
	t.stats.TotalAnnounces++
	t.stats.mu.Unlock()

	// Get or create swarm
	swarm := t.getOrCreateSwarm(req.InfoHash)

	// Handle peer based on event
	switch req.Event {
	case "stopped":
		t.removePeer(swarm, req.PeerID)
		return
	case "completed":
		// Mark as seeder
		t.updatePeer(swarm, &req, conn, remoteAddr, true)
	default:
		// started or empty (ongoing)
		isSeeder := req.Left == 0
		t.updatePeer(swarm, &req, conn, remoteAddr, isSeeder)
	}

	// Get peer list
	peers := t.getPeerList(swarm, req.PeerID, req.NumWant)

	// Count seeders and leechers
	complete, incomplete := t.countPeers(swarm)

	// Build response
	resp := AnnounceResponse{
		Action:     "announce",
		InfoHash:   req.InfoHash,
		Complete:   complete,
		Incomplete: incomplete,
		Interval:   int(t.config.AnnounceInterval.Seconds()),
		Peers:      peers,
	}

	// Include offer if present (WebRTC signaling)
	if req.Offer != nil && req.OfferID != "" {
		resp.Offer = req.Offer
		resp.OfferID = req.OfferID
	}

	// Send response
	t.sendMessage(conn, resp)

	// Persist to database
	go t.persistPeer(swarm, req.PeerID, remoteAddr)
}

// handleScrape processes scrape requests
func (t *Tracker) handleScrape(conn *websocket.Conn, message []byte) {
	var req ScrapeRequest
	if err := json.Unmarshal(message, &req); err != nil {
		t.sendError(conn, "", "invalid scrape message")
		return
	}

	t.stats.mu.Lock()
	t.stats.TotalScrapes++
	t.stats.mu.Unlock()

	// Build response
	resp := ScrapeResponse{
		Action: "scrape",
		Files:  make(map[string]ScrapeFileInfo),
	}

	// Get stats for each requested info hash
	for _, infoHash := range req.InfoHash {
		swarm := t.getSwarm(infoHash)
		if swarm == nil {
			continue
		}

		complete, incomplete := t.countPeers(swarm)
		resp.Files[infoHash] = ScrapeFileInfo{
			Complete:   complete,
			Incomplete: incomplete,
			Downloaded: 0, // Would need to track this separately
		}
	}

	// Send response
	t.sendMessage(conn, resp)
}

// getOrCreateSwarm gets or creates a swarm for an info hash
func (t *Tracker) getOrCreateSwarm(infoHash string) *PeerSwarm {
	t.mu.Lock()
	defer t.mu.Unlock()

	swarm, exists := t.peers[infoHash]
	if !exists {
		swarm = &PeerSwarm{
			InfoHash:    infoHash,
			Peers:       make(map[string]*TrackerPeer),
			LastUpdated: time.Now(),
		}
		t.peers[infoHash] = swarm

		t.stats.mu.Lock()
		t.stats.TotalSwarms++
		t.stats.mu.Unlock()
	}

	return swarm
}

// getSwarm gets a swarm for an info hash
func (t *Tracker) getSwarm(infoHash string) *PeerSwarm {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.peers[infoHash]
}

// updatePeer updates or adds a peer to a swarm
func (t *Tracker) updatePeer(swarm *PeerSwarm, req *AnnounceRequest, conn *websocket.Conn, remoteAddr string, isSeeder bool) {
	swarm.mu.Lock()
	defer swarm.mu.Unlock()

	peer, exists := swarm.Peers[req.PeerID]
	if !exists {
		// Extract IP from remote address
		ip := remoteAddr
		if idx := len(ip) - 1; idx >= 0 {
			for i := idx; i >= 0; i-- {
				if ip[i] == ':' {
					ip = ip[:i]
					break
				}
			}
		}

		peer = &TrackerPeer{
			PeerID:   req.PeerID,
			InfoHash: req.InfoHash,
			IP:       ip,
			Port:     0, // WebTorrent doesn't use traditional ports
			Conn:     conn,
			IsSeeder: isSeeder,
		}
		swarm.Peers[req.PeerID] = peer

		t.stats.mu.Lock()
		t.stats.TotalPeers++
		t.stats.mu.Unlock()
	}

	// Update peer info
	peer.mu.Lock()
	peer.LastSeen = time.Now()
	peer.Uploaded = req.Uploaded
	peer.Downloaded = req.Downloaded
	peer.Left = req.Left
	peer.Event = req.Event
	peer.OfferID = req.OfferID
	peer.IsSeeder = isSeeder
	peer.mu.Unlock()

	swarm.LastUpdated = time.Now()
}

// removePeer removes a peer from a swarm
func (t *Tracker) removePeer(swarm *PeerSwarm, peerID string) {
	swarm.mu.Lock()
	defer swarm.mu.Unlock()

	if _, exists := swarm.Peers[peerID]; exists {
		delete(swarm.Peers, peerID)
		t.stats.mu.Lock()
		t.stats.TotalPeers--
		t.stats.mu.Unlock()
	}
}

// getPeerList returns a list of peers for a swarm
func (t *Tracker) getPeerList(swarm *PeerSwarm, excludePeerID string, numWant int) []WebTorrentPeer {
	swarm.mu.RLock()
	defer swarm.mu.RUnlock()

	if numWant == 0 || numWant > t.config.MaxPeersToReturn {
		numWant = t.config.MaxPeersToReturn
	}

	peers := make([]WebTorrentPeer, 0, numWant)
	for _, peer := range swarm.Peers {
		if peer.PeerID == excludePeerID {
			continue
		}

		peer.mu.Lock()
		wtPeer := WebTorrentPeer{
			PeerID:  peer.PeerID,
			OfferID: peer.OfferID,
		}
		peer.mu.Unlock()

		peers = append(peers, wtPeer)

		if len(peers) >= numWant {
			break
		}
	}

	return peers
}

// countPeers counts seeders and leechers in a swarm
func (t *Tracker) countPeers(swarm *PeerSwarm) (complete int, incomplete int) {
	swarm.mu.RLock()
	defer swarm.mu.RUnlock()

	for _, peer := range swarm.Peers {
		peer.mu.Lock()
		if peer.IsSeeder {
			complete++
		} else {
			incomplete++
		}
		peer.mu.Unlock()
	}

	return complete, incomplete
}

// persistPeer saves peer info to database
func (t *Tracker) persistPeer(swarm *PeerSwarm, peerID string, remoteAddr string) {
	ctx := context.Background()

	swarm.mu.RLock()
	peer, exists := swarm.Peers[peerID]
	swarm.mu.RUnlock()

	if !exists {
		return
	}

	peer.mu.Lock()
	defer peer.mu.Unlock()

	// Create or update peer in database
	dbPeer := &domain.TorrentPeer{
		ID:              uuid.New(),
		InfoHash:        peer.InfoHash,
		PeerID:          peer.PeerID,
		IPAddress:       peer.IP,
		Port:            peer.Port,
		UploadedBytes:   peer.Uploaded,
		DownloadedBytes: peer.Downloaded,
		LeftBytes:       peer.Left,
		Event:           peer.Event,
		UserAgent:       peer.UserAgent,
		LastAnnounceAt:  time.Now(),
	}

	if err := t.peerRepo.UpsertPeer(ctx, dbPeer); err != nil {
		t.logger.WithError(err).Error("Failed to persist peer")
	}
}

// sendMessage sends a JSON message to a WebSocket connection
func (t *Tracker) sendMessage(conn *websocket.Conn, msg interface{}) {
	if err := conn.SetWriteDeadline(time.Now().Add(t.config.WriteTimeout)); err != nil {
		t.logger.WithError(err).Debug("Failed to set write deadline")
		return
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.logger.WithError(err).Debug("Failed to send WebSocket message")
	}
}

// sendError sends an error response
func (t *Tracker) sendError(conn *websocket.Conn, infoHash string, reason string) {
	resp := ErrorResponse{
		Action:        "error",
		FailureReason: reason,
		InfoHash:      infoHash,
	}
	t.sendMessage(conn, resp)

	t.stats.mu.Lock()
	t.stats.AnnounceErrors++
	t.stats.mu.Unlock()
}

// cleanupWorker periodically removes expired peers
func (t *Tracker) cleanupWorker() {
	ticker := time.NewTicker(t.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.cleanupExpiredPeers()
		case <-t.ctx.Done():
			return
		}
	}
}

// cleanupExpiredPeers removes peers that haven't announced recently
func (t *Tracker) cleanupExpiredPeers() {
	now := time.Now()
	expiration := t.config.PeerExpirationTime

	t.mu.Lock()
	defer t.mu.Unlock()

	for infoHash, swarm := range t.peers {
		swarm.mu.Lock()
		for peerID, peer := range swarm.Peers {
			peer.mu.Lock()
			if now.Sub(peer.LastSeen) > expiration {
				delete(swarm.Peers, peerID)
				t.stats.mu.Lock()
				t.stats.TotalPeers--
				t.stats.mu.Unlock()
			}
			peer.mu.Unlock()
		}

		// Remove empty swarms
		if len(swarm.Peers) == 0 {
			delete(t.peers, infoHash)
			t.stats.mu.Lock()
			t.stats.TotalSwarms--
			t.stats.mu.Unlock()
		}
		swarm.mu.Unlock()
	}
}

// statsWorker periodically logs statistics
func (t *Tracker) statsWorker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.logStats()
		case <-t.ctx.Done():
			return
		}
	}
}

// logStats logs current tracker statistics
func (t *Tracker) logStats() {
	t.stats.mu.RLock()
	defer t.stats.mu.RUnlock()

	t.logger.WithFields(logrus.Fields{
		"swarms":             t.stats.TotalSwarms,
		"peers":              t.stats.TotalPeers,
		"active_connections": t.stats.ActiveConnections,
		"total_announces":    t.stats.TotalAnnounces,
		"total_scrapes":      t.stats.TotalScrapes,
		"announce_errors":    t.stats.AnnounceErrors,
		"connection_errors":  t.stats.ConnectionErrors,
		"uptime":             time.Since(t.stats.StartTime).String(),
	}).Info("Tracker statistics")
}

// GetStats returns current tracker statistics
func (t *Tracker) GetStats() TrackerStats {
	t.stats.mu.RLock()
	defer t.stats.mu.RUnlock()
	return TrackerStats{
		TotalAnnounces:    t.stats.TotalAnnounces,
		TotalScrapes:      t.stats.TotalScrapes,
		ActiveConnections: t.stats.ActiveConnections,
		TotalPeers:        t.stats.TotalPeers,
		TotalSwarms:       t.stats.TotalSwarms,
		AnnounceErrors:    t.stats.AnnounceErrors,
		ConnectionErrors:  t.stats.ConnectionErrors,
		StartTime:         t.stats.StartTime,
	}
}

// GetSwarmInfo returns information about a specific swarm
func (t *Tracker) GetSwarmInfo(infoHash string) map[string]interface{} {
	swarm := t.getSwarm(infoHash)
	if swarm == nil {
		return nil
	}

	complete, incomplete := t.countPeers(swarm)

	return map[string]interface{}{
		"info_hash":    infoHash,
		"seeders":      complete,
		"leechers":     incomplete,
		"total_peers":  complete + incomplete,
		"last_updated": swarm.LastUpdated,
	}
}
