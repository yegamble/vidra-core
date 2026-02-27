package auth

import (
	"net/http"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/usecase"
)

// PublicUser is the public-safe representation of a user.
// It deliberately omits sensitive fields: Email, BitcoinWallet, IsActive, TwoFAEnabled, EmailVerified.
type PublicUser struct {
	ID              string        `json:"id"`
	Username        string        `json:"username"`
	DisplayName     string        `json:"display_name"`
	Bio             string        `json:"bio"`
	Avatar          *PublicAvatar `json:"avatar,omitempty"`
	Role            string        `json:"role"`
	SubscriberCount int64         `json:"subscriber_count"`
	CreatedAt       time.Time     `json:"created_at"`
}

// PublicAvatar is the public representation of a user avatar.
type PublicAvatar struct {
	ID          string  `json:"id"`
	IPFSCID     *string `json:"ipfs_cid,omitempty"`
	WebPIPFSCID *string `json:"webp_ipfs_cid,omitempty"`
}

// toPublicUser converts a domain.User to a PublicUser, stripping sensitive fields.
func toPublicUser(u *domain.User) PublicUser {
	p := PublicUser{
		ID:              u.ID,
		Username:        u.Username,
		DisplayName:     u.DisplayName,
		Bio:             u.Bio,
		Role:            string(u.Role),
		SubscriberCount: u.SubscriberCount,
		CreatedAt:       u.CreatedAt,
	}
	if u.Avatar != nil {
		var ipfsCID, webpCID *string
		if u.Avatar.IPFSCID.Valid {
			s := u.Avatar.IPFSCID.String
			ipfsCID = &s
		}
		if u.Avatar.WebPIPFSCID.Valid {
			s := u.Avatar.WebPIPFSCID.String
			webpCID = &s
		}
		p.Avatar = &PublicAvatar{
			ID:          u.Avatar.ID,
			IPFSCID:     ipfsCID,
			WebPIPFSCID: webpCID,
		}
	}
	return p
}

// GetPublicUserHandler returns a public-safe user profile by ID.
// It deliberately excludes Email, BitcoinWallet, IsActive, TwoFAEnabled, and EmailVerified.
func GetPublicUserHandler(repo usecase.UserRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := shared.RequireUUIDParam(w, r, "id",
			"MISSING_USER_ID", "INVALID_USER_ID",
			"User ID is required", "Invalid user ID format")
		if !ok {
			return
		}

		user, err := repo.GetByID(r.Context(), userID)
		if err != nil {
			if err == domain.ErrUserNotFound {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "User not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load user"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, toPublicUser(user))
	}
}
