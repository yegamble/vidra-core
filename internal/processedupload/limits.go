// Package processedupload defines the limits shared by admission and the
// credential-free media processor.
package processedupload

import (
	"errors"

	"github.com/google/uuid"
)

const (
	MaxInputBytes   int64 = 20 << 30
	MaxScannedBytes int64 = (2 << 30) - 3
)

// SourceKey marks a reconstructed, scanned MP4. A creator-supplied filename
// cannot select this key: ordinary originals use the UUID plus one extension.
func SourceKey(id uuid.UUID) string { return "web-videos/" + id.String() + ".vidra-processed.mp4" }

// ErrOutputScannerRequired prevents an unconfigured worker from publishing new derivatives.
var ErrOutputScannerRequired = errors.New("reconstructed media requires generated-output scanning")
