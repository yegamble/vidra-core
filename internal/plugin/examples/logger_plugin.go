package examples

import (
	"context"
	"fmt"
	"log"
	"os"

	"athena/internal/domain"
	"athena/internal/plugin"
)

// LoggerPlugin logs all plugin events to a file or stdout
type LoggerPlugin struct {
	name        string
	version     string
	author      string
	description string
	enabled     bool

	logger *log.Logger
	file   *os.File
}

// NewLoggerPlugin creates a new logger plugin
func NewLoggerPlugin() *LoggerPlugin {
	return &LoggerPlugin{
		name:        "logger",
		version:     "1.0.0",
		author:      "Athena Team",
		description: "Logs all plugin events for debugging",
		enabled:     false,
	}
}

// Name returns the plugin name
func (p *LoggerPlugin) Name() string {
	return p.name
}

// Version returns the plugin version
func (p *LoggerPlugin) Version() string {
	return p.version
}

// Author returns the plugin author
func (p *LoggerPlugin) Author() string {
	return p.author
}

// Description returns the plugin description
func (p *LoggerPlugin) Description() string {
	return p.description
}

// Initialize initializes the plugin
func (p *LoggerPlugin) Initialize(ctx context.Context, config map[string]any) error {
	// Extract log file path from config (optional)
	if logPath, ok := config["log_file"].(string); ok && logPath != "" {
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		p.file = file
		p.logger = log.New(file, "[PLUGIN-LOGGER] ", log.LstdFlags)
	} else {
		// Log to stdout by default
		p.logger = log.New(os.Stdout, "[PLUGIN-LOGGER] ", log.LstdFlags)
	}

	p.logger.Println("Logger plugin initialized")
	return nil
}

// Shutdown shuts down the plugin
func (p *LoggerPlugin) Shutdown(ctx context.Context) error {
	p.logger.Println("Logger plugin shutting down")
	if p.file != nil {
		return p.file.Close()
	}
	return nil
}

// Enabled returns whether the plugin is enabled
func (p *LoggerPlugin) Enabled() bool {
	return p.enabled
}

// SetEnabled sets the enabled state
func (p *LoggerPlugin) SetEnabled(enabled bool) {
	p.enabled = enabled
}

// OnVideoUploaded is called when a new video is uploaded
func (p *LoggerPlugin) OnVideoUploaded(ctx context.Context, video *domain.Video) error {
	p.logger.Printf("VIDEO UPLOADED: id=%s, title=%s, user_id=%s\n",
		video.ID, video.Title, video.UserID)
	return nil
}

// OnVideoProcessed is called when video encoding completes
func (p *LoggerPlugin) OnVideoProcessed(ctx context.Context, video *domain.Video) error {
	p.logger.Printf("VIDEO PROCESSED: id=%s, title=%s, status=%s, duration=%d\n",
		video.ID, video.Title, video.Status, video.Duration)
	return nil
}

// OnVideoDeleted is called when a video is deleted
func (p *LoggerPlugin) OnVideoDeleted(ctx context.Context, videoID string) error {
	p.logger.Printf("VIDEO DELETED: id=%s\n", videoID)
	return nil
}

// OnVideoUpdated is called when video metadata is updated
func (p *LoggerPlugin) OnVideoUpdated(ctx context.Context, video *domain.Video) error {
	p.logger.Printf("VIDEO UPDATED: id=%s, title=%s\n", video.ID, video.Title)
	return nil
}

// OnUserRegistered is called when a new user registers
func (p *LoggerPlugin) OnUserRegistered(ctx context.Context, user *domain.User) error {
	p.logger.Printf("USER REGISTERED: id=%s, username=%s, email=%s\n",
		user.ID, user.Username, user.Email)
	return nil
}

// OnUserLogin is called when a user logs in
func (p *LoggerPlugin) OnUserLogin(ctx context.Context, user *domain.User) error {
	p.logger.Printf("USER LOGIN: id=%s, username=%s\n", user.ID, user.Username)
	return nil
}

// OnUserDeleted is called when a user account is deleted
func (p *LoggerPlugin) OnUserDeleted(ctx context.Context, userID string) error {
	p.logger.Printf("USER DELETED: id=%s\n", userID)
	return nil
}

// OnUserUpdated is called when user profile is updated
func (p *LoggerPlugin) OnUserUpdated(ctx context.Context, user *domain.User) error {
	p.logger.Printf("USER UPDATED: id=%s, username=%s\n", user.ID, user.Username)
	return nil
}

// OnChannelCreated is called when a new channel is created
func (p *LoggerPlugin) OnChannelCreated(ctx context.Context, channel *domain.Channel) error {
	p.logger.Printf("CHANNEL CREATED: id=%s, name=%s, user_id=%s\n",
		channel.ID, channel.Name, channel.UserID)
	return nil
}

// OnChannelUpdated is called when a channel is updated
func (p *LoggerPlugin) OnChannelUpdated(ctx context.Context, channel *domain.Channel) error {
	p.logger.Printf("CHANNEL UPDATED: id=%s, name=%s\n", channel.ID, channel.Name)
	return nil
}

// OnChannelDeleted is called when a channel is deleted
func (p *LoggerPlugin) OnChannelDeleted(ctx context.Context, channelID string) error {
	p.logger.Printf("CHANNEL DELETED: id=%s\n", channelID)
	return nil
}

// OnChannelSubscribed is called when a user subscribes to a channel
func (p *LoggerPlugin) OnChannelSubscribed(ctx context.Context, channelID, userID string) error {
	p.logger.Printf("CHANNEL SUBSCRIBED: channel_id=%s, user_id=%s\n", channelID, userID)
	return nil
}

// Ensure LoggerPlugin implements multiple plugin interfaces
var _ plugin.VideoPlugin = (*LoggerPlugin)(nil)
var _ plugin.UserPlugin = (*LoggerPlugin)(nil)
var _ plugin.ChannelPlugin = (*LoggerPlugin)(nil)
