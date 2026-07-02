// Package federation implements Vidra's ActivityPub federation surface. It
// currently provides NodeInfo discovery (Slice 1) and the actor identity layer —
// local Person/Group actors, lazily-minted keypairs, and WebFinger (Slice 2).
// Signatures, inbox/outbox and delivery land in later slices. See
// .ralph/specs/federation.md for the full design and slice plan.
package federation

import (
	"context"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/secretbox"
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
}

// Option configures a Service.
type Option func(*Service)

// WithBaseURL sets the canonical public origin used to build actor/object URLs.
func WithBaseURL(u string) Option { return func(s *Service) { s.baseURL = u } }

// WithCipher sets the envelope cipher used to seal actor private keys at rest.
// When unset, private keys are stored raw (dev only).
func WithCipher(c *secretbox.Cipher) Option { return func(s *Service) { s.cipher = c } }

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
