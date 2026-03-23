package compat

import (
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// PeerTubeNotImplemented returns a consistent 501 response for PeerTube route
// families Vidra Core has declared but does not implement yet.
func PeerTubeNotImplemented(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		shared.WriteError(
			w,
			http.StatusNotImplemented,
			domain.NewDomainError("NOT_IMPLEMENTED", feature+" is not implemented in Vidra Core"),
		)
	}
}
