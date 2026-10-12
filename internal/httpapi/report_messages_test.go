package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/moderation"
	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// modMessageRow mirrors a report_messages row (0163) in the moderation fake.
type modMessageRow struct {
	id, reportID, authorID uuid.UUID
	byModerator            bool
	body                   string
	createdAt              time.Time
}

func (f *moderationFakeRepo) GetReportReporterID(_ context.Context, id uuid.UUID) (pgtype.UUID, error) {
	r, ok := f.reportByID(id)
	if !ok {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	// A remote Flag report stores no reporter_id (0153).
	return pgtype.UUID{Bytes: r.reporterID, Valid: r.remoteActor == ""}, nil
}

func (f *moderationFakeRepo) CreateReportMessage(_ context.Context, a sqlcgen.CreateReportMessageParams) (sqlcgen.CreateReportMessageRow, error) {
	m := modMessageRow{id: uuid.New(), reportID: a.ReportID, authorID: a.AuthorID, byModerator: a.IsModerator,
		body: a.Body, createdAt: time.Now()}
	f.messages = append(f.messages, m)
	return sqlcgen.CreateReportMessageRow{ID: m.id, CreatedAt: m.createdAt}, nil
}

func (f *moderationFakeRepo) ListReportMessages(_ context.Context, a sqlcgen.ListReportMessagesParams) ([]sqlcgen.ListReportMessagesRow, error) {
	var rows []sqlcgen.ListReportMessagesRow
	for _, m := range f.messages { // insertion order = oldest first
		if m.reportID != a.ReportID {
			continue
		}
		row := sqlcgen.ListReportMessagesRow{ID: m.id, AuthorID: pgtype.UUID{Bytes: m.authorID, Valid: true},
			IsModerator: m.byModerator, Body: m.body, CreatedAt: m.createdAt}
		for _, u := range f.auth.users {
			if u.ID == m.authorID {
				row.AuthorUsername = u.Username
			}
		}
		rows = append(rows, row)
	}
	rows = rows[min(int(a.ResultOffset), len(rows)):]
	return rows[:min(int(a.ResultLimit), len(rows))], nil
}

func (f *moderationFakeRepo) CountReportMessages(ctx context.Context, reportID uuid.UUID) (int64, error) {
	rows, err := f.ListReportMessages(ctx, sqlcgen.ListReportMessagesParams{ReportID: reportID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

// NotifyReportMessage mirrors the shape of the real set-based fan-out (0163)
// closely enough to prove the HTTP wiring: a moderator's message reaches the
// local reporter with no actor, the reporter's reaches every active staff
// member, never the author, honouring the report_message preference and one
// notification per (user, message). The statement itself is proved in
// store.TestReportMessageFanOutOnRealPG.
func (f *notifFakeRepo) NotifyReportMessage(_ context.Context, messageID uuid.UUID) (int64, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	var msg modMessageRow
	for _, m := range f.reports.messages {
		if m.id == messageID {
			msg = m
		}
	}
	r, ok := f.reports.reportByID(msg.reportID)
	if !ok {
		return 0, nil
	}
	var n int64
	for _, u := range f.auth.users {
		recipient := u.Role == "admin" || u.Role == "moderator"
		if msg.byModerator {
			recipient = r.remoteActor == "" && u.ID == r.reporterID
		}
		if !recipient || u.ID == msg.authorID || !u.IsActive || u.DeletedAt.Valid {
			continue
		}
		if enabled, ok := f.prefs[notifPrefKey(u.ID, notification.TypeReportMessage)]; ok && !enabled {
			continue
		}
		f.notifs = append(f.notifs, sqlcgen.Notification{
			ID: uuid.New(), UserID: u.ID, Type: notification.TypeReportMessage,
			ActorID:         pgtype.UUID{Bytes: msg.authorID, Valid: !msg.byModerator},
			ReportID:        pgtype.UUID{Bytes: r.id, Valid: true},
			ReportMessageID: pgtype.UUID{Bytes: msg.id, Valid: true},
			CreatedAt:       time.Now(),
		})
		n++
	}
	return n, nil
}

// reportThreadFixture is a filed video report plus every kind of caller the
// thread's access rule distinguishes.
type reportThreadFixture struct {
	srv                                *Server
	notifRepo                          *notifFakeRepo
	reportID                           string
	admin, mod, reporter, other, modID string
}

func newReportThreadFixture(t *testing.T) reportThreadFixture {
	t.Helper()
	srv, _, _, notifRepo := videoServerEnv(t, testConfig())
	fx := reportThreadFixture{srv: srv, notifRepo: notifRepo}
	// The first registered account ("ada") becomes admin.
	fx.admin = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, fx.admin, "ada", `{"title":"Clip","privacy":"public"}`)
	fx.mod, fx.modID = registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+fx.modID, `{"role":"moderator"}`, fx.admin); rec.Code != http.StatusOK {
		t.Fatalf("promote mia = %d; body=%s", rec.Code, rec.Body.String())
	}
	fx.reporter = registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	fx.other = registerAndToken(t, srv, `{"username":"pat","email":"pat@example.test","password":"supersecret"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+vid+"/report", `{"reason":"spam"}`, fx.reporter); rec.Code != http.StatusNoContent {
		t.Fatalf("report = %d; body=%s", rec.Code, rec.Body.String())
	}
	var open reportListResponse
	_ = json.Unmarshal(listReports(srv, "?status=open", fx.admin).Body.Bytes(), &open)
	if len(open.Reports) != 1 {
		t.Fatalf("open reports = %d, want 1", len(open.Reports))
	}
	fx.reportID = open.Reports[0].ID
	return fx
}

func (fx reportThreadFixture) thread(t *testing.T, token string) reportMessageListResponse {
	t.Helper()
	rec := sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/reports/"+fx.reportID+"/messages", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("thread = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out reportMessageListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func (fx reportThreadFixture) post(token, body string) int {
	return sendJSONAuth(fx.srv, http.MethodPost, "/api/v1/reports/"+fx.reportID+"/messages", body, token).Code
}

// The authorization matrix: the report's reporter and staff who can act on
// reports (admin, moderator) may read and write the thread; any other user gets
// 404 — identical to an unknown report, so a report's existence never leaks —
// and an anonymous caller 401.
func TestReportMessagesAuthzMatrix(t *testing.T) {
	fx := newReportThreadFixture(t)
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"reporter", fx.reporter, http.StatusCreated},
		{"moderator", fx.mod, http.StatusCreated},
		{"admin", fx.admin, http.StatusCreated},
		{"another user", fx.other, http.StatusNotFound},
		{"anonymous", "", http.StatusUnauthorized},
	} {
		if got := fx.post(tc.token, `{"body":"from the `+tc.name+`"}`); got != tc.want {
			t.Errorf("%s POST = %d, want %d", tc.name, got, tc.want)
		}
		wantGet := tc.want
		if wantGet == http.StatusCreated {
			wantGet = http.StatusOK
		}
		if got := sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/reports/"+fx.reportID+"/messages", "", tc.token).Code; got != wantGet {
			t.Errorf("%s GET = %d, want %d", tc.name, got, wantGet)
		}
	}

	// The 404 for another user is the 404 for no report at all (only the
	// per-request id differs).
	errorOf := func(path string) (int, string) {
		rec := sendJSONAuth(fx.srv, http.MethodGet, path, "", fx.other)
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return rec.Code, e.Error.Code + "/" + e.Error.Message
	}
	sCode, sErr := errorOf("/api/v1/reports/" + fx.reportID + "/messages")
	uCode, uErr := errorOf("/api/v1/reports/" + uuid.NewString() + "/messages")
	if sCode != http.StatusNotFound || uCode != http.StatusNotFound || sErr != uErr {
		t.Errorf("stranger = %d %q, unknown = %d %q; want identical 404s", sCode, sErr, uCode, uErr)
	}
	for _, tok := range []string{fx.reporter, fx.admin} {
		if got := sendJSONAuth(fx.srv, http.MethodGet, "/api/v1/reports/"+uuid.NewString()+"/messages", "", tok).Code; got != http.StatusNotFound {
			t.Errorf("unknown report GET = %d, want 404", got)
		}
		if got := sendJSONAuth(fx.srv, http.MethodPost, "/api/v1/reports/not-a-uuid/messages", `{"body":"x"}`, tok).Code; got != http.StatusNotFound {
			t.Errorf("malformed id POST = %d, want 404", got)
		}
	}

	// Both sides see the same three messages, oldest first. Staff see who wrote
	// each one; the reporter never learns which moderator wrote.
	staffView := fx.thread(t, fx.mod)
	if staffView.Total != 3 || len(staffView.Messages) != 3 || !staffView.CanReply {
		t.Fatalf("staff thread = %+v, want 3 messages and can_reply", staffView)
	}
	if m := staffView.Messages[0]; m.ByModerator || m.AuthorUsername != "bob" || m.Body != "from the reporter" {
		t.Errorf("first message = %+v, want bob's reporter-side message", m)
	}
	if m := staffView.Messages[1]; !m.ByModerator || m.AuthorUsername != "mia" || !m.IsMine {
		t.Errorf("second message (mia's own view) = %+v, want moderator-side, by mia, is_mine", m)
	}
	reporterView := fx.thread(t, fx.reporter)
	for i, m := range reporterView.Messages {
		if m.AuthorUsername != "" {
			t.Errorf("reporter view message %d names author %q; the reporter never sees who moderated", i, m.AuthorUsername)
		}
		if m.IsMine != (i == 0) || m.ByModerator != (i != 0) {
			t.Errorf("reporter view message %d = %+v, want only the first to be theirs", i, m)
		}
	}

	// Validation: empty, whitespace-only and over-long bodies are refused.
	for _, body := range []string{`{"body":""}`, `{"body":"   "}`, `{"body":"` + strings.Repeat("é", 3001) + `"}`} {
		if got := fx.post(fx.reporter, body); got != http.StatusUnprocessableEntity {
			t.Errorf("POST %.20s… = %d, want 422", body, got)
		}
	}
	if got := fx.post(fx.reporter, `{"body":"`+strings.Repeat("é", 3000)+`"}`); got != http.StatusCreated {
		t.Errorf("3000-character body = %d, want 201 (the limit counts characters, not bytes)", got)
	}
}

// Each new message notifies the OTHER side — the reporter when staff write,
// every staff member when the reporter writes — with no body in the
// notification, never the author, honouring the opt-out, and never failing the
// message when the notification store is down.
func TestReportMessageNotifications(t *testing.T) {
	fx := newReportThreadFixture(t)
	before := map[string]int64{"admin": unreadCount(t, fx.srv, fx.admin), "mod": unreadCount(t, fx.srv, fx.mod),
		"reporter": unreadCount(t, fx.srv, fx.reporter), "other": unreadCount(t, fx.srv, fx.other)}
	delta := func(name, token string) int64 { return unreadCount(t, fx.srv, token) - before[name] }

	const secret = "the-secret-body-text"
	if got := fx.post(fx.reporter, `{"body":"`+secret+` from bob"}`); got != http.StatusCreated {
		t.Fatalf("reporter POST = %d", got)
	}
	if delta("admin", fx.admin) != 1 || delta("mod", fx.mod) != 1 || delta("reporter", fx.reporter) != 0 || delta("other", fx.other) != 0 {
		t.Errorf("after the reporter wrote: admin+%d mod+%d reporter+%d other+%d; want staff +1 each, nobody else",
			delta("admin", fx.admin), delta("mod", fx.mod), delta("reporter", fx.reporter), delta("other", fx.other))
	}
	rec := listNotifications(fx.srv, fx.mod)
	var staffList notificationListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &staffList)
	if n := staffList.Notifications[0]; n.Type != "report_message" || n.ReportID != fx.reportID || n.Actor == nil || n.Actor.Username != "bob" {
		t.Errorf("staff notification = %+v, want report_message on %s with the reporter as actor", n, fx.reportID)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Error("a notification payload carried the message body")
	}

	if got := fx.post(fx.mod, `{"body":"`+secret+` from mia"}`); got != http.StatusCreated {
		t.Fatalf("moderator POST = %d", got)
	}
	if delta("reporter", fx.reporter) != 1 || delta("admin", fx.admin) != 1 || delta("mod", fx.mod) != 1 {
		t.Errorf("after mia wrote: reporter+%d admin+%d mod+%d; want only the reporter told",
			delta("reporter", fx.reporter), delta("admin", fx.admin), delta("mod", fx.mod))
	}
	rec = listNotifications(fx.srv, fx.reporter)
	var reporterList notificationListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &reporterList)
	if n := reporterList.Notifications[0]; n.Type != "report_message" || n.ReportID != fx.reportID || n.Actor != nil {
		t.Errorf("reporter notification = %+v, want report_message with no actor (the moderator is never named)", n)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Error("the reporter's notification payload carried the message body")
	}

	// The reporter turns report_message off: staff messages no longer reach them.
	if rec := sendJSONAuth(fx.srv, http.MethodPatch, "/api/v1/me/notification-prefs", `{"prefs":{"report_message":false}}`, fx.reporter); rec.Code != http.StatusOK {
		t.Fatalf("disable report_message = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := fx.post(fx.admin, `{"body":"anything else?"}`); got != http.StatusCreated {
		t.Fatalf("admin POST = %d", got)
	}
	if delta("reporter", fx.reporter) != 1 {
		t.Errorf("opted-out reporter unread +%d, want still +1", delta("reporter", fx.reporter))
	}

	// Best-effort: a failing notification store never fails the message.
	fx.notifRepo.createErr = errors.New("notification store down")
	if got := fx.post(fx.reporter, `{"body":"still here"}`); got != http.StatusCreated {
		t.Errorf("POST with failing notifier = %d, want 201", got)
	}
	if th := fx.thread(t, fx.reporter); th.Total != 4 {
		t.Errorf("thread total = %d, want 4 (the message persisted)", th.Total)
	}
}

// Staff messages are audited by id; the reporter's are not, and no log line —
// audit or warning — ever carries a message body.
func TestReportMessageAuditCarriesIDsOnly(t *testing.T) {
	fx := newReportThreadFixture(t)
	var buf bytes.Buffer
	fx.srv.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	fx.notifRepo.createErr = errors.New("notification store down") // also exercise the warning path

	const secret = "do-not-log-this-body"
	if got := fx.post(fx.reporter, `{"body":"`+secret+` (reporter)"}`); got != http.StatusCreated {
		t.Fatalf("reporter POST = %d", got)
	}
	if ev := findAudit(auditEvents(t, &buf), observability.ActionReportMessage, observability.ResultSuccess); ev != nil {
		t.Errorf("a reporter's message was audited as staff activity: %v", ev)
	}
	rec := sendJSONAuth(fx.srv, http.MethodPost, "/api/v1/reports/"+fx.reportID+"/messages", `{"body":"`+secret+` (staff)"}`, fx.mod)
	if rec.Code != http.StatusCreated {
		t.Fatalf("moderator POST = %d", rec.Code)
	}
	var msg reportMessageView
	_ = json.Unmarshal(rec.Body.Bytes(), &msg)
	ev := findAudit(auditEvents(t, &buf), observability.ActionReportMessage, observability.ResultSuccess)
	if ev == nil {
		t.Fatalf("no %s audit event for a staff message", observability.ActionReportMessage)
	}
	if ev["actor_id"] != fx.modID || ev["resource_id"] != fx.reportID || ev["reason"] != "message="+msg.ID {
		t.Errorf("audit = %v, want actor %s, resource %s, reason message=%s", ev, fx.modID, fx.reportID, msg.ID)
	}
	if strings.Contains(buf.String(), secret) {
		t.Errorf("a log line carried the message body:\n%s", buf.String())
	}
}

// A report another instance filed (a remote Flag) has no local reporter: staff
// can read its thread, which says it cannot be replied to, but nobody can post
// to it — and to everyone else it is a 404 like any report.
func TestRemoteReportThreadIsReadOnly(t *testing.T) {
	fx := newReportThreadFixture(t)
	vid := createPublishedVideo(t, fx.srv, fx.admin, "ada", `{"title":"Other","privacy":"public"}`)
	remoteID, err := moderation.NewService(fx.notifRepo.reports).ReportFromRemote(context.Background(), moderation.RemoteReport{
		FlagActivityID: "https://peertube.example/admin/abuses/9", ActorURL: "https://peertube.example/accounts/peertube",
		Domain: "peertube.example", TargetType: moderation.TargetVideo, TargetID: uuid.MustParse(vid), Reason: "spam",
	})
	if err != nil {
		t.Fatalf("ReportFromRemote: %v", err)
	}
	path := "/api/v1/reports/" + remoteID.String() + "/messages"
	rec := sendJSONAuth(fx.srv, http.MethodGet, path, "", fx.mod)
	var th reportMessageListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &th)
	if rec.Code != http.StatusOK || th.CanReply || th.Total != 0 || th.Messages == nil {
		t.Errorf("staff GET remote thread = %d %+v, want 200, an empty list and can_reply=false", rec.Code, th)
	}
	if rec := sendJSONAuth(fx.srv, http.MethodPost, path, `{"body":"hello?"}`, fx.admin); rec.Code != http.StatusConflict {
		t.Errorf("staff POST to remote report = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if got := sendJSONAuth(fx.srv, http.MethodGet, path, "", fx.reporter).Code; got != http.StatusNotFound {
		t.Errorf("non-staff GET remote thread = %d, want 404", got)
	}
}

// Every reporter message notifies every staff member, so posting carries its
// own per-USER budget on top of the general per-IP one: past it the post is a
// 429, nothing is stored, and staff are told about exactly the posts that got
// through.
func TestReportMessagePostsAreRateLimitedPerUser(t *testing.T) {
	fx := newReportThreadFixture(t)
	const budget = 3
	fx.srv.reportMessageLimit = ratelimit.NewLimiter(ratelimit.NewMemoryCounter(), budget, time.Hour)
	before := unreadCount(t, fx.srv, fx.mod)
	for i := 0; i < budget; i++ {
		if got := fx.post(fx.reporter, `{"body":"again"}`); got != http.StatusCreated {
			t.Fatalf("post %d = %d, want 201", i+1, got)
		}
	}
	rec := sendJSONAuth(fx.srv, http.MethodPost, "/api/v1/reports/"+fx.reportID+"/messages", `{"body":"one too many"}`, fx.reporter)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("post %d = %d (Retry-After %q), want 429 with Retry-After", budget+1, rec.Code, rec.Header().Get("Retry-After"))
	}
	if got := unreadCount(t, fx.srv, fx.mod) - before; got != budget {
		t.Errorf("staff were told about %d messages, want %d (the refused post notifies nobody)", got, budget)
	}
	if th := fx.thread(t, fx.mod); th.Total != budget {
		t.Errorf("thread holds %d messages, want %d", th.Total, budget)
	}
	// The budget is the reporter's, not the report's: staff can still answer.
	if got := fx.post(fx.mod, `{"body":"slow down please"}`); got != http.StatusCreated {
		t.Errorf("moderator post after the reporter's budget ran out = %d, want 201", got)
	}
}

// PostgreSQL text cannot hold a NUL byte; the body is refused as a validation
// error rather than reaching the database as a 500.
func TestReportMessageRejectsNULByte(t *testing.T) {
	fx := newReportThreadFixture(t)
	if got := fx.post(fx.reporter, `{"body":"before\u0000after"}`); got != http.StatusUnprocessableEntity {
		t.Errorf("NUL in body = %d, want 422", got)
	}
}
