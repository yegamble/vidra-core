package migration_etl

import "vidra-core/internal/domain"

// mapPeerTubeRole converts PeerTube numeric role to domain UserRole.
// PeerTube: 0=admin, 1=moderator, 2=user
func mapPeerTubeRole(role int) domain.UserRole {
	switch role {
	case 0:
		return domain.RoleAdmin
	case 1:
		return domain.RoleMod
	default:
		return domain.RoleUser
	}
}

// mapPeerTubePrivacy converts PeerTube numeric privacy to domain Privacy.
// PeerTube: 1=public, 2=unlisted, 3=private, 4=internal (treated as private)
func mapPeerTubePrivacy(privacy int) domain.Privacy {
	switch privacy {
	case 1:
		return domain.PrivacyPublic
	case 2:
		return domain.PrivacyUnlisted
	default:
		return domain.PrivacyPrivate
	}
}
