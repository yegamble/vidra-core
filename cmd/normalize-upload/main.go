// normalize-upload runs once inside the isolated, credential-free media container.
// Its input and output are operator-mounted paths. The caller must scan the
// finished MP4 before admitting it to the regular publication pipeline.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/vidra/vidra-core/internal/processedupload"
)

// FFmpeg can resolve URLs embedded in some containers. Use a seekable,
// inherited descriptor and restrict the demuxer to self-contained formats.
func inputArgs() []string {
	return []string{"-protocol_whitelist", "fd", "-format_whitelist",
		"mov,matroska,webm,avi,mpeg,mpegts,flv,ogg,asf,mp3,aac,flac,wav", "-fd", "3"}
}

type profile struct {
	videoKbps int
	audioKbps int
	height    int
}

func chooseProfile(seconds float64, hasVideo bool) (profile, error) {
	// Leave room for the MP4 boxes and the rate controller's final buffer.
	total := int(min(24192, float64(processedupload.MaxScannedBytes)*8*.90/(1000*seconds)))
	if !hasVideo {
		if total < 16 {
			return profile{}, errors.New("duration exceeds the available audio bitrate")
		}
		return profile{audioKbps: min(total, 192)}, nil
	}
	audio := min(192, max(32, total/8))
	video := total - audio
	if video < 100 {
		return profile{}, errors.New("duration exceeds the available processed-file bitrate")
	}
	height := 2160
	switch {
	case video < 600:
		height = 360
	case video < 1500:
		height = 480
	case video < 3000:
		height = 720
	case video < 8000:
		height = 1080
	}
	return profile{video, audio, height}, nil
}

func (p profile) arguments() []string {
	scale := fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:force_divisible_by=2:reset_sar=1", p.height*16/9, p.height)
	rate := func(kbps int) string { return strconv.Itoa(kbps) + "k" }
	args := []string{"-hide_banner", "-nostdin", "-v", "error", "-xerror", "-max_alloc", "268435456", "-threads", "2", "-filter_threads", "2"}
	args = append(args, inputArgs()...)
	args = append(args, "-i", "fd:", "-map", "0:V:0?", "-map", "0:a:0?", "-map_metadata", "-1", "-map_metadata:s", "-1", "-map_chapters", "-1", "-sn", "-dn")
	if p.videoKbps > 0 {
		args = append(args, "-vf", scale, "-c:v", "libx264", "-a53cc", "0", "-threads:v", "2", "-preset", "veryfast", "-crf", "20", "-maxrate", rate(p.videoKbps), "-bufsize", rate(2*p.videoKbps), "-pix_fmt", "yuv420p")
	}
	// Write the first fragment's edit list after the initial timestamps are known.
	// An empty upfront moov loses delayed audio. Bound fragments for audio-only
	// inputs too, since they have no video keyframes to trigger the next fragment.
	return append(args, "-c:a", "aac", "-ac", "2", "-ar", "48000", "-b:a", rate(p.audioKbps), "-movflags", "+frag_keyframe+delay_moov+default_base_moof", "-frag_duration", "5000000", "-f", "mp4", "pipe:1")
}

func normalize(ctx context.Context, input, output string) error {
	src, err := os.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() < 1 || st.Size() > processedupload.MaxInputBytes {
		return errors.New("invalid source file size or type")
	}
	inputTracks, err := inspectTracks(ctx, src)
	if err != nil {
		return err
	}
	p, err := chooseProfile(inputTracks.duration(), inputTracks.video.present)
	if err != nil {
		return err
	}
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// The broker mounts just this empty inode writable. A decoder has no
	// writable host directory. The broker discards the inode on any error.
	dst, err := os.OpenFile(output, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer dst.Close()
	target, err := dst.Stat()
	if err != nil {
		return err
	}
	if !target.Mode().IsRegular() || target.Size() != 0 || os.SameFile(st, target) {
		return errors.New("output must be a separate empty file")
	}
	encodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(encodeCtx, "ffmpeg", p.arguments()...)
	cmd.ExtraFiles = []*os.File{src}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	n, copyErr := io.Copy(dst, io.LimitReader(out, processedupload.MaxScannedBytes+1))
	if copyErr != nil || n > processedupload.MaxScannedBytes {
		cancel()
	}
	waitErr := cmd.Wait()
	switch {
	case copyErr != nil:
		return copyErr
	case n > processedupload.MaxScannedBytes:
		return errors.New("processed file exceeds scanning limit")
	case waitErr != nil:
		return fmt.Errorf("encode media: %w", waitErr)
	}
	outputTracks, err := inspectTracks(ctx, dst)
	if err != nil {
		return err
	}
	if !inputTracks.matches(outputTracks) {
		return errors.New("processed duration differs from source")
	}
	return dst.Sync()
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	if err := normalize(ctx, "/input/source", "/output/processed.mp4"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
