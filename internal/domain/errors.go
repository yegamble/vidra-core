package domain

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound           = errors.New("resource not found")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrValidation         = errors.New("validation error")
	ErrInternalServer     = errors.New("internal server error")
	ErrBadRequest         = errors.New("bad request")
	ErrConflict           = errors.New("resource already exists")
	ErrTooManyRequests    = errors.New("too many requests")
	ErrServiceUnavailable = errors.New("service unavailable")
)

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrUserAlreadyExists  = errors.New("user already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
	ErrTokenExpired       = errors.New("token expired")
)

var (
	ErrVideoNotFound   = errors.New("video not found")
	ErrVideoProcessing = errors.New("video is being processed")
	ErrVideoFailed     = errors.New("video processing failed")
	ErrInvalidFormat   = errors.New("invalid video format")
	ErrFileTooLarge    = errors.New("file too large")
	ErrChunkMissing    = errors.New("chunk missing")
	ErrInvalidChunk    = errors.New("invalid chunk")
)

var (
	ErrIPFSUnavailable = errors.New("IPFS service unavailable")
	ErrStorageError    = errors.New("storage error")
	ErrProcessingError = errors.New("processing error")
)

var (
	ErrMessageNotFound      = errors.New("message not found")
	ErrConversationNotFound = errors.New("conversation not found")
	ErrCannotMessageSelf    = errors.New("cannot send message to yourself")
	ErrMessageTooLong       = errors.New("message content too long")
	ErrInvalidMessageType   = errors.New("invalid message type")
)

var (
	ErrNotificationNotFound = errors.New("notification not found")
)

type DomainError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

func (e DomainError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewDomainError(code, message string) DomainError {
	return DomainError{Code: code, Message: message}
}

func NewDomainErrorWithDetails(code, message, details string) DomainError {
	return DomainError{Code: code, Message: message, Details: details}
}
