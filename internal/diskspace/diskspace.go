// Package diskspace reports free space on the filesystem holding a path.
//
// It exists so `vidra doctor` and the transcode worker measure the same thing
// the same way: a diagnostic that says the disk is fine while the worker refuses
// to start jobs — or the reverse — is worse than either alone.
package diskspace

import "syscall"

// Usage is what statfs says about one filesystem, as seen by the CALLING user.
type Usage struct {
	TotalBytes uint64
	FreeBytes  uint64
}

// FreeFraction is free space as a fraction of the total, 0 when the total is 0
// (an unreadable or zero-sized filesystem is reported as full, not as empty).
func (u Usage) FreeFraction() float64 {
	if u.TotalBytes == 0 {
		return 0
	}
	return float64(u.FreeBytes) / float64(u.TotalBytes)
}

// Measure reports the space on the filesystem holding path.
func Measure(path string) (Usage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Usage{}, err
	}
	// Bavail, not Bfree: the blocks a filesystem reserves for root are not space
	// an unprivileged process can write into, and counting them is how a "10%
	// free" check passes on a disk that is already refusing writes.
	bsize := uint64(st.Bsize) //nolint:gosec // Bsize is a positive block size on every supported platform.
	return Usage{
		TotalBytes: st.Blocks * bsize,
		FreeBytes:  uint64(st.Bavail) * bsize, //nolint:gosec // Bavail is non-negative.
	}, nil
}
