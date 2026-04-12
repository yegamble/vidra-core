package domain

// VideoStoryboard describes a sprite-sheet storyboard generated for a video.
type VideoStoryboard struct {
	ID             int64   `json:"id" db:"id"`
	VideoID        string  `json:"videoId" db:"video_id"`
	Filename       string  `json:"filename" db:"filename"`
	TotalHeight    int     `json:"totalHeight" db:"total_height"`
	TotalWidth     int     `json:"totalWidth" db:"total_width"`
	SpriteHeight   int     `json:"spriteHeight" db:"sprite_height"`
	SpriteWidth    int     `json:"spriteWidth" db:"sprite_width"`
	SpriteDuration float64 `json:"spriteDuration" db:"sprite_duration"`
	// PeerTube v8.0+ deprecation compat: storyboardPath → fileUrl
	StoryboardPath string `json:"storyboardPath,omitempty" db:"-"`
	FileURL        string `json:"fileUrl,omitempty" db:"-"`
}

// ComputeFileURL sets StoryboardPath and FileURL from the Filename and base URL.
func (s *VideoStoryboard) ComputeFileURL(baseURL string) {
	if s.Filename != "" {
		s.StoryboardPath = "/lazy-static/storyboards/" + s.Filename
		s.FileURL = baseURL + s.StoryboardPath
	}
}
