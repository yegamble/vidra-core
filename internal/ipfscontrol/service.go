package ipfscontrol

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

var (
	ErrConflict           = errors.New("ipfs_config_conflict")
	ErrExternal           = errors.New("ipfs_external_provider")
	ErrManagerUnavailable = errors.New("ipfs_manager_unavailable")
)

type Repository interface {
	EnsureIPFSControlConfig(context.Context, []byte) (sqlcgen.IpfsControlConfig, error)
	GetIPFSControlConfig(context.Context) (sqlcgen.IpfsControlConfig, error)
	UpdateIPFSControlConfig(context.Context, sqlcgen.UpdateIPFSControlConfigParams) (sqlcgen.UpdateIPFSControlConfigRow, error)
	RequestIPFSControlOperation(context.Context, sqlcgen.RequestIPFSControlOperationParams) (sqlcgen.RequestIPFSControlOperationRow, error)
	GetIPFSControlOperation(context.Context, uuid.UUID) (sqlcgen.IpfsControlOperation, error)
	LatestIPFSControlOperation(context.Context) (sqlcgen.IpfsControlOperation, error)
	NextIPFSControlOperation(context.Context) (sqlcgen.IpfsControlOperation, error)
	ObserveIPFSControlOperation(context.Context, sqlcgen.ObserveIPFSControlOperationParams) (int64, error)
}
type Host interface {
	Status(context.Context) (HostStatus, error)
	Dispatch(context.Context, string, HostEnvelope) (HostOperation, error)
}
type Document struct {
	Revision     int64          `json:"revision"`
	Config       Config         `json:"config"`
	PolicyActive bool           `json:"policy_active"`
	Operation    *HostOperation `json:"operation,omitempty"`
}
type Service struct {
	repo     Repository
	host     Host
	defaults Config
}

func NewService(repo Repository, host Host, defaults Config) *Service {
	return &Service{repo: repo, host: host, defaults: defaults}
}

func (s *Service) Config(ctx context.Context) (Document, error) {
	row, err := s.repo.GetIPFSControlConfig(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		b, e := json.Marshal(s.defaults)
		if e != nil {
			return Document{}, e
		}
		row, err = s.repo.EnsureIPFSControlConfig(ctx, b)
	}
	if err != nil {
		return Document{}, err
	}
	var c Config
	if err = json.Unmarshal(row.Config, &c); err != nil {
		return Document{}, err
	}
	if err = c.Validate(); err != nil {
		return Document{}, err
	}
	return Document{Revision: row.Revision, Config: c, PolicyActive: row.PolicyActive}, nil
}

func (s *Service) Save(ctx context.Context, expected int64, c Config, actor uuid.UUID) (Document, error) {
	if expected < 1 {
		return Document{}, ErrConflict
	}
	if err := c.Validate(); err != nil {
		return Document{}, err
	}
	if _, err := s.Config(ctx); err != nil {
		return Document{}, err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return Document{}, err
	}
	row, err := s.repo.UpdateIPFSControlConfig(ctx, sqlcgen.UpdateIPFSControlConfigParams{ExpectedRevision: expected, Config: b, OperationID: uuid.New(), Actor: pgtype.UUID{Bytes: actor, Valid: actor != uuid.Nil}})
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrConflict
	}
	if err != nil {
		return Document{}, err
	}
	out := Document{Revision: row.Revision, Config: c, PolicyActive: true}
	if row.OperationID != uuid.Nil {
		op, err := s.repo.GetIPFSControlOperation(ctx, row.OperationID)
		if err != nil {
			return Document{}, err
		}
		out.Operation = operationView(op)
	}
	return out, nil
}

func (s *Service) Request(ctx context.Context, action string, expected int64, id, actor uuid.UUID) (HostOperation, error) {
	if !oneOf(action, "apply", "restart") || id == uuid.Nil || expected < 1 {
		return HostOperation{}, ErrConflict
	}
	if s.host == nil {
		return HostOperation{}, ErrManagerUnavailable
	}
	if _, err := s.Config(ctx); err != nil {
		return HostOperation{}, err
	}
	op, err := s.repo.RequestIPFSControlOperation(ctx, sqlcgen.RequestIPFSControlOperationParams{ID: id, Action: action, ExpectedRevision: expected, Actor: pgtype.UUID{Bytes: actor, Valid: actor != uuid.Nil}})
	if errors.Is(err, pgx.ErrNoRows) {
		doc, e := s.Config(ctx)
		if e != nil {
			return HostOperation{}, e
		}
		if doc.Revision == expected && doc.Config.Provider == "external" {
			return HostOperation{}, ErrExternal
		}
		return HostOperation{}, ErrConflict
	}
	if err != nil {
		return HostOperation{}, err
	}
	return HostOperation{ID: op.ID.String(), Sequence: op.Sequence, ConfigRevision: op.ConfigRevision, State: op.State}, nil
}
func operationView(op sqlcgen.IpfsControlOperation) *HostOperation {
	return &HostOperation{ID: op.ID.String(), Sequence: op.Sequence, ConfigRevision: op.ConfigRevision, State: op.State}
}
