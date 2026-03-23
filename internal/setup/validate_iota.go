package setup

import (
	"errors"
	"strings"
)

var (
	ErrInvalidIOTAURL     = errors.New("IOTA node URL must start with http:// or https://")
	ErrInvalidIOTANetwork = errors.New("IOTA network must be 'mainnet' or 'testnet'")
)

func ValidateIOTANodeURL(url string) error {
	if url == "" {
		return ErrInvalidIOTAURL
	}

	if containsShellMetachars(url) {
		return ErrShellMetachars
	}

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return ErrInvalidIOTAURL
	}

	return nil
}

func ValidateIOTANetwork(network string) error {
	if network != "mainnet" && network != "testnet" {
		return ErrInvalidIOTANetwork
	}
	return nil
}
