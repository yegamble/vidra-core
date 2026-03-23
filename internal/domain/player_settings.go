package domain

// PlayerSettings represents player settings for a specific video or channel.
type PlayerSettings struct {
	ID               int64   `json:"id" db:"id"`
	VideoID          *string `json:"videoId,omitempty" db:"video_id"`
	ChannelHandle    *string `json:"channelHandle,omitempty" db:"channel_handle"`
	Autoplay         bool    `json:"autoplay" db:"autoplay"`
	Loop             bool    `json:"loop" db:"loop"`
	DefaultQuality   string  `json:"defaultQuality" db:"default_quality"`
	DefaultSpeed     float64 `json:"defaultSpeed" db:"default_speed"`
	SubtitlesEnabled bool    `json:"subtitlesEnabled" db:"subtitles_enabled"`
	Theatre          bool    `json:"theatre" db:"theatre"`
}
