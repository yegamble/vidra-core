package video

import (
	"errors"

	"vidra-core/internal/domain"
)

func isVideoNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrVideoNotFound) {
		return true
	}

	var domainErr domain.DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code == "VIDEO_NOT_FOUND" || domainErr.Code == "NOT_FOUND"
	}

	var domainErrPtr *domain.DomainError
	if errors.As(err, &domainErrPtr) && domainErrPtr != nil {
		return domainErrPtr.Code == "VIDEO_NOT_FOUND" || domainErrPtr.Code == "NOT_FOUND"
	}

	return false
}

func videoErrorOrDefault(err error, fallbackCode, fallbackMessage string) domain.DomainError {
	var domainErr domain.DomainError
	if errors.As(err, &domainErr) {
		return domainErr
	}

	var domainErrPtr *domain.DomainError
	if errors.As(err, &domainErrPtr) && domainErrPtr != nil {
		return *domainErrPtr
	}

	return domain.NewDomainError(fallbackCode, fallbackMessage)
}
