package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type track struct {
	present    bool
	start, end float64
}

type tracks struct{ video, audio track }

func (t tracks) duration() float64 {
	if !t.video.present {
		return t.audio.end - t.audio.start
	}
	if !t.audio.present {
		return t.video.end - t.video.start
	}
	return max(t.video.end, t.audio.end) - min(t.video.start, t.audio.start)
}

func (t tracks) matches(out tracks) bool {
	if t.video.present != out.video.present || t.audio.present != out.audio.present {
		return false
	}
	for _, pair := range [][2]track{{t.video, out.video}, {t.audio, out.audio}} {
		a, b := pair[0], pair[1]
		if a.present && math.Abs((a.end-a.start)-(b.end-b.start)) > 1 {
			return false
		}
	}
	if t.video.present && t.audio.present && math.Abs((t.audio.start-t.video.start)-(out.audio.start-out.video.start)) > 1 {
		return false
	}
	return math.Abs(t.duration()-out.duration()) <= 1
}

func probeCommand(ctx context.Context, f *os.File, selector string, flags ...string) (*exec.Cmd, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	args := append([]string{"-v", "error"}, inputArgs()...)
	args = append(args, "-select_streams", selector)
	args = append(args, flags...)
	args = append(args, "fd:")
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.ExtraFiles = []*os.File{f}
	return cmd, nil
}

func timestamp(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// Some containers, including Matroska, omit per-track durations. Walk the
// selected track's packet timestamps with bounded memory in that case.
func packetTimes(ctx context.Context, f *os.File, selector string) (track, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := probeCommand(ctx, f, selector, "-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0")
	if err != nil {
		return track{}, err
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return track{}, err
	}
	if err := cmd.Start(); err != nil {
		return track{}, err
	}
	result := track{start: math.Inf(1), end: math.Inf(-1)}
	lines := bufio.NewScanner(pipe)
	for lines.Scan() {
		values := strings.SplitN(lines.Text(), ",", 3)
		if len(values) < 2 {
			continue
		}
		at, ok := timestamp(values[0])
		if !ok {
			continue
		}
		length, _ := timestamp(values[1])
		result.start = min(at, result.start)
		result.end = max(at+max(0, length), result.end)
	}
	if lines.Err() != nil {
		cancel()
	}
	if err := cmd.Wait(); err != nil {
		return track{}, err
	}
	if err := lines.Err(); err != nil {
		return track{}, err
	}
	if result.end <= result.start {
		return track{}, errors.New("selected track has no measurable timeline")
	}
	result.present = true
	return result, nil
}

func inspectTrack(ctx context.Context, f *os.File, selector string) (track, error) {
	cmd, err := probeCommand(ctx, f, selector, "-show_entries", "stream=index,start_time,duration", "-of", "json")
	if err != nil {
		return track{}, err
	}
	out, err := cmd.Output()
	if err != nil {
		return track{}, fmt.Errorf("inspect %s: %w", selector, err)
	}
	var info struct {
		Streams []struct {
			Start    string `json:"start_time"`
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return track{}, err
	}
	if len(info.Streams) == 0 {
		return track{}, nil
	}
	start, _ := timestamp(info.Streams[0].Start)
	dur, ok := timestamp(info.Streams[0].Duration)
	if !ok || dur <= 0 {
		return packetTimes(ctx, f, selector)
	}
	return track{true, start, start + dur}, nil
}

func inspectTracks(ctx context.Context, f *os.File) (tracks, error) {
	v, err := inspectTrack(ctx, f, "V:0")
	if err != nil {
		return tracks{}, err
	}
	a, err := inspectTrack(ctx, f, "a:0")
	if err != nil {
		return tracks{}, err
	}
	if !v.present && !a.present {
		return tracks{}, errors.New("no supported video or audio track")
	}
	return tracks{v, a}, nil
}
