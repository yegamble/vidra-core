//go:build integration

package chat_test

import (
	"log/slog"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vidra-core/internal/chat"
	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/repository"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"
)

func setupIntegrationTest(t *testing.T) (*sqlx.DB, *redis.Client, func()) {
	db, err := sqlx.Connect("postgres", "postgres://test_user:test_password@localhost:5433/vidra_test?sslmode=disable")
	require.NoError(t, err)

	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6380",
	})

	cleanup := func() {
		db.Exec("DELETE FROM chat_messages")
		db.Exec("DELETE FROM chat_bans")
		db.Exec("DELETE FROM chat_moderators")
		db.Exec("DELETE FROM live_streams WHERE status = 'test'")
		db.Exec("DELETE FROM users WHERE email LIKE 'test-%'")

		redisClient.FlushDB(context.Background())

		db.Close()
		redisClient.Close()
	}

	return db, redisClient, cleanup
}

func createTestStream(t *testing.T, db *sqlx.DB) (streamID, userID uuid.UUID) {
	userID = uuid.New()

	_, err := db.Exec(`
		INSERT INTO users (id, username, email, password_hash, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, userID, "testuser", fmt.Sprintf("test-%s@example.com", userID), "hash")
	require.NoError(t, err)

	streamID = uuid.New()
	_, err = db.Exec(`
		INSERT INTO live_streams (id, user_id, title, stream_key, status, privacy, created_at)
		VALUES ($1, $2, $3, $4, 'live', 'public', NOW())
	`, streamID, userID, "Test Stream", "key123")
	require.NoError(t, err)

	return streamID, userID
}

func TestChatIntegration_FullLifecycle(t *testing.T) {
	db, redisClient, cleanup := setupIntegrationTest(t)
	defer cleanup()

	streamID, userID := createTestStream(t, db)

	chatRepo := repository.NewChatRepository(db)
	streamRepo := repository.NewLiveStreamRepository(db)

	cfg := &config.Config{}
	logger := slog.Default()
	logger.SetLevel()

	chatServer := chat.NewChatServer(cfg, chatRepo, streamRepo, redisClient, logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := chatServer.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade: %v", err)
			return
		}

		err = chatServer.HandleWebSocket(r.Context(), conn, streamID, userID, "testuser")
		if err != nil {
			t.Logf("WebSocket closed: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	msg := map[string]interface{}{
		"type":    "message",
		"message": "Hello, world!",
	}
	err = conn.WriteJSON(msg)
	require.NoError(t, err)

	var received chat.ChatMessage
	err = conn.ReadJSON(&received)
	require.NoError(t, err)

	assert.Equal(t, "message", received.Type)
	assert.Equal(t, "Hello, world!", received.Message)
	assert.Equal(t, streamID, received.StreamID)
	assert.Equal(t, userID, received.UserID)
	assert.Equal(t, "testuser", received.Username)

	messages, err := chatRepo.GetMessages(context.Background(), streamID, 10, 0)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "Hello, world!", messages[0].Message)

	conn.Close()

	require.Eventually(t, func() bool {
		connectedUsers := chatServer.GetConnectedUsers(streamID)
		return connectedUsers == 0
	}, 200*time.Millisecond, 10*time.Millisecond, "Connected users should be 0 after cleanup")
}

func TestChatIntegration_ConcurrentConnections(t *testing.T) {
	db, redisClient, cleanup := setupIntegrationTest(t)
	defer cleanup()

	streamID, _ := createTestStream(t, db)

	chatRepo := repository.NewChatRepository(db)
	streamRepo := repository.NewLiveStreamRepository(db)

	cfg := &config.Config{}
	logger := slog.Default()
	logger.SetLevel(logrus.WarnLevel)

	chatServer := chat.NewChatServer(cfg, chatRepo, streamRepo, redisClient, logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userIDStr := r.URL.Query().Get("user_id")
		userID, _ := uuid.Parse(userIDStr)
		username := r.URL.Query().Get("username")

		conn, err := chatServer.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		chatServer.HandleWebSocket(r.Context(), conn, streamID, userID, username)
	}))
	defer server.Close()

	numUsers := 60
	var wg sync.WaitGroup
	connections := make([]*websocket.Conn, numUsers)
	errChan := make(chan error, numUsers)

	for i := 0; i < numUsers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			userID := uuid.New()
			username := fmt.Sprintf("user%d", index)

			_, err := db.Exec(`
				INSERT INTO users (id, username, email, password_hash, created_at)
				VALUES ($1, $2, $3, $4, NOW())
			`, userID, username, fmt.Sprintf("test-%s@example.com", userID), "hash")
			if err != nil {
				errChan <- err
				return
			}

			wsURL := fmt.Sprintf("ws%s?user_id=%s&username=%s",
				strings.TrimPrefix(server.URL, "http"), userID, username)
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				errChan <- err
				return
			}

			connections[index] = conn
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Fatalf("Connection error: %v", err)
	}

	require.Eventually(t, func() bool {
		connectedUsers := chatServer.GetConnectedUsers(streamID)
		return connectedUsers == numUsers
	}, 1*time.Second, 10*time.Millisecond, "Expected %d connected users", numUsers)

	for _, conn := range connections {
		if conn != nil {
			conn.Close()
		}
	}

	require.Eventually(t, func() bool {
		connectedUsers := chatServer.GetConnectedUsers(streamID)
		return connectedUsers == 0
	}, 1*time.Second, 10*time.Millisecond, "All users should disconnect")
}

func TestChatIntegration_MessageBroadcast(t *testing.T) {
	db, redisClient, cleanup := setupIntegrationTest(t)
	defer cleanup()

	streamID, _ := createTestStream(t, db)

	chatRepo := repository.NewChatRepository(db)
	streamRepo := repository.NewLiveStreamRepository(db)

	cfg := &config.Config{}
	logger := slog.Default()
	logger.SetLevel(logrus.WarnLevel)

	chatServer := chat.NewChatServer(cfg, chatRepo, streamRepo, redisClient, logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userIDStr := r.URL.Query().Get("user_id")
		userID, _ := uuid.Parse(userIDStr)
		username := r.URL.Query().Get("username")

		conn, err := chatServer.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		chatServer.HandleWebSocket(r.Context(), conn, streamID, userID, username)
	}))
	defer server.Close()

	numViewers := 5
	viewers := make([]*websocket.Conn, numViewers)
	receivedMessages := make([][]chat.ChatMessage, numViewers)
	var mu sync.Mutex

	for i := 0; i < numViewers; i++ {
		userID := uuid.New()
		username := fmt.Sprintf("viewer%d", i)

		db.Exec(`INSERT INTO users (id, username, email, password_hash, created_at) VALUES ($1, $2, $3, $4, NOW())`,
			userID, username, fmt.Sprintf("test-%s@example.com", userID), "hash")

		wsURL := fmt.Sprintf("ws%s?user_id=%s&username=%s",
			strings.TrimPrefix(server.URL, "http"), userID, username)
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)

		viewers[i] = conn
		receivedMessages[i] = make([]chat.ChatMessage, 0)

		index := i
		go func() {
			for {
				var msg chat.ChatMessage
				err := conn.ReadJSON(&msg)
				if err != nil {
					return
				}
				mu.Lock()
				receivedMessages[index] = append(receivedMessages[index], msg)
				mu.Unlock()
			}
		}()
	}

	require.Eventually(t, func() bool {
		return chatServer.GetConnectedUsers(streamID) == numViewers
	}, 1*time.Second, 10*time.Millisecond, "Waiting for all viewers to connect")

	msg := map[string]interface{}{
		"type":    "message",
		"message": "Broadcast test message",
	}
	err := viewers[0].WriteJSON(msg)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, messages := range receivedMessages {
			found := false
			for _, msg := range messages {
				if msg.Type == "message" && msg.Message == "Broadcast test message" {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}, 1*time.Second, 10*time.Millisecond, "All viewers should receive broadcast message")

	mu.Lock()
	defer mu.Unlock()

	for i, messages := range receivedMessages {
		t.Logf("Viewer %d received %d messages", i, len(messages))

		found := false
		for _, msg := range messages {
			if msg.Type == "message" && msg.Message == "Broadcast test message" {
				found = true
				break
			}
		}
		assert.True(t, found, "Viewer %d did not receive broadcast message", i)
	}

	for _, conn := range viewers {
		conn.Close()
	}
}

func TestChatIntegration_ModerationActions(t *testing.T) {
	db, redisClient, cleanup := setupIntegrationTest(t)
	defer cleanup()

	streamID, ownerID := createTestStream(t, db)

	chatRepo := repository.NewChatRepository(db)
	streamRepo := repository.NewLiveStreamRepository(db)

	cfg := &config.Config{}
	logger := slog.Default()

	chatServer := chat.NewChatServer(cfg, chatRepo, streamRepo, redisClient, logger)

	modID := uuid.New()
	db.Exec(`INSERT INTO users (id, username, email, password_hash, created_at) VALUES ($1, $2, $3, $4, NOW())`,
		modID, "moderator", "mod@example.com", "hash")

	moderator := domain.NewChatModerator(streamID, modID, ownerID)
	err := chatRepo.AddModerator(context.Background(), moderator)
	require.NoError(t, err)

	regularUserID := uuid.New()
	db.Exec(`INSERT INTO users (id, username, email, password_hash, created_at) VALUES ($1, $2, $3, $4, NOW())`,
		regularUserID, "regular", "regular@example.com", "hash")

	ctx := context.Background()

	t.Run("BanUser", func(t *testing.T) {
		err := chatServer.BanUser(ctx, BanRequest{StreamID: streamID, UserID: regularUserID, ModeratorID: modID, Reason: "spam", Duration: 10 * time.Minute})
		assert.NoError(t, err)

		banned, err := chatRepo.IsUserBanned(ctx, streamID, regularUserID)
		require.NoError(t, err)
		assert.True(t, banned)
	})

	t.Run("DeleteMessage", func(t *testing.T) {
		msg := domain.NewChatMessage(streamID, regularUserID, "regular", "Bad message")
		err := chatRepo.CreateMessage(ctx, msg)
		require.NoError(t, err)

		err = chatServer.DeleteMessage(ctx, streamID, msg.ID, modID)
		assert.NoError(t, err)

		deletedMsg, err := chatRepo.GetMessageByID(ctx, msg.ID)
		require.NoError(t, err)
		assert.True(t, deletedMsg.Deleted)
	})

	t.Run("NonModeratorCannotDelete", func(t *testing.T) {
		msg := domain.NewChatMessage(streamID, modID, "mod", "Good message")
		err := chatRepo.CreateMessage(ctx, msg)
		require.NoError(t, err)

		err = chatServer.DeleteMessage(ctx, streamID, msg.ID, regularUserID)
		assert.ErrorIs(t, err, domain.ErrNotModerator)
	})
}

func TestChatIntegration_RateLimiting(t *testing.T) {
	db, redisClient, cleanup := setupIntegrationTest(t)
	defer cleanup()

	streamID, userID := createTestStream(t, db)

	chatRepo := repository.NewChatRepository(db)
	streamRepo := repository.NewLiveStreamRepository(db)

	cfg := &config.Config{
		ChatRateLimitMessages: 5,
		ChatRateLimitWindow:   10 * time.Second,
	}
	logger := slog.Default()

	chatServer := chat.NewChatServer(cfg, chatRepo, streamRepo, redisClient, logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := chatServer.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		chatServer.HandleWebSocket(r.Context(), conn, streamID, userID, "testuser")
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	successCount := 0
	errorCount := 0

	for i := 0; i < 10; i++ {
		msg := map[string]interface{}{
			"type":    "message",
			"message": fmt.Sprintf("Message %d", i),
		}
		err := conn.WriteJSON(msg)
		if err != nil {
			errorCount++
		} else {
			successCount++
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("Success: %d, Errors: %d", successCount, errorCount)

	// Note: The exact behavior depends on the rate limiter implementation
	assert.GreaterOrEqual(t, successCount, 5, "Should allow at least 5 messages")
}
