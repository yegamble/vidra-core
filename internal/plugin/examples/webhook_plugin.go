package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/plugin"
)

// WebhookPlugin sends webhooks when events occur
type WebhookPlugin struct {
	name        string
	version     string
	author      string
	description string
	enabled     bool

	webhookURL string
	secret     string
	timeout    time.Duration
	httpClient *http.Client
}

// NewWebhookPlugin creates a new webhook plugin
func NewWebhookPlugin() *WebhookPlugin {
	return &WebhookPlugin{
		name:        "webhook",
		version:     "1.0.0",
		author:      "Vidra Core Team",
		description: "Sends HTTP webhooks for video events",
		enabled:     false,
		timeout:     10 * time.Second,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Name returns the plugin name
func (p *WebhookPlugin) Name() string {
	return p.name
}

// Version returns the plugin version
func (p *WebhookPlugin) Version() string {
	return p.version
}

// Author returns the plugin author
func (p *WebhookPlugin) Author() string {
	return p.author
}

// Description returns the plugin description
func (p *WebhookPlugin) Description() string {
	return p.description
}

// Initialize initializes the plugin
func (p *WebhookPlugin) Initialize(ctx context.Context, config map[string]any) error {
	// Extract webhook URL from config
	if url, ok := config["webhook_url"].(string); ok {
		p.webhookURL = url
	} else {
		return fmt.Errorf("webhook_url is required in configuration")
	}

	// Extract secret (optional)
	if secret, ok := config["secret"].(string); ok {
		p.secret = secret
	}

	// Extract timeout (optional)
	if timeout, ok := config["timeout_seconds"].(float64); ok {
		p.timeout = time.Duration(timeout) * time.Second
		p.httpClient.Timeout = p.timeout
	}

	return nil
}

// Shutdown shuts down the plugin
func (p *WebhookPlugin) Shutdown(ctx context.Context) error {
	// Cleanup resources if needed
	return nil
}

// Enabled returns whether the plugin is enabled
func (p *WebhookPlugin) Enabled() bool {
	return p.enabled
}

// SetEnabled sets the enabled state
func (p *WebhookPlugin) SetEnabled(enabled bool) {
	p.enabled = enabled
}

// OnVideoUploaded is called when a new video is uploaded
func (p *WebhookPlugin) OnVideoUploaded(ctx context.Context, video *domain.Video) error {
	return p.sendWebhook(ctx, "video.uploaded", map[string]any{
		"video_id":    video.ID,
		"title":       video.Title,
		"user_id":     video.UserID,
		"channel_id":  video.ChannelID,
		"privacy":     video.Privacy,
		"uploaded_at": video.CreatedAt,
	})
}

// OnVideoProcessed is called when video encoding completes
func (p *WebhookPlugin) OnVideoProcessed(ctx context.Context, video *domain.Video) error {
	return p.sendWebhook(ctx, "video.processed", map[string]any{
		"video_id":     video.ID,
		"title":        video.Title,
		"status":       video.Status,
		"duration":     video.Duration,
		"processed_at": video.UpdatedAt,
	})
}

// OnVideoDeleted is called when a video is deleted
func (p *WebhookPlugin) OnVideoDeleted(ctx context.Context, videoID string) error {
	return p.sendWebhook(ctx, "video.deleted", map[string]any{
		"video_id":   videoID,
		"deleted_at": time.Now(),
	})
}

// OnVideoUpdated is called when video metadata is updated
func (p *WebhookPlugin) OnVideoUpdated(ctx context.Context, video *domain.Video) error {
	return p.sendWebhook(ctx, "video.updated", map[string]any{
		"video_id":   video.ID,
		"title":      video.Title,
		"updated_at": video.UpdatedAt,
	})
}

// sendWebhook sends an HTTP POST request to the configured webhook URL
func (p *WebhookPlugin) sendWebhook(ctx context.Context, event string, data map[string]any) error {
	payload := map[string]any{
		"event":     event,
		"data":      data,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Vidra Core-Webhook-Plugin/"+p.version)

	// Add signature header if secret is configured
	if p.secret != "" {
		// In production, use HMAC-SHA256 for signing
		req.Header.Set("X-Webhook-Secret", p.secret)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	return nil
}

// Ensure WebhookPlugin implements VideoPlugin interface
var _ plugin.VideoPlugin = (*WebhookPlugin)(nil)
