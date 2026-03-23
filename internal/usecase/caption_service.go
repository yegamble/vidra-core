package usecase

import uccaption "vidra-core/internal/usecase/caption"

// Backwards-compatible aliases while we migrate to feature slice packages
type CaptionService = uccaption.Service

var NewCaptionService = uccaption.NewService
