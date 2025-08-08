package ipfs

import (
	"github.com/ipfs/kubo/client/rpc"
	"github.com/yourname/gotube/internal/config"
)

// Return the raw *rpc.HttpApi so callers don't have to deal with a wrapper.
func NewClient(cfg *config.Config) (*rpc.HttpApi, error) {
	if cfg.IPFSPath != "" {
		// For a custom API endpoint (e.g., Docker service URL)
		return rpc.NewURLApiWithClient(cfg.IPFSPath, nil)
	}
	// Default to local IPFS API for development
	return rpc.NewLocalApi()
}
