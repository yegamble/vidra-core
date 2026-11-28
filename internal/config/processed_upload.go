package config

import "github.com/vidra/vidra-core/internal/processedupload"

// UploadLimit keeps raw uploads within the whole-file scanner's limit even
// when an operator removes the broker while a larger database cap is active.
func (c *Config) UploadLimit(configured int64, resumable bool) int64 {
	if c.UploadNormalizerSocket == "" && (c.ClamAVAddr == "" || c.MalwareScanMode != "fail-closed") {
		return configured
	}
	limit := processedupload.MaxScannedBytes
	if resumable && c.UploadNormalizerSocket != "" {
		limit = processedupload.MaxInputBytes
	}
	if configured > 0 && configured < limit {
		return configured
	}
	return limit
}
