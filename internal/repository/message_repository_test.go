package repository

import (
    "context"
    "testing"
    "time"

    "athena/internal/domain"
    "athena/internal/testutil"
    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestMessageRepository_GetMessages_PaginationAndOrder(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    ur := NewUserRepository(td.DB)
    mr := NewMessageRepository(td.DB)

    ctx := context.Background()
    u1 := createTestUser(t, ur, ctx, "m_pag_user1", "m_pag_user1@example.com")
    u2 := createTestUser(t, ur, ctx, "m_pag_user2", "m_pag_user2@example.com")

    // Seed 15 messages with increasing CreatedAt
    base := time.Now().Add(-15 * time.Minute)
    for i := 0; i < 15; i++ {
        m := &domain.Message{
            ID:          uuid.NewString(),
            SenderID:    u1.ID,
            RecipientID: u2.ID,
            Content:     "m" + uuid.NewString(),
            MessageType: domain.MessageTypeText,
            CreatedAt:   base.Add(time.Duration(i) * time.Minute),
            UpdatedAt:   base.Add(time.Duration(i) * time.Minute),
        }
        require.NoError(t, mr.CreateMessage(ctx, m))
    }

    // Page 1
    msgs, err := mr.GetMessages(ctx, u1.ID, u2.ID, 5, 0)
    require.NoError(t, err)
    require.Len(t, msgs, 5)
    // Ensure desc order by created_at
    for i := 1; i < len(msgs); i++ {
        assert.True(t, msgs[i-1].CreatedAt.After(msgs[i].CreatedAt) || msgs[i-1].CreatedAt.Equal(msgs[i].CreatedAt))
    }

    // Page 2
    msgs2, err := mr.GetMessages(ctx, u1.ID, u2.ID, 5, 5)
    require.NoError(t, err)
    require.Len(t, msgs2, 5)

    // Page 3
    msgs3, err := mr.GetMessages(ctx, u1.ID, u2.ID, 5, 10)
    require.NoError(t, err)
    require.Len(t, msgs3, 5)

    // Out of bounds
    msgs4, err := mr.GetMessages(ctx, u1.ID, u2.ID, 5, 20)
    require.NoError(t, err)
    assert.Len(t, msgs4, 0)
}

func TestMessageRepository_SoftDelete_Visibility(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    ur := NewUserRepository(td.DB)
    mr := NewMessageRepository(td.DB)
    ctx := context.Background()
    u1 := createTestUser(t, ur, ctx, "m_del_user1", "m_del_user1@example.com")
    u2 := createTestUser(t, ur, ctx, "m_del_user2", "m_del_user2@example.com")
    u3 := createTestUser(t, ur, ctx, "m_del_user3", "m_del_user3@example.com")

    m := &domain.Message{
        ID:          uuid.NewString(),
        SenderID:    u1.ID,
        RecipientID: u2.ID,
        Content:     "to be hidden",
        MessageType: domain.MessageTypeText,
        CreatedAt:   time.Now(),
        UpdatedAt:   time.Now(),
    }
    require.NoError(t, mr.CreateMessage(ctx, m))

    // Delete by sender hides from sender's view
    require.NoError(t, mr.DeleteMessage(ctx, m.ID, u1.ID))

    msgsSender, err := mr.GetMessages(ctx, u1.ID, u2.ID, 10, 0)
    require.NoError(t, err)
    assert.Len(t, msgsSender, 0)

    msgsRecipient, err := mr.GetMessages(ctx, u2.ID, u1.ID, 10, 0)
    require.NoError(t, err)
    assert.Len(t, msgsRecipient, 1)

    // Delete with unrelated user should return not found
    err = mr.DeleteMessage(ctx, m.ID, u3.ID)
    assert.ErrorIs(t, err, domain.ErrMessageNotFound)

    // Recipient deletes too -> hidden for both
    require.NoError(t, mr.DeleteMessage(ctx, m.ID, u2.ID))
    msgsRecipient, err = mr.GetMessages(ctx, u2.ID, u1.ID, 10, 0)
    require.NoError(t, err)
    assert.Len(t, msgsRecipient, 0)
}

func TestMessageRepository_MarkRead_And_GetUnreadCount(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    ur := NewUserRepository(td.DB)
    mr := NewMessageRepository(td.DB)
    ctx := context.Background()
    u1 := createTestUser(t, ur, ctx, "m_read_user1", "m_read_user1@example.com")
    u2 := createTestUser(t, ur, ctx, "m_read_user2", "m_read_user2@example.com")

    // Two messages to u2, one to u1
    m1 := &domain.Message{ID: uuid.NewString(), SenderID: u1.ID, RecipientID: u2.ID, Content: "1", MessageType: domain.MessageTypeText, CreatedAt: time.Now(), UpdatedAt: time.Now()}
    m2 := &domain.Message{ID: uuid.NewString(), SenderID: u1.ID, RecipientID: u2.ID, Content: "2", MessageType: domain.MessageTypeText, CreatedAt: time.Now(), UpdatedAt: time.Now()}
    m3 := &domain.Message{ID: uuid.NewString(), SenderID: u2.ID, RecipientID: u1.ID, Content: "3", MessageType: domain.MessageTypeText, CreatedAt: time.Now(), UpdatedAt: time.Now()}
    require.NoError(t, mr.CreateMessage(ctx, m1))
    require.NoError(t, mr.CreateMessage(ctx, m2))
    require.NoError(t, mr.CreateMessage(ctx, m3))

    // Unread for u2 should be 2
    cnt, err := mr.GetUnreadCount(ctx, u2.ID)
    require.NoError(t, err)
    assert.Equal(t, 2, cnt)

    // Mark one as read by recipient
    require.NoError(t, mr.MarkMessageAsRead(ctx, m1.ID, u2.ID))

    // Marking again returns not found (already read)
    assert.ErrorIs(t, mr.MarkMessageAsRead(ctx, m1.ID, u2.ID), domain.ErrMessageNotFound)

    // Wrong user cannot mark
    assert.ErrorIs(t, mr.MarkMessageAsRead(ctx, m2.ID, u1.ID), domain.ErrMessageNotFound)

    // Count updates
    cnt, err = mr.GetUnreadCount(ctx, u2.ID)
    require.NoError(t, err)
    assert.Equal(t, 1, cnt)
}

func TestMessageRepository_GetConversations_OrderAndUnread(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    ur := NewUserRepository(td.DB)
    mr := NewMessageRepository(td.DB)
    ctx := context.Background()
    u1 := createTestUser(t, ur, ctx, "m_conv_user1", "m_conv_user1@example.com")
    u2 := createTestUser(t, ur, ctx, "m_conv_user2", "m_conv_user2@example.com")
    u3 := createTestUser(t, ur, ctx, "m_conv_user3", "m_conv_user3@example.com")

    // u1<->u2 (latest from u2->u1, contributes to unread for u1)
    mA := &domain.Message{ID: uuid.NewString(), SenderID: u1.ID, RecipientID: u2.ID, Content: "a", MessageType: domain.MessageTypeText, CreatedAt: time.Now().Add(-3 * time.Minute), UpdatedAt: time.Now().Add(-3 * time.Minute)}
    mB := &domain.Message{ID: uuid.NewString(), SenderID: u2.ID, RecipientID: u1.ID, Content: "b", MessageType: domain.MessageTypeText, CreatedAt: time.Now().Add(-2 * time.Minute), UpdatedAt: time.Now().Add(-2 * time.Minute)}
    // u1->u3 (latest overall, from u1)
    mC := &domain.Message{ID: uuid.NewString(), SenderID: u1.ID, RecipientID: u3.ID, Content: "c", MessageType: domain.MessageTypeText, CreatedAt: time.Now().Add(-1 * time.Minute), UpdatedAt: time.Now().Add(-1 * time.Minute)}
    require.NoError(t, mr.CreateMessage(ctx, mA))
    require.NoError(t, mr.CreateMessage(ctx, mB))
    require.NoError(t, mr.CreateMessage(ctx, mC))

    convs, err := mr.GetConversations(ctx, u1.ID, 10, 0)
    require.NoError(t, err)
    require.Len(t, convs, 2)

    // Ordered by last_message_at desc; first is with u3
    assert.NotNil(t, convs[0].LastMessage)
    assert.Equal(t, mC.ID, convs[0].LastMessage.ID)
    // Unread count for u1 should be 1 from u2
    // We need to identify which conversation is u2
    var unreadForU2 int
    for _, c := range convs {
        if (c.ParticipantOneID == u1.ID && c.ParticipantTwoID == u2.ID) || (c.ParticipantOneID == u2.ID && c.ParticipantTwoID == u1.ID) {
            unreadForU2 = c.UnreadCount
        }
    }
    assert.Equal(t, 1, unreadForU2)

    // Pagination
    convsPage1, err := mr.GetConversations(ctx, u1.ID, 1, 0)
    require.NoError(t, err)
    require.Len(t, convsPage1, 1)
    convsPage2, err := mr.GetConversations(ctx, u1.ID, 1, 1)
    require.NoError(t, err)
    require.Len(t, convsPage2, 1)
    assert.NotEqual(t, convsPage1[0].ID, convsPage2[0].ID)
}

