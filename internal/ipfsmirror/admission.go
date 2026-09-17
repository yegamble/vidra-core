package ipfsmirror

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vidra/vidra-core/internal/ipfscontrol"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type admissionRepo interface {
	EnsureIPFSCapacity(context.Context) (sqlcgen.IpfsCapacity, error)
	GetIPFSCapacity(context.Context) (sqlcgen.IpfsCapacity, error)
	AdmitIPFSPin(context.Context, sqlcgen.AdmitIPFSPinParams) (sqlcgen.MediaIpfsPin, error)
	ReleaseIPFSReservation(context.Context, sqlcgen.ReleaseIPFSReservationParams) (int64, error)
	RenewIPFSReservation(context.Context, sqlcgen.RenewIPFSReservationParams) (int64, error)
	ListIPFSAdmissionCandidates(context.Context, int32) ([]sqlcgen.MediaIpfsPin, error)
	DeferIPFSAdmission(context.Context, sqlcgen.DeferIPFSAdmissionParams) error
	ExpireIPFSReservation(context.Context, sqlcgen.ExpireIPFSReservationParams) error
	ListExpiredIPFSReservations(context.Context) ([]sqlcgen.MediaIpfsPin, error)
	CompleteIPFSAdmission(context.Context, sqlcgen.CompleteIPFSAdmissionParams) (string, error)
	ClaimIPFSRemovals(context.Context, int32) ([]sqlcgen.ClaimIPFSRemovalsRow, error)
	EvictColdIPFSPin(context.Context) (string, error)
	TagIPFSPolicyIntent(context.Context, sqlcgen.TagIPFSPolicyIntentParams) error
	GetIPFSControlOperation(context.Context, uuid.UUID) (sqlcgen.IpfsControlOperation, error)
	SeedIPFSManagedBackfill(context.Context) (int64, error)
}

// ConfigureControl is called only during construction, before any worker starts.
// Configured RPC clients remain available for withdrawals while copying is paused.
func (s *Service) ConfigureControl(control *ipfscontrol.Service) {
	s.control = control
	if s.client != nil {
		s.enabled = true
		s.publicEnabled = true
	}
}

func (s *Service) admissionInventory(ctx context.Context, row sqlcgen.MediaIpfsPin) (map[string]int64, string, int64, error) {
	files := map[string]int64{}
	generation := row.ObjectKey
	if MediaClass(row.MediaClass) == ClassHLS {
		reader, ok := s.lookups.(masterKeyReader)
		if !ok || !row.VideoID.Valid {
			return nil, "", 0, errors.New("generation_unavailable")
		}
		master, found, err := reader.VideoHLSMasterKey(ctx, uuid.UUID(row.VideoID.Bytes))
		if err != nil {
			return nil, "", 0, err
		}
		if !found {
			return nil, "", 0, errors.New("generation_unavailable")
		}
		generation = master
		inventory, ok := s.blobs.(storage.ObjectInventory)
		if !ok {
			return nil, "", 0, errors.New("inventory_unavailable")
		}
		objects, err := inventory.ListObjects(ctx, path.Dir(master)+"/")
		if err != nil {
			return nil, "", 0, err
		}
		for _, object := range objects {
			if path.Base(object.Key) != media.VP9WebMFilename {
				files[object.Key] = object.Size
			}
		}
		if _, ok := files[master]; !ok {
			return nil, "", 0, errors.New("master_missing")
		}
	} else {
		rc, err := s.blobs.Open(ctx, row.ObjectKey)
		if err != nil {
			return nil, "", 0, err
		}
		files[row.ObjectKey] = storage.SizeOf(rc)
		_ = rc.Close()
	}
	// A candidate has no committed root yet; use current generation solely for
	// the existing live eligibility/provenance gate, never to authorize delivery.
	row.CommittedGeneration = generation
	allowed, err := s.gatewayRowEligible(ctx, row)
	if err != nil {
		return nil, "", 0, err
	}
	if !allowed {
		return nil, "", 0, errors.New("ineligible")
	}
	var bytes int64
	if len(files) == 0 || len(files) > 100000 {
		return nil, "", 0, errors.New("inventory_size")
	}
	for _, size := range files {
		if size < 0 || size > 1<<50 || bytes > (1<<50)-size {
			return nil, "", 0, errors.New("size_unknown")
		}
		bytes += size
	}
	// Include ample UnixFS/chunk/directory overhead and never assume deduplication.
	return files, generation, 2*bytes + int64(len(files))*16384 + (1 << 20), nil
}

func (s *Service) drainManaged(ctx context.Context, nc netClient, batch int, doc ipfscontrol.Document) (int, error) {
	r, ok := s.repo.(admissionRepo)
	if !ok {
		return 0, errors.New("ipfs admission repository unavailable")
	}
	removals, err := r.ClaimIPFSRemovals(ctx, int32(batch))
	if err != nil {
		return 0, err
	}
	done := 0
	for _, row := range removals {
		if s.process(ctx, nc, sqlcgen.ClaimDueIPFSPinsRow(row)) {
			done++
		}
	}
	if done > 0 {
		gcctx, cancel := context.WithTimeout(ctx, repoGCTimeout)
		_, _ = nc.client.RepoGC(gcctx)
		cancel()
	}
	if !doc.PolicyActive {
		return done, nil
	}
	if doc.Config.Provider != "internal" {
		return done, nil
	}
	_, host, err := s.control.Admission(ctx)
	if err != nil || host.ObservedState != "running" || host.AppliedConfigRevision != doc.Revision || host.RepoUsedBytes == nil || host.FilesystemFreeBytes == nil || time.Since(host.ObservedAt) > 30*time.Second || time.Until(host.ObservedAt) > 5*time.Second {
		return done, nil
	}
	if _, err = r.EnsureIPFSCapacity(ctx); err != nil {
		return done, err
	}
	if pending, err := s.recoverClaims(ctx, r, doc, host); err != nil || pending {
		return done, err
	}
	if !doc.Config.Enabled {
		return done, nil
	}
	if doc.Config.BackfillEnabled {
		if _, err = r.SeedIPFSManagedBackfill(ctx); err != nil {
			return done, err
		}
	}
	rows, err := r.ListIPFSAdmissionCandidates(ctx, int32(batch))
	if err != nil {
		return done, err
	}
	for _, row := range rows {
		if (row.PolicyReason == "new" && !doc.Config.AutoPinNew && !(row.DemandAt.Valid && doc.Config.DemandPin)) || (row.PolicyReason == "demand" && !doc.Config.DemandPin) || ((row.PolicyReason == "legacy" || row.PolicyReason == "backfill") && !doc.Config.BackfillEnabled) {
			continue
		}
		// Playback has one preferred encoded representation; source/WebM duplicates
		// must not exhaust a small cache alongside a ready HLS ladder.
		if row.VideoID.Valid && (row.MediaClass == string(ClassVideoOriginal) || row.MediaClass == string(ClassWebM)) {
			if reader, ok := s.lookups.(masterKeyReader); ok {
				_, ready, e := reader.VideoHLSMasterKey(ctx, uuid.UUID(row.VideoID.Bytes))
				if e != nil {
					continue
				}
				if ready {
					_ = r.DeferIPFSAdmission(ctx, sqlcgen.DeferIPFSAdmissionParams{ObjectKey: row.ObjectKey, Reason: "hls_preferred"})
					continue
				}
			}
		}
		files, generation, reservation, err := s.admissionInventory(ctx, row)
		if err != nil {
			_ = r.DeferIPFSAdmission(ctx, sqlcgen.DeferIPFSAdmissionParams{ObjectKey: row.ObjectKey, Reason: "source_unavailable"})
			continue
		}
		if reservation > doc.Config.BudgetBytes {
			_ = r.DeferIPFSAdmission(ctx, sqlcgen.DeferIPFSAdmissionParams{ObjectKey: row.ObjectKey, Reason: "oversized"})
			continue
		}
		var sourceBytes int64
		for _, size := range files {
			sourceBytes += size
		}
		if sourceBytes/(doc.Config.CopyBytesPerSecond/int64(doc.Config.Workers)) > int64((24*time.Hour-120*time.Second)/time.Second) {
			_ = r.DeferIPFSAdmission(ctx, sqlcgen.DeferIPFSAdmissionParams{ObjectKey: row.ObjectKey, Reason: "copy_deadline_exceeded"})
			continue
		}
		claim, err := r.AdmitIPFSPin(ctx, sqlcgen.AdmitIPFSPinParams{ObjectKey: row.ObjectKey, ClaimToken: pgUUID(uuid.New()), ReservationBytes: reservation, SourceGeneration: generation, ConfigRevision: doc.Revision, RepoUsedBytes: *host.RepoUsedBytes, FilesystemFreeBytes: *host.FilesystemFreeBytes, ObservedAt: host.ObservedAt, HostSequence: host.LastOperationSequence})
		if errors.Is(err, pgx.ErrNoRows) {
			capacity, e := r.GetIPFSCapacity(ctx)
			if e != nil {
				return done, e
			}
			if capacity.ActiveClaims >= int32(doc.Config.Workers) || host.ObservedAt.Before(capacity.MeasureAfter) {
				break
			}
			reason := "budget_exhausted"
			if *host.FilesystemFreeBytes-capacity.ReservedBytes-reservation < doc.Config.MinFreeBytes {
				reason = "filesystem_headroom"
			}
			_ = r.DeferIPFSAdmission(ctx, sqlcgen.DeferIPFSAdmissionParams{ObjectKey: row.ObjectKey, Reason: reason})
			// At most one retirement per pass, followed by unpin/GC and a fresh
			// measurement on the next pass. Estimated sizes never count as freed.
			_, e = r.EvictColdIPFSPin(ctx)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return done, e
			}
			break
		}
		if err != nil {
			return done, err
		}
		go s.copyAdmission(ctx, nc, r, doc, claim, files)
		done++
	}
	return done, nil
}

func (s *Service) recoverClaims(ctx context.Context, r admissionRepo, doc ipfscontrol.Document, host ipfscontrol.HostStatus) (bool, error) {
	expired, err := r.ListExpiredIPFSReservations(ctx)
	if err != nil || len(expired) == 0 {
		return false, err
	}
	capacity, err := r.GetIPFSCapacity(ctx)
	if err != nil {
		return true, err
	}
	// Don't interrupt healthy copies. No new claims start while stale ones exist.
	if int64(capacity.ActiveClaims) > int64(len(expired)) {
		return true, nil
	}
	for _, claim := range expired {
		if host.Operation != nil && host.Operation.State == "succeeded" && host.LastOperationSequence > claim.AdmittedHostSequence {
			id, e := uuid.Parse(host.Operation.ID)
			if e != nil {
				return true, e
			}
			op, e := r.GetIPFSControlOperation(ctx, id)
			if e == nil && op.Action == "restart" && op.Sequence == host.LastOperationSequence && op.ConfigRevision == doc.Revision {
				if _, e = r.ReleaseIPFSReservation(ctx, sqlcgen.ReleaseIPFSReservationParams{ObjectKey: claim.ObjectKey, ClaimToken: claim.ClaimToken}); e != nil {
					return true, e
				}
				continue
			}
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("vidra-ipfs-recover:%d:%d", doc.Revision, claim.AdmittedHostSequence)))
		_, err = s.control.Request(ctx, "restart", doc.Revision, id, uuid.Nil)
		return true, err
	}
	return true, nil // force a new post-restart/release measurement
}

type fencedPinRepo struct {
	Repository
	admission admissionRepo
	token     sqlcgen.MediaIpfsPin
	progress  *atomic.Int64
	expected  int64
}

func (r *fencedPinRepo) MarkIPFSPinned(ctx context.Context, p sqlcgen.MarkIPFSPinnedParams) (string, error) {
	if r.progress.Load() != r.expected {
		return "", errors.New("ipfs copy ended before complete source consumption")
	}
	return r.admission.CompleteIPFSAdmission(ctx, sqlcgen.CompleteIPFSAdmissionParams{ObjectKey: p.ObjectKey, ClaimToken: r.token.ClaimToken, Cid: p.Cid, CarRoot: p.CarRoot, ByteSize: p.ByteSize})
}

// The claim owner decides retry/release after the node request settles. Legacy
// retry helpers must not mutate a newer intent through their unfenced SQL.
func (r *fencedPinRepo) MarkIPFSPinFailed(context.Context, sqlcgen.MarkIPFSPinFailedParams) error {
	return nil
}
func (r *fencedPinRepo) RescheduleIPFSPin(context.Context, sqlcgen.RescheduleIPFSPinParams) error {
	return nil
}

func (s *Service) copyAdmission(ctx context.Context, nc netClient, r admissionRepo, doc ipfscontrol.Document, claim sqlcgen.MediaIpfsPin, files map[string]int64) {
	var maximum int64
	for _, size := range files {
		maximum += size
	}
	rate := doc.Config.CopyBytesPerSecond / int64(doc.Config.Workers)
	timeout := time.Duration(maximum/rate+120) * time.Second
	if timeout > 24*time.Hour {
		timeout = 24 * time.Hour
	}
	copyctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var progress atomic.Int64
	blobs, err := newLimitedSource(s.blobs, files, rate, maximum, &progress)
	if err != nil {
		cleanup, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer c()
		_, _ = r.ReleaseIPFSReservation(cleanup, sqlcgen.ReleaseIPFSReservationParams{ObjectKey: claim.ObjectKey, ClaimToken: claim.ClaimToken})
		return
	}
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-finished:
				return
			case <-copyctx.Done():
				return
			case <-ticker.C:
				current, host, e := s.control.Admission(copyctx)
				if e != nil || current.Revision != doc.Revision || host.ObservedState != "running" || time.Since(host.ObservedAt) > 30*time.Second || time.Until(host.ObservedAt) > 5*time.Second || host.FilesystemFreeBytes == nil || host.RepoUsedBytes == nil || *host.FilesystemFreeBytes < doc.Config.MinFreeBytes || *host.RepoUsedBytes > doc.Config.BudgetBytes {
					cancel()
					return
				}
				candidate := claim
				candidate.CommittedGeneration = claim.SourceGeneration
				allowed, e := s.gatewayRowEligible(copyctx, candidate)
				if e != nil || !allowed {
					cancel()
					return
				}
				n, e := r.RenewIPFSReservation(copyctx, sqlcgen.RenewIPFSReservationParams{ObjectKey: claim.ObjectKey, ClaimToken: claim.ClaimToken, CopiedBytes: progress.Load()})
				if e != nil || n != 1 {
					cancel()
					return
				}
			}
		}
	}()
	runner := New(&fencedPinRepo{Repository: s.repo, admission: r, token: claim, progress: &progress, expected: maximum}, s.lookups, blobs, nc.client, Config{Enabled: true, GatewayURL: s.gatewayURL, Cluster: nc.cluster, ClusterEnabled: s.clusterEnabled, Logger: s.logger, MaxAttempts: s.maxAttempts, BaseBackoff: s.baseBackoff})
	row := sqlcgen.ClaimDueIPFSPinsRow{ObjectKey: claim.ObjectKey, MediaClass: claim.MediaClass, Cid: claim.Cid, CarRoot: claim.CarRoot, State: claim.State, Attempts: claim.Attempts, Network: claim.Network, VideoID: claim.VideoID, OwnerUserID: claim.OwnerUserID}
	ok := runner.pin(copyctx, nc, row)
	cleanup, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer c()
	if ok {
		_, _ = r.ReleaseIPFSReservation(cleanup, sqlcgen.ReleaseIPFSReservationParams{ObjectKey: claim.ObjectKey, ClaimToken: claim.ClaimToken})
	} else {
		// A transport failure is an unknown server outcome. Retain capacity until
		// the host has definitely stopped that request; never assume cancellation
		// means Kubo has finished writing its last buffered blocks.
		_ = r.ExpireIPFSReservation(cleanup, sqlcgen.ExpireIPFSReservationParams{ObjectKey: claim.ObjectKey, ClaimToken: claim.ClaimToken})
	}
}

// DemandPublicVideo coalesces lower-priority demand only from an authorized
// playback session. It derives its own storage keys and rechecks public facts.
func (s *Service) DemandPublicVideo(ctx context.Context, id uuid.UUID) error {
	if s.control == nil {
		return nil
	}
	doc, err := s.control.Config(ctx)
	if err != nil || !doc.PolicyActive || !doc.Config.Enabled || !doc.Config.DemandPin {
		return err
	}
	r, ok := s.repo.(admissionRepo)
	if !ok {
		return nil
	}
	class := ClassHLS
	key := media.HLSKeyPrefix(id) + "/"
	reader, ok := s.lookups.(masterKeyReader)
	if !ok {
		return nil
	}
	master, ready, err := reader.VideoHLSMasterKey(ctx, id)
	if err != nil {
		return err
	}
	if !ready {
		refs, e := s.lookups.VideoFiles(ctx, id)
		if e != nil {
			return e
		}
		key = ""
		class = ClassVideoOriginal
		for _, ref := range refs {
			if ref.Kind == "original" {
				key = ref.StorageKey
				break
			}
		}
		if key == "" {
			return nil
		}
	}
	row := sqlcgen.MediaIpfsPin{ObjectKey: key, MediaClass: string(class), VideoID: pgUUID(id), Network: "public", CommittedGeneration: master}
	allowed, err := s.gatewayRowEligible(ctx, row)
	if err != nil || !allowed {
		return err
	}
	if _, err = s.repo.BackfillIPFSPinIntent(ctx, sqlcgen.BackfillIPFSPinIntentParams{ObjectKey: key, MediaClass: string(class), VideoID: pgUUID(id), Network: "public"}); err != nil {
		return err
	}
	return r.TagIPFSPolicyIntent(ctx, sqlcgen.TagIPFSPolicyIntentParams{ObjectKey: key, Reason: "demand"})
}
