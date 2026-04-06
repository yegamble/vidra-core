package obs

import (
	"fmt"

	"vidra-core/internal/domain"
)

// This file contains EntityAuditView implementations for the 8 entity types that
// PeerTube audits: videos, users, channels, comments, config, abuses, video imports,
// and channel syncs.
//
// Each view filters to a safe subset of fields (no passwords, tokens, or secrets).

// --- Video ---

// VideoAuditView is an EntityAuditView for domain.Video.
type VideoAuditView struct {
	v *domain.Video
}

// NewVideoAuditView creates an audit view for a Video.
func NewVideoAuditView(v *domain.Video) *VideoAuditView {
	return &VideoAuditView{v: v}
}

// ToLogKeys returns safe, auditable fields for a Video.
func (vav *VideoAuditView) ToLogKeys() map[string]interface{} {
	return map[string]interface{}{
		"video-id":        vav.v.ID,
		"video-name":      vav.v.Title,
		"video-privacy":   fmt.Sprintf("%v", vav.v.Privacy),
		"video-is-remote": vav.v.IsRemote,
		"video-user-id":   vav.v.UserID,
	}
}

// --- User ---

// UserAuditView is an EntityAuditView for domain.User.
type UserAuditView struct {
	u *domain.User
}

// NewUserAuditView creates an audit view for a User.
func NewUserAuditView(u *domain.User) *UserAuditView {
	return &UserAuditView{u: u}
}

// ToLogKeys returns safe, auditable fields for a User.
// Email is intentionally excluded — it is PII and audit files are long-lived.
// Password, tokens, and 2FA secrets are always excluded.
func (uav *UserAuditView) ToLogKeys() map[string]interface{} {
	return map[string]interface{}{
		"user-id":       uav.u.ID,
		"user-username": uav.u.Username,
		"user-role":     fmt.Sprintf("%v", uav.u.Role),
		"user-active":   uav.u.IsActive,
	}
}

// --- Channel ---

// ChannelAuditView is an EntityAuditView for domain.Channel.
type ChannelAuditView struct {
	c *domain.Channel
}

// NewChannelAuditView creates an audit view for a Channel.
func NewChannelAuditView(c *domain.Channel) *ChannelAuditView {
	return &ChannelAuditView{c: c}
}

// ToLogKeys returns safe, auditable fields for a Channel.
func (cav *ChannelAuditView) ToLogKeys() map[string]interface{} {
	desc := ""
	if cav.c.Description != nil {
		desc = *cav.c.Description
	}
	return map[string]interface{}{
		"channel-id":          cav.c.ID.String(),
		"channel-handle":      cav.c.Handle,
		"channel-name":        cav.c.DisplayName,
		"channel-description": desc,
		"channel-is-local":    cav.c.IsLocal,
		"channel-owner-id":    cav.c.UserID.String(),
	}
}

// --- Comment ---

// CommentAuditView is an EntityAuditView for domain.Comment.
type CommentAuditView struct {
	c *domain.Comment
}

// NewCommentAuditView creates an audit view for a Comment.
func NewCommentAuditView(c *domain.Comment) *CommentAuditView {
	return &CommentAuditView{c: c}
}

// ToLogKeys returns safe, auditable fields for a Comment.
// Full comment body is intentionally excluded — it may contain PII.
func (cav *CommentAuditView) ToLogKeys() map[string]interface{} {
	parentID := ""
	if cav.c.ParentID != nil {
		parentID = cav.c.ParentID.String()
	}
	return map[string]interface{}{
		"comment-id":        cav.c.ID.String(),
		"comment-video-id":  cav.c.VideoID.String(),
		"comment-user-id":   cav.c.UserID.String(),
		"comment-parent-id": parentID,
	}
}

// --- Config ---

// ConfigAuditView is an EntityAuditView for arbitrary configuration changes.
type ConfigAuditView struct {
	m map[string]interface{}
}

// NewConfigAuditView creates an audit view for configuration data.
func NewConfigAuditView(data map[string]interface{}) *ConfigAuditView {
	return &ConfigAuditView{m: data}
}

// ToLogKeys returns the config change data.
func (cav *ConfigAuditView) ToLogKeys() map[string]interface{} {
	result := make(map[string]interface{}, len(cav.m))
	for k, v := range cav.m {
		result["config-"+k] = v
	}
	return result
}

// --- Abuse ---

// AbuseAuditView is an EntityAuditView for domain.AbuseReport.
type AbuseAuditView struct {
	a *domain.AbuseReport
}

// NewAbuseAuditView creates an audit view for an AbuseReport.
func NewAbuseAuditView(a *domain.AbuseReport) *AbuseAuditView {
	return &AbuseAuditView{a: a}
}

// ToLogKeys returns safe, auditable fields for an AbuseReport.
func (aav *AbuseAuditView) ToLogKeys() map[string]interface{} {
	return map[string]interface{}{
		"abuse-id":          aav.a.ID,
		"abuse-reason":      aav.a.Reason,
		"abuse-reporter-id": aav.a.ReporterID,
		"abuse-entity-type": fmt.Sprintf("%v", aav.a.EntityType),
	}
}

// --- VideoImport ---

// VideoImportAuditView is an EntityAuditView for domain.VideoImport.
type VideoImportAuditView struct {
	vi *domain.VideoImport
}

// NewVideoImportAuditView creates an audit view for a VideoImport.
func NewVideoImportAuditView(vi *domain.VideoImport) *VideoImportAuditView {
	return &VideoImportAuditView{vi: vi}
}

// ToLogKeys returns safe, auditable fields for a VideoImport.
func (viav *VideoImportAuditView) ToLogKeys() map[string]interface{} {
	videoID := ""
	if viav.vi.VideoID != nil {
		videoID = *viav.vi.VideoID
	}
	return map[string]interface{}{
		"video-import-id":         viav.vi.ID,
		"video-import-source-url": viav.vi.SourceURL,
		"video-import-user-id":    viav.vi.UserID,
		"video-import-video-id":   videoID,
	}
}

// --- ChannelSync ---

// ChannelSyncAuditView is an EntityAuditView for domain.ChannelSync.
type ChannelSyncAuditView struct {
	cs *domain.ChannelSync
}

// NewChannelSyncAuditView creates an audit view for a ChannelSync.
func NewChannelSyncAuditView(cs *domain.ChannelSync) *ChannelSyncAuditView {
	return &ChannelSyncAuditView{cs: cs}
}

// ToLogKeys returns safe, auditable fields for a ChannelSync.
func (csav *ChannelSyncAuditView) ToLogKeys() map[string]interface{} {
	return map[string]interface{}{
		"channel-sync-id":                   csav.cs.ID,
		"channel-sync-channel-id":           csav.cs.ChannelID,
		"channel-sync-external-channel-url": csav.cs.ExternalChannelURL,
	}
}
