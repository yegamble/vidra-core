// Package federation implements Vidra's ActivityPub federation surface. It
// currently provides NodeInfo discovery (Slice 1) and the actor identity layer —
// local Person/Group actors, lazily-minted keypairs, and WebFinger (Slice 2).
// Signatures, inbox/outbox and delivery land in later slices. See
// .ralph/specs/federation.md for the full design and slice plan.
package federation

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/secretbox"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Repository is the persistence surface the federation service needs. It is
// satisfied by *sqlcgen.Queries; tests use a small fake.
type Repository interface {
	// NodeInfo usage counts.
	CountUsers(ctx context.Context) (int64, error)
	CountPublicVideos(ctx context.Context) (int64, error)
	CountComments(ctx context.Context) (int64, error)
	// Actor resolution + lazily-minted keypairs.
	GetUserActorByUsername(ctx context.Context, username string) (sqlcgen.GetUserActorByUsernameRow, error)
	GetChannelByHandle(ctx context.Context, handle string) (sqlcgen.Channel, error)
	GetAccountActorKey(ctx context.Context, userID uuid.UUID) (sqlcgen.GetAccountActorKeyRow, error)
	InsertAccountActorKeyIfAbsent(ctx context.Context, arg sqlcgen.InsertAccountActorKeyIfAbsentParams) (int64, error)
	GetChannelActorKey(ctx context.Context, channelID uuid.UUID) (sqlcgen.GetChannelActorKeyRow, error)
	InsertChannelActorKeyIfAbsent(ctx context.Context, arg sqlcgen.InsertChannelActorKeyIfAbsentParams) (int64, error)
	// Remote-actor cache (Slice 3b).
	GetRemoteActor(ctx context.Context, actorURL string) (sqlcgen.RemoteActor, error)
	UpsertRemoteActor(ctx context.Context, arg sqlcgen.UpsertRemoteActorParams) error
	// Inbound activities (Slice 4a).
	IsActivityProcessed(ctx context.Context, activityID string) (bool, error)
	MarkActivityProcessed(ctx context.Context, activityID string) error
	InsertRemoteFollow(ctx context.Context, arg sqlcgen.InsertRemoteFollowParams) error
	DeleteRemoteFollow(ctx context.Context, arg sqlcgen.DeleteRemoteFollowParams) error
	// Outbox fan-out (Slice 5b).
	GetVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	GetChannelByID(ctx context.Context, id uuid.UUID) (sqlcgen.Channel, error)
	ListRemoteFollowerInboxes(ctx context.Context, channelID uuid.UUID) ([]string, error)
	// Actor collections (Slice 5c).
	CountChannelFollowers(ctx context.Context, channelID uuid.UUID) (int64, error)
	CountRemoteFollowers(ctx context.Context, channelID uuid.UUID) (int64, error)
	CountPublicVideosByChannel(ctx context.Context, channelID uuid.UUID) (int64, error)
	ListChannelOutboxVideos(ctx context.Context, arg sqlcgen.ListChannelOutboxVideosParams) ([]sqlcgen.ListChannelOutboxVideosRow, error)
	// Outbound delivery queue (Slice 5a).
	EnqueueDelivery(ctx context.Context, arg sqlcgen.EnqueueDeliveryParams) error
	ClaimDueDeliveries(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueDeliveriesRow, error)
	MarkDeliveryDelivered(ctx context.Context, id uuid.UUID) error
	RescheduleDelivery(ctx context.Context, arg sqlcgen.RescheduleDeliveryParams) error
	FailDelivery(ctx context.Context, arg sqlcgen.FailDeliveryParams) error
	// Remote-video ingestion (remote-content Slice 1).
	UpsertRemoteVideo(ctx context.Context, arg sqlcgen.UpsertRemoteVideoParams) (sqlcgen.UpsertRemoteVideoRow, error)
	SetRemoteVideoThumbnail(ctx context.Context, arg sqlcgen.SetRemoteVideoThumbnailParams) error
	// Instance-level moderation (remote-content §8): the inbox drops activities
	// from blocked domains and the drain cancels deliveries to them.
	IsInstanceBlocked(ctx context.Context, domain string) (bool, error)
	// Federation policy gates (config-parity W12): the pending channel-follower
	// approval queue and the auto-follow-back edges (migration 0087).
	InsertRemoteFollowPending(ctx context.Context, arg sqlcgen.InsertRemoteFollowPendingParams) error
	ListPendingRemoteFollows(ctx context.Context, arg sqlcgen.ListPendingRemoteFollowsParams) ([]sqlcgen.ListPendingRemoteFollowsRow, error)
	AcceptPendingRemoteFollowByID(ctx context.Context, id uuid.UUID) (sqlcgen.AcceptPendingRemoteFollowByIDRow, error)
	DeletePendingRemoteFollowByID(ctx context.Context, id uuid.UUID) (sqlcgen.DeletePendingRemoteFollowByIDRow, error)
	InsertChannelFollowBackIfAbsent(ctx context.Context, arg sqlcgen.InsertChannelFollowBackIfAbsentParams) (int64, error)
	AcceptChannelFollowBackByActivity(ctx context.Context, arg sqlcgen.AcceptChannelFollowBackByActivityParams) (int64, error)
	DeleteChannelFollowBackByActivity(ctx context.Context, arg sqlcgen.DeleteChannelFollowBackByActivityParams) (int64, error)
	// Outbound remote-channel follows (remote-content §3): a local user follows
	// a remote channel; the accepted edges also gate remote-video ingestion.
	GetUserActorByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetUserActorByIDRow, error)
	UpsertRemoteChannelFollow(ctx context.Context, arg sqlcgen.UpsertRemoteChannelFollowParams) (sqlcgen.RemoteChannelFollow, error)
	GetRemoteChannelFollowByID(ctx context.Context, arg sqlcgen.GetRemoteChannelFollowByIDParams) (sqlcgen.GetRemoteChannelFollowByIDRow, error)
	ListRemoteChannelFollows(ctx context.Context, arg sqlcgen.ListRemoteChannelFollowsParams) ([]sqlcgen.ListRemoteChannelFollowsRow, error)
	DeleteRemoteChannelFollowByID(ctx context.Context, arg sqlcgen.DeleteRemoteChannelFollowByIDParams) (int64, error)
	AcceptRemoteChannelFollowByActivity(ctx context.Context, arg sqlcgen.AcceptRemoteChannelFollowByActivityParams) (int64, error)
	DeleteRemoteChannelFollowByActivity(ctx context.Context, arg sqlcgen.DeleteRemoteChannelFollowByActivityParams) (int64, error)
	HasAcceptedRemoteChannelFollow(ctx context.Context, remoteActorURL string) (bool, error)
	HasRemoteChannelFollow(ctx context.Context, remoteActorURL string) (bool, error)
	// Federated comments (remote-content §6): inbound Create/Update{Note}
	// storage + the comment reads outbound fan-out needs.
	GetComment(ctx context.Context, id uuid.UUID) (sqlcgen.Comment, error)
	GetCommentByRemoteObjectURL(ctx context.Context, remoteObjectURL string) (sqlcgen.Comment, error)
	CreateRemoteComment(ctx context.Context, arg sqlcgen.CreateRemoteCommentParams) (sqlcgen.Comment, error)
	UpdateComment(ctx context.Context, arg sqlcgen.UpdateCommentParams) (sqlcgen.Comment, error)
	DeleteComment(ctx context.Context, id uuid.UUID) error
	// Inbound Delete authority + effects (remote-content §7): retracted remote
	// videos and deleted remote actors (whose content cascades away).
	GetRemoteVideoByObjectURL(ctx context.Context, objectURL string) (sqlcgen.GetRemoteVideoByObjectURLRow, error)
	DeleteRemoteVideoByObjectURL(ctx context.Context, objectURL string) (int64, error)
	DeleteRemoteActor(ctx context.Context, actorURL string) (int64, error)
}

// CommentFlagger runs the watched-words moderation flagging over a stored
// federated comment (remote-content §6: remote comments are subject to the
// same flagging as local ones). *watchword.Service satisfies it; wiring it is
// optional (nil = no flagging).
type CommentFlagger interface {
	FlagComment(ctx context.Context, commentID uuid.UUID, body string) (int, error)
}

// FollowEdgeChecker reports whether any LOCAL user has an accepted outbound
// follow edge to the given remote actor — the anti-spam ingestion gate of
// .ralph/specs/federation-remote-content.md §2 (we only ingest content we asked
// for by following). By default the gate consults the repository's
// remote_channel_follows edges directly; this interface exists so tests can
// substitute a fake via WithFollowEdgeChecker.
type FollowEdgeChecker interface {
	HasAcceptedFollow(ctx context.Context, remoteActorURL string) (bool, error)
}

// NodeInfoUsage is the fediverse NodeInfo "usage" block: total users plus local
// post and comment counts. See https://nodeinfo.diaspora.software/.
type NodeInfoUsage struct {
	Users         int64
	LocalPosts    int64
	LocalComments int64
}

// Service exposes federation read models and mints/serves local actors. The only
// secret it touches is the actor private key, which it seals via cipher before
// storage and never returns.
type Service struct {
	repo    Repository
	baseURL string            // canonical public origin, e.g. https://videos.example (no trailing slash)
	cipher  *secretbox.Cipher // nil in dev → private keys stored raw

	// edges gates remote-video ingestion on an accepted local follow edge
	// (see FollowEdgeChecker). Nil = consult the repository's
	// remote_channel_follows edges directly (the production wiring).
	edges FollowEdgeChecker
	// media stores best-effort cached remote thumbnails
	// (remote-thumbnails/<id>.jpg). Nil = thumbnails are not cached.
	media storage.Backend
	// flagger runs watched-words flagging over inbound federated comments
	// (remote-content §6). Nil = no flagging.
	flagger CommentFlagger

	// allowPrivateFetch relaxes the SSRF guard for remote-actor fetches so backed
	// e2e / dev can reach a loopback origin. Never true in production.
	allowPrivateFetch bool
	// fetchClient, when set, overrides the SSRF-guarded client used for remote
	// fetches (tests inject one; production leaves it nil to build the guard).
	fetchClient *http.Client

	// Federation policy gates (config-parity W12): provider funcs over the
	// instance-settings overlay, read per inbound activity so an admin change
	// applies without a restart. All gate the ActivityPub inbox ONLY (ATProto
	// is outbound cross-posting, no inbound path). Nil funcs keep the shipped
	// defaults: comments ingested, channel follows auto-accepted, no approval
	// queue, no follow-back.
	acceptRemoteComments  func() bool
	allowChannelFollowers func() bool
	followerApproval      func() bool
	autoFollowBack        func() bool
	// logger, when set, records dropped/refused inbound activities (the
	// federation_accept_remote_comments drop path). Nil = silent.
	logger *slog.Logger
}

// Option configures a Service.
type Option func(*Service)

// WithBaseURL sets the canonical public origin used to build actor/object URLs.
func WithBaseURL(u string) Option { return func(s *Service) { s.baseURL = u } }

// WithCipher sets the envelope cipher used to seal actor private keys at rest.
// When unset, private keys are stored raw (dev only).
func WithCipher(c *secretbox.Cipher) Option { return func(s *Service) { s.cipher = c } }

// WithAllowPrivateFetch relaxes the SSRF guard for remote-actor fetches (dev/test
// only; wired from HTTP_IMPORT_ALLOW_PRIVATE_URLS).
func WithAllowPrivateFetch(v bool) Option { return func(s *Service) { s.allowPrivateFetch = v } }

// WithFetchClient overrides the HTTP client used to fetch remote actors (tests).
func WithFetchClient(c *http.Client) Option { return func(s *Service) { s.fetchClient = c } }

// WithFollowEdgeChecker overrides the accepted-local-follow ingestion gate for
// inbound Create/Announce (remote-content §2). When unset, the gate consults
// the repository's remote_channel_follows edges (the production default);
// tests substitute a fake here.
func WithFollowEdgeChecker(e FollowEdgeChecker) Option { return func(s *Service) { s.edges = e } }

// WithMediaStorage wires the blob backend used to cache remote thumbnails
// (remote-content §5). When unset, ingestion skips the thumbnail cache.
func WithMediaStorage(b storage.Backend) Option { return func(s *Service) { s.media = b } }

// WithCommentFlagger wires the watched-words flagging applied to inbound
// federated comments (remote-content §6). When unset, remote comments are
// stored unflagged.
func WithCommentFlagger(f CommentFlagger) Option { return func(s *Service) { s.flagger = f } }

// WithAcceptRemoteCommentsFunc wires the federation_accept_remote_comments
// gate (config-parity W12): when it reports false, inbound Create/Update{Note}
// activities are dropped (after the blocked-domain check). Not retroactive —
// existing comments are untouched. AP inbox only.
func WithAcceptRemoteCommentsFunc(f func() bool) Option {
	return func(s *Service) { s.acceptRemoteComments = f }
}

// WithAllowChannelFollowersFunc wires the federation_allow_channel_followers
// gate (config-parity W12): when it reports false, an inbound channel Follow
// is answered with a Reject instead of the auto-Accept. Existing followers are
// untouched. AP inbox only.
func WithAllowChannelFollowersFunc(f func() bool) Option {
	return func(s *Service) { s.allowChannelFollowers = f }
}

// WithFollowerApprovalFunc wires the federation_follower_approval gate
// (config-parity W12): when it reports true, a new inbound channel Follow is
// recorded PENDING (no Accept sent) and waits in the admin follower-requests
// queue. Applies to CHANNEL followers — a documented vidra deviation from
// PeerTube's instance-follower approval, since vidra has no instance actor.
func WithFollowerApprovalFunc(f func() bool) Option {
	return func(s *Service) { s.followerApproval = f }
}

// WithAutoFollowBackFunc wires the federation_auto_follow_back gate
// (config-parity W12): when it reports true, an ACCEPTED inbound channel
// Follow triggers an outbound follow-back of the follower, signed by the
// FOLLOWED CHANNEL's actor (vidra has no instance-level AP actor — the
// followed channel is the only local actor with a relationship to the
// follower). Respects the domain blocklist and never duplicates an existing
// follow/follow-back edge.
func WithAutoFollowBackFunc(f func() bool) Option {
	return func(s *Service) { s.autoFollowBack = f }
}

// WithLogger wires the structured logger used for inbound-policy drop/refuse
// events (never bodies or tokens). Nil = silent.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.logger = l } }

// acceptRemoteCommentsEnabled resolves the comment-ingestion gate (default true).
func (s *Service) acceptRemoteCommentsEnabled() bool {
	return s.acceptRemoteComments == nil || s.acceptRemoteComments()
}

// channelFollowersAllowed resolves the channel-follower gate (default true).
func (s *Service) channelFollowersAllowed() bool {
	return s.allowChannelFollowers == nil || s.allowChannelFollowers()
}

// followerApprovalRequired resolves the follower-approval gate (default false).
func (s *Service) followerApprovalRequired() bool {
	return s.followerApproval != nil && s.followerApproval()
}

// autoFollowBackEnabled resolves the auto-follow-back gate (default false).
func (s *Service) autoFollowBackEnabled() bool {
	return s.autoFollowBack != nil && s.autoFollowBack()
}

// NewService builds a federation Service over the given repository.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo}
	for _, o := range opts {
		o(s)
	}
	return s
}

// NodeInfoUsage returns the counts NodeInfo advertises: total users, public
// published videos (local posts), and comments (local comments). Any repository
// error is returned so the handler can fail rather than serve misleading zeros.
func (s *Service) NodeInfoUsage(ctx context.Context) (NodeInfoUsage, error) {
	users, err := s.repo.CountUsers(ctx)
	if err != nil {
		return NodeInfoUsage{}, err
	}
	posts, err := s.repo.CountPublicVideos(ctx)
	if err != nil {
		return NodeInfoUsage{}, err
	}
	comments, err := s.repo.CountComments(ctx)
	if err != nil {
		return NodeInfoUsage{}, err
	}
	return NodeInfoUsage{Users: users, LocalPosts: posts, LocalComments: comments}, nil
}
