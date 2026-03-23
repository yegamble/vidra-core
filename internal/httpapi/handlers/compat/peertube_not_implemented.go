package compat

import (
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
)

// PeerTubeNotImplemented returns a consistent 501 response for PeerTube route
// families Athena has declared but does not implement yet.
func PeerTubeNotImplemented(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		shared.WriteError(
			w,
			http.StatusNotImplemented,
			domain.NewDomainError("NOT_IMPLEMENTED", feature+" is not implemented in Athena"),
		)
	}
}
