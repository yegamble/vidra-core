// Package media extracts technical metadata from stored media files. The
// FFprobe-backed prober is the real implementation behind video.Prober; the
// pure parser is split out so it is unit-testable without the ffprobe binary.
package media

// Metadata is the technical information a probe extracts from a media file. A
// zero field means "unknown" (the probe could not determine it — e.g. an
// audio-only file has no width/height).
type Metadata struct {
	DurationSeconds int
	Width           int
	Height          int
	// FPS is the video stream's average frame rate (frames per second); 0 when
	// unknown. The transcoder uses it to decide whether transcoding_max_fps
	// needs an fps filter (a cap is never applied to a slower/unknown source).
	FPS float64
}
