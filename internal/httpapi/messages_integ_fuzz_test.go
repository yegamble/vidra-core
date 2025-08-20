package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
)

// FuzzSendMessageHandler_WithService performs end-to-end fuzzing of sending messages
// using a live DB-backed repository and service. It validates that the handler never
// panics and does not return 5xx for adversarial inputs.
func FuzzSendMessageHandler_WithService(f *testing.F) {
	// seeds: seed, ops
	f.Add(int64(1), int64(5))
	f.Add(int64(42), int64(10))

	f.Fuzz(func(t *testing.T, seed, ops int64) {
		td := testutil.SetupTestDB(t)
		if td == nil {
			return
		}
		td.TruncateTables(t, "messages", "conversations", "users")

		ur := repository.NewUserRepository(td.DB)
		mr := repository.NewMessageRepository(td.DB)
		svc := usecase.NewMessageService(mr, ur)
		ctx := context.Background()

		u1 := createTestUser(t, ur, ctx, "fuzz_msg_u1", "fuzz_msg_u1@example.com")
		u2 := createTestUser(t, ur, ctx, "fuzz_msg_u2", "fuzz_msg_u2@example.com")
		u3 := createTestUser(t, ur, ctx, "fuzz_msg_u3", "fuzz_msg_u3@example.com")

		handler := SendMessageHandler(svc)

		rng := rand.New(rand.NewSource(seed))
		n := int(ops%15 + 1)

		// maintain message IDs to optionally thread replies
		type msgMeta struct{ id, sender, recipient string }
		var history []msgMeta

		for i := 0; i < n; i++ {
			sender := u1
			recipient := u2
			if rng.Intn(2) == 1 {
				sender, recipient = u2, u1
			}
			// sometimes mix in a third user to test parent-message access across convos
			if rng.Intn(10) == 0 {
				recipient = u3
			}
			contentLen := rng.Intn(2100) // may exceed 2000 to trigger validation
			if contentLen < 1 {
				contentLen = 1
			}
			content := bytes.Repeat([]byte("x"), contentLen)

			payload := map[string]any{
				"recipient_id": recipient.ID,
				"content":      string(content),
			}
			// Occasionally include a parent reference (valid or invalid)
			if len(history) > 0 && rng.Intn(3) == 0 {
				parent := history[rng.Intn(len(history))]
				// 50/50 chance to use unrelated parent (should cause 404)
				if rng.Intn(2) == 0 {
					payload["parent_message_id"] = parent.id
				} else {
					// synthesize random id to likely cause not found
					payload["parent_message_id"] = uuid.NewString()
				}
			}

			b, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, sender.ID))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Allowed: 201, 400, 404 (validation, parent not in convo), never 5xx
			if rr.Code >= 500 {
				t.Fatalf("unexpected 5xx: %d body=%s", rr.Code, rr.Body.String())
			}
			if rr.Code == http.StatusCreated {
				var env Response
				_ = json.Unmarshal(rr.Body.Bytes(), &env)
				var mr domain.MessageResponse
				b2, _ := json.Marshal(env.Data)
				_ = json.Unmarshal(b2, &mr)
				history = append(history, msgMeta{mr.Message.ID, mr.Message.SenderID, mr.Message.RecipientID})
			}
		}
	})
}

// FuzzMessageHandlers_ReadDeleteAndList fuzzes mark-read, delete, list, conversations, and unread-count using live service.
func FuzzMessageHandlers_ReadDeleteAndList(f *testing.F) {
	f.Add(int64(7), int64(10))
	f.Add(int64(99), int64(6))

	f.Fuzz(func(t *testing.T, seed, ops int64) {
		td := testutil.SetupTestDB(t)
		if td == nil {
			return
		}
		td.TruncateTables(t, "messages", "conversations", "users")

		ur := repository.NewUserRepository(td.DB)
		mr := repository.NewMessageRepository(td.DB)
		svc := usecase.NewMessageService(mr, ur)
		ctx := context.Background()

		u1 := createTestUser(t, ur, ctx, "fuzz_rl_u1", "fuzz_rl_u1@example.com")
		u2 := createTestUser(t, ur, ctx, "fuzz_rl_u2", "fuzz_rl_u2@example.com")

		sendHandler := SendMessageHandler(svc)
		readHandler := MarkMessageReadHandler(svc)
		delHandler := DeleteMessageHandler(svc)
		listHandler := GetMessagesHandler(svc)
		convHandler := GetConversationsHandler(svc)
		unreadHandler := GetUnreadCountHandler(svc)

		rng := rand.New(rand.NewSource(seed))
		n := int(ops%20 + 1)

		// seed a handful of messages
		for i := 0; i < 5; i++ {
			body := map[string]any{"recipient_id": u2.ID, "content": "seed-" + uuid.NewString()}
			b, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u1.ID))
			rr := httptest.NewRecorder()
			sendHandler.ServeHTTP(rr, req)
			if rr.Code >= 500 {
				t.Fatalf("seeding 5xx: %d body=%s", rr.Code, rr.Body.String())
			}
		}

		// pull current messages
		reqList := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+u2.ID+"&limit=50&offset=0", nil)
		reqList = reqList.WithContext(context.WithValue(reqList.Context(), middleware.UserIDKey, u1.ID))
		rrList := httptest.NewRecorder()
		listHandler.ServeHTTP(rrList, reqList)
		if rrList.Code >= 500 {
			t.Fatalf("list 5xx: %d body=%s", rrList.Code, rrList.Body.String())
		}
		var env Response
		_ = json.Unmarshal(rrList.Body.Bytes(), &env)
		var lr domain.MessagesResponse
		bList, _ := json.Marshal(env.Data)
		_ = json.Unmarshal(bList, &lr)

		// randomized ops across mark-read/delete/list/conversations/unread-count
		for i := 0; i < n; i++ {
			action := rng.Intn(5)
			switch action {
			case 0: // mark read a random message as recipient (u2)
				if len(lr.Messages) == 0 {
					continue
				}
				m := lr.Messages[rng.Intn(len(lr.Messages))]
				req := httptest.NewRequest(http.MethodPut, "/messages/"+m.ID+"/read", nil)
				req = withChiURLParam(req, "messageId", m.ID)
				req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u2.ID))
				rr := httptest.NewRecorder()
				readHandler.ServeHTTP(rr, req)
				if rr.Code >= 500 {
					t.Fatalf("mark read 5xx: %d body=%s", rr.Code, rr.Body.String())
				}
			case 1: // mark read by non-recipient (should 404)
				if len(lr.Messages) == 0 {
					continue
				}
				m := lr.Messages[rng.Intn(len(lr.Messages))]
				req := httptest.NewRequest(http.MethodPut, "/messages/"+m.ID+"/read", nil)
				req = withChiURLParam(req, "messageId", m.ID)
				req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u1.ID))
				rr := httptest.NewRecorder()
				readHandler.ServeHTTP(rr, req)
				if rr.Code >= 500 {
					t.Fatalf("mark read (wrong user) 5xx: %d body=%s", rr.Code, rr.Body.String())
				}
			case 2: // delete by sender (u1)
				if len(lr.Messages) == 0 {
					continue
				}
				m := lr.Messages[rng.Intn(len(lr.Messages))]
				req := httptest.NewRequest(http.MethodDelete, "/messages/"+m.ID, nil)
				req = withChiURLParam(req, "messageId", m.ID)
				req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u1.ID))
				rr := httptest.NewRecorder()
				delHandler.ServeHTTP(rr, req)
				if rr.Code >= 500 {
					t.Fatalf("delete 5xx: %d body=%s", rr.Code, rr.Body.String())
				}
			case 3: // list messages with random pagination
				limit := rng.Intn(120) // may exceed 100 to exercise defaulting
				offset := rng.Intn(20)
				url := "/messages?conversation_with=" + u2.ID + "&limit=" + itoaSafe(limit) + "&offset=" + itoaSafe(offset)
				req := httptest.NewRequest(http.MethodGet, url, nil)
				req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u1.ID))
				rr := httptest.NewRecorder()
				listHandler.ServeHTTP(rr, req)
				if rr.Code >= 500 {
					t.Fatalf("list 5xx: %d body=%s", rr.Code, rr.Body.String())
				}
			case 4: // conversations and unread count
				reqC := httptest.NewRequest(http.MethodGet, "/conversations?limit=10&offset=0", nil)
				reqC = reqC.WithContext(context.WithValue(reqC.Context(), middleware.UserIDKey, u1.ID))
				rrC := httptest.NewRecorder()
				convHandler.ServeHTTP(rrC, reqC)
				if rrC.Code >= 500 {
					t.Fatalf("conversations 5xx: %d body=%s", rrC.Code, rrC.Body.String())
				}
				reqU := httptest.NewRequest(http.MethodGet, "/conversations/unread-count", nil)
				reqU = reqU.WithContext(context.WithValue(reqU.Context(), middleware.UserIDKey, u1.ID))
				rrU := httptest.NewRecorder()
				unreadHandler.ServeHTTP(rrU, reqU)
				if rrU.Code >= 500 {
					t.Fatalf("unread 5xx: %d body=%s", rrU.Code, rrU.Body.String())
				}
			}
		}
	})
}
