package video

import (
	"github.com/google/uuid"

	"vidra-core/internal/livestream"
)

type HLSTranscoderInterface interface {
	IsTranscoding(streamID uuid.UUID) bool

	GetSession(streamID uuid.UUID) (*livestream.TranscodeSession, bool)
}
