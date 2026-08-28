package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// pathUUID parses a UUID path parameter, answering 404 with the caller's
// entity-specific message when it is absent or malformed.
//
// The 404 (rather than the 400 a malformed id would otherwise deserve) is
// deliberate and must stay: a route that answered 400 for "not a UUID" and 404
// for "no such row" would let an unauthenticated caller distinguish a
// well-formed id that does not exist from one that does, which is the same
// existence leak the 404-instead-of-403 convention elsewhere exists to close.
// The few admin routes that DO return 400 are behind requireRole, where the
// caller is already trusted to know what exists.
func pathUUID(c echo.Context, param, notFoundMsg string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusNotFound, notFoundMsg)
	}
	return id, nil
}
