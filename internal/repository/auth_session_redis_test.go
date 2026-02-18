package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionKey_PrefixesWithSess(t *testing.T) {
	assert.Equal(t, "sess:abc-123", sessionKey("abc-123"))
}

func TestSessionKey_EmptyString(t *testing.T) {
	assert.Equal(t, "sess:", sessionKey(""))
}

func TestSessionKey_UUIDLike(t *testing.T) {
	id := "550e8400-e29b-41d4-a716-446655440000"
	assert.Equal(t, "sess:"+id, sessionKey(id))
}

func TestUserSessionsKey_PrefixesCorrectly(t *testing.T) {
	assert.Equal(t, "user:sessions:user-42", userSessionsKey("user-42"))
}

func TestUserSessionsKey_EmptyString(t *testing.T) {
	assert.Equal(t, "user:sessions:", userSessionsKey(""))
}

func TestUserSessionsKey_UUIDLike(t *testing.T) {
	id := "550e8400-e29b-41d4-a716-446655440000"
	assert.Equal(t, "user:sessions:"+id, userSessionsKey(id))
}

func TestSessionKey_DifferentIDsProduceDifferentKeys(t *testing.T) {
	k1 := sessionKey("session-1")
	k2 := sessionKey("session-2")
	assert.NotEqual(t, k1, k2)
}

func TestUserSessionsKey_DifferentUsersDifferentKeys(t *testing.T) {
	k1 := userSessionsKey("user-1")
	k2 := userSessionsKey("user-2")
	assert.NotEqual(t, k1, k2)
}

func TestNewRedisSessionRepository_ReturnsNonNil(t *testing.T) {
	repo := NewRedisSessionRepository(nil)
	assert.NotNil(t, repo)
}
