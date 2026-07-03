package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// notifFakeRepo is an in-memory notification.Repository that resolves the actor /
// channel / video / report join columns from the sibling fakes, mirroring the
// real query. createErr, when set, fails CreateNotification — for proving the
// notify side effects are best-effort. prefs maps "userID\x00type" → enabled
// (absent = default enabled), mirroring notification_prefs.
type notifFakeRepo struct {
	auth      *authFakeRepo
	channels  *channelFakeRepo
	videos    *videoFakeRepo
	reports   *moderationFakeRepo
	notifs    []sqlcgen.Notification
	prefs     map[string]bool
	createErr error
}

func notifPrefKey(userID uuid.UUID, typ string) string { return userID.String() + "\x00" + typ }

func (f *notifFakeRepo) ListNotificationPrefs(_ context.Context, userID uuid.UUID) ([]sqlcgen.ListNotificationPrefsRow, error) {
	var rows []sqlcgen.ListNotificationPrefsRow
	for _, typ := range notification.KnownTypes() { // stable order, like ORDER BY type
		if enabled, ok := f.prefs[notifPrefKey(userID, typ)]; ok {
			rows = append(rows, sqlcgen.ListNotificationPrefsRow{Type: typ, Enabled: enabled})
		}
	}
	return rows, nil
}

func (f *notifFakeRepo) UpsertNotificationPref(_ context.Context, a sqlcgen.UpsertNotificationPrefParams) error {
	if f.prefs == nil {
		f.prefs = map[string]bool{}
	}
	f.prefs[notifPrefKey(a.UserID, a.Type)] = a.Enabled
	return nil
}

func (f *notifFakeRepo) IsNotificationTypeEnabled(_ context.Context, a sqlcgen.IsNotificationTypeEnabledParams) (bool, error) {
	if enabled, ok := f.prefs[notifPrefKey(a.UserID, a.Type)]; ok {
		return enabled, nil
	}
	return true, nil
}

func (f *notifFakeRepo) userByID(id uuid.UUID) (sqlcgen.User, bool) {
	for _, u := range f.auth.users {
		if u.ID == id {
			return u, true
		}
	}
	return sqlcgen.User{}, false
}

func (f *notifFakeRepo) channelByID(id uuid.UUID) (sqlcgen.Channel, bool) {
	for _, ch := range f.channels.byHandle {
		if ch.ID == id {
			return ch, true
		}
	}
	return sqlcgen.Channel{}, false
}

func (f *notifFakeRepo) CreateNotification(_ context.Context, a sqlcgen.CreateNotificationParams) (sqlcgen.Notification, error) {
	if f.createErr != nil {
		return sqlcgen.Notification{}, f.createErr
	}
	n := sqlcgen.Notification{
		ID: uuid.New(), UserID: a.UserID, Type: a.Type,
		ActorID: a.ActorID, ChannelID: a.ChannelID, VideoID: a.VideoID, CommentID: a.CommentID,
		ConversationID: a.ConversationID, ReportID: a.ReportID, CreatedAt: time.Now(),
	}
	f.notifs = append(f.notifs, n)
	return n, nil
}

func (f *notifFakeRepo) ListNotifications(_ context.Context, a sqlcgen.ListNotificationsParams) ([]sqlcgen.ListNotificationsRow, error) {
	var rows []sqlcgen.ListNotificationsRow
	for i := len(f.notifs) - 1; i >= 0; i-- { // newest first
		n := f.notifs[i]
		if n.UserID != a.UserID {
			continue
		}
		if a.UnreadOnly && n.ReadAt.Valid {
			continue
		}
		row := sqlcgen.ListNotificationsRow{
			ID: n.ID, Type: n.Type, ActorID: n.ActorID, ChannelID: n.ChannelID,
			VideoID: n.VideoID, CommentID: n.CommentID, ConversationID: n.ConversationID,
			ReportID: n.ReportID, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt,
		}
		if n.ActorID.Valid {
			if u, ok := f.userByID(uuid.UUID(n.ActorID.Bytes)); ok {
				un, dn := u.Username, u.DisplayName
				row.ActorUsername, row.ActorDisplayName = &un, &dn
			}
		}
		if n.ChannelID.Valid {
			if ch, ok := f.channelByID(uuid.UUID(n.ChannelID.Bytes)); ok {
				h, dn := ch.Handle, ch.DisplayName
				row.ChannelHandle, row.ChannelDisplayName = &h, &dn
			}
		}
		if n.VideoID.Valid {
			if v, ok := f.videos.videos[uuid.UUID(n.VideoID.Bytes)]; ok {
				tt := v.Title
				row.VideoTitle = &tt
			}
		}
		if n.ReportID.Valid && f.reports != nil {
			if r, ok := f.reports.reportByID(uuid.UUID(n.ReportID.Bytes)); ok {
				st, tt := r.status, r.targetType
				row.ReportStatus, row.ReportTargetType = &st, &tt
			}
		}
		rows = append(rows, row)
	}
	lo := int(a.ResultOffset)
	if lo > len(rows) {
		lo = len(rows)
	}
	hi := lo + int(a.ResultLimit)
	if hi > len(rows) {
		hi = len(rows)
	}
	return rows[lo:hi], nil
}

func (f *notifFakeRepo) CountUnreadNotifications(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, x := range f.notifs {
		if x.UserID == userID && !x.ReadAt.Valid {
			n++
		}
	}
	return n, nil
}

func (f *notifFakeRepo) MarkNotificationRead(_ context.Context, a sqlcgen.MarkNotificationReadParams) (int64, error) {
	for i := range f.notifs {
		if f.notifs[i].ID == a.ID && f.notifs[i].UserID == a.UserID {
			f.notifs[i].ReadAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			return 1, nil
		}
	}
	return 0, nil
}

func (f *notifFakeRepo) MarkAllNotificationsRead(_ context.Context, userID uuid.UUID) error {
	for i := range f.notifs {
		if f.notifs[i].UserID == userID {
			f.notifs[i].ReadAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
	}
	return nil
}

func listNotifications(srv *Server, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/me/notifications", "", token)
}

func unreadCount(t *testing.T, srv *Server, token string) int64 {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/notifications/unread-count", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread-count = %d; body=%s", rec.Code, rec.Body.String())
	}
	var uc unreadCountResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &uc)
	return uc.UnreadCount
}

func TestFollowCreatesNotificationForOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	fanTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada/follow", "", fanTok); rec.Code != http.StatusNoContent {
		t.Fatalf("follow = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := listNotifications(srv, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body notificationListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Notifications) != 1 || body.UnreadCount != 1 {
		t.Fatalf("notifications=%d unread=%d, want 1/1; body=%s", len(body.Notifications), body.UnreadCount, rec.Body.String())
	}
	n := body.Notifications[0]
	if n.Type != "follow" || n.Read || n.ChannelHandle != "ada" {
		t.Errorf("notification = %+v, want follow/unread/channel=ada", n)
	}
	if n.Actor == nil || n.Actor.Username != "bob" {
		t.Errorf("actor = %+v, want username bob", n.Actor)
	}

	// Marking it read drops the unread count to 0.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/notifications/"+n.ID+"/read", "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("mark read = %d", rec.Code)
	}
	if got := unreadCount(t, srv, ownerTok); got != 0 {
		t.Errorf("unread after read = %d, want 0", got)
	}
	// Idempotent.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/notifications/"+n.ID+"/read", "", ownerTok); rec.Code != http.StatusNoContent {
		t.Errorf("mark read again = %d, want 204", rec.Code)
	}
}

func TestSelfFollowCreatesNoNotification(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	// The owner follows their own channel → no self-notification.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada/follow", "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("self-follow = %d", rec.Code)
	}
	if got := unreadCount(t, srv, ownerTok); got != 0 {
		t.Errorf("unread after self-follow = %d, want 0", got)
	}
}

func TestCommentCreatesNotificationForVideoOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"My clip","privacy":"public"}`)
	fanTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	if rec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"nice clip"}`, fanTok); rec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body=%s", rec.Code, rec.Body.String())
	}

	rec := listNotifications(srv, ownerTok)
	var body notificationListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Notifications) != 1 {
		t.Fatalf("notifications=%d, want 1; body=%s", len(body.Notifications), rec.Body.String())
	}
	n := body.Notifications[0]
	if n.Type != "comment" || n.VideoID != vid || n.VideoTitle != "My clip" {
		t.Errorf("notification = %+v, want comment/video=%s/title=My clip", n, vid)
	}

	// Commenting on your own video does not notify yourself.
	if rec := postJSONAuth(srv, "/api/v1/videos/"+vid+"/comments", `{"body":"my own"}`, ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("owner self-comment = %d", rec.Code)
	}
	if got := unreadCount(t, srv, ownerTok); got != 1 {
		t.Errorf("unread after self-comment = %d, want 1 (unchanged)", got)
	}

	// Mark-all clears the unread count.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/notifications/read-all", "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("read-all = %d", rec.Code)
	}
	if got := unreadCount(t, srv, ownerTok); got != 0 {
		t.Errorf("unread after read-all = %d, want 0", got)
	}
}

func TestNotificationsRequireAuth(t *testing.T) {
	srv := videoServer(t)
	someID := "00000000-0000-0000-0000-000000000000"
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me/notifications"},
		{http.MethodGet, "/api/v1/me/notifications/unread-count"},
		{http.MethodPost, "/api/v1/me/notifications/read-all"},
		{http.MethodPost, "/api/v1/me/notifications/" + someID + "/read"},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(srv, tc.method, tc.path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestMarkUnknownNotificationIs404(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/notifications/"+uuid.New().String()+"/read", "", tok); rec.Code != http.StatusNotFound {
		t.Errorf("mark unknown = %d, want 404", rec.Code)
	}
}

func getPrefs(t *testing.T, srv *Server, token string) map[string]bool {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/notification-prefs", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("get prefs = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body notificationPrefsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Prefs
}

func TestNotificationPrefsDefaultAllEnabled(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	prefs := getPrefs(t, srv, tok)
	if len(prefs) != len(notification.KnownTypes()) {
		t.Fatalf("prefs len = %d, want %d; prefs=%v", len(prefs), len(notification.KnownTypes()), prefs)
	}
	for _, typ := range notification.KnownTypes() {
		if !prefs[typ] {
			t.Errorf("prefs[%s] = false, want default enabled", typ)
		}
	}
}

func TestPatchNotificationPrefsFlipsAndValidates(t *testing.T) {
	srv := videoServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Disable follow; the response carries the full updated map.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{"follow":false}}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body notificationPrefsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Prefs["follow"] || !body.Prefs["comment"] {
		t.Errorf("prefs after patch = %v, want follow=false comment=true", body.Prefs)
	}
	// Persisted: a fresh GET agrees.
	if prefs := getPrefs(t, srv, tok); prefs["follow"] {
		t.Error("follow still enabled on a fresh GET")
	}

	// Unknown type → 422, and nothing was changed.
	rec = sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{"comment":false,"premium_offers":true}}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown type = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if prefs := getPrefs(t, srv, tok); !prefs["comment"] {
		t.Error("comment was flipped although the update contained an unknown type")
	}

	// Empty prefs → 422.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{}}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty prefs = %d, want 422", rec.Code)
	}

	// Auth required on both routes.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/notification-prefs", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon get prefs = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{"follow":true}}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon patch prefs = %d, want 401", rec.Code)
	}
}

// TestDisabledPrefSuppressesFollowNotification proves the Create-side filter
// end to end: with follow notifications disabled, a real follow creates no
// notification; re-enabling restores delivery.
func TestDisabledPrefSuppressesFollowNotification(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	fanTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// The owner turns follow notifications off.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{"follow":false}}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("patch prefs = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada/follow", "", fanTok); rec.Code != http.StatusNoContent {
		t.Fatalf("follow = %d", rec.Code)
	}
	if got := unreadCount(t, srv, ownerTok); got != 0 {
		t.Fatalf("unread after suppressed follow = %d, want 0", got)
	}

	// Re-enable → a new follow (from another account) notifies again.
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{"follow":true}}`, ownerTok); rec.Code != http.StatusOK {
		t.Fatalf("re-enable prefs = %d", rec.Code)
	}
	carolTok := registerAndToken(t, srv, `{"username":"carol","email":"carol@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada/follow", "", carolTok); rec.Code != http.StatusNoContent {
		t.Fatalf("second follow = %d", rec.Code)
	}
	if got := unreadCount(t, srv, ownerTok); got != 1 {
		t.Errorf("unread after re-enabled follow = %d, want 1", got)
	}
}
