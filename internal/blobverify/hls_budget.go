package blobverify

import (
	"errors"
	"fmt"
	"io"

	"github.com/vidra/vidra-core/internal/hlsmanifest"
)

type hlsLimits struct {
	playlistBytes, manifestBytes, keyBytes int64
	nodes, depth                           int
}

// Fixed reviewed bounds. Exhaustion is incomplete verification, never a reason
// to raise a limit automatically or omit the remaining subtree from evidence.
var defaultHLSLimits = hlsLimits{
	playlistBytes: hlsmanifest.MaxPlaylistBytes, manifestBytes: 128 << 20,
	nodes: 1000000, keyBytes: 64 << 20, depth: 32,
}

var errHLSLimit = errors.New("HLS traversal limit exhausted")

type hlsUsage struct {
	nodes                   int
	keyBytes, manifestBytes int64
	depth                   int
	exhausted               string
}

type hlsBudget struct {
	limits hlsLimits
	usage  hlsUsage
	seen   map[string]bool
}

func (b *hlsBudget) refuse(limit string) error {
	if b.usage.exhausted == "" {
		b.usage.exhausted = limit
	}
	return fmt.Errorf("%w: %s", errHLSLimit, b.usage.exhausted)
}

// admit runs before a new key is retained or queued. Roles and physical storage
// inspection belong to the walker; logical membership is charged per tree.
func (b *hlsBudget) admit(key string, depth int) (bool, error) {
	if b.usage.exhausted != "" {
		return false, b.refuse(b.usage.exhausted)
	}
	if depth > b.limits.depth {
		return false, b.refuse("depth")
	}
	if !b.seen[key] {
		if b.usage.nodes >= b.limits.nodes {
			return false, b.refuse("nodes")
		}
		if int64(len(key)) > b.limits.keyBytes-b.usage.keyBytes {
			return false, b.refuse("key_bytes")
		}
		if b.seen == nil {
			b.seen = make(map[string]bool)
		}
		b.seen[key] = true
		b.usage.nodes++
		b.usage.keyBytes += int64(len(key))
		if depth > b.usage.depth {
			b.usage.depth = depth
		}
		return true, nil
	}
	if depth > b.usage.depth {
		b.usage.depth = depth
	}
	return false, nil
}

func (b *hlsBudget) readPlaylist(r io.Reader) ([]byte, error) {
	if b.usage.exhausted != "" {
		return nil, b.refuse(b.usage.exhausted)
	}
	remaining := b.limits.manifestBytes - b.usage.manifestBytes
	if remaining <= 0 {
		return nil, b.refuse("manifest_bytes")
	}
	ceiling, limit := b.limits.playlistBytes, "playlist_bytes"
	if remaining < ceiling {
		ceiling, limit = remaining, "manifest_bytes"
	}
	// One overflow byte distinguishes an exact ceiling from truncation. It is
	// not admitted or parsed, and no further read occurs after exhaustion.
	body, err := io.ReadAll(io.LimitReader(r, ceiling+1))
	b.usage.manifestBytes += min(int64(len(body)), ceiling)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > ceiling {
		return nil, b.refuse(limit)
	}
	return body, nil
}
