package usecase

import ucviews "vidra-core/internal/usecase/views"

type ViewsService = ucviews.Service

var NewViewsService = ucviews.NewService
var ValidateTrackingRequest = ucviews.ValidateTrackingRequest
var GenerateFingerprint = ucviews.GenerateFingerprint
