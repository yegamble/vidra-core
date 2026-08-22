// Package media extracts technical metadata from stored media files. The
// FFprobe-backed prober is the real implementation behind video.Prober; the
// pure parser is split out so it is unit-testable without the ffprobe binary.
package media

import (
	"errors"
	"fmt"
)

// PermanentError marks a transcode failure that RETRYING CANNOT FIX: the answer
// would be identical on every attempt, so the queue's exponential backoff buys
// nothing and costs the operator a quarter of an hour of a job sitting in
// 'pending' before it says what it was always going to say.
//
// It lives here, not in internal/transcode, because permanence is decided where
// the failure is diagnosed — and internal/transcode already imports this package
// (the reverse would be a cycle). The queue asks IsPermanent and dead-letters
// immediately; the error text is stored exactly as any other failure's is.
//
// Reserve it for CONFIGURATION and CONTENT verdicts — "this packager cannot
// express this source" — never for anything that touches the network, the disk
// or a subprocess, all of which can succeed on a second try.
type PermanentError struct{ msg string }

func (e *PermanentError) Error() string { return e.msg }

// NewPermanentError declares a failure final. It is exported because the
// judgement is the package's to make but not the package's alone to state: a
// caller that diagnoses a configuration verdict of its own says so with this,
// and the queue's short-circuit then applies to it unchanged.
func NewPermanentError(msg string) error { return &PermanentError{msg: msg} }

// permanentf is NewPermanentError with a formatted message.
func permanentf(format string, args ...any) error {
	return NewPermanentError(fmt.Sprintf(format, args...))
}

// IsPermanent reports whether err (or anything it wraps) is a verdict that will
// not change on a retry.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

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
	// HasAudio reports whether the source carries an audio stream. The MPEG-TS
	// ladder never needs it — it maps audio optionally into every rung and simply
	// gets nothing from a silent source — but CMAF must decide UP FRONT whether
	// to declare an audio adaptation set, because declaring one a silent source
	// then leaves empty produces an MPD with a Representation-less AdaptationSet.
	HasAudio bool
	// AudioKbps is the source audio stream's bitrate in whole kbps; 0 when the
	// container does not report one. It is a HINT, used only where re-encoding
	// above the source would be pure waste — the audio-only rendition, which has
	// no ladder rung to read a budget from.
	AudioKbps int
}

// HasVideo reports whether the probe found a usable video stream to ladder.
//
// A cover-art still is not one, and it is worth being explicit about why the
// check cannot be "did ffprobe report a video stream": it does report one for an
// attached picture, with real dimensions (mjpeg 600x600 on a typical podcast
// episode). parseFFProbe skips streams flagged attached_pic, so by the time
// dimensions land here they belong to something with a timeline.
func (m Metadata) HasVideo() bool { return m.Width > 0 && m.Height > 0 }

// AudioOnly reports whether the source is audio with no video to ladder: a
// podcast episode, a music track, or a video container someone stripped the
// video out of. It is the shape that used to dead-letter — the ladder planner
// returns nothing for it, and "nothing to plan" was read as "unprobeable".
//
// It is deliberately NOT "no video": a file with neither video nor audio is
// not media at all, and must keep failing.
func (m Metadata) AudioOnly() bool { return m.HasAudio && !m.HasVideo() }
