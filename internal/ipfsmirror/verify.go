package ipfsmirror

import (
	"context"
	"time"

	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The ledger<->node reconciliation A31 measured as absent in BOTH directions:
//
//	| staged                                     | reconcile did | verdict         |
//	| the node loses a CID (`ipfs pin rm` by hand)| nothing       | not implemented |
//	| a stray pin the ledger never knew          | nothing       | not implemented |
//
// `Reconcile` re-arms dead letters and `SweepIneligible` converges the ledger on
// visibility facts. Neither ever asks the node what it actually holds, so the
// ledger and the node can disagree indefinitely — a video the ledger reports as
// published on IPFS with nothing behind the CID, or bytes on the node that no
// live row accounts for.
//
// THE ALGORITHM, per swarm, per tick:
//
//  1. Ask the node for every recursive pin it holds (ONE call — see
//     ipfs.Client.ListPins for why not N × IsPinned). A node that does not answer
//     ends the sweep for that swarm with no writes: a repair pass that cannot see
//     the node must not act on what it cannot see, and re-adding the whole ledger
//     during a node outage is the worst thing this could do.
//  2. Walk ONE PAGE of that swarm's 'pinned' rows, resuming where the last tick
//     stopped (an in-memory keyset cursor; a restart costs at most one restarted
//     cycle). Any row whose CID the node does not hold is re-armed to 'pending' —
//     the worker re-adds and re-pins it on the next drain, and content addressing
//     means the CID it lands on is the one the row already advertises.
//  3. Read every CID the ledger has a LIVE claim on, and report any node pin that
//     appears in none of them.
//
// STEP 3 IS ALSO THE HEALTH PROBE'S, and that is the point of countStrays being a
// free function with no logging: the leader's sweep and every process's five-
// minute probe run the SAME comparison, so the number an operator reads on
// /admin/system is computed by the code that logs the remedy, in the process that
// is rendering it. See strayPins for the cost and for the failure this closes.
//
// WHY A STRAY IS REPORTED AND NEVER REMOVED. There is no way to tell a Vidra pin
// from an operator's. Kubo pins are bare CIDs: `add` attaches no marker, no
// namespace and no name that survives into `pin/ls`, and this instance's node is
// explicitly allowed to be an operator's own node with an operator's own pins on
// it (a shared gateway, a node that also serves something else). An unpin here
// would therefore be Vidra silently deleting data it did not create, to fix a
// bookkeeping mismatch — an unrecoverable action taken on a guess. So the sweep
// counts strays, logs them, and surfaces the count on the `ipfs` component; an
// operator who wants them gone has `ipfs pin rm`, and knows which are theirs.
//
// A pinned CID with no live ledger row is also NOT automatically a leak. The
// mirror's own reference-count guard means a CID shared by several objects
// survives one of them being unpinned, and a CID whose row is mid-flight
// legitimately has no 'pinned' row for a moment — which is exactly why the stray
// side compares against 'pending' and 'unpinning' rows too, not just 'pinned'.

const (
	// verifyBatch is how many 'pinned' rows one sweep checks against the node, per
	// swarm. Bounded for the same reason every other sweep here is: this runs on
	// the reconcile ticker (IPFS_RECONCILE_INTERVAL, 5 minutes by default) forever,
	// and a pass that walked a 100k-row ledger would spend the interval doing it.
	// 500 rows per swarm per tick walks 144k rows a day, which is past any ledger
	// this mirror has produced.
	verifyBatch = 500
	// verifyMaxLedgerCIDs caps the live-CID set the stray comparison is made
	// against. A truncated set would manufacture strays out of legitimate pins, so
	// the sweep declines the stray half rather than reporting from a partial list —
	// the same rule ipfs.ListPins applies to a truncated pinset.
	verifyMaxLedgerCIDs = 100_000
	// strayCompareTimeout bounds ONE probe-side comparison (the pin/ls plus the
	// live-CID read). Generous relative to gatewayProbeTimeout because listing a
	// large pinset is legitimately slower than fetching one CID, and short enough
	// that a hung node cannot stack comparisons behind each other on the probe
	// ticker (whose floor, IPFS_HEALTH_PROBE_INTERVAL, is 10s).
	strayCompareTimeout = 30 * time.Second
)

// verifyReader is the ledger read side the comparison needs. *sqlcgen.Queries
// satisfies it; the in-memory repository fakes satisfy it in unit tests. It is a
// SEPARATE interface from Repository, consulted by type assertion, so that every
// existing fake keeps compiling and a repository that cannot answer simply skips
// the comparison instead of breaking the mirror.
type verifyReader interface {
	ListIPFSPinsForVerify(ctx context.Context, arg sqlcgen.ListIPFSPinsForVerifyParams) ([]sqlcgen.ListIPFSPinsForVerifyRow, error)
	ListLiveIPFSPinCIDs(ctx context.Context, arg sqlcgen.ListLiveIPFSPinCIDsParams) ([]string, error)
	RearmLostIPFSPin(ctx context.Context, arg sqlcgen.RearmLostIPFSPinParams) (int64, error)
}

// ledgerHealthReader is the component's fact source, on the same
// assert-don't-require footing as verifyReader.
type ledgerHealthReader interface {
	IPFSPinLedgerHealth(ctx context.Context, network string) (sqlcgen.IPFSPinLedgerHealthRow, error)
}

// VerifyResult is one swarm's comparison outcome.
type VerifyResult struct {
	Network string
	// Checked is how many 'pinned' rows this tick compared.
	Checked int
	// Rearmed is how many of them the node did not hold and were re-armed.
	Rearmed int
	// Strays is how many node pins no live ledger row accounts for. -1 means the
	// stray half did not run (the ledger was too large to compare against, or the
	// repository cannot answer), which is deliberately not the same as 0.
	Strays int64
	// Wrapped reports that the cursor reached the end of the ledger and restarted,
	// i.e. a full pass over this swarm completed.
	Wrapped bool
}

// VerifyPins compares the ledger with each active node and repairs what it may.
// It returns one result per swarm. Errors are per-swarm and NOT returned: a node
// that cannot be listed ends that swarm's sweep with Checked=0 and is logged —
// the mirror is non-authoritative and a reconciliation pass must never be able to
// fail the tick that also runs Reconcile and SweepIneligible.
func (s *Service) VerifyPins(ctx context.Context) []VerifyResult {
	if !s.enabled {
		return nil
	}
	reader, ok := s.repo.(verifyReader)
	if !ok {
		return nil
	}
	var out []VerifyResult
	for _, nc := range s.activeNetworks() {
		out = append(out, s.verifyNetwork(ctx, reader, nc))
	}
	return out
}

// verifyNetwork runs the comparison for ONE swarm through that swarm's own client.
// Threading nc rather than s.client keeps the cardinal invariant structural here
// too: the private ledger is never compared against the public node.
func (s *Service) verifyNetwork(ctx context.Context, reader verifyReader, nc netClient) VerifyResult {
	res := VerifyResult{Network: nc.network, Strays: -1}

	nodePins, err := nc.client.ListPins(ctx, verifyMaxLedgerCIDs)
	if err != nil {
		// No writes. See the package comment: acting on an unseen node is the one
		// thing a repair pass must not do.
		s.logger.Warn("ipfs_verify_skipped", "network", nc.network,
			"reason", "the node did not list its pins, so the ledger was not compared against it", "error", err)
		return res
	}

	// --- direction 1: the ledger claims a pin the node does not hold ------------
	cursor := s.verifyCursor(nc.network)
	rows, err := reader.ListIPFSPinsForVerify(ctx, sqlcgen.ListIPFSPinsForVerifyParams{
		Network: nc.network, AfterObjectKey: cursor, BatchSize: verifyBatch,
	})
	if err != nil {
		s.logger.Warn("ipfs_verify_skipped", "network", nc.network,
			"reason", "the pin ledger page could not be read", "error", err)
		return res
	}
	if len(rows) < verifyBatch {
		// The page ran short, so this was the tail: restart the cursor so the next
		// tick begins a fresh pass rather than sitting at the end forever.
		s.setVerifyCursor(nc.network, "")
		res.Wrapped = true
	} else {
		s.setVerifyCursor(nc.network, rows[len(rows)-1].ObjectKey)
	}
	res.Checked = len(rows)
	for _, row := range rows {
		if _, held := nodePins[row.Cid]; held {
			continue
		}
		n, err := reader.RearmLostIPFSPin(ctx, sqlcgen.RearmLostIPFSPinParams{
			ObjectKey: row.ObjectKey, Cid: row.Cid,
		})
		if err != nil {
			s.logger.Warn("ipfs_verify_rearm_failed", "network", nc.network,
				"media_class", row.MediaClass, "object_key", jobstatus.RedactDetail(row.ObjectKey), "error", err)
			continue
		}
		if n == 0 {
			// The row moved under us — a privacy flip, a delete or a re-transcode won
			// the race. Every one of those is better informed than this pass.
			continue
		}
		res.Rearmed++
		// The CID is deliberately absent from the info line, the same discipline
		// ipfs_pin_ok keeps: a CID is a public capability handle.
		s.logger.Warn("ipfs_pin_lost", "network", nc.network, "media_class", row.MediaClass,
			"object_key", jobstatus.RedactDetail(row.ObjectKey),
			"reason", "the ledger recorded this object as pinned but the node does not hold its CID; re-armed for re-add")
	}

	// --- direction 2: the node holds a pin no live ledger row accounts for ------
	// The count itself is countStrays' — the SAME function the health probe calls,
	// so the number an operator reads on /admin/system and the number this sweep
	// logs can never be two different computations that drifted apart. What is
	// leader-only is the LOGGING: one WARN naming the remedy, on the tick that also
	// repairs, rather than one per process per probe interval forever.
	var skipped string
	res.Strays, skipped = countStrays(ctx, reader, nc.network, nodePins)
	if skipped != "" {
		s.logger.Warn("ipfs_verify_strays_skipped", "network", nc.network, "reason", skipped)
	}
	if res.Strays > 0 {
		// One line for the whole set, not one per CID: a node that also serves an
		// operator's own pins would otherwise fill the log every five minutes forever.
		// It says explicitly that nothing was removed, because the obvious reading of
		// "stray pin" is that something dealt with it.
		s.logger.Warn("ipfs_stray_pins", "network", nc.network, "count", res.Strays,
			"reason", "the node holds recursive pins no live ledger row accounts for. NOTHING WAS REMOVED: a kubo pin carries no marker distinguishing a Vidra pin from one an operator made themselves, so unpinning here would be deleting data on a guess. Compare 'ipfs pin ls --type=recursive' against the pin ledger and remove by hand what is yours")
	}

	if res.Rearmed > 0 || res.Strays > 0 {
		s.logger.Info("ipfs_verify_done", "network", nc.network,
			"checked", res.Checked, "rearmed", res.Rearmed, "strays", res.Strays)
	}
	return res
}

// countStrays reports how many node pins the ledger cannot account for, or -1
// with a reason when the comparison could not be made safely.
//
// A FREE FUNCTION, and deliberately silent. It has two callers — the leader's
// VerifyPins sweep and every process's health probe (health.go) — and only the
// sweep may log: an operator gets one actionable WARN per reconcile tick, not one
// per role per probe. Returning the skip reason rather than logging it is what
// lets the same comparison serve both without either caller inheriting the
// other's verbosity.
func countStrays(ctx context.Context, reader verifyReader, network string, nodePins map[string]struct{}) (int64, string) {
	cids, err := reader.ListLiveIPFSPinCIDs(ctx, sqlcgen.ListLiveIPFSPinCIDsParams{
		Network: network, MaxRows: verifyMaxLedgerCIDs + 1,
	})
	if err != nil {
		return -1, "the live CID set could not be read: " + err.Error()
	}
	if len(cids) > verifyMaxLedgerCIDs {
		return -1, "the ledger holds more live CIDs than one sweep compares, so every node pin would be reported as a stray"
	}
	live := make(map[string]struct{}, len(cids))
	for _, cid := range cids {
		live[cid] = struct{}{}
	}
	var strays int64
	for cid := range nodePins {
		if _, ok := live[cid]; !ok {
			strays++
		}
	}
	return strays, ""
}

// strayPins runs the WHOLE comparison for one swarm from scratch — one
// `pin/ls?type=recursive` plus one bounded live-CID read — and returns the count,
// or -1 when it could not be completed. It is the health probe's entry point
// (health.go), and it is why the number exists in an api-role process at all.
//
// WHY THE PROBE RECOMPUTES INSTEAD OF READING WHAT THE SWEEP FOUND. It used to
// read a per-process sync.Map that only VerifyPins wrote, and VerifyPins runs
// under runWorkers behind the leader lock. A31's rehearsal measured the
// consequence on the topology docker-compose.prod.yml actually renders: the api
// role — the one process serving /readyz and /admin/system — never wrote that map,
// so `unaccounted_node_pins` was absent on every deployment that separates the
// roles, and present only in a single-process positive control. A number computed
// in one process and read in another is not a component; it is a coincidence.
//
// THE COST, stated rather than hidden. Per process, per IPFS_HEALTH_PROBE_INTERVAL
// (5m default), per active swarm: ONE pin/ls RPC (bounded to a 16 MiB body and
// verifyMaxLedgerCIDs entries by ipfs.Client.ListPins — a node holding more errors
// out and the count reports absent rather than a lie) and ONE indexed
// `SELECT DISTINCT cid` capped at verifyMaxLedgerCIDs+1 rows. At the 100k-CID cap
// that is roughly 6 MB of transient strings per probe. Two roles on the shipped
// topology means the node is listed twice per interval instead of once — that is
// the whole price of the api role having its own answer, and it is the same order
// as the gateway fetch already on this ticker.
//
// NO WRITES. The repair half (re-arming a pin the node lost) stays leader-only in
// VerifyPins, so nothing changed about who may act on the comparison — only about
// who may SEE it.
//
// THE CAPPED CASE IS REPORTED AS ABSENT, NOT AS A FLOOR. A ledger read that ran
// short would leave live CIDs out of the accounted set, so every one of their node
// pins would be counted as a stray: the error runs UPWARD, and "N+" would be a
// false floor under a number that is already too high. Silence is the honest
// answer, and it is the rule verify.go already applied to a truncated pinset.
func (s *Service) strayPins(ctx context.Context, network string) int64 {
	reader, ok := s.repo.(verifyReader)
	if !ok {
		return -1
	}
	nc, ok := s.networkClient(network)
	if !ok {
		return -1
	}
	lctx, cancel := context.WithTimeout(ctx, strayCompareTimeout)
	defer cancel()
	nodePins, err := nc.client.ListPins(lctx, verifyMaxLedgerCIDs)
	if err != nil {
		// Absent, never 0. "The node would not list its pins" and "the node holds
		// nothing unaccounted for" are the two answers A31 caught the old reconcile
		// conflating by silence, and the renderer distinguishes them only if this does.
		s.logger.Debug("ipfs_stray_probe_skipped", "network", network,
			"reason", "the node did not list its pins, so the ledger was not compared against it", "error", err)
		return -1
	}
	n, skipped := countStrays(lctx, reader, network, nodePins)
	if skipped != "" {
		s.logger.Debug("ipfs_stray_probe_skipped", "network", network, "reason", skipped)
	}
	return n
}

// verifyCursor reads this swarm's keyset cursor. In memory on purpose: it is a
// hint about where to resume a repeating full pass, not state anything depends
// on, and persisting it would be a migration for a value a restart may safely
// forget.
func (s *Service) verifyCursor(network string) string {
	v, ok := s.verifyCursors.Load(network)
	if !ok {
		return ""
	}
	c, _ := v.(string)
	return c
}

func (s *Service) setVerifyCursor(network, cursor string) {
	s.verifyCursors.Store(network, cursor)
}
