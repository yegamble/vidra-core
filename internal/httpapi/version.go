package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/version"
)

// versionResponse is returned by GET /version.
type versionResponse struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Go        string `json:"go"`
}

// handleVersion reports the running build's version metadata. It performs no
// dependency checks and is safe to call unauthenticated; it exposes only build
// information, never secrets or configuration.
func (s *Server) handleVersion(c echo.Context) error {
	return c.JSON(http.StatusOK, versionResponse{
		Name:      "vidra",
		Version:   version.Version,
		Commit:    version.Commit,
		BuildDate: version.Date,
		Go:        version.GoVersion(),
	})
}
