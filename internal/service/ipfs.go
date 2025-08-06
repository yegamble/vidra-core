package service

import (
	"context"
	"fmt"
	"io"
	"os"

	files "github.com/ipfs/boxo/files"
	ipath "github.com/ipfs/boxo/path"
	rpc "github.com/ipfs/kubo/client/rpc"
)

// IPFSService wraps the Kubo RPC client to provide higher level helpers for
// adding files to IPFS. It holds a reference to the underlying client
// pointing at the configured API URL. In production, ensure that the
// IPFS daemon is running and accessible.
type IPFSService struct {
	api *rpc.HttpApi
}

// NewIPFSService creates a new IPFSService given the API URL (e.g.
// "http://localhost:5001").
func NewIPFSService(apiURL string) *IPFSService {
	api, err := rpc.NewURL(apiURL)
	if err != nil {
		panic(fmt.Errorf("create IPFS client: %w", err))
	}
	return &IPFSService{api: api}
}

// AddFile uploads the specified file to IPFS. It returns the resulting
// content identifier (CID) on success. The caller is responsible for
// ensuring the file exists. Note: IPFS will pin the file by default
// when using the local client. If you run an external node or cluster,
// configure pinning accordingly.
func (s *IPFSService) AddFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file for IPFS: %w", err)
	}
	defer f.Close()
	p, err := s.api.Unixfs().Add(context.Background(), files.NewReaderFile(f))
	if err != nil {
		return "", fmt.Errorf("ipfs add: %w", err)
	}
	return p.Cid().String(), nil
}

// Cat retrieves the contents of an IPFS object by its CID. This can be
// used to verify that uploaded files are retrievable. It returns the data
// as a byte slice.
func (s *IPFSService) Cat(cid string) ([]byte, error) {
	node, err := s.api.Unixfs().Get(context.Background(), ipath.New("/ipfs/"+cid))
	if err != nil {
		return nil, fmt.Errorf("ipfs cat: %w", err)
	}
	file, ok := node.(files.File)
	if !ok {
		return nil, fmt.Errorf("unexpected node type %T", node)
	}
	return io.ReadAll(file)
}
