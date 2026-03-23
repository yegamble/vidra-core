package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/plugin"
)

// AnalyticsExportPlugin exports analytics events to a file
type AnalyticsExportPlugin struct {
	name        string
	version     string
	author      string
	description string
	enabled     bool

	exportPath    string
	batchSize     int
	flushInterval time.Duration

	events []map[string]any
	mu     sync.Mutex
	ticker *time.Ticker
	done   chan struct{}
}

// NewAnalyticsExportPlugin creates a new analytics export plugin
func NewAnalyticsExportPlugin() *AnalyticsExportPlugin {
	return &AnalyticsExportPlugin{
		name:          "analytics-export",
		version:       "1.0.0",
		author:        "Vidra Core Team",
		description:   "Exports analytics events to JSON files",
		enabled:       false,
		batchSize:     100,
		flushInterval: 1 * time.Minute,
		events:        make([]map[string]any, 0, 100),
		done:          make(chan struct{}),
	}
}

// Name returns the plugin name
func (p *AnalyticsExportPlugin) Name() string {
	return p.name
}

// Version returns the plugin version
func (p *AnalyticsExportPlugin) Version() string {
	return p.version
}

// Author returns the plugin author
func (p *AnalyticsExportPlugin) Author() string {
	return p.author
}

// Description returns the plugin description
func (p *AnalyticsExportPlugin) Description() string {
	return p.description
}

// Initialize initializes the plugin
func (p *AnalyticsExportPlugin) Initialize(ctx context.Context, config map[string]any) error {
	// Extract export path from config
	if path, ok := config["export_path"].(string); ok {
		p.exportPath = path
	} else {
		return fmt.Errorf("export_path is required in configuration")
	}

	// Ensure export directory exists
	if err := os.MkdirAll(p.exportPath, 0750); err != nil {
		return fmt.Errorf("failed to create export directory: %w", err)
	}

	// Extract batch size (optional)
	if batchSize, ok := config["batch_size"].(float64); ok {
		p.batchSize = int(batchSize)
	}

	// Extract flush interval (optional)
	if intervalSeconds, ok := config["flush_interval_seconds"].(float64); ok {
		p.flushInterval = time.Duration(intervalSeconds) * time.Second
	}

	// Start background flush ticker
	p.ticker = time.NewTicker(p.flushInterval)
	go p.flushLoop()

	return nil
}

// Shutdown shuts down the plugin
func (p *AnalyticsExportPlugin) Shutdown(ctx context.Context) error {
	close(p.done)
	if p.ticker != nil {
		p.ticker.Stop()
	}

	// Flush any remaining events
	return p.flush()
}

// Enabled returns whether the plugin is enabled
func (p *AnalyticsExportPlugin) Enabled() bool {
	return p.enabled
}

// SetEnabled sets the enabled state
func (p *AnalyticsExportPlugin) SetEnabled(enabled bool) {
	p.enabled = enabled
}

// OnAnalyticsEvent is called when an analytics event is tracked
func (p *AnalyticsExportPlugin) OnAnalyticsEvent(ctx context.Context, event *domain.AnalyticsEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Add event to buffer
	p.events = append(p.events, map[string]any{
		"video_id":    event.VideoID,
		"event_type":  event.EventType,
		"user_id":     event.UserID,
		"session_id":  event.SessionID,
		"timestamp":   event.TimestampSeconds,
		"duration":    event.WatchDurationSecs,
		"device_type": event.DeviceType,
		"browser":     event.Browser,
		"os":          event.OS,
		"quality":     event.Quality,
		"created_at":  event.CreatedAt,
	})

	// Flush if batch size reached
	if len(p.events) >= p.batchSize {
		return p.flushUnlocked()
	}

	return nil
}

// OnDailyAggregation is called when daily analytics are aggregated
func (p *AnalyticsExportPlugin) OnDailyAggregation(ctx context.Context, videoID string, date string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Add aggregation event to buffer
	p.events = append(p.events, map[string]any{
		"type":       "daily_aggregation",
		"video_id":   videoID,
		"date":       date,
		"created_at": time.Now(),
	})

	// Flush if batch size reached
	if len(p.events) >= p.batchSize {
		return p.flushUnlocked()
	}

	return nil
}

// flushLoop periodically flushes events to disk
func (p *AnalyticsExportPlugin) flushLoop() {
	for {
		select {
		case <-p.ticker.C:
			_ = p.flush()
		case <-p.done:
			return
		}
	}
}

// flush writes buffered events to disk
func (p *AnalyticsExportPlugin) flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.flushUnlocked()
}

// flushUnlocked writes buffered events to disk (must be called with lock held)
func (p *AnalyticsExportPlugin) flushUnlocked() error {
	if len(p.events) == 0 {
		return nil
	}

	// Generate filename with timestamp
	filename := fmt.Sprintf("analytics_%s.json", time.Now().Format("20060102_150405"))
	filepath := filepath.Join(p.exportPath, filename)

	// Write events to file
	data, err := json.MarshalIndent(p.events, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal events: %w", err)
	}

	if err := os.WriteFile(filepath, data, 0600); err != nil {
		return fmt.Errorf("failed to write events to file: %w", err)
	}

	// Clear buffer
	p.events = make([]map[string]any, 0, p.batchSize)

	return nil
}

// Ensure AnalyticsExportPlugin implements AnalyticsPlugin interface
var _ plugin.AnalyticsPlugin = (*AnalyticsExportPlugin)(nil)
