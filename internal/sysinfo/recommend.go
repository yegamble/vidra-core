package sysinfo

import "fmt"

type Recommendation struct {
	EnableIPFS    bool
	EnableClamAV  bool
	EnableWhisper bool
	Explanation   string
}

func Recommend(resources *Resources) *Recommendation {
	rec := &Recommendation{}

	if resources.RAMGB < 2.0 || resources.CPUCores < 2 {
		rec.EnableIPFS = false
		rec.EnableClamAV = false
		rec.EnableWhisper = false
		rec.Explanation = fmt.Sprintf(
			"Minimal tier (%.1f GB RAM, %d cores): Core services only. "+
				"IPFS disabled (requires 2GB+ RAM). "+
				"ClamAV disabled (requires 2GB+ RAM). "+
				"Whisper disabled (requires 8GB+ RAM).",
			resources.RAMGB, resources.CPUCores,
		)
	} else if resources.RAMGB >= 8.0 && resources.CPUCores >= 4 {
		rec.EnableIPFS = true
		rec.EnableClamAV = true
		rec.EnableWhisper = true
		rec.Explanation = fmt.Sprintf(
			"Full tier (%.1f GB RAM, %d cores): All services enabled.",
			resources.RAMGB, resources.CPUCores,
		)
	} else {
		rec.EnableIPFS = false
		rec.EnableClamAV = true
		rec.EnableWhisper = false
		rec.Explanation = fmt.Sprintf(
			"Standard tier (%.1f GB RAM, %d cores): Core + ClamAV enabled. "+
				"IPFS optional (can enable manually). "+
				"Whisper disabled (requires 8GB+ RAM).",
			resources.RAMGB, resources.CPUCores,
		)
	}

	return rec
}
