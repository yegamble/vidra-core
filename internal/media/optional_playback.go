package media

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"

	"github.com/vidra/vidra-core/internal/storage"
)

// Stop writing single-file derivatives once they exceed the scan policy. FFmpeg
// stops at a packet boundary after the limit is reached and can still finalize
// its container. One byte above the ceiling makes every size-limited partial
// file ineligible even when its container looks valid.
func limitSingleFile(args []string, limit int64) []string {
	if limit <= 0 {
		return args
	}
	dst := args[len(args)-1]
	return append(args[:len(args)-1], "-fs", strconv.FormatInt(limit+1, 10), dst)
}

// A full-length trick-play track is an optional, single-file derivative. For
// a long or unknown timeline, keep the segmented player and seek previews and
// avoid an unbounded scratch encode. Shorter tracks are checked by actual size.
func skipSingleFileTrickPlay(limit int64, seconds int, rungs []HLSRung) bool {
	if limit == 0 || len(rungs) == 0 {
		return false
	}
	return seconds <= 0 || float64(seconds)*float64(rungs[0].VideoKbps)*125 >= float64(limit)
}

// Omitted files must also disappear on a retry against a backend without
// prefix deletion. Avoid creating a delete marker for a never-written object.
func deleteExistingOutput(ctx context.Context, blobs storage.Backend, key string) error {
	exists, err := blobs.Exists(ctx, key)
	if err != nil || !exists {
		return err
	}
	return blobs.Delete(ctx, key)
}

func (tools packageTools) omitOversized(ctx context.Context, local, key string) (bool, error) {
	if tools.outputLimit == 0 {
		return false, nil
	}
	info, err := os.Stat(local)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || info.Size() <= tools.outputLimit {
		return false, err
	}
	if err := os.Remove(local); err != nil {
		return false, err
	}
	return true, deleteExistingOutput(ctx, tools.blobs, key)
}

// A silent source or an oversized audio-only download can replace a prior
// generation attempt. Clear the stale asset before advertising the new tree.
func (req packageRequest) topLevelSize(ctx context.Context) (int64, error) {
	local := filepath.Join(req.scratch, HLSAudioDownloadFilename)
	key := path.Join(req.prefix, HLSAudioDownloadFilename)
	omitted, err := req.tools.omitOversized(ctx, local, key)
	if err != nil {
		return 0, err
	}
	if !omitted {
		if _, err := os.Stat(local); errors.Is(err, os.ErrNotExist) {
			if err := req.tools.blobs.Delete(ctx, key); err != nil {
				return 0, err
			}
		} else if err != nil {
			return 0, err
		}
	}
	return directorySize(req.scratch)
}

func (req packageRequest) trickPlay(ctx context.Context, tree treeIO, playlist, media string) (hlsTrickPlayInfo, error) {
	if req.tools.outputLimit > 0 {
		tree.out = localOutput(req.scratch)
		mediaKey := path.Join(req.prefix, tree.rel, media)
		omitted, err := req.tools.omitOversized(ctx, filepath.Join(tree.scratch, media), mediaKey)
		if err != nil {
			return hlsTrickPlayInfo{}, err
		}
		if req.skipTrick || omitted {
			if req.skipTrick {
				if err := deleteExistingOutput(ctx, req.tools.blobs, mediaKey); err != nil {
					return hlsTrickPlayInfo{}, err
				}
			}
			if err := os.Remove(filepath.Join(tree.scratch, playlist)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return hlsTrickPlayInfo{}, err
			}
			return hlsTrickPlayInfo{}, deleteExistingOutput(ctx, req.tools.blobs, path.Join(req.prefix, tree.rel, playlist))
		}
	}
	return finalizeTrickPlayIn(ctx, req.tools.ffprobe, tree, playlist, media)
}
