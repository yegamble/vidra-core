package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/version"
)

// NodeInfo (https://nodeinfo.diaspora.software/) is the fediverse's instance
// self-description: software name/version, supported protocols, whether signup
// is open, and coarse usage counts. Remote servers and crawlers read it to learn
// what this instance runs. These endpoints are mounted only when federation is
// enabled (see routes()); they are a federation contract, not the REST API, so
// they are documented in .ralph/specs/federation.md rather than api/openapi.yaml.

const nodeInfo21Rel = "http://nodeinfo.diaspora.software/ns/schema/2.1"

// nodeInfo21ContentType is the profile content type NodeInfo 2.1 documents are
// conventionally served with.
const nodeInfo21ContentType = `application/json; profile="http://nodeinfo.diaspora.software/ns/schema/2.1#"`

type nodeInfoLink struct {
	Rel  string `json:"rel"`
	Href string `json:"href"`
}

type nodeInfoDiscovery struct {
	Links []nodeInfoLink `json:"links"`
}

// handleNodeInfoDiscovery serves /.well-known/nodeinfo — the pointer to the
// versioned document.
func (s *Server) handleNodeInfoDiscovery(c echo.Context) error {
	return c.JSON(http.StatusOK, nodeInfoDiscovery{Links: []nodeInfoLink{{
		Rel:  nodeInfo21Rel,
		Href: s.cfg.PublicBaseURL + "/nodeinfo/2.1",
	}}})
}

type nodeInfoSoftware struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type nodeInfoServices struct {
	Inbound  []string `json:"inbound"`
	Outbound []string `json:"outbound"`
}

type nodeInfoUsers struct {
	Total int64 `json:"total"`
}

type nodeInfoUsage struct {
	Users         nodeInfoUsers `json:"users"`
	LocalPosts    int64         `json:"localPosts"`
	LocalComments int64         `json:"localComments"`
}

type nodeInfo21Document struct {
	Version           string           `json:"version"`
	Software          nodeInfoSoftware `json:"software"`
	Protocols         []string         `json:"protocols"`
	Services          nodeInfoServices `json:"services"`
	OpenRegistrations bool             `json:"openRegistrations"`
	Usage             nodeInfoUsage    `json:"usage"`
	Metadata          map[string]any   `json:"metadata"`
}

// handleNodeInfo21 serves /nodeinfo/2.1 — the NodeInfo 2.1 document with live
// usage counts.
func (s *Server) handleNodeInfo21(c echo.Context) error {
	usage, err := s.fedsvc.NodeInfoUsage(c.Request().Context())
	if err != nil {
		return err
	}
	doc := nodeInfo21Document{
		Version:           "2.1",
		Software:          nodeInfoSoftware{Name: "vidra", Version: version.Version},
		Protocols:         []string{"activitypub"},
		Services:          nodeInfoServices{Inbound: []string{}, Outbound: []string{}},
		OpenRegistrations: s.cfg.RegistrationEnabled,
		Usage: nodeInfoUsage{
			Users:         nodeInfoUsers{Total: usage.Users},
			LocalPosts:    usage.LocalPosts,
			LocalComments: usage.LocalComments,
		},
		Metadata: map[string]any{"nodeName": s.cfg.InstanceName},
	}
	// Set the profile content type before c.JSON (Echo only sets a default when
	// none is present), so consumers see the NodeInfo 2.1 profile.
	c.Response().Header().Set(echo.HeaderContentType, nodeInfo21ContentType)
	return c.JSON(http.StatusOK, doc)
}
