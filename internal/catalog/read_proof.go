package catalog

// ReadProof is one comparable source SQL observation, not receiver authority or
// request readiness. Callers retain the actual viewer and compare the complete
// value again after receiver HTTP and canonical hydration.
type ReadProof struct {
	Full          FullProof
	HideSensitive bool
}
