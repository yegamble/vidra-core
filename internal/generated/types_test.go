package generated

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUserSerialization(t *testing.T) {
	displayName := "John Doe"
	user := User{
		ID:          "user123",
		Username:    "johndoe",
		Email:       "john@example.com",
		DisplayName: &displayName,
		Role:        UserRoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	data, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("Failed to marshal user: %v", err)
	}

	var unmarshaled User
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal user: %v", err)
	}

	if unmarshaled.ID != user.ID {
		t.Errorf("Expected ID %s, got %s", user.ID, unmarshaled.ID)
	}

	if unmarshaled.Username != user.Username {
		t.Errorf("Expected username %s, got %s", user.Username, unmarshaled.Username)
	}

	if unmarshaled.Role != user.Role {
		t.Errorf("Expected role %s, got %s", user.Role, unmarshaled.Role)
	}
}

func TestLoginRequest(t *testing.T) {
	req := LoginRequest{
		Email:    "test@example.com",
		Password: "password123",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal login request: %v", err)
	}

	var unmarshaled LoginRequest
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal login request: %v", err)
	}

	if unmarshaled.Email != req.Email {
		t.Errorf("Expected email %s, got %s", req.Email, unmarshaled.Email)
	}
}

func TestAuthResponse(t *testing.T) {
	user := User{
		ID:       "user123",
		Username: "testuser",
		Email:    "test@example.com",
		Role:     UserRoleUser,
		IsActive: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	response := AuthResponse{
		User:         user,
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresIn:    900,
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Failed to marshal auth response: %v", err)
	}

	var unmarshaled AuthResponse
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal auth response: %v", err)
	}

	if unmarshaled.User.ID != response.User.ID {
		t.Errorf("Expected user ID %s, got %s", response.User.ID, unmarshaled.User.ID)
	}

	if unmarshaled.AccessToken != response.AccessToken {
		t.Errorf("Expected access token %s, got %s", response.AccessToken, unmarshaled.AccessToken)
	}
}