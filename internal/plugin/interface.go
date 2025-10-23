package plugin

import (
	"context"

	"athena/internal/domain"

	"github.com/go-chi/chi/v5"
)

// Plugin is the base interface that all plugins must implement
type Plugin interface {
	// Name returns the plugin name
	Name() string

	// Version returns the plugin version (semantic versioning)
	Version() string

	// Author returns the plugin author
	Author() string

	// Description returns a brief description of what the plugin does
	Description() string

	// Initialize initializes the plugin with the given configuration
	Initialize(ctx context.Context, config map[string]any) error

	// Shutdown gracefully shuts down the plugin
	Shutdown(ctx context.Context) error

	// Enabled returns whether the plugin is currently enabled
	Enabled() bool

	// SetEnabled sets the enabled state of the plugin
	SetEnabled(enabled bool)
}

// HookFunc is the function signature for hook callbacks
// The data parameter contains context-specific data for the event
type HookFunc func(ctx context.Context, data any) error

// VideoPlugin extends Plugin with video-related hooks
type VideoPlugin interface {
	Plugin

	// OnVideoUploaded is called when a new video is uploaded
	OnVideoUploaded(ctx context.Context, video *domain.Video) error

	// OnVideoProcessed is called when video encoding completes
	OnVideoProcessed(ctx context.Context, video *domain.Video) error

	// OnVideoDeleted is called when a video is deleted
	OnVideoDeleted(ctx context.Context, videoID string) error

	// OnVideoUpdated is called when video metadata is updated
	OnVideoUpdated(ctx context.Context, video *domain.Video) error
}

// UserPlugin extends Plugin with user-related hooks
type UserPlugin interface {
	Plugin

	// OnUserRegistered is called when a new user registers
	OnUserRegistered(ctx context.Context, user *domain.User) error

	// OnUserLogin is called when a user logs in
	OnUserLogin(ctx context.Context, user *domain.User) error

	// OnUserDeleted is called when a user account is deleted
	OnUserDeleted(ctx context.Context, userID string) error

	// OnUserUpdated is called when user profile is updated
	OnUserUpdated(ctx context.Context, user *domain.User) error
}

// ChannelPlugin extends Plugin with channel-related hooks
type ChannelPlugin interface {
	Plugin

	// OnChannelCreated is called when a new channel is created
	OnChannelCreated(ctx context.Context, channel *domain.Channel) error

	// OnChannelUpdated is called when a channel is updated
	OnChannelUpdated(ctx context.Context, channel *domain.Channel) error

	// OnChannelDeleted is called when a channel is deleted
	OnChannelDeleted(ctx context.Context, channelID string) error

	// OnChannelSubscribed is called when a user subscribes to a channel
	OnChannelSubscribed(ctx context.Context, channelID, userID string) error
}

// LiveStreamPlugin extends Plugin with live stream-related hooks
type LiveStreamPlugin interface {
	Plugin

	// OnStreamStarted is called when a live stream starts
	OnStreamStarted(ctx context.Context, stream *domain.LiveStream) error

	// OnStreamEnded is called when a live stream ends
	OnStreamEnded(ctx context.Context, stream *domain.LiveStream) error

	// OnViewerJoined is called when a viewer joins a live stream
	OnViewerJoined(ctx context.Context, streamID, viewerID string) error

	// OnViewerLeft is called when a viewer leaves a live stream
	OnViewerLeft(ctx context.Context, streamID, viewerID string) error
}

// CommentPlugin extends Plugin with comment-related hooks
type CommentPlugin interface {
	Plugin

	// OnCommentCreated is called when a new comment is posted
	OnCommentCreated(ctx context.Context, comment *domain.Comment) error

	// OnCommentDeleted is called when a comment is deleted
	OnCommentDeleted(ctx context.Context, commentID string) error

	// OnCommentReported is called when a comment is reported
	OnCommentReported(ctx context.Context, commentID, reporterID string) error
}

// APIPlugin extends Plugin with API route registration
type APIPlugin interface {
	Plugin

	// RegisterRoutes registers custom HTTP routes with the router
	// The plugin can add new API endpoints under /api/v1/plugins/{plugin-name}/
	RegisterRoutes(router chi.Router)
}

// StoragePlugin extends Plugin with storage-related hooks
type StoragePlugin interface {
	Plugin

	// OnFileStored is called when a file is stored
	OnFileStored(ctx context.Context, path string, size int64) error

	// OnFileDeleted is called when a file is deleted
	OnFileDeleted(ctx context.Context, path string) error
}

// ModerationPlugin extends Plugin with moderation-related hooks
type ModerationPlugin interface {
	Plugin

	// OnContentReported is called when content is reported
	OnContentReported(ctx context.Context, contentType, contentID, reporterID string) error

	// OnContentModerated is called when content is moderated (approved/rejected)
	OnContentModerated(ctx context.Context, contentType, contentID string, action string) error
}

// AnalyticsPlugin extends Plugin with analytics-related hooks
type AnalyticsPlugin interface {
	Plugin

	// OnAnalyticsEvent is called when an analytics event is tracked
	OnAnalyticsEvent(ctx context.Context, event *domain.AnalyticsEvent) error

	// OnDailyAggregation is called when daily analytics are aggregated
	OnDailyAggregation(ctx context.Context, videoID string, date string) error
}

// NotificationPlugin extends Plugin with notification-related hooks
type NotificationPlugin interface {
	Plugin

	// OnNotificationCreated is called when a notification is created
	OnNotificationCreated(ctx context.Context, notification *domain.Notification) error

	// OnNotificationSent is called when a notification is sent
	OnNotificationSent(ctx context.Context, notificationID string, success bool) error
}

// FederationPlugin extends Plugin with federation-related hooks
type FederationPlugin interface {
	Plugin

	// OnActivityReceived is called when an ActivityPub activity is received
	OnActivityReceived(ctx context.Context, actorID string, activity any) error

	// OnActivitySent is called when an ActivityPub activity is sent
	OnActivitySent(ctx context.Context, actorID string, activity any) error
}

// SearchPlugin extends Plugin with search-related hooks
type SearchPlugin interface {
	Plugin

	// OnSearchQuery is called when a search query is performed
	OnSearchQuery(ctx context.Context, query string, results int) error

	// OnVideoIndexed is called when a video is indexed for search
	OnVideoIndexed(ctx context.Context, videoID string) error
}

// EventType represents the type of hook event
type EventType string

const (
	// Video events
	EventVideoUploaded  EventType = "video.uploaded"
	EventVideoProcessed EventType = "video.processed"
	EventVideoDeleted   EventType = "video.deleted"
	EventVideoUpdated   EventType = "video.updated"

	// User events
	EventUserRegistered EventType = "user.registered"
	EventUserLogin      EventType = "user.login"
	EventUserDeleted    EventType = "user.deleted"
	EventUserUpdated    EventType = "user.updated"

	// Channel events
	EventChannelCreated    EventType = "channel.created"
	EventChannelUpdated    EventType = "channel.updated"
	EventChannelDeleted    EventType = "channel.deleted"
	EventChannelSubscribed EventType = "channel.subscribed"

	// Live stream events
	EventStreamStarted EventType = "stream.started"
	EventStreamEnded   EventType = "stream.ended"
	EventViewerJoined  EventType = "viewer.joined"
	EventViewerLeft    EventType = "viewer.left"

	// Comment events
	EventCommentCreated  EventType = "comment.created"
	EventCommentDeleted  EventType = "comment.deleted"
	EventCommentReported EventType = "comment.reported"

	// Storage events
	EventFileStored  EventType = "file.stored"
	EventFileDeleted EventType = "file.deleted"

	// Moderation events
	EventContentReported  EventType = "content.reported"
	EventContentModerated EventType = "content.moderated"

	// Analytics events
	EventAnalyticsEvent   EventType = "analytics.event"
	EventDailyAggregation EventType = "analytics.aggregation"

	// Notification events
	EventNotificationCreated EventType = "notification.created"
	EventNotificationSent    EventType = "notification.sent"

	// Federation events
	EventActivityReceived EventType = "federation.activity.received"
	EventActivitySent     EventType = "federation.activity.sent"

	// Search events
	EventSearchQuery  EventType = "search.query"
	EventVideoIndexed EventType = "search.video.indexed"
)

// EventData contains the data passed to event hooks
type EventData struct {
	// Type is the event type
	Type EventType

	// Data is the event-specific data (e.g., *domain.Video, *domain.User)
	Data any

	// Metadata contains additional context
	Metadata map[string]any
}

// PluginInfo contains metadata about a plugin
type PluginInfo struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Author      string         `json:"author"`
	Description string         `json:"description"`
	Enabled     bool           `json:"enabled"`
	Config      map[string]any `json:"config,omitempty"`
	Permissions []string       `json:"permissions"`
	Hooks       []EventType    `json:"hooks"`
}

// Permission represents a plugin permission
type Permission string

const (
	// Video permissions
	PermissionReadVideos   Permission = "read_videos"
	PermissionWriteVideos  Permission = "write_videos"
	PermissionDeleteVideos Permission = "delete_videos"

	// User permissions
	PermissionReadUsers   Permission = "read_users"
	PermissionWriteUsers  Permission = "write_users"
	PermissionDeleteUsers Permission = "delete_users"

	// Channel permissions
	PermissionReadChannels   Permission = "read_channels"
	PermissionWriteChannels  Permission = "write_channels"
	PermissionDeleteChannels Permission = "delete_channels"

	// Storage permissions
	PermissionReadStorage   Permission = "read_storage"
	PermissionWriteStorage  Permission = "write_storage"
	PermissionDeleteStorage Permission = "delete_storage"

	// Analytics permissions
	PermissionReadAnalytics  Permission = "read_analytics"
	PermissionWriteAnalytics Permission = "write_analytics"

	// Moderation permissions
	PermissionModerateContent Permission = "moderate_content"

	// Admin permissions
	PermissionAdminAccess Permission = "admin_access"

	// API permissions
	PermissionRegisterRoutes Permission = "register_routes"

	// Federation permissions
	PermissionFederation Permission = "federation"
)
