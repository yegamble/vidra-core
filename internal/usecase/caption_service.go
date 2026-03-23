package usecase

import uccaption "athena/internal/usecase/caption"

// Backwards-compatible aliases while we migrate to feature slice packages
type CaptionService = uccaption.Service

var NewCaptionService = uccaption.NewService
