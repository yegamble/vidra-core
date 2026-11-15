package main

import (
	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/httpapi"
	"github.com/vidra/vidra-core/internal/unsubscribe"
)

// unsubscribeURLFunc builds the per-user RFC 8058 unsubscribe URL the composer
// puts in List-Unsubscribe, from the same signer and route path the HTTP server
// verifies with. Anything unbuildable (bad user id, no public origin, no secret)
// yields "" and the mail goes without the header.
func unsubscribeURLFunc(publicBaseURL, jwtSecret string) func(userID string) string {
	signer := unsubscribe.New([]byte(jwtSecret))
	return func(userID string) string {
		id, err := uuid.Parse(userID)
		if err != nil {
			return ""
		}
		return signer.URL(publicBaseURL, httpapi.UnsubscribeURLPath, id)
	}
}
