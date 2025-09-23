package usecase

import (
	ucchannel "athena/internal/usecase/channel"
)

// Backwards-compatible aliases while we migrate to feature slice packages
type ChannelService = ucchannel.Service

var NewChannelService = ucchannel.NewService
