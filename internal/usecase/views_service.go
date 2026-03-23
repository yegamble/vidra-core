package usecase

import ucviews "athena/internal/usecase/views"

type ViewsService = ucviews.Service

var NewViewsService = ucviews.NewService
var ValidateTrackingRequest = ucviews.ValidateTrackingRequest
var GenerateFingerprint = ucviews.GenerateFingerprint
