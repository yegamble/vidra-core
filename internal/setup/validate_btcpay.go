package setup

import (
	"errors"
	"strings"
)

var (
	ErrInvalidBTCPayURL = errors.New("BTCPay Server URL must start with http:// or https://")
)

func ValidateBTCPayServerURL(url string) error {
	if url == "" {
		return ErrInvalidBTCPayURL
	}

	if containsShellMetachars(url) {
		return ErrShellMetachars
	}

	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return ErrInvalidBTCPayURL
	}

	return nil
}
