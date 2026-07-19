package federation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository for the federation tests. Count fields feed
// NodeInfo; the maps feed actor resolution + key minting. Maps mutate in place
// (map headers survive the value-receiver copy), so a single fakeRepo value both
// serves and stores minted keys.
type fakeRepo struct {
	users, videos, comments int64
	err                     error

	usersByName     map[string]sqlcgen.GetUserActorByUsernameRow
	channels        map[string]sqlcgen.Channel
	acctKeys        map[uuid.UUID]sqlcgen.GetAccountActorKeyRow
	chanKeys        map[uuid.UUID]sqlcgen.GetChannelActorKeyRow
	remoteActors    map[string]sqlcgen.RemoteActor
	processed       map[string]bool
	remoteFollows   map[string]*fakeRemoteFollow // keyed channelID|actorURL
	followBacks     map[string]*fakeFollowBack   // keyed channelID|actorURL (W12)
	deliveries      map[uuid.UUID]*fakeDelivery
	videosByID      map[uuid.UUID]sqlcgen.GetVideoByIDRow
	channelsByID    map[uuid.UUID]sqlcgen.Channel
	followerInboxes map[uuid.UUID][]string
	localFollowers  map[uuid.UUID]int64
	remoteFollowerN map[uuid.UUID]int64
	channelVideoN   map[uuid.UUID]int64
	outboxVideos    map[uuid.UUID][]sqlcgen.ListChannelOutboxVideosRow
	remoteVideos    map[string]*fakeRemoteVideo // keyed by object_url
	blockedDomains  map[string]bool
	usersByID       map[uuid.UUID]sqlcgen.GetUserActorByIDRow
	rcFollows       map[uuid.UUID]*sqlcgen.RemoteChannelFollow // keyed by row id
	commentsByID    map[uuid.UUID]sqlcgen.Comment              // federated-comment rows (§6)
	// apDisabled marks channels whose activitypub_enabled is false (migration
	// 0096). Nil-safe: a channel not listed here reads back AP-ENABLED, matching
	// the DB column default (TRUE) so existing fixtures federate as before.
	apDisabled map[uuid.UUID]bool
}

// withAP stamps a fixture channel's activitypub_enabled from the apDisabled set
// so channels default to enabled (the DB default) and a test opts specific ones
// out via apDisabled.
func (f fakeRepo) withAP(c sqlcgen.Channel) sqlcgen.Channel {
	c.ActivitypubEnabled = !f.apDisabled[c.ID]
	return c
}

// fakeRemoteVideo is an in-memory remote_videos row.
type fakeRemoteVideo struct {
	id           uuid.UUID
	params       sqlcgen.UpsertRemoteVideoParams
	thumbnailKey *string
}

// fakeRemoteFollow is an in-memory remote_follows row (migration 0037 + the
// 0087 surrogate id/state used by the W12 follower-approval queue).
type fakeRemoteFollow struct {
	ID                uuid.UUID
	ChannelID         uuid.UUID
	RemoteActorUrl    string
	FollowActivityUrl string
	State             string // accepted | pending
	CreatedAt         time.Time
}

// fakeFollowBack is an in-memory channel_follow_backs row (migration 0087).
type fakeFollowBack struct {
	ChannelID         uuid.UUID
	RemoteActorUrl    string
	FollowActivityUrl string
	State             string // pending | accepted
}

// fakeDelivery is an in-memory federation_deliveries row.
type fakeDelivery struct {
	row         sqlcgen.ClaimDueDeliveriesRow
	state       string
	nextAttempt time.Time
	lastError   string
}

func (f fakeRepo) CountUsers(context.Context) (int64, error)        { return f.users, f.err }
func (f fakeRepo) CountPublicVideos(context.Context) (int64, error) { return f.videos, f.err }
func (f fakeRepo) CountComments(context.Context) (int64, error)     { return f.comments, f.err }

func (f fakeRepo) GetUserActorByUsername(_ context.Context, name string) (sqlcgen.GetUserActorByUsernameRow, error) {
	if u, ok := f.usersByName[strings.ToLower(name)]; ok {
		return u, nil
	}
	return sqlcgen.GetUserActorByUsernameRow{}, pgx.ErrNoRows
}

func (f fakeRepo) GetChannelByHandle(_ context.Context, handle string) (sqlcgen.Channel, error) {
	if c, ok := f.channels[strings.ToLower(handle)]; ok {
		return f.withAP(c), nil
	}
	return sqlcgen.Channel{}, pgx.ErrNoRows
}

func (f fakeRepo) GetAccountActorKey(_ context.Context, id uuid.UUID) (sqlcgen.GetAccountActorKeyRow, error) {
	if k, ok := f.acctKeys[id]; ok {
		return k, nil
	}
	return sqlcgen.GetAccountActorKeyRow{}, pgx.ErrNoRows
}

func (f fakeRepo) InsertAccountActorKeyIfAbsent(_ context.Context, arg sqlcgen.InsertAccountActorKeyIfAbsentParams) (int64, error) {
	if _, ok := f.acctKeys[arg.UserID]; ok {
		return 0, nil
	}
	f.acctKeys[arg.UserID] = sqlcgen.GetAccountActorKeyRow{PublicKeyPem: arg.PublicKeyPem, PrivateKeyPem: arg.PrivateKeyPem}
	return 1, nil
}

func (f fakeRepo) GetChannelActorKey(_ context.Context, id uuid.UUID) (sqlcgen.GetChannelActorKeyRow, error) {
	if k, ok := f.chanKeys[id]; ok {
		return k, nil
	}
	return sqlcgen.GetChannelActorKeyRow{}, pgx.ErrNoRows
}

func (f fakeRepo) InsertChannelActorKeyIfAbsent(_ context.Context, arg sqlcgen.InsertChannelActorKeyIfAbsentParams) (int64, error) {
	if _, ok := f.chanKeys[arg.ChannelID]; ok {
		return 0, nil
	}
	f.chanKeys[arg.ChannelID] = sqlcgen.GetChannelActorKeyRow{PublicKeyPem: arg.PublicKeyPem, PrivateKeyPem: arg.PrivateKeyPem}
	return 1, nil
}

func (f fakeRepo) GetRemoteActor(_ context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	if r, ok := f.remoteActors[actorURL]; ok {
		return r, nil
	}
	return sqlcgen.RemoteActor{}, pgx.ErrNoRows
}

func (f fakeRepo) UpsertRemoteActor(_ context.Context, arg sqlcgen.UpsertRemoteActorParams) error {
	f.remoteActors[arg.ActorUrl] = sqlcgen.RemoteActor{
		ActorUrl:          arg.ActorUrl,
		ActorType:         arg.ActorType,
		PreferredUsername: arg.PreferredUsername,
		Domain:            arg.Domain,
		InboxUrl:          arg.InboxUrl,
		SharedInboxUrl:    arg.SharedInboxUrl,
		PublicKeyPem:      arg.PublicKeyPem,
		FollowersUrl:      arg.FollowersUrl,
	}
	return nil
}

func (f fakeRepo) IsActivityProcessed(_ context.Context, id string) (bool, error) {
	return f.processed[id], nil
}

func (f fakeRepo) MarkActivityProcessed(_ context.Context, id string) error {
	f.processed[id] = true
	return nil
}

func (f fakeRepo) InsertRemoteFollow(_ context.Context, arg sqlcgen.InsertRemoteFollowParams) error {
	key := arg.ChannelID.String() + "|" + arg.RemoteActorUrl
	if row, ok := f.remoteFollows[key]; ok {
		row.State = "accepted"
		row.FollowActivityUrl = arg.FollowActivityUrl
		return nil
	}
	f.remoteFollows[key] = &fakeRemoteFollow{
		ID: uuid.New(), ChannelID: arg.ChannelID, RemoteActorUrl: arg.RemoteActorUrl,
		FollowActivityUrl: arg.FollowActivityUrl, State: "accepted", CreatedAt: time.Now(),
	}
	return nil
}

func (f fakeRepo) DeleteRemoteFollow(_ context.Context, arg sqlcgen.DeleteRemoteFollowParams) error {
	delete(f.remoteFollows, arg.ChannelID.String()+"|"+arg.RemoteActorUrl)
	return nil
}

// --- W12 follower-approval queue + follow-backs (migration 0087) ------------

func (f fakeRepo) InsertRemoteFollowPending(_ context.Context, arg sqlcgen.InsertRemoteFollowPendingParams) error {
	key := arg.ChannelID.String() + "|" + arg.RemoteActorUrl
	if _, ok := f.remoteFollows[key]; ok {
		return nil // ON CONFLICT DO NOTHING — existing state untouched
	}
	f.remoteFollows[key] = &fakeRemoteFollow{
		ID: uuid.New(), ChannelID: arg.ChannelID, RemoteActorUrl: arg.RemoteActorUrl,
		FollowActivityUrl: arg.FollowActivityUrl, State: "pending", CreatedAt: time.Now(),
	}
	return nil
}

func (f fakeRepo) ListPendingRemoteFollows(_ context.Context, arg sqlcgen.ListPendingRemoteFollowsParams) ([]sqlcgen.ListPendingRemoteFollowsRow, error) {
	var out []sqlcgen.ListPendingRemoteFollowsRow
	for _, row := range f.remoteFollows {
		if row.State != "pending" {
			continue
		}
		ch := f.channelsByID[row.ChannelID]
		ra := f.remoteActors[row.RemoteActorUrl]
		out = append(out, sqlcgen.ListPendingRemoteFollowsRow{
			ID: row.ID, ChannelID: row.ChannelID, RemoteActorUrl: row.RemoteActorUrl,
			FollowActivityUrl: row.FollowActivityUrl, CreatedAt: row.CreatedAt,
			ChannelHandle: ch.Handle, PreferredUsername: ra.PreferredUsername, Domain: ra.Domain,
		})
	}
	return out, nil
}

func (f fakeRepo) AcceptPendingRemoteFollowByID(_ context.Context, id uuid.UUID) (sqlcgen.AcceptPendingRemoteFollowByIDRow, error) {
	for _, row := range f.remoteFollows {
		if row.ID == id && row.State == "pending" {
			row.State = "accepted"
			return sqlcgen.AcceptPendingRemoteFollowByIDRow{
				ChannelID: row.ChannelID, RemoteActorUrl: row.RemoteActorUrl, FollowActivityUrl: row.FollowActivityUrl,
			}, nil
		}
	}
	return sqlcgen.AcceptPendingRemoteFollowByIDRow{}, pgx.ErrNoRows
}

func (f fakeRepo) DeletePendingRemoteFollowByID(_ context.Context, id uuid.UUID) (sqlcgen.DeletePendingRemoteFollowByIDRow, error) {
	for key, row := range f.remoteFollows {
		if row.ID == id && row.State == "pending" {
			delete(f.remoteFollows, key)
			return sqlcgen.DeletePendingRemoteFollowByIDRow{
				ChannelID: row.ChannelID, RemoteActorUrl: row.RemoteActorUrl, FollowActivityUrl: row.FollowActivityUrl,
			}, nil
		}
	}
	return sqlcgen.DeletePendingRemoteFollowByIDRow{}, pgx.ErrNoRows
}

func (f fakeRepo) InsertChannelFollowBackIfAbsent(_ context.Context, arg sqlcgen.InsertChannelFollowBackIfAbsentParams) (int64, error) {
	for _, fb := range f.followBacks {
		if fb.RemoteActorUrl == arg.RemoteActorUrl {
			return 0, nil // at most one follow-back per actor, instance-wide
		}
	}
	f.followBacks[arg.ChannelID.String()+"|"+arg.RemoteActorUrl] = &fakeFollowBack{
		ChannelID: arg.ChannelID, RemoteActorUrl: arg.RemoteActorUrl,
		FollowActivityUrl: arg.FollowActivityUrl, State: "pending",
	}
	return 1, nil
}

func (f fakeRepo) AcceptChannelFollowBackByActivity(_ context.Context, arg sqlcgen.AcceptChannelFollowBackByActivityParams) (int64, error) {
	for _, fb := range f.followBacks {
		if fb.FollowActivityUrl == arg.FollowActivityUrl && fb.RemoteActorUrl == arg.RemoteActorUrl {
			fb.State = "accepted"
			return 1, nil
		}
	}
	return 0, nil
}

func (f fakeRepo) DeleteChannelFollowBackByActivity(_ context.Context, arg sqlcgen.DeleteChannelFollowBackByActivityParams) (int64, error) {
	for key, fb := range f.followBacks {
		if fb.FollowActivityUrl == arg.FollowActivityUrl && fb.RemoteActorUrl == arg.RemoteActorUrl {
			delete(f.followBacks, key)
			return 1, nil
		}
	}
	return 0, nil
}

func (f fakeRepo) HasRemoteChannelFollow(_ context.Context, remoteActorURL string) (bool, error) {
	for _, row := range f.rcFollows {
		if row.RemoteActorUrl == remoteActorURL {
			return true, nil
		}
	}
	return false, nil
}

func (f fakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	if v, ok := f.videosByID[id]; ok {
		return v, nil
	}
	return sqlcgen.GetVideoByIDRow{}, pgx.ErrNoRows
}

func (f fakeRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	if c, ok := f.channelsByID[id]; ok {
		return f.withAP(c), nil
	}
	return sqlcgen.Channel{}, pgx.ErrNoRows
}

func (f fakeRepo) ListRemoteFollowerInboxes(_ context.Context, channelID uuid.UUID) ([]string, error) {
	return f.followerInboxes[channelID], nil
}

func (f fakeRepo) CountChannelFollowers(_ context.Context, id uuid.UUID) (int64, error) {
	return f.localFollowers[id], nil
}
func (f fakeRepo) CountRemoteFollowers(_ context.Context, id uuid.UUID) (int64, error) {
	return f.remoteFollowerN[id], nil
}
func (f fakeRepo) CountPublicVideosByChannel(_ context.Context, id uuid.UUID) (int64, error) {
	return f.channelVideoN[id], nil
}
func (f fakeRepo) ListChannelOutboxVideos(_ context.Context, arg sqlcgen.ListChannelOutboxVideosParams) ([]sqlcgen.ListChannelOutboxVideosRow, error) {
	all := f.outboxVideos[arg.ChannelID]
	lo := int(arg.Offset)
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + int(arg.Limit)
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], nil
}

func (f fakeRepo) EnqueueDelivery(_ context.Context, arg sqlcgen.EnqueueDeliveryParams) error {
	id := uuid.New()
	f.deliveries[id] = &fakeDelivery{
		row: sqlcgen.ClaimDueDeliveriesRow{
			ID:                   id,
			InboxUrl:             arg.InboxUrl,
			Payload:              arg.Payload,
			SigningChannelID:     arg.SigningChannelID,
			SigningChannelHandle: arg.SigningChannelHandle,
			SigningUserID:        arg.SigningUserID,
			SigningUsername:      arg.SigningUsername,
		},
		state: "pending",
	}
	return nil
}

func (f fakeRepo) ClaimDueDeliveries(_ context.Context, limit int32) ([]sqlcgen.ClaimDueDeliveriesRow, error) {
	var out []sqlcgen.ClaimDueDeliveriesRow
	for _, d := range f.deliveries {
		if d.state == "pending" && !d.nextAttempt.After(time.Now()) {
			out = append(out, d.row)
			if len(out) >= int(limit) {
				break
			}
		}
	}
	return out, nil
}

func (f fakeRepo) MarkDeliveryDelivered(_ context.Context, id uuid.UUID) error {
	if d, ok := f.deliveries[id]; ok {
		d.state = "delivered"
	}
	return nil
}

func (f fakeRepo) RescheduleDelivery(_ context.Context, arg sqlcgen.RescheduleDeliveryParams) error {
	if d, ok := f.deliveries[arg.ID]; ok {
		d.row.Attempts++
		d.nextAttempt = arg.NextAttemptAt
	}
	return nil
}

func (f fakeRepo) FailDelivery(_ context.Context, arg sqlcgen.FailDeliveryParams) error {
	if d, ok := f.deliveries[arg.ID]; ok {
		d.row.Attempts++
		d.state = "failed"
		d.lastError = arg.LastError
	}
	return nil
}

func (f fakeRepo) UpsertRemoteVideo(_ context.Context, arg sqlcgen.UpsertRemoteVideoParams) (sqlcgen.UpsertRemoteVideoRow, error) {
	if rv, ok := f.remoteVideos[arg.ObjectUrl]; ok {
		rv.params = arg
		return sqlcgen.UpsertRemoteVideoRow{ID: rv.id, ThumbnailKey: rv.thumbnailKey}, nil
	}
	rv := &fakeRemoteVideo{id: uuid.New(), params: arg}
	f.remoteVideos[arg.ObjectUrl] = rv
	return sqlcgen.UpsertRemoteVideoRow{ID: rv.id}, nil
}

func (f fakeRepo) SetRemoteVideoThumbnail(_ context.Context, arg sqlcgen.SetRemoteVideoThumbnailParams) error {
	for _, rv := range f.remoteVideos {
		if rv.id == arg.ID {
			rv.thumbnailKey = arg.ThumbnailKey
		}
	}
	return nil
}

func (f fakeRepo) IsInstanceBlocked(_ context.Context, domain string) (bool, error) {
	return f.blockedDomains[domain], nil
}

func (f fakeRepo) GetUserActorByID(_ context.Context, id uuid.UUID) (sqlcgen.GetUserActorByIDRow, error) {
	if u, ok := f.usersByID[id]; ok {
		return u, nil
	}
	return sqlcgen.GetUserActorByIDRow{}, pgx.ErrNoRows
}

func (f fakeRepo) UpsertRemoteChannelFollow(_ context.Context, arg sqlcgen.UpsertRemoteChannelFollowParams) (sqlcgen.RemoteChannelFollow, error) {
	for _, row := range f.rcFollows {
		if row.UserID == arg.UserID && row.RemoteActorUrl == arg.RemoteActorUrl {
			return *row, nil // conflict: keep the existing row untouched
		}
	}
	row := &sqlcgen.RemoteChannelFollow{
		ID:                uuid.New(),
		UserID:            arg.UserID,
		RemoteActorUrl:    arg.RemoteActorUrl,
		State:             "pending",
		FollowActivityUrl: arg.FollowActivityUrl,
		CreatedAt:         time.Now(),
	}
	f.rcFollows[row.ID] = row
	return *row, nil
}

func (f fakeRepo) GetRemoteChannelFollowByID(_ context.Context, arg sqlcgen.GetRemoteChannelFollowByIDParams) (sqlcgen.GetRemoteChannelFollowByIDRow, error) {
	row, ok := f.rcFollows[arg.ID]
	if !ok || row.UserID != arg.UserID {
		return sqlcgen.GetRemoteChannelFollowByIDRow{}, pgx.ErrNoRows
	}
	ra := f.remoteActors[row.RemoteActorUrl]
	inbox := ra.InboxUrl
	if ra.SharedInboxUrl != nil && *ra.SharedInboxUrl != "" {
		inbox = *ra.SharedInboxUrl
	}
	return sqlcgen.GetRemoteChannelFollowByIDRow{
		ID: row.ID, UserID: row.UserID, RemoteActorUrl: row.RemoteActorUrl,
		State: row.State, FollowActivityUrl: row.FollowActivityUrl, CreatedAt: row.CreatedAt,
		PreferredUsername: ra.PreferredUsername, Domain: ra.Domain, InboxUrl: inbox,
	}, nil
}

func (f fakeRepo) ListRemoteChannelFollows(_ context.Context, arg sqlcgen.ListRemoteChannelFollowsParams) ([]sqlcgen.ListRemoteChannelFollowsRow, error) {
	var out []sqlcgen.ListRemoteChannelFollowsRow
	for _, row := range f.rcFollows {
		if row.UserID != arg.UserID {
			continue
		}
		ra := f.remoteActors[row.RemoteActorUrl]
		out = append(out, sqlcgen.ListRemoteChannelFollowsRow{
			ID: row.ID, RemoteActorUrl: row.RemoteActorUrl, State: row.State,
			CreatedAt: row.CreatedAt, PreferredUsername: ra.PreferredUsername, Domain: ra.Domain,
		})
	}
	return out, nil
}

func (f fakeRepo) DeleteRemoteChannelFollowByID(_ context.Context, arg sqlcgen.DeleteRemoteChannelFollowByIDParams) (int64, error) {
	if row, ok := f.rcFollows[arg.ID]; ok && row.UserID == arg.UserID {
		delete(f.rcFollows, arg.ID)
		return 1, nil
	}
	return 0, nil
}

func (f fakeRepo) AcceptRemoteChannelFollowByActivity(_ context.Context, arg sqlcgen.AcceptRemoteChannelFollowByActivityParams) (int64, error) {
	for _, row := range f.rcFollows {
		if row.FollowActivityUrl == arg.FollowActivityUrl && row.RemoteActorUrl == arg.RemoteActorUrl {
			row.State = "accepted"
			return 1, nil
		}
	}
	return 0, nil
}

func (f fakeRepo) DeleteRemoteChannelFollowByActivity(_ context.Context, arg sqlcgen.DeleteRemoteChannelFollowByActivityParams) (int64, error) {
	for id, row := range f.rcFollows {
		if row.FollowActivityUrl == arg.FollowActivityUrl && row.RemoteActorUrl == arg.RemoteActorUrl {
			delete(f.rcFollows, id)
			return 1, nil
		}
	}
	return 0, nil
}

func (f fakeRepo) HasAcceptedRemoteChannelFollow(_ context.Context, remoteActorURL string) (bool, error) {
	for _, row := range f.rcFollows {
		if row.RemoteActorUrl == remoteActorURL && row.State == "accepted" {
			return true, nil
		}
	}
	// Mirrors the SQL: an accepted channel follow-back (W12) is an ingestion
	// edge too — the instance asked for that content by following back.
	for _, fb := range f.followBacks {
		if fb.RemoteActorUrl == remoteActorURL && fb.State == "accepted" {
			return true, nil
		}
	}
	return false, nil
}

// --- federated comments + inbound deletes (remote-content §6-7) -------------

func (f fakeRepo) GetComment(_ context.Context, id uuid.UUID) (sqlcgen.Comment, error) {
	if c, ok := f.commentsByID[id]; ok {
		return c, nil
	}
	return sqlcgen.Comment{}, pgx.ErrNoRows
}

func (f fakeRepo) GetCommentByRemoteObjectURL(_ context.Context, u string) (sqlcgen.Comment, error) {
	for _, c := range f.commentsByID {
		if c.RemoteObjectUrl != nil && *c.RemoteObjectUrl == u {
			return c, nil
		}
	}
	return sqlcgen.Comment{}, pgx.ErrNoRows
}

func (f fakeRepo) CreateRemoteComment(_ context.Context, arg sqlcgen.CreateRemoteCommentParams) (sqlcgen.Comment, error) {
	if arg.RemoteObjectUrl != nil {
		for id, c := range f.commentsByID {
			if c.RemoteObjectUrl != nil && *c.RemoteObjectUrl == *arg.RemoteObjectUrl {
				c.Body = arg.Body
				c.UpdatedAt = time.Now()
				f.commentsByID[id] = c
				return c, nil
			}
		}
	}
	c := sqlcgen.Comment{
		ID: uuid.New(), VideoID: arg.VideoID, Body: arg.Body, ParentID: arg.ParentID,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		RemoteActorUrl: arg.RemoteActorUrl, RemoteAuthorName: arg.RemoteAuthorName,
		RemoteObjectUrl: arg.RemoteObjectUrl,
	}
	f.commentsByID[c.ID] = c
	return c, nil
}

func (f fakeRepo) UpdateComment(_ context.Context, arg sqlcgen.UpdateCommentParams) (sqlcgen.Comment, error) {
	c, ok := f.commentsByID[arg.ID]
	if !ok {
		return sqlcgen.Comment{}, pgx.ErrNoRows
	}
	c.Body = arg.Body
	c.UpdatedAt = time.Now()
	f.commentsByID[arg.ID] = c
	return c, nil
}

func (f fakeRepo) DeleteComment(_ context.Context, id uuid.UUID) error {
	delete(f.commentsByID, id)
	return nil
}

func (f fakeRepo) GetRemoteVideoByObjectURL(_ context.Context, objectURL string) (sqlcgen.GetRemoteVideoByObjectURLRow, error) {
	if rv, ok := f.remoteVideos[objectURL]; ok {
		return sqlcgen.GetRemoteVideoByObjectURLRow{ID: rv.id, ObjectUrl: objectURL, RemoteActorUrl: rv.params.RemoteActorUrl}, nil
	}
	return sqlcgen.GetRemoteVideoByObjectURLRow{}, pgx.ErrNoRows
}

func (f fakeRepo) DeleteRemoteVideoByObjectURL(_ context.Context, objectURL string) (int64, error) {
	if _, ok := f.remoteVideos[objectURL]; ok {
		delete(f.remoteVideos, objectURL)
		return 1, nil
	}
	return 0, nil
}

func (f fakeRepo) DeleteRemoteActor(_ context.Context, actorURL string) (int64, error) {
	if _, ok := f.remoteActors[actorURL]; !ok {
		return 0, nil
	}
	delete(f.remoteActors, actorURL)
	// Mirror the FK cascades: the actor's videos, comments, and follow edges.
	for u, rv := range f.remoteVideos {
		if rv.params.RemoteActorUrl == actorURL {
			delete(f.remoteVideos, u)
		}
	}
	for id, c := range f.commentsByID {
		if c.RemoteActorUrl != nil && *c.RemoteActorUrl == actorURL {
			delete(f.commentsByID, id)
		}
	}
	for id, row := range f.rcFollows {
		if row.RemoteActorUrl == actorURL {
			delete(f.rcFollows, id)
		}
	}
	return 1, nil
}

func TestNodeInfoUsage(t *testing.T) {
	svc := NewService(fakeRepo{users: 7, videos: 3, comments: 11})
	got, err := svc.NodeInfoUsage(context.Background())
	if err != nil {
		t.Fatalf("NodeInfoUsage: %v", err)
	}
	want := NodeInfoUsage{Users: 7, LocalPosts: 3, LocalComments: 11}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

func TestNodeInfoUsageErrorPropagates(t *testing.T) {
	sentinel := errors.New("db down")
	_, err := NewService(fakeRepo{err: sentinel}).NodeInfoUsage(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
