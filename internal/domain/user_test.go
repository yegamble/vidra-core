package domain

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestUser_MarshalJSON(t *testing.T) {
	t.Run("user without avatar", func(t *testing.T) {
		user := User{
			ID:          "user-123",
			Username:    "testuser",
			Email:       "test@example.com",
			DisplayName: "Test User",
			Bio:         "Test bio",
			Role:        RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		data, err := json.Marshal(user)
		if err != nil {
			t.Fatalf("Failed to marshal user: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("Failed to unmarshal result: %v", err)
		}

		if result["id"] != user.ID {
			t.Errorf("Expected id %s, got %v", user.ID, result["id"])
		}

		if result["username"] != user.Username {
			t.Errorf("Expected username %s, got %v", user.Username, result["username"])
		}
	})

	t.Run("user with avatar", func(t *testing.T) {
		ipfsCID := "QmTest123"
		webpCID := "QmTestWebP456"

		user := User{
			ID:          "user-123",
			Username:    "testuser",
			Email:       "test@example.com",
			DisplayName: "Test User",
			Avatar: &Avatar{
				ID:          "avatar-123",
				IPFSCID:     sql.NullString{String: ipfsCID, Valid: true},
				WebPIPFSCID: sql.NullString{String: webpCID, Valid: true},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		data, err := json.Marshal(user)
		if err != nil {
			t.Fatalf("Failed to marshal user: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("Failed to unmarshal result: %v", err)
		}

		avatar, ok := result["avatar"].(map[string]interface{})
		if !ok {
			t.Fatal("Expected avatar to be present")
		}

		if avatar["id"] != "avatar-123" {
			t.Errorf("Expected avatar id 'avatar-123', got %v", avatar["id"])
		}

		if avatar["ipfs_cid"] != ipfsCID {
			t.Errorf("Expected ipfs_cid %s, got %v", ipfsCID, avatar["ipfs_cid"])
		}

		if avatar["webp_ipfs_cid"] != webpCID {
			t.Errorf("Expected webp_ipfs_cid %s, got %v", webpCID, avatar["webp_ipfs_cid"])
		}
	})

	t.Run("user with avatar but null CIDs", func(t *testing.T) {
		user := User{
			ID:       "user-123",
			Username: "testuser",
			Avatar: &Avatar{
				ID:          "avatar-123",
				IPFSCID:     sql.NullString{Valid: false},
				WebPIPFSCID: sql.NullString{Valid: false},
			},
		}

		data, err := json.Marshal(user)
		if err != nil {
			t.Fatalf("Failed to marshal user: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("Failed to unmarshal result: %v", err)
		}

		avatar, ok := result["avatar"].(map[string]interface{})
		if !ok {
			t.Fatal("Expected avatar to be present")
		}

		// Null values should be represented as nil/null in JSON
		if avatar["ipfs_cid"] != nil {
			t.Errorf("Expected null ipfs_cid, got %v", avatar["ipfs_cid"])
		}

		if avatar["webp_ipfs_cid"] != nil {
			t.Errorf("Expected null webp_ipfs_cid, got %v", avatar["webp_ipfs_cid"])
		}
	})
}

func TestNullStringToPtr(t *testing.T) {
	t.Run("valid null string", func(t *testing.T) {
		ns := sql.NullString{String: "test", Valid: true}
		ptr := nullStringToPtr(ns)

		if ptr == nil {
			t.Fatal("Expected non-nil pointer")
		}

		if *ptr != "test" {
			t.Errorf("Expected 'test', got %s", *ptr)
		}
	})

	t.Run("invalid null string", func(t *testing.T) {
		ns := sql.NullString{Valid: false}
		ptr := nullStringToPtr(ns)

		if ptr != nil {
			t.Error("Expected nil pointer for invalid NullString")
		}
	})

	t.Run("empty but valid null string", func(t *testing.T) {
		ns := sql.NullString{String: "", Valid: true}
		ptr := nullStringToPtr(ns)

		if ptr == nil {
			t.Fatal("Expected non-nil pointer")
		}

		if *ptr != "" {
			t.Errorf("Expected empty string, got %s", *ptr)
		}
	})
}

func TestUserRole_Constants(t *testing.T) {
	tests := []struct {
		name     string
		role     UserRole
		expected string
	}{
		{"RoleUser", RoleUser, "user"},
		{"RoleAdmin", RoleAdmin, "admin"},
		{"RoleMod", RoleMod, "moderator"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.role) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, string(tt.role))
			}
		})
	}
}

func TestLoginRequest_Struct(t *testing.T) {
	req := LoginRequest{
		Email:    "test@example.com",
		Password: "password123",
	}

	if req.Email != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got %s", req.Email)
	}

	if req.Password != "password123" {
		t.Errorf("Expected password 'password123', got %s", req.Password)
	}
}

func TestRegisterRequest_Struct(t *testing.T) {
	req := RegisterRequest{
		Username:    "testuser",
		Email:       "test@example.com",
		Password:    "password123",
		DisplayName: "Test User",
	}

	if req.Username != "testuser" {
		t.Errorf("Expected username 'testuser', got %s", req.Username)
	}

	if req.Email != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got %s", req.Email)
	}

	if req.Password != "password123" {
		t.Errorf("Expected password 'password123', got %s", req.Password)
	}

	if req.DisplayName != "Test User" {
		t.Errorf("Expected display name 'Test User', got %s", req.DisplayName)
	}
}

func TestAuthResponse_Struct(t *testing.T) {
	user := User{
		ID:       "user-123",
		Username: "testuser",
	}

	resp := AuthResponse{
		User:         user,
		AccessToken:  "access-token-123",
		RefreshToken: "refresh-token-456",
		ExpiresIn:    3600,
	}

	if resp.User.ID != "user-123" {
		t.Errorf("Expected user ID 'user-123', got %s", resp.User.ID)
	}

	if resp.AccessToken != "access-token-123" {
		t.Errorf("Expected access token 'access-token-123', got %s", resp.AccessToken)
	}

	if resp.RefreshToken != "refresh-token-456" {
		t.Errorf("Expected refresh token 'refresh-token-456', got %s", resp.RefreshToken)
	}

	if resp.ExpiresIn != 3600 {
		t.Errorf("Expected expires in 3600, got %d", resp.ExpiresIn)
	}
}

func TestUser_JSONRoundTrip(t *testing.T) {
	original := User{
		ID:              "user-123",
		Username:        "testuser",
		Email:           "test@example.com",
		DisplayName:     "Test User",
		Bio:             "Test bio",
		BitcoinWallet:   "bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh",
		Role:            RoleUser,
		IsActive:        true,
		EmailVerified:   true,
		CreatedAt:       time.Now().UTC().Truncate(time.Second),
		UpdatedAt:       time.Now().UTC().Truncate(time.Second),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded User
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID mismatch: expected %s, got %s", original.ID, decoded.ID)
	}

	if decoded.Username != original.Username {
		t.Errorf("Username mismatch: expected %s, got %s", original.Username, decoded.Username)
	}

	if decoded.Email != original.Email {
		t.Errorf("Email mismatch: expected %s, got %s", original.Email, decoded.Email)
	}

}

func TestAvatar_Struct(t *testing.T) {
	avatar := Avatar{
		ID:          "avatar-123",
		IPFSCID:     sql.NullString{String: "QmTest", Valid: true},
		WebPIPFSCID: sql.NullString{String: "QmWebP", Valid: true},
	}

	if avatar.ID != "avatar-123" {
		t.Errorf("Expected ID 'avatar-123', got %s", avatar.ID)
	}

	if !avatar.IPFSCID.Valid || avatar.IPFSCID.String != "QmTest" {
		t.Error("Expected valid IPFSCID with value 'QmTest'")
	}

	if !avatar.WebPIPFSCID.Valid || avatar.WebPIPFSCID.String != "QmWebP" {
		t.Error("Expected valid WebPIPFSCID with value 'QmWebP'")
	}
}

func TestUser_EmailVerifiedTimestamp(t *testing.T) {
	verifiedAt := time.Now().UTC()
	user := User{
		ID:              "user-123",
		EmailVerified:   true,
		EmailVerifiedAt: sql.NullTime{Time: verifiedAt, Valid: true},
	}

	if !user.EmailVerified {
		t.Error("Expected EmailVerified to be true")
	}

	if !user.EmailVerifiedAt.Valid {
		t.Fatal("Expected EmailVerifiedAt to be valid")
	}

	if !user.EmailVerifiedAt.Time.Equal(verifiedAt) {
		t.Errorf("Expected EmailVerifiedAt to be %v, got %v", verifiedAt, user.EmailVerifiedAt.Time)
	}
}
