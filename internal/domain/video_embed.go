package domain

// Embed privacy status constants.
const (
	EmbedEnabled   = 1 // embedding allowed everywhere
	EmbedDisabled  = 2 // embedding disabled
	EmbedWhitelist = 3 // embedding restricted to allowed domains
)

// VideoEmbedPrivacy controls where a video may be embedded.
type VideoEmbedPrivacy struct {
	VideoID        string   `json:"videoId" db:"video_id"`
	Status         int      `json:"status" db:"status"` // 1=enabled, 2=disabled, 3=whitelist
	AllowedDomains []string `json:"allowedDomains" db:"-"`
}

// VideoEmbedAllowedDomain is a single domain entry for embed whitelisting.
type VideoEmbedAllowedDomain struct {
	ID      int64  `json:"id" db:"id"`
	VideoID string `json:"videoId" db:"video_id"`
	Domain  string `json:"domain" db:"domain"`
}
