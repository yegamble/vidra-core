// Package processedupload defines the limits shared by admission and the
// credential-free media processor.
package processedupload

const (
	MaxInputBytes   int64 = 20 << 30
	MaxScannedBytes int64 = (2 << 30) - 3
)
