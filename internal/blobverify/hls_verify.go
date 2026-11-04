package blobverify

import (
	"context"
	"path"

	"github.com/vidra/vidra-core/internal/mediagc"
)

// HLSTreeReport describes the admitted graph. Unresolved descendants are unknown.
type HLSTreeReport struct {
	VideoID       string `json:"video_id"`
	Root          string `json:"root,omitempty"`
	Complete      bool   `json:"complete"`
	Nodes         int    `json:"nodes"`
	KeyBytes      int64  `json:"key_bytes"`
	ManifestBytes int64  `json:"manifest_bytes"`
	MaxDepth      int    `json:"max_depth"`
	ObservedMedia int    `json:"observed_media_nodes"`
	Exhausted     string `json:"exhausted_limit,omitempty"`
}

var findingReasons = map[class]string{
	classMissing: "missing", classKnownMissing: "known_missing", classError: "unreadable",
	classMismatch: "hash_mismatch", classStaleSentinel: "stale_sentinel",
}

func deepWalk(ctx context.Context, walker hlsTreeWalker, playlists []mediagc.PlaylistRef, rep *Report) error {
	for _, pl := range playlists {
		result, err := walker.walk(ctx, pl)
		if err != nil {
			return err
		}
		root, _ := resolveHLSReference(pl, pl.MasterKey, path.Base(pl.MasterKey))
		rep.Trees = append(rep.Trees, HLSTreeReport{VideoID: pl.VideoID.String(), Root: root,
			Complete: result.complete, Nodes: result.usage.nodes, KeyBytes: result.usage.keyBytes,
			ManifestBytes: result.usage.manifestBytes, MaxDepth: result.usage.depth,
			ObservedMedia: result.media, Exhausted: result.usage.exhausted})
		rep.DeepPlaylists++
		rep.DeepListed += result.usage.nodes
		if !result.complete {
			rep.Incomplete++
			prefix := pl.Prefix
			if root != "" {
				prefix = path.Dir(root)
			}
			rep.sample(&rep.IncompletePrefixes, prefix)
		}
	}
	return nil
}
