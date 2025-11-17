# Notifications API Documentation

## Overview

The Athena notification system provides real-time updates to users about important events on the platform. Notifications are created automatically through database triggers and can be managed through a comprehensive REST API.

## Notification Types

| Type | Description | Trigger |
|------|-------------|---------|
| `new_video` | New video from subscribed channel | When a public video is uploaded by a subscribed channel |
| `video_processed` | Video processing completed | When user's own video finishes processing |
| `video_failed` | Video processing failed | When user's own video fails processing |
| `new_subscriber` | New channel subscriber | When someone subscribes to user's channel |
| `comment` | Comment on video | When someone comments on user's video |
| `mention` | User mentioned | When user is mentioned in a comment |
| `new_message` | New direct message | When user receives a message |
| `message_read` | Message read receipt | When recipient reads user's message (optional) |
| `system` | System announcement | Platform-wide announcements |

## API Endpoints

### Get Notifications

Retrieve notifications for the authenticated user.

**Endpoint:** `GET /api/v1/notifications`

**Authentication:** Required

**Query Parameters:**
- `limit` (integer, optional): Maximum results per page (1-100, default: 50)
- `offset` (integer, optional): Pagination offset (default: 0)
- `unread` (boolean, optional): Filter to unread notifications only

**Response:**
```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "user_id": "123e4567-e89b-12d3-a456-426614174000",
    "type": "new_video",
    "title": "New video from TechChannel",
    "message": "TechChannel uploaded: Introduction to Go Programming",
    "data": {
      "video_id": "789e0123-e89b-12d3-a456-426614174000",
      "channel_id": "456e7890-e89b-12d3-a456-426614174000",
      "channel_name": "TechChannel",
      "video_title": "Introduction to Go Programming",
      "thumbnail_cid": "QmThumbnailCID123"
    },
    "read": false,
    "created_at": "2024-01-15T10:30:00Z",
    "read_at": null
  }
]
```

### Get Unread Count

Get the count of unread notifications.

**Endpoint:** `GET /api/v1/notifications/unread-count`

**Authentication:** Required

**Response:**
```json
{
  "unread_count": 5
}
```

### Get Notification Statistics

Get detailed statistics about user's notifications.

**Endpoint:** `GET /api/v1/notifications/stats`

**Authentication:** Required

**Response:**
```json
{
  "total_count": 150,
  "unread_count": 5,
  "by_type": {
    "new_video": 50,
    "new_message": 45,
    "comment": 30,
    "new_subscriber": 15,
    "mention": 10
  }
}
```

### Mark Notification as Read

Mark a specific notification as read.

**Endpoint:** `PUT /api/v1/notifications/{id}/read`

**Authentication:** Required

**Path Parameters:**
- `id` (UUID): Notification ID

**Response:**
```json
{
  "success": true
}
```

### Mark All as Read

Mark all notifications as read for the authenticated user.

**Endpoint:** `PUT /api/v1/notifications/read-all`

**Authentication:** Required

**Response:**
```json
{
  "success": true
}
```

### Delete Notification

Delete a specific notification.

**Endpoint:** `DELETE /api/v1/notifications/{id}`

**Authentication:** Required

**Path Parameters:**
- `id` (UUID): Notification ID

**Response:** `204 No Content`

## Notification Data Structure

Each notification contains a flexible `data` field with type-specific information:

### Video Notification Data
```json
{
  "video_id": "UUID",
  "channel_id": "UUID",
  "channel_name": "string",
  "video_title": "string",
  "thumbnail_cid": "string"
}
```

### Message Notification Data
```json
{
  "message_id": "UUID",
  "sender_id": "UUID",
  "sender_name": "string",
  "message_preview": "string (max 100 chars)",
  "conversation_id": "UUID"
}
```

### Subscriber Notification Data
```json
{
  "subscriber_id": "UUID",
  "subscriber_name": "string",
  "subscriber_avatar": "string (optional)"
}
```

### Comment Notification Data
```json
{
  "comment_id": "UUID",
  "video_id": "UUID",
  "commenter_id": "UUID",
  "commenter_name": "string",
  "comment_text": "string (preview)"
}
```

## Automatic Notification Creation

Notifications are created automatically through PostgreSQL triggers:

### Video Upload Notifications

When a video's status changes to `completed` and privacy is `public`, all subscribers of the channel receive a notification.

**Trigger:** `trg_notify_video_upload`
**Function:** `notify_subscribers_on_video_upload()`

### Message Notifications

When a new message is inserted (excluding system messages), the recipient receives a notification.

**Trigger:** `trg_notify_new_message`
**Function:** `notify_on_new_message()`

Message previews are automatically truncated to 100 characters with ellipsis for longer messages.

## Filtering and Pagination

### Filter by Unread Status
```bash
GET /api/v1/notifications?unread=true
```

### Paginated Results
```bash
GET /api/v1/notifications?limit=20&offset=40
```

### Combined Filters
```bash
GET /api/v1/notifications?unread=true&limit=10
```

## Error Responses

### 401 Unauthorized
```json
{
  "error": "UNAUTHORIZED",
  "message": "Authentication required"
}
```

### 404 Not Found
```json
{
  "error": "NOT_FOUND",
  "message": "Notification not found"
}
```

### 400 Bad Request
```json
{
  "error": "INVALID_REQUEST",
  "message": "Invalid notification ID format"
}
```

## Best Practices

1. **Polling**: Poll `/api/v1/notifications/unread-count` periodically to check for new notifications
2. **Pagination**: Use pagination for large notification lists to improve performance
3. **Batch Operations**: Use `/api/v1/notifications/read-all` instead of marking individual notifications
4. **Caching**: Cache notification counts client-side and update after user actions
5. **Filtering**: Use the `unread` filter to show only new notifications to users

## WebSocket Support (Future)

Future versions will support WebSocket connections for real-time notification delivery:
- Subscribe to notification events
- Receive instant updates without polling
- Reduced server load and improved user experience

## Rate Limits

- Standard rate limits apply (see main API documentation)
- Notification endpoints are subject to per-user rate limiting
- Excessive polling may result in temporary blocking

## Examples

### cURL Examples

**Get unread notifications:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "https://api.example.com/api/v1/notifications?unread=true"
```

**Mark notification as read:**
```bash
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  "https://api.example.com/api/v1/notifications/550e8400-e29b-41d4-a716-446655440000/read"
```

**Get notification statistics:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "https://api.example.com/api/v1/notifications/stats"
```

### JavaScript Example

```javascript
// Fetch unread notifications
async function getUnreadNotifications(token) {
  const response = await fetch('/api/v1/notifications?unread=true', {
    headers: {
      'Authorization': `Bearer ${token}`
    }
  });
  return response.json();
}

// Mark notification as read
async function markAsRead(token, notificationId) {
  const response = await fetch(`/api/v1/notifications/${notificationId}/read`, {
    method: 'PUT',
    headers: {
      'Authorization': `Bearer ${token}`
    }
  });
  return response.json();
}

// Poll for new notifications
async function pollNotifications(token, interval = 30000) {
  setInterval(async () => {
    const response = await fetch('/api/v1/notifications/unread-count', {
      headers: {
        'Authorization': `Bearer ${token}`
      }
    });
    const data = await response.json();
    if (data.unread_count > 0) {
      // Update UI with notification badge
      updateNotificationBadge(data.unread_count);
    }
  }, interval);
}
```

## Database Schema

```sql
CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    message TEXT,
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    read_at TIMESTAMP WITH TIME ZONE
);

-- Indexes for performance
CREATE INDEX idx_notifications_user_id ON notifications(user_id);
CREATE INDEX idx_notifications_user_read ON notifications(user_id, read);
CREATE INDEX idx_notifications_created_at ON notifications(created_at DESC);
CREATE INDEX idx_notifications_user_unread_recent
    ON notifications(user_id, created_at DESC)
    WHERE read = FALSE;
```

## Migration Guide

For existing systems looking to integrate the notification system:

1. **Apply Migration**: Run migration `020_create_notifications_table.sql`
2. **Add Message Triggers**: Run migration `021_add_message_notifications.sql`
3. **Update Services**: Inject `NotificationService` into video and message handlers
4. **Add API Routes**: Mount notification handlers at `/api/v1/notifications`
5. **Update Frontend**: Add notification UI components and polling logic

## Testing

The notification system includes comprehensive tests:

- **Integration Tests**: Full workflow tests for video and message notifications
- **Unit Tests**: Service method testing with mocked dependencies
- **Trigger Tests**: Database trigger validation
- **API Tests**: Endpoint testing with authentication

Run tests:
```bash
go test ./internal/httpapi -run TestNotification
go test ./internal/httpapi -run TestMessageNotification
```

## Performance Considerations

- Notifications are indexed for fast user queries
- Batch operations reduce database round trips
- JSONB data field allows flexible content without schema changes
- Automatic cleanup of old read notifications (configurable)
- Composite indexes optimize common query patterns

## Future Enhancements

- WebSocket support for real-time delivery
- Push notifications (mobile/desktop)
- Notification preferences and filtering
- Email notification digests
- Notification templates and customization
- Analytics and engagement tracking
