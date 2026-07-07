package ipfs

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
)

// FakeIPFSClient is an in-memory Client for unit tests. Unit tests across the
// mirror (worker, enqueue, admin status) use this so they NEVER touch a real node
// — the guardrail from .ralph/specs/ipfs-media.md §11. It lives in the package
// (not a _test file) so other packages can depend on it in their own unit tests.
//
// Adds are content-addressed with the same RawLeafCIDv1 / DirCIDv1 helpers as the
// real contract, so identical bytes ⇒ identical CID (exercising the reference-
// checked-unpin dedupe path) and every returned CID passes ValidateCID. Set Down
// to simulate an unreachable node: every RPC then fails, letting a test assert the
// write path degrades gracefully and a later "recovery" (Down=false) backfills.
type FakeIPFSClient struct {
	mu sync.Mutex

	// Down, when true, makes every RPC fail (node unreachable/unhealthy).
	Down bool
	// VersionString is returned by Version when the node is up.
	VersionString string

	// AddHook, when set, is invoked by Add/AddDirectory AFTER the bytes are pinned
	// on the fake node but BEFORE the call returns to the caller — i.e. exactly the
	// window between the worker's ClaimDue lease and its MarkIPFSPinned. Tests use it
	// to deterministically interleave a concurrent privacy flip "while the Add is
	// streaming", exercising the lost-update race (P19 audit BLOCKER) without a real
	// node or timing games. Fired with the fake's mutex released so the hook may
	// touch other fakes (e.g. the ledger) freely. Nil in production/normal tests.
	AddHook func()

	// content maps CID → stored bytes; pins is the set of currently-pinned CIDs.
	content map[string][]byte
	pins    map[string]bool

	// AddCount / PinCount / UnpinCount are call counters for assertions.
	AddCount   int
	PinCount   int
	UnpinCount int
}

var _ Client = (*FakeIPFSClient)(nil)

// errNodeDown is returned by every method when Down is set.
var errNodeDown = errors.New("ipfs: fake node is down")

// NewFakeIPFSClient returns a ready, healthy fake.
func NewFakeIPFSClient() *FakeIPFSClient {
	return &FakeIPFSClient{
		VersionString: "0.0.0-fake",
		content:       map[string][]byte{},
		pins:          map[string]bool{},
	}
}

func (f *FakeIPFSClient) ensure() {
	if f.content == nil {
		f.content = map[string][]byte{}
	}
	if f.pins == nil {
		f.pins = map[string]bool{}
	}
}

func (f *FakeIPFSClient) Version(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Down {
		return "", errNodeDown
	}
	v := f.VersionString
	if v == "" {
		v = "0.0.0-fake"
	}
	return v, nil
}

func (f *FakeIPFSClient) Add(ctx context.Context, name string, r io.Reader) (AddResult, error) {
	f.mu.Lock()
	f.ensure()
	if f.Down {
		f.mu.Unlock()
		return AddResult{}, errNodeDown
	}
	data, err := io.ReadAll(r)
	if err != nil {
		f.mu.Unlock()
		return AddResult{}, err
	}
	cid := RawLeafCIDv1(data)
	f.content[cid] = data
	f.pins[cid] = true
	f.AddCount++
	hook := f.AddHook
	f.mu.Unlock()
	// Fire the interleaving hook outside the lock (the bytes are already "pinned"),
	// simulating a concurrent event landing between the lease and MarkIPFSPinned.
	if hook != nil {
		hook()
	}
	return AddResult{CID: cid, Size: int64(len(data))}, nil
}

func (f *FakeIPFSClient) AddDirectory(ctx context.Context, entries []DirEntry) (AddResult, error) {
	f.mu.Lock()
	f.ensure()
	if f.Down {
		f.mu.Unlock()
		return AddResult{}, errNodeDown
	}
	// Content-address the whole tree deterministically: concatenate each entry's
	// path + bytes in a stable (path-sorted) order, then hash. Identical trees ⇒
	// identical root CID.
	sorted := append([]DirEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var buf []byte
	var total int64
	for _, e := range sorted {
		data, err := io.ReadAll(e.Data)
		if err != nil {
			f.mu.Unlock()
			return AddResult{}, err
		}
		buf = append(buf, e.Path...)
		buf = append(buf, 0)
		buf = append(buf, data...)
		total += int64(len(data))
	}
	root := DirCIDv1(buf)
	f.content[root] = buf
	f.pins[root] = true
	f.AddCount++
	hook := f.AddHook
	f.mu.Unlock()
	// Interleaving hook (see Add): fires the "mid-Add" concurrent event for the
	// directory (HLS tree) pin path too.
	if hook != nil {
		hook()
	}
	return AddResult{CID: root, Size: total}, nil
}

func (f *FakeIPFSClient) Pin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	if f.Down {
		return errNodeDown
	}
	f.pins[cid] = true
	f.PinCount++
	return nil
}

func (f *FakeIPFSClient) Unpin(ctx context.Context, cid string) error {
	if err := ValidateCID(cid); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	if f.Down {
		return errNodeDown
	}
	delete(f.pins, cid)
	f.UnpinCount++
	return nil
}

func (f *FakeIPFSClient) IsPinned(ctx context.Context, cid string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	if f.Down {
		return false, errNodeDown
	}
	return f.pins[cid], nil
}

// Content returns the bytes stored under a CID (test helper), and whether present.
func (f *FakeIPFSClient) Content(cid string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	b, ok := f.content[cid]
	return b, ok
}
