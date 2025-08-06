package service

import (
    "fmt"
    "os"

    shell "github.com/ipfs/go-ipfs-api"
)

// IPFSService wraps the go-ipfs-api to provide higher level helpers for
// adding files to IPFS. It holds a reference to the underlying Shell
// pointing at the configured API URL. In production, ensure that the
// IPFS daemon is running and accessible.
type IPFSService struct {
    sh *shell.Shell
}

// NewIPFSService creates a new IPFSService given the API URL (e.g.
// "http://localhost:5001").
func NewIPFSService(apiURL string) *IPFSService {
    return &IPFSService{sh: shell.NewShell(apiURL)}
}

// AddFile uploads the specified file to IPFS. It returns the resulting
// content identifier (CID) on success. The caller is responsible for
// ensuring the file exists. Note: IPFS will pin the file by default
// when using the local shell. If you run an external node or cluster,
// configure pinning accordingly.
func (s *IPFSService) AddFile(filePath string) (string, error) {
    f, err := os.Open(filePath)
    if err != nil {
        return "", fmt.Errorf("open file for IPFS: %w", err)
    }
    defer f.Close()
    cid, err := s.sh.Add(f)
    if err != nil {
        return "", fmt.Errorf("ipfs add: %w", err)
    }
    return cid, nil
}

// Cat retrieves the contents of an IPFS object by its CID. This can be
// used to verify that uploaded files are retrievable. It returns the data
// as a byte slice.
func (s *IPFSService) Cat(cid string) ([]byte, error) {
    return s.sh.Cat(cid)
}