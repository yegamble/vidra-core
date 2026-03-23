package usecase

import ucsocial "vidra-core/internal/usecase/social"

// Backwards-compatible aliases while we migrate to feature slice packages
type SocialService = ucsocial.Service

var NewSocialService = ucsocial.NewService
