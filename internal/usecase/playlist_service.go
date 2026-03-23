package usecase

import ucplaylist "athena/internal/usecase/playlist"

// Backwards-compatible aliases while we migrate to feature slice packages
type PlaylistService = ucplaylist.Service

var NewPlaylistService = ucplaylist.NewService
