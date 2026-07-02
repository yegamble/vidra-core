// Package federation implements Vidra's ActivityPub federation surface. This
// first slice provides only the keypair-free discovery data (NodeInfo); the
// actor model, WebFinger, signatures, inbox/outbox and delivery land in later
// slices. See .ralph/specs/federation.md for the full design and slice plan.
package federation

import "context"

// Repository is the persistence surface the federation service needs. It is
// satisfied by *sqlcgen.Queries; tests use a small fake.
type Repository interface {
	CountUsers(ctx context.Context) (int64, error)
	CountPublicVideos(ctx context.Context) (int64, error)
	CountComments(ctx context.Context) (int64, error)
}

// NodeInfoUsage is the fediverse NodeInfo "usage" block: total users plus local
// post and comment counts. See https://nodeinfo.diaspora.software/.
type NodeInfoUsage struct {
	Users         int64
	LocalPosts    int64
	LocalComments int64
}

// Service exposes federation read models. It holds no secrets.
type Service struct {
	repo Repository
}

// NewService builds a federation Service over the given repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
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
