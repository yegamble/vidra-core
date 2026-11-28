package config

import "github.com/vidra/vidra-core/internal/processedupload"

// UploadLimit leaves existing installations alone. When the processor is
// configured only the Studio resumable route can accept the larger input.
func (c *Config) UploadLimit(configured int64, resumable bool) int64 {
	if c.UploadNormalizerSocket == "" {
		return configured
	}
	limit := processedupload.MaxScannedBytes
	if resumable {
		limit = processedupload.MaxInputBytes
	}
	if configured > 0 && configured < limit {
		return configured
	}
	return limit
}
