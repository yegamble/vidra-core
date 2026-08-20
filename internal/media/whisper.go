package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// defaultWhisperTimeout bounds a single transcription round-trip (audio POST →
// response). Transcribing a long track is slow, so this is generous.
const defaultWhisperTimeout = 10 * time.Minute

// maxWhisperResponse caps how many bytes are read from the transcription
// response (verbose_json carries per-segment token arrays, so it can be large).
const maxWhisperResponse = 32 << 20 // 32 MiB

// WhisperClient turns a stored video's audio into a WebVTT caption track using an
// external Whisper transcription service (fix_plan P13). It extracts the audio
// track with ffmpeg (→ 16 kHz mono WAV in a temp file), POSTs it to the
// configured endpoint's /inference route as multipart/form-data, and renders the
// verbose_json response to WebVTT.
//
// The expected endpoint contract is whisper.cpp's server /inference (also
// OpenAI-audio-compatible): a multipart POST with `file` (the audio),
// `response_format=verbose_json`, `temperature=0`, and an optional `language`
// hint, answered with a JSON body carrying a `segments` array of
// {start,end,text} in seconds. The pure argument/rendering helpers are unit-
// tested; the ffmpeg extraction path is covered by a -tags=integration test.
//
// It satisfies captionjob.Transcriber.
type WhisperClient struct {
	blobs    storage.Backend
	endpoint string
	client   *http.Client
	bin      string         // ffmpeg
	extract  audioExtractor // seam: default ffmpeg, stubbed in tests
}

// audioExtractor extracts the audio track of the object at sourceKey to a 16 kHz
// mono WAV temp file, returning its path and a cleanup func. It is the seam that
// lets the worker's HTTP/rendering path be tested without the ffmpeg binary.
type audioExtractor func(ctx context.Context, sourceKey string) (wavPath string, cleanup func(), err error)

// WhisperOption customises a WhisperClient.
type WhisperOption func(*WhisperClient)

// WithWhisperHTTPClient injects the HTTP client used to POST the audio (tests
// point it at an httptest fake whisper server). Production leaves it as the
// timeout-bounded default.
func WithWhisperHTTPClient(c *http.Client) WhisperOption {
	return func(w *WhisperClient) {
		if c != nil {
			w.client = c
		}
	}
}

// WithAudioExtractor overrides the audio-extraction seam (tests substitute a
// stub that writes a canned WAV, so the HTTP + rendering path runs without
// ffmpeg or real media).
func WithAudioExtractor(fn audioExtractor) WhisperOption {
	return func(w *WhisperClient) {
		if fn != nil {
			w.extract = fn
		}
	}
}

// NewWhisperClient builds a WhisperClient posting to endpoint and reading source
// media from blobs. endpoint is trusted operator config (an internal service),
// so it is NOT SSRF-guarded.
func NewWhisperClient(blobs storage.Backend, endpoint string, opts ...WhisperOption) *WhisperClient {
	w := &WhisperClient{
		blobs:    blobs,
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   &http.Client{Timeout: defaultWhisperTimeout},
		bin:      "ffmpeg",
	}
	w.extract = w.ffmpegExtract
	for _, o := range opts {
		o(w)
	}
	return w
}

// Transcribe extracts the audio of the video stored at sourceKey, sends it to
// the transcription service, and returns the resulting WebVTT bytes.
// languageHint (may be empty) is passed through to the service.
func (w *WhisperClient) Transcribe(ctx context.Context, sourceKey, languageHint string) ([]byte, error) {
	wavPath, cleanup, err := w.extract(ctx, sourceKey)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return w.transcribeWav(ctx, wavPath, languageHint)
}

// ffmpegExtract is the default audioExtractor: it runs ffmpeg to write a 16 kHz
// mono PCM WAV of the source's audio track to a temp file.
func (w *WhisperClient) ffmpegExtract(ctx context.Context, sourceKey string) (string, func(), error) {
	src, cleanupSrc, err := openSource(ctx, w.blobs, sourceKey)
	if err != nil {
		return "", func() {}, err
	}
	tmp, err := os.CreateTemp("", "vidra-whisper-*.wav")
	if err != nil {
		cleanupSrc()
		return "", func() {}, err
	}
	dst := tmp.Name()
	_ = tmp.Close()
	cleanup := func() {
		_ = os.Remove(dst)
		cleanupSrc()
	}
	cmd := exec.CommandContext(ctx, w.bin, whisperWavArgs(src, dst)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("media: ffmpeg audio-extract for %q: %w: %s", sourceKey, err, tailOf(stderr.String()))
	}
	return dst, cleanup, nil
}

// whisperWavArgs builds the ffmpeg argument vector extracting src's audio to a
// 16 kHz mono signed-16-bit PCM WAV at dst — the canonical input Whisper models
// expect. Pure (no exec) so it is unit-testable.
func whisperWavArgs(src source, dst string) []string {
	args := []string{"-y"}
	args = append(args, src.inputArgs()...)
	return append(args,
		"-vn",      // drop any video track
		"-ac", "1", // mono
		"-ar", "16000", // 16 kHz
		"-c:a", "pcm_s16le", // signed 16-bit little-endian PCM
		"-f", "wav",
		dst,
	)
}

// transcribeWav POSTs the WAV at wavPath to <endpoint>/inference and renders the
// response to WebVTT. The audio is streamed (not buffered) so a long track does
// not balloon memory.
func (w *WhisperClient) transcribeWav(ctx context.Context, wavPath, languageHint string) ([]byte, error) {
	f, err := os.Open(wavPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()
	go func() {
		var werr error
		defer func() { _ = pw.CloseWithError(werr) }()
		part, e := mw.CreateFormFile("file", "audio.wav")
		if e != nil {
			werr = e
			return
		}
		if _, e = io.Copy(part, f); e != nil {
			werr = e
			return
		}
		if e = mw.WriteField("response_format", "verbose_json"); e != nil {
			werr = e
			return
		}
		if e = mw.WriteField("temperature", "0"); e != nil {
			werr = e
			return
		}
		if languageHint != "" {
			if e = mw.WriteField("language", languageHint); e != nil {
				werr = e
				return
			}
		}
		werr = mw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint+"/inference", pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media: whisper transcription returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWhisperResponse))
	if err != nil {
		return nil, err
	}
	segs, err := parseWhisperResponse(body)
	if err != nil {
		return nil, err
	}
	return renderWebVTT(segs), nil
}

// whisperSegment is one transcription cue: its start/end offsets (seconds) and
// text. It is the subset of the verbose_json response we render.
type whisperSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// whisperResponse is the subset of the transcription service's verbose_json body
// we read.
type whisperResponse struct {
	Segments []whisperSegment `json:"segments"`
}

// parseWhisperResponse decodes the verbose_json body into ordered segments. It
// is pure (no IO) so it is unit-testable with canned fixtures. A malformed body
// is an error; a well-formed body with no segments yields an empty slice (a
// valid, empty caption).
func parseWhisperResponse(body []byte) ([]whisperSegment, error) {
	var out whisperResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("media: parse whisper response: %w", err)
	}
	sort.SliceStable(out.Segments, func(i, j int) bool { return out.Segments[i].Start < out.Segments[j].Start })
	return out.Segments, nil
}

// renderWebVTT renders segments to a WebVTT track. Segments with empty (trimmed)
// text are skipped; timestamps are HH:MM:SS.mmm. Pure (no IO) so it is unit-
// testable. The output always starts with the WEBVTT signature, so it passes the
// caption-store's WebVTT validation even when there are no cues.
func renderWebVTT(segments []whisperSegment) []byte {
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	n := 0
	for _, s := range segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		n++
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s --> %s\n", formatVTTTimestamp(s.Start), formatVTTTimestamp(s.End))
		b.WriteString(text)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// formatVTTTimestamp renders a second offset as a WebVTT timestamp
// (HH:MM:SS.mmm). A negative offset clamps to zero.
func formatVTTTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	totalMS := int64(seconds*1000 + 0.5)
	h := totalMS / 3_600_000
	m := (totalMS / 60_000) % 60
	s := (totalMS / 1000) % 60
	ms := totalMS % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
