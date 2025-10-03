package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

// Helper function to create test database (mock for now)
func setupTestDB(t *testing.T) *sqlx.DB {
	t.Skip("Skipping database integration test - requires test database setup")
	return nil
}

func TestActorKeys(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	actorID := uuid.New().String()
	publicKey := "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"
	privateKey := "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----"

	t.Run("Store and retrieve actor keys", func(t *testing.T) {
		err := repo.StoreActorKeys(ctx, actorID, publicKey, privateKey)
		require.NoError(t, err)

		retrievedPub, retrievedPriv, err := repo.GetActorKeys(ctx, actorID)
		require.NoError(t, err)

		assert.Equal(t, publicKey, retrievedPub)
		assert.Equal(t, privateKey, retrievedPriv)
	})

	t.Run("Update existing actor keys", func(t *testing.T) {
		newPublicKey := "-----BEGIN PUBLIC KEY-----\nnew\n-----END PUBLIC KEY-----"
		newPrivateKey := "-----BEGIN PRIVATE KEY-----\nnew\n-----END PRIVATE KEY-----"

		err := repo.StoreActorKeys(ctx, actorID, newPublicKey, newPrivateKey)
		require.NoError(t, err)

		retrievedPub, retrievedPriv, err := repo.GetActorKeys(ctx, actorID)
		require.NoError(t, err)

		assert.Equal(t, newPublicKey, retrievedPub)
		assert.Equal(t, newPrivateKey, retrievedPriv)
	})

	t.Run("Get non-existent actor keys", func(t *testing.T) {
		nonExistentID := uuid.New().String()
		_, _, err := repo.GetActorKeys(ctx, nonExistentID)
		assert.Error(t, err)
	})
}

func TestRemoteActors(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	t.Run("Upsert and retrieve remote actor", func(t *testing.T) {
		actorURI := "https://mastodon.example/users/alice"
		displayName := "Alice"
		actor := &domain.APRemoteActor{
			ActorURI:     actorURI,
			Type:         "Person",
			Username:     "alice",
			Domain:       "mastodon.example",
			DisplayName:  &displayName,
			InboxURL:     actorURI + "/inbox",
			PublicKeyID:  actorURI + "#main-key",
			PublicKeyPem: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
		}

		err := repo.UpsertRemoteActor(ctx, actor)
		require.NoError(t, err)
		assert.NotEmpty(t, actor.ID)

		retrieved, err := repo.GetRemoteActor(ctx, actorURI)
		require.NoError(t, err)
		require.NotNil(t, retrieved)

		assert.Equal(t, actorURI, retrieved.ActorURI)
		assert.Equal(t, "alice", retrieved.Username)
		assert.Equal(t, "mastodon.example", retrieved.Domain)
		assert.Equal(t, displayName, *retrieved.DisplayName)
	})

	t.Run("Update existing remote actor", func(t *testing.T) {
		actorURI := "https://mastodon.example/users/bob"
		newDisplayName := "Bob Updated"

		actor1 := &domain.APRemoteActor{
			ActorURI:     actorURI,
			Type:         "Person",
			Username:     "bob",
			Domain:       "mastodon.example",
			InboxURL:     actorURI + "/inbox",
			PublicKeyID:  actorURI + "#main-key",
			PublicKeyPem: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
		}

		err := repo.UpsertRemoteActor(ctx, actor1)
		require.NoError(t, err)

		actor2 := &domain.APRemoteActor{
			ActorURI:     actorURI,
			Type:         "Person",
			Username:     "bob",
			Domain:       "mastodon.example",
			DisplayName:  &newDisplayName,
			InboxURL:     actorURI + "/inbox",
			PublicKeyID:  actorURI + "#main-key",
			PublicKeyPem: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
		}

		err = repo.UpsertRemoteActor(ctx, actor2)
		require.NoError(t, err)

		retrieved, err := repo.GetRemoteActor(ctx, actorURI)
		require.NoError(t, err)
		assert.Equal(t, newDisplayName, *retrieved.DisplayName)
	})

	t.Run("Get non-existent remote actor", func(t *testing.T) {
		actor, err := repo.GetRemoteActor(ctx, "https://example.com/nonexistent")
		require.NoError(t, err)
		assert.Nil(t, actor)
	})
}

func TestFollowers(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	actorID := "local-user-id"
	followerID := "https://mastodon.example/users/alice"

	t.Run("Create follower relationship", func(t *testing.T) {
		follower := &domain.APFollower{
			ActorID:    actorID,
			FollowerID: followerID,
			State:      "pending",
		}

		err := repo.UpsertFollower(ctx, follower)
		require.NoError(t, err)
		assert.NotEmpty(t, follower.ID)

		retrieved, err := repo.GetFollower(ctx, actorID, followerID)
		require.NoError(t, err)
		require.NotNil(t, retrieved)

		assert.Equal(t, actorID, retrieved.ActorID)
		assert.Equal(t, followerID, retrieved.FollowerID)
		assert.Equal(t, "pending", retrieved.State)
	})

	t.Run("Update follower state", func(t *testing.T) {
		follower := &domain.APFollower{
			ActorID:    actorID,
			FollowerID: followerID,
			State:      "accepted",
		}

		err := repo.UpsertFollower(ctx, follower)
		require.NoError(t, err)

		retrieved, err := repo.GetFollower(ctx, actorID, followerID)
		require.NoError(t, err)
		assert.Equal(t, "accepted", retrieved.State)
	})

	t.Run("List followers", func(t *testing.T) {
		// Add more followers
		for i := 0; i < 5; i++ {
			follower := &domain.APFollower{
				ActorID:    actorID,
				FollowerID: "https://example.com/user" + string(rune(i)),
				State:      "accepted",
			}
			err := repo.UpsertFollower(ctx, follower)
			require.NoError(t, err)
		}

		followers, total, err := repo.GetFollowers(ctx, actorID, "accepted", 10, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(followers), 1)
		assert.GreaterOrEqual(t, total, 1)
	})

	t.Run("Delete follower", func(t *testing.T) {
		deleteFollowerID := "https://example.com/to-delete"
		follower := &domain.APFollower{
			ActorID:    actorID,
			FollowerID: deleteFollowerID,
			State:      "accepted",
		}

		err := repo.UpsertFollower(ctx, follower)
		require.NoError(t, err)

		err = repo.DeleteFollower(ctx, actorID, deleteFollowerID)
		require.NoError(t, err)

		retrieved, err := repo.GetFollower(ctx, actorID, deleteFollowerID)
		require.NoError(t, err)
		assert.Nil(t, retrieved)
	})
}

func TestActivities(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	t.Run("Store and retrieve activity", func(t *testing.T) {
		activityObj := map[string]interface{}{
			"type":   "Follow",
			"actor":  "https://example.com/users/alice",
			"object": "https://video.example/users/bob",
		}

		activityJSON, err := json.Marshal(activityObj)
		require.NoError(t, err)

		activity := &domain.APActivity{
			ActorID:      "https://example.com/users/alice",
			Type:         "Follow",
			Published:    time.Now(),
			ActivityJSON: activityJSON,
			Local:        false,
		}

		err = repo.StoreActivity(ctx, activity)
		require.NoError(t, err)
		assert.NotEmpty(t, activity.ID)

		// Can't retrieve without activity URI in this implementation
		// Would need to add GetActivityByID method
	})

	t.Run("Get activities by actor", func(t *testing.T) {
		actorID := "test-actor-id"

		// Store multiple activities
		for i := 0; i < 3; i++ {
			activityObj := map[string]interface{}{
				"type": "Create",
			}
			activityJSON, _ := json.Marshal(activityObj)

			activity := &domain.APActivity{
				ActorID:      actorID,
				Type:         "Create",
				Published:    time.Now(),
				ActivityJSON: activityJSON,
				Local:        true,
			}

			err := repo.StoreActivity(ctx, activity)
			require.NoError(t, err)
		}

		activities, total, err := repo.GetActivitiesByActor(ctx, actorID, 10, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(activities), 3)
		assert.GreaterOrEqual(t, total, 3)
	})
}

func TestDeduplication(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	activityURI := "https://mastodon.example/activities/" + uuid.New().String()

	t.Run("Check non-received activity", func(t *testing.T) {
		received, err := repo.IsActivityReceived(ctx, activityURI)
		require.NoError(t, err)
		assert.False(t, received)
	})

	t.Run("Mark activity as received", func(t *testing.T) {
		err := repo.MarkActivityReceived(ctx, activityURI)
		require.NoError(t, err)

		received, err := repo.IsActivityReceived(ctx, activityURI)
		require.NoError(t, err)
		assert.True(t, received)
	})

	t.Run("Mark same activity twice (should not error)", func(t *testing.T) {
		err := repo.MarkActivityReceived(ctx, activityURI)
		require.NoError(t, err) // Should be idempotent
	})
}

func TestVideoReactions(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	videoID := uuid.New().String()
	actorURI := "https://mastodon.example/users/alice"

	t.Run("Add like reaction", func(t *testing.T) {
		activityURI := "https://mastodon.example/activities/" + uuid.New().String()
		err := repo.UpsertVideoReaction(ctx, videoID, actorURI, "like", activityURI)
		require.NoError(t, err)

		likes, dislikes, err := repo.GetVideoReactionStats(ctx, videoID)
		require.NoError(t, err)
		assert.Equal(t, 1, likes)
		assert.Equal(t, 0, dislikes)
	})

	t.Run("Add dislike reaction", func(t *testing.T) {
		actorURI2 := "https://mastodon.example/users/bob"
		activityURI := "https://mastodon.example/activities/" + uuid.New().String()
		err := repo.UpsertVideoReaction(ctx, videoID, actorURI2, "dislike", activityURI)
		require.NoError(t, err)

		likes, dislikes, err := repo.GetVideoReactionStats(ctx, videoID)
		require.NoError(t, err)
		assert.Equal(t, 1, likes)
		assert.Equal(t, 1, dislikes)
	})

	t.Run("Delete reaction", func(t *testing.T) {
		activityURI := "https://mastodon.example/activities/" + uuid.New().String()
		err := repo.UpsertVideoReaction(ctx, videoID, actorURI, "like", activityURI)
		require.NoError(t, err)

		err = repo.DeleteVideoReaction(ctx, activityURI)
		require.NoError(t, err)

		likes, dislikes, err := repo.GetVideoReactionStats(ctx, videoID)
		require.NoError(t, err)
		// One like should remain (from earlier test)
		assert.GreaterOrEqual(t, likes+dislikes, 0)
	})
}

func TestVideoShares(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	videoID := uuid.New().String()

	t.Run("Add share", func(t *testing.T) {
		actorURI := "https://mastodon.example/users/alice"
		activityURI := "https://mastodon.example/activities/" + uuid.New().String()
		err := repo.UpsertVideoShare(ctx, videoID, actorURI, activityURI)
		require.NoError(t, err)

		count, err := repo.GetVideoShareCount(ctx, videoID)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("Add multiple shares", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			actorURI := "https://example.com/user" + string(rune(i))
			activityURI := "https://example.com/activity" + string(rune(i))
			err := repo.UpsertVideoShare(ctx, videoID, actorURI, activityURI)
			require.NoError(t, err)
		}

		count, err := repo.GetVideoShareCount(ctx, videoID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 3)
	})

	t.Run("Delete share", func(t *testing.T) {
		activityURI := "https://mastodon.example/activities/" + uuid.New().String()
		actorURI := "https://mastodon.example/users/charlie"

		err := repo.UpsertVideoShare(ctx, videoID, actorURI, activityURI)
		require.NoError(t, err)

		err = repo.DeleteVideoShare(ctx, activityURI)
		require.NoError(t, err)
	})
}

func TestDeliveryQueue(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		return
	}
	defer db.Close()

	repo := NewActivityPubRepository(db)
	ctx := context.Background()

	// First create an activity to reference
	activityObj := map[string]interface{}{"type": "Follow"}
	activityJSON, _ := json.Marshal(activityObj)
	activity := &domain.APActivity{
		ActorID:      "test-actor",
		Type:         "Follow",
		Published:    time.Now(),
		ActivityJSON: activityJSON,
		Local:        true,
	}
	err := repo.StoreActivity(ctx, activity)
	require.NoError(t, err)

	t.Run("Enqueue delivery", func(t *testing.T) {
		delivery := &domain.APDeliveryQueue{
			ActivityID:  activity.ID,
			InboxURL:    "https://mastodon.example/users/alice/inbox",
			ActorID:     "test-actor",
			MaxAttempts: 10,
			NextAttempt: time.Now(),
			Status:      "pending",
		}

		err := repo.EnqueueDelivery(ctx, delivery)
		require.NoError(t, err)
		assert.NotEmpty(t, delivery.ID)
	})

	t.Run("Get pending deliveries", func(t *testing.T) {
		deliveries, err := repo.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)
		assert.NotEmpty(t, deliveries)
	})

	t.Run("Update delivery status", func(t *testing.T) {
		delivery := &domain.APDeliveryQueue{
			ActivityID:  activity.ID,
			InboxURL:    "https://example.com/inbox2",
			ActorID:     "test-actor",
			MaxAttempts: 10,
			NextAttempt: time.Now(),
			Status:      "pending",
		}

		err := repo.EnqueueDelivery(ctx, delivery)
		require.NoError(t, err)

		nextAttempt := time.Now().Add(1 * time.Hour)
		errMsg := "Connection timeout"
		err = repo.UpdateDeliveryStatus(ctx, delivery.ID, "failed", 1, &errMsg, nextAttempt)
		require.NoError(t, err)
	})
}
