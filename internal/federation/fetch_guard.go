package federation

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/vidra/vidra-core/internal/urlsafety"
)

// errInsecureFetchScheme: a federation document URL that is not https, in a
// deployment where private fetches are not allowed (production).
var errInsecureFetchScheme = errors.New("federation: federation fetches require https")

// fetchGuard is the SSRF guard every federation fetch is built from. Relaxing
// private fetches (labs, backed e2e) still keeps the cloud instance-metadata
// ranges blocked: no lab needs the host's metadata endpoint, and a relaxed
// guard that reached it would hand a remote document's author the host's
// credentials.
func (s *Service) fetchGuard() urlsafety.Guard {
	return urlsafety.Guard{AllowPrivate: s.allowPrivateFetch, KeepMetadataBlocked: true}
}

// validateFetchURL validates a federation document URL (keyId/actor, object,
// outbox page, WebFinger) through fetchGuard and, in production, requires
// https — the same rule the remote-interaction template already applies:
// plain http only where private fetches are allowed.
func (s *Service) validateFetchURL(raw string) (*url.URL, error) {
	u, err := s.fetchGuard().ValidateURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && !s.allowPrivateFetch {
		return nil, fmt.Errorf("%w: %s", errInsecureFetchScheme, u.Scheme)
	}
	return u, nil
}
