package ipfs

import (
    "github.com/ipfs/kubo/client/rpc"
    "github.com/yegamble/athena/internal/config"
)

// NewClient returns a Kubo RPC client.  When IPFSPath is non-empty the client
// connects to the given URL (e.g. http://kubo:5001).  Otherwise it attempts
// to connect to a local Kubo daemon via the default socket.
func NewClient(cfg *config.Config) (*rpc.HttpApi, error) {
    if cfg.IPFSPath != "" {
        // For a custom API endpoint
        return rpc.NewURLApiWithClient(cfg.IPFSPath, nil)
    }
    return rpc.NewLocalApi()
}