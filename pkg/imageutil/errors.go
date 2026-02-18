package imageutil

import "errors"

// ErrWebPUnavailable is returned when the WebP encoder is not available (e.g. build tag missing).
// It might also be used if the encoder fails in a specific way, though usually other errors are returned.
var ErrWebPUnavailable = errors.New("webp encoder unavailable")
