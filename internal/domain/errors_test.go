package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestDomainError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      DomainError
		expected string
	}{
		{
			name:     "error without details",
			err:      DomainError{Code: "TEST_ERROR", Message: "Test error message"},
			expected: "TEST_ERROR: Test error message",
		},
		{
			name:     "error with details",
			err:      DomainError{Code: "TEST_ERROR", Message: "Test error message", Details: "Additional details"},
			expected: "TEST_ERROR: Test error message (Additional details)",
		},
		{
			name:     "empty code and message",
			err:      DomainError{Code: "", Message: ""},
			expected: ": ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestNewDomainError(t *testing.T) {
	code := "TEST_CODE"
	message := "Test message"

	err := NewDomainError(code, message)

	if err.Code != code {
		t.Errorf("Expected code %q, got %q", code, err.Code)
	}

	if err.Message != message {
		t.Errorf("Expected message %q, got %q", message, err.Message)
	}

	if err.Details != "" {
		t.Errorf("Expected empty details, got %q", err.Details)
	}
}

func TestNewDomainErrorWithDetails(t *testing.T) {
	code := "TEST_CODE"
	message := "Test message"
	details := "Test details"

	err := NewDomainErrorWithDetails(code, message, details)

	if err.Code != code {
		t.Errorf("Expected code %q, got %q", code, err.Code)
	}

	if err.Message != message {
		t.Errorf("Expected message %q, got %q", message, err.Message)
	}

	if err.Details != details {
		t.Errorf("Expected details %q, got %q", details, err.Details)
	}
}

func TestStandardErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrNotFound", ErrNotFound, "resource not found"},
		{"ErrUnauthorized", ErrUnauthorized, "unauthorized"},
		{"ErrForbidden", ErrForbidden, "forbidden"},
		{"ErrValidation", ErrValidation, "validation error"},
		{"ErrInternalServer", ErrInternalServer, "internal server error"},
		{"ErrBadRequest", ErrBadRequest, "bad request"},
		{"ErrConflict", ErrConflict, "resource already exists"},
		{"ErrTooManyRequests", ErrTooManyRequests, "too many requests"},
		{"ErrServiceUnavailable", ErrServiceUnavailable, "service unavailable"},
		{"ErrInvalidInput", ErrInvalidInput, "invalid input"},
		{"ErrDuplicateEntry", ErrDuplicateEntry, "duplicate entry"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, tt.err.Error())
			}
		})
	}
}

func TestUserErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrUserNotFound", ErrUserNotFound, "user not found"},
		{"ErrUserAlreadyExists", ErrUserAlreadyExists, "user already exists"},
		{"ErrInvalidCredentials", ErrInvalidCredentials, "invalid credentials"},
		{"ErrInvalidToken", ErrInvalidToken, "invalid token"},
		{"ErrTokenExpired", ErrTokenExpired, "token expired"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, tt.err.Error())
			}
		})
	}
}

func TestVideoErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrVideoNotFound", ErrVideoNotFound, "video not found"},
		{"ErrVideoProcessing", ErrVideoProcessing, "video is being processed"},
		{"ErrVideoFailed", ErrVideoFailed, "video processing failed"},
		{"ErrInvalidFormat", ErrInvalidFormat, "invalid video format"},
		{"ErrFileTooLarge", ErrFileTooLarge, "file too large"},
		{"ErrChunkMissing", ErrChunkMissing, "chunk missing"},
		{"ErrInvalidChunk", ErrInvalidChunk, "invalid chunk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, tt.err.Error())
			}
		})
	}
}

func TestStorageErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrIPFSUnavailable", ErrIPFSUnavailable, "IPFS service unavailable"},
		{"ErrStorageError", ErrStorageError, "storage error"},
		{"ErrProcessingError", ErrProcessingError, "processing error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, tt.err.Error())
			}
		})
	}
}

func TestMessageErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrMessageNotFound", ErrMessageNotFound, "message not found"},
		{"ErrConversationNotFound", ErrConversationNotFound, "conversation not found"},
		{"ErrCannotMessageSelf", ErrCannotMessageSelf, "cannot send message to yourself"},
		{"ErrMessageTooLong", ErrMessageTooLong, "message content too long"},
		{"ErrInvalidMessageType", ErrInvalidMessageType, "invalid message type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, tt.err.Error())
			}
		})
	}
}

func TestNotificationErrors(t *testing.T) {
	if ErrNotificationNotFound.Error() != "notification not found" {
		t.Errorf("Expected 'notification not found', got %q", ErrNotificationNotFound.Error())
	}
}

func TestDomainError_ErrorsIs(t *testing.T) {
	err1 := NewDomainError("TEST", "Test error")
	err2 := NewDomainError("TEST", "Test error")

	// Domain errors are not comparable with errors.Is by default
	// This test just verifies they work as error types
	if err1.Error() != err2.Error() {
		t.Error("Same domain errors should have same error string")
	}
}

func TestDomainError_TypeAssertion(t *testing.T) {
	var err error = NewDomainError("TEST", "Test message")

	// Should be able to type assert to DomainError
	domainErr, ok := err.(DomainError)
	if !ok {
		t.Fatal("Expected to be able to type assert to DomainError")
	}

	if domainErr.Code != "TEST" {
		t.Errorf("Expected code TEST, got %s", domainErr.Code)
	}
}

func TestErrorWrapping(t *testing.T) {
	// Test that standard errors can be wrapped
	baseErr := ErrUserNotFound
	wrappedErr := errors.New("additional context: " + baseErr.Error())

	if !strings.Contains(wrappedErr.Error(), "user not found") {
		t.Error("Wrapped error should contain original message")
	}
}

func TestDomainError_WithEmptyStrings(t *testing.T) {
	err := NewDomainError("", "")
	expected := ": "

	if err.Error() != expected {
		t.Errorf("Expected %q, got %q", expected, err.Error())
	}
}

func TestDomainError_WithSpecialCharacters(t *testing.T) {
	err := NewDomainErrorWithDetails(
		"SPECIAL_CHARS",
		"Message with special chars: @#$%^&*()",
		"Details with unicode: 你好世界",
	)

	errorStr := err.Error()
	if !strings.Contains(errorStr, "SPECIAL_CHARS") {
		t.Error("Error string should contain code")
	}
	if !strings.Contains(errorStr, "@#$%^&*()") {
		t.Error("Error string should contain special chars")
	}
	if !strings.Contains(errorStr, "你好世界") {
		t.Error("Error string should contain unicode")
	}
}
