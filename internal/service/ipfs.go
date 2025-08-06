package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

type IPFSService struct {
	files map[string]string
}

func NewIPFSService(apiURL string) *IPFSService {
	return &IPFSService{files: make(map[string]string)}
}

func (s *IPFSService) AddFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file for IPFS: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	cid := hex.EncodeToString(h.Sum(nil))
	s.files[cid] = filePath
	return cid, nil
}

func (s *IPFSService) Cat(cid string) ([]byte, error) {
	path, ok := s.files[cid]
	if !ok {
		return nil, fmt.Errorf("cid not found: %s", cid)
	}
	return os.ReadFile(path)
}
