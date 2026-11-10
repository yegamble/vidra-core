package catalog

// CaptureDemand is advisory polling data, never source readiness or a hint ACK.
// Capture fixes exact dirty membership in its own authority-locked RR snapshot.
type CaptureDemand struct {
	Dirty             bool
	LegacyOutstanding bool
}
