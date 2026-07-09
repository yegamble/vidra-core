package peertubeimport

import "fmt"

// MediaMode selects how the importer handles source media objects.
type MediaMode string

const (
	// MediaModeCopy streams source objects into the destination storage layout.
	MediaModeCopy MediaMode = "copy"
	// MediaModeReference records the existing PeerTube object keys in Vidra's DB.
	// The Vidra server must be configured to use the same object store.
	MediaModeReference MediaMode = "reference"
	// MediaModeNone imports metadata only and writes no media rows.
	MediaModeNone MediaMode = "none"
)

func ParseMediaMode(raw string) (MediaMode, error) {
	switch MediaMode(raw) {
	case "", MediaModeCopy:
		return MediaModeCopy, nil
	case MediaModeReference:
		return MediaModeReference, nil
	case MediaModeNone:
		return MediaModeNone, nil
	default:
		return "", fmt.Errorf("peertubeimport: media mode %q must be copy, reference, or none", raw)
	}
}

func (m MediaMode) String() string {
	if m == "" {
		return string(MediaModeCopy)
	}
	return string(m)
}
