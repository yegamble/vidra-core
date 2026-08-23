package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// --- phase-3 item 7: hardware transcoding ------------------------------------

// hwPlan is a CMAF ladder plan transformed for one backend, the way boot does it.
func hwPlan(t *testing.T, hw string, hevc, av1 bool) ladderPlan {
	t.Helper()
	profiles, err := applyHardware(videoCodecProfiles(hevc, av1), hw, "")
	if err != nil {
		t.Fatalf("applyHardware(%q): %v", hw, err)
	}
	return ladderPlan{rungs: cmafTestRungs(), codecs: profiles, hasAudio: true}
}

// TestHardwareOffChangesNothing is the pin the whole feature is built behind: a
// deployment that never set TRANSCODING_HW must emit the argument vector it
// emitted before hardware support existed, and "off" must be indistinguishable
// from unset. Stated as an EQUALITY against the untransformed plan rather than as
// a list of expected strings, so nothing can be pinned and wrong at once.
func TestHardwareOffChangesNothing(t *testing.T) {
	src, out := localSource("/in/src.mp4"), localOutput("/out")
	for _, codecs := range []struct{ hevc, av1 bool }{{false, false}, {true, false}, {true, true}} {
		software := ladderPlan{rungs: cmafTestRungs(), codecs: videoCodecProfiles(codecs.hevc, codecs.av1), hasAudio: true}
		for _, name := range []string{"", HardwareOff} {
			off := hwPlan(t, name, codecs.hevc, codecs.av1)
			a := hlsLadderArgsWith(cmafPackager{}, src, out, software)
			b := hlsLadderArgsWith(cmafPackager{}, src, out, off)
			if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
				t.Errorf("TRANSCODING_HW=%q (hevc %v, av1 %v) changed the software vector:\n got %s\nwant %s",
					name, codecs.hevc, codecs.av1, strings.Join(b, " "), strings.Join(a, " "))
			}
			if got := hardwareBackendOf(off.profiles()); got != "" {
				t.Errorf("TRANSCODING_HW=%q reports backend %q", name, got)
			}
		}
	}
}

// TestHardwareEncodeArgVectors pins ONE representation's encoder arguments for
// every backend and both codecs it may touch.
//
// These are spelled out in full rather than described, because every one of the
// per-backend differences is a hard ffmpeg error when it is wrong rather than a
// warning: an encoder with no -preset rejects the option outright, and naming a
// software -pix_fmt at an encoder reading GPU memory contradicts the filter graph
// instead of converting anything. A vector that "looks right" is not evidence.
func TestHardwareEncodeArgVectors(t *testing.T) {
	rung := cmafTestRungs()[0] // 480x360 @ 800 kbps
	const keyframes = " -force_key_frames:v:0 expr:gte(t,n_forced*6)"
	rate := func(kbps int) string {
		return fmt.Sprintf(" -b:v:0 %dk -maxrate:v:0 %dk -bufsize:v:0 %dk", kbps, kbps, 2*kbps)
	}
	h264Rate, hevcRate := rate(800), rate(hevcProfile.videoKbps(rung))

	for _, tc := range []struct {
		hw, codec, want string
	}{
		// VideoToolbox: no preset (its lever is -realtime, and the default is the
		// batch setting), software frames, so yuv420p carries over unchanged.
		{HardwareVideoToolbox, VideoCodecH264,
			"-c:v:0 h264_videotoolbox -profile:v:0 main -pix_fmt:v:0 yuv420p" + h264Rate + keyframes},
		{HardwareVideoToolbox, VideoCodecHEVC,
			"-c:v:0 hevc_videotoolbox -tag:v:0 hvc1 -profile:v:0 main -pix_fmt:v:0 yuv420p" + hevcRate + keyframes},

		// VAAPI: no preset either, and NO -pix_fmt — the frames are hardware frames
		// the filter tail already uploaded.
		{HardwareVAAPI, VideoCodecH264,
			"-c:v:0 h264_vaapi -profile:v:0 main" + h264Rate + keyframes},
		{HardwareVAAPI, VideoCodecHEVC,
			"-c:v:0 hevc_vaapi -tag:v:0 hvc1 -profile:v:0 main" + hevcRate + keyframes},

		// QSV is the one backend that borrowed libx264's preset words, so
		// "veryfast" survives the transform meaning the same thing.
		{HardwareQSV, VideoCodecH264,
			"-c:v:0 h264_qsv -profile:v:0 main -preset:v:0 veryfast" + h264Rate + keyframes},
		{HardwareQSV, VideoCodecHEVC,
			"-c:v:0 hevc_qsv -tag:v:0 hvc1 -profile:v:0 main -preset:v:0 veryfast" + hevcRate + keyframes},

		// NVENC: its own p1..p7 scale, software frames, and an EXPLICIT -rc vbr —
		// the default depends on driver and preset, and a rung that ignores -maxrate
		// is picked by an ABR player that then cannot carry it.
		{HardwareNVENC, VideoCodecH264,
			"-c:v:0 h264_nvenc -profile:v:0 main -preset:v:0 p4 -pix_fmt:v:0 yuv420p" + h264Rate + " -rc:v:0 vbr" + keyframes},
		{HardwareNVENC, VideoCodecHEVC,
			"-c:v:0 hevc_nvenc -tag:v:0 hvc1 -profile:v:0 main -preset:v:0 p4 -pix_fmt:v:0 yuv420p" + hevcRate + " -rc:v:0 vbr" + keyframes},
	} {
		plan := hwPlan(t, tc.hw, true, false)
		var prof codecProfile
		for _, p := range plan.profiles() {
			if p.Name == tc.codec {
				prof = p
			}
		}
		got := strings.Join(hlsRungVideoEncodeArgs(rung, prof, sharedVideoStream(0), ""), " ")
		if got != tc.want {
			t.Errorf("%s/%s:\n got %s\nwant %s", tc.hw, tc.codec, got, tc.want)
		}
	}
}

// TestHardwarePreservesCodecIdentity is the invariant that makes hardware a
// TRANSFORM rather than a fourth codec: everything a manifest, an adaptation set
// or a stored row is derived from must survive it untouched. If any of these
// moved, a deployment that turned the knob on would need its trees re-encoded to
// go back, and the rollback would stop being config-only.
func TestHardwarePreservesCodecIdentity(t *testing.T) {
	rung := cmafTestRungs()[0]
	for _, hw := range []string{HardwareVideoToolbox, HardwareVAAPI, HardwareQSV, HardwareNVENC} {
		soft := videoCodecProfiles(true, true)
		hard := hwPlan(t, hw, true, true).profiles()
		if len(soft) != len(hard) {
			t.Fatalf("%s: %d profiles, want %d — a backend must not add or drop a codec", hw, len(hard), len(soft))
		}
		for i := range soft {
			s, h := soft[i], hard[i]
			// Rate, CRF and ScoreBonus are in this list on purpose. The ceiling the
			// master playlist promises, and the rank a client picks on, are
			// properties of the RUNG and the CODEC — not of the silicon — so a
			// backend that changed either would make the manifest lie about a
			// stream it never touched the budget of.
			// EnvVar is deliberately NOT in this list: the hardware transform is
			// allowed to fill an empty one in with TRANSCODING_HW, which is the knob
			// that actually put this encoder in the plan. It is asserted separately
			// by TestHardwareProfilesNameTheirKnob.
			if s.Name != h.Name || s.CodecsID != h.CodecsID || s.Tag != h.Tag ||
				s.BitrateMultiplier != h.BitrateMultiplier || s.Rate != h.Rate ||
				s.CRF != h.CRF || s.ScoreBonus != h.ScoreBonus ||
				s.Profile != h.Profile {
				t.Errorf("%s changed %s's identity:\n got %+v\nwant %+v", hw, s.Name, h, s)
			}
			if s.videoKbps(rung) != h.videoKbps(rung) {
				t.Errorf("%s changed %s's budget: %d != %d", hw, s.Name, h.videoKbps(rung), s.videoKbps(rung))
			}
		}
	}
}

// TestAV1IsNeverHardwareEncoded pins the deliberate exception. Hardware AV1
// encode exists on Arc, Ada and RDNA3 and nowhere else; TRANSCODING_AV1_ENABLED
// means "spend CPU for ~45% fewer bytes", and quietly turning that into "spend a
// little GPU for an unknown number of bytes on the minority of hosts that can" is
// a different setting wearing the same name.
func TestAV1IsNeverHardwareEncoded(t *testing.T) {
	for _, hw := range []string{HardwareVideoToolbox, HardwareVAAPI, HardwareQSV, HardwareNVENC} {
		profiles := hwPlan(t, hw, true, true).profiles()
		av1 := profiles[2]
		if av1.Name != VideoCodecAV1 {
			t.Fatalf("profile 2 is %q, want av1", av1.Name)
		}
		if av1.Encoder != av1Profile.Encoder || av1.Hardware != "" ||
			av1.FilterChain != "" || av1.Preset != av1Profile.Preset {
			t.Errorf("TRANSCODING_HW=%s moved AV1 onto hardware: %+v", hw, av1)
		}
	}
}

// TestHardwareFilterGraphs pins the three graph shapes, which is where the
// backends differ most and where a mistake is silent: a graph that hands
// system-memory frames to a VAAPI encoder fails loudly, but a graph that uploads
// frames a software encoder then reads produces nothing at all.
func TestHardwareFilterGraphs(t *testing.T) {
	src, out := localSource("/in/src.mp4"), localOutput("/out")
	graphOf := func(plan ladderPlan) string {
		return argValue(t, hlsLadderArgsWith(cmafPackager{}, src, out, plan), "-filter_complex")
	}
	for _, tc := range []struct {
		name        string
		hw          string
		hevc, av1   bool
		wantGraph   string
		wantInit    []string
		description string
	}{
		{
			name: "a backend with no upload leaves the graph exactly as it was",
			hw:   HardwareVideoToolbox,
			wantGraph: "[0:v]split=2[b0][b1];" +
				"[b0]scale=480:360[v0];[b1]scale=320:240[v1]",
		},
		{
			name: "one uploading codec appends its tail to the scale, with no fork",
			hw:   HardwareVAAPI,
			wantGraph: "[0:v]split=2[b0][b1];" +
				"[b0]scale=480:360,format=nv12,hwupload[v0];" +
				"[b1]scale=320:240,format=nv12,hwupload[v1]",
			wantInit: []string{"-init_hw_device", "vaapi=vidra_hw:/dev/dri/renderD128", "-filter_hw_device", "vidra_hw"},
		},
		{
			name: "two uploading codecs fork FIRST, then upload once each",
			hw:   HardwareQSV,
			hevc: true,
			wantGraph: "[0:v]split=2[b0][b1];" +
				"[b0]scale=480:360,split=2[u0][u2];" +
				"[u0]format=nv12,hwupload=extra_hw_frames=64[v0];" +
				"[u2]format=nv12,hwupload=extra_hw_frames=64[v2];" +
				"[b1]scale=320:240,split=2[u1][u3];" +
				"[u1]format=nv12,hwupload=extra_hw_frames=64[v1];" +
				"[u3]format=nv12,hwupload=extra_hw_frames=64[v3]",
			wantInit: []string{"-init_hw_device", "qsv=vidra_hw:/dev/dri/renderD128", "-filter_hw_device", "vidra_hw"},
		},
		{
			name: "a software codec beside two hardware ones keeps reading system memory",
			hw:   HardwareVAAPI,
			hevc: true,
			av1:  true,
			wantGraph: "[0:v]split=2[b0][b1];" +
				"[b0]scale=480:360,split=3[u0][u2][u4];" +
				"[u0]format=nv12,hwupload[v0];" +
				"[u2]format=nv12,hwupload[v2];" +
				// AV1 stayed on the CPU, so its link is a `null` off the same scale
				// rather than an upload. This is the assertion the whole per-CODEC
				// fork exists for.
				"[u4]null[v4];" +
				"[b1]scale=320:240,split=3[u1][u3][u5];" +
				"[u1]format=nv12,hwupload[v1];" +
				"[u3]format=nv12,hwupload[v3];" +
				"[u5]null[v5]",
			wantInit: []string{"-init_hw_device", "vaapi=vidra_hw:/dev/dri/renderD128", "-filter_hw_device", "vidra_hw"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := hwPlan(t, tc.hw, tc.hevc, tc.av1)
			if got := graphOf(plan); got != tc.wantGraph {
				t.Errorf("graph:\n got %s\nwant %s", got, tc.wantGraph)
			}
			args := hlsLadderArgsWith(cmafPackager{}, src, out, plan)
			// The device is initialised ONCE however many codecs share it — a second
			// -init_hw_device under the same alias is an error — and it is initialised
			// BEFORE the input, because both the encoders and the filter tails read
			// the alias it binds.
			if init := hwInitArgs(plan.profiles()); strings.Join(init, " ") != strings.Join(tc.wantInit, " ") {
				t.Errorf("init args = %v, want %v", init, tc.wantInit)
			}
			if len(tc.wantInit) > 0 {
				prologue := strings.Join(args[:1+len(tc.wantInit)], " ")
				if want := "-y " + strings.Join(tc.wantInit, " "); prologue != want {
					t.Errorf("prologue = %q, want %q (the device must be initialised before the input)", prologue, want)
				}
			}
			if n := strings.Count(strings.Join(args, " "), "-init_hw_device"); n > 1 {
				t.Errorf("-init_hw_device appears %d times; the alias would collide", n)
			}
		})
	}
}

// TestHardwareDeviceOverride proves TRANSCODING_HW_DEVICE reaches the device
// initialisation. It matters on exactly the host that is hardest to debug: one
// with an iGPU and a discrete card, where the default render node is the wrong
// one and every symptom is "it is slow" rather than an error.
func TestHardwareDeviceOverride(t *testing.T) {
	profiles, err := applyHardware(videoCodecProfiles(false, false), HardwareVAAPI, "/dev/dri/renderD129")
	if err != nil {
		t.Fatalf("applyHardware: %v", err)
	}
	if got := strings.Join(hwInitArgs(profiles), " "); !strings.Contains(got, "vaapi=vidra_hw:/dev/dri/renderD129") {
		t.Errorf("init args = %q, want the configured render node", got)
	}
	// A backend that names no device path must not grow one.
	vt, err := applyHardware(videoCodecProfiles(false, false), HardwareVideoToolbox, "")
	if err != nil {
		t.Fatalf("applyHardware: %v", err)
	}
	if got := hwInitArgs(vt); len(got) != 0 {
		t.Errorf("videotoolbox init args = %v, want none", got)
	}
}

// TestTrickPlayAndDerivedAssetsStaySoftware pins the compatibility floor under a
// hardware ladder. Trick-play is a dense-I-frame rendition at one frame per
// second — a hardware encoder buys nothing on it and its rate control is not
// tuned for that shape — and it runs as its own pass, so it must stay libx264
// whatever the variant ladder is doing.
func TestTrickPlayAndDerivedAssetsStaySoftware(t *testing.T) {
	src, out := localSource("/in/src.mp4"), localOutput("/out")
	plan := hwPlan(t, HardwareVAAPI, true, false)
	trick := strings.Join(hlsTrickPlayLadderArgsWith(cmafPackager{}, src, out, plan), " ")
	if !strings.Contains(trick, "-c:v libx264") {
		t.Errorf("trick-play left libx264 under a hardware ladder:\n%s", trick)
	}
	for _, unwanted := range []string{"vaapi", "hwupload", "-init_hw_device"} {
		if strings.Contains(trick, unwanted) {
			t.Errorf("trick-play picked up %q:\n%s", unwanted, trick)
		}
	}
	if web := strings.Join(webVideoLadderArgs(src, "/out", plan.rungs, 0), " "); !strings.Contains(web, "-c:v libx264") {
		t.Errorf("the standalone web-video ladder left libx264:\n%s", web)
	}
}

// --- boot-time refusals -------------------------------------------------------

func newTestTranscoder(t *testing.T, packager string) *HLSTranscoder {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	tc := NewHLSTranscoder(blobs)
	if err := tc.SetPackager(packager); err != nil {
		t.Fatalf("SetPackager(%q): %v", packager, err)
	}
	return tc
}

// TestSetHardwareRefusesTheMPEGTSPackager pins the CMAF-only rule at the second
// gate. MPEG-TS is the frozen rollback format: "the format we can always go back
// to" stops meaning anything if going back also changes which silicon encoded it.
func TestSetHardwareRefusesTheMPEGTSPackager(t *testing.T) {
	tc := newTestTranscoder(t, PackagerTS)
	for _, hw := range []string{HardwareVideoToolbox, HardwareVAAPI, HardwareQSV, HardwareNVENC} {
		err := tc.SetHardware(hw, "")
		if err == nil {
			t.Fatalf("the MPEG-TS packager accepted TRANSCODING_HW=%s", hw)
		}
		if !strings.Contains(err.Error(), PackagerCMAF) {
			t.Errorf("error %q does not name the packager an operator must switch to", err)
		}
	}
	// off is always fine, on every packager, and leaves the shipped ladder.
	if err := tc.SetHardware(HardwareOff, ""); err != nil {
		t.Fatalf("SetHardware(off) on MPEG-TS: %v", err)
	}
}

// TestSetHardwareRefusesUnknownAndMisplacedValues covers the two operator typos
// that would otherwise be silent: a misspelled backend (which must not fall back
// to software — the operator asked for speed and would never learn they did not
// get it) and a device path handed to a backend that names none.
func TestSetHardwareRefusesUnknownAndMisplacedValues(t *testing.T) {
	tc := newTestTranscoder(t, PackagerCMAF)
	err := tc.SetHardware("vappi", "")
	if err == nil {
		t.Fatal("a misspelled backend was accepted")
	}
	for _, want := range HardwareNames() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not list the accepted value %q", err, want)
		}
	}
	if err := tc.SetHardware(HardwareNVENC, "/dev/dri/renderD128"); err == nil {
		t.Error("nvenc accepted a device path it will not use")
	}
	if err := tc.SetHardware(HardwareVideoToolbox, "/dev/dri/renderD128"); err == nil {
		t.Error("videotoolbox accepted a device path it will not use")
	}
	if err := tc.SetHardware(HardwareVAAPI, "/dev/dri/renderD129"); err != nil {
		t.Errorf("vaapi refused its own device path: %v", err)
	}
}

// TestHardwareBootProbeNamesTheCombination drives the boot probe against stub
// ffmpeg listings.
//
// The message is the point. "This ffmpeg has no hevc_qsv" is never solved by
// turning HEVC off if HEVC is what the operator wanted — it is solved by turning
// the BACKEND off, which leaves HEVC on libx265 — so the failure has to name both
// knobs and say which one keeps the most of what was asked for.
func TestHardwareBootProbeNamesTheCombination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	stub := func(listing string) string {
		path := filepath.Join(t.TempDir(), "ffmpeg-stub")
		if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'EOF'\n"+listing+"EOF\n"), 0o755); err != nil {
			t.Fatalf("write stub: %v", err)
		}
		return path
	}
	const header = "Encoders:\n V..... = Video\n ------\n"
	row := func(name string) string { return " V....D " + name + "  an encoder\n" }
	software := row("libx264") + row("libx265") + row("libsvtav1")
	ctx := context.Background()

	// The realistic shipped-image shape: vaapi present, and it works.
	full := header + software + row("h264_vaapi") + row("hevc_vaapi")
	plan, err := applyHardware(videoCodecProfiles(true, false), HardwareVAAPI, "")
	if err != nil {
		t.Fatalf("applyHardware: %v", err)
	}
	if err := verifyVideoEncoders(ctx, stub(full), plan); err != nil {
		t.Fatalf("a complete vaapi ffmpeg was rejected: %v", err)
	}

	// The shipped image measured for real: h264_vaapi/hevc_vaapi yes, *_qsv and
	// *_nvenc no. Asking for one of those must fail at boot naming the backend.
	for _, hw := range []string{HardwareQSV, HardwareNVENC} {
		p, err := applyHardware(videoCodecProfiles(false, false), hw, "")
		if err != nil {
			t.Fatalf("applyHardware(%s): %v", hw, err)
		}
		err = verifyVideoEncoders(ctx, stub(full), p)
		if err == nil {
			t.Fatalf("TRANSCODING_HW=%s was accepted by an ffmpeg without its encoder", hw)
		}
		for _, want := range []string{"TRANSCODING_HW=" + hw, "h264_" + hw, "TRANSCODING_HW=off"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s error %q does not mention %q", hw, err, want)
			}
		}
	}

	// The combination case: the backend can do H.264 but this build has no HEVC
	// encoder for it, while TRANSCODING_HEVC_ENABLED is on.
	halfVAAPI := header + software + row("h264_vaapi")
	err = verifyVideoEncoders(ctx, stub(halfVAAPI), plan)
	if err == nil {
		t.Fatal("a missing hevc_vaapi was accepted with TRANSCODING_HEVC_ENABLED=true")
	}
	for _, want := range []string{
		"TRANSCODING_HEVC_ENABLED=true", "TRANSCODING_HW=vaapi", "hevc_vaapi",
		"TRANSCODING_HW=off", "libx265", "TRANSCODING_HEVC_ENABLED=false",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — it must name the COMBINATION and both exits", err, want)
		}
	}
	// H.264 alone on the same build is fine: the missing encoder is HEVC's.
	h264Only, err := applyHardware(videoCodecProfiles(false, false), HardwareVAAPI, "")
	if err != nil {
		t.Fatalf("applyHardware: %v", err)
	}
	if err := verifyVideoEncoders(ctx, stub(halfVAAPI), h264Only); err != nil {
		t.Errorf("H.264-only vaapi was rejected by a build that has h264_vaapi: %v", err)
	}
}

// TestHardwarePlanIsProbedEvenWithOneCodec pins the boot probe over a
// single-codec HARDWARE plan.
//
// It was written against a base where the probe ran only for multi-codec plans,
// and it is kept now that the probe is unconditional, because the two guarantees
// are different and only one of them is about this feature: "every plan is
// probed" is a property of SetVideoCodecs, while "a hardware plan is probed for
// the encoder the TRANSFORM chose" is a property of the transform running BEFORE
// the probe. Reorder those two and this deployment probes libx264, finds it,
// boots clean on an image with no h264_nvenc, and dead-letters every upload.
func TestHardwarePlanIsProbedEvenWithOneCodec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	path := filepath.Join(t.TempDir(), "ffmpeg-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'EOF'\nEncoders:\n V..... = Video\n ------\n V....D libx264  x264\nEOF\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	tc := NewHLSTranscoder(blobs)
	tc.bin = path
	if err := tc.SetPackager(PackagerCMAF); err != nil {
		t.Fatalf("SetPackager: %v", err)
	}
	if err := tc.SetHardware(HardwareNVENC, ""); err != nil {
		t.Fatalf("SetHardware: %v", err)
	}
	if err := tc.SetVideoCodecs(false, false); err == nil {
		t.Fatal("a single-codec nvenc plan was accepted by an ffmpeg with no h264_nvenc")
	}
	// The error names the encoder the TRANSFORM asked for, not the one the plan
	// started as. That is the whole assertion: h264_nvenc is what would have run,
	// libx264 is what is present, and a message naming libx264 would send the
	// operator to rebuild an image that is already correct.
	err = tc.SetVideoCodecs(false, false)
	if !strings.Contains(err.Error(), "h264_nvenc") || strings.Contains(err.Error(), "libx264") {
		t.Errorf("error %q must name the hardware encoder the transform chose, not the software one it replaced", err)
	}

	// The same stub with the SOFTWARE plan passes, which is what proves the
	// failure above is about the transform and not about the stub being thin.
	tc2 := NewHLSTranscoder(blobs)
	tc2.bin = path
	if err := tc2.SetPackager(PackagerCMAF); err != nil {
		t.Fatalf("SetPackager: %v", err)
	}
	if err := tc2.SetVideoCodecs(false, false); err != nil {
		t.Errorf("the software H.264 plan was rejected by an ffmpeg that has libx264: %v", err)
	}
}

// --- job-time failure ---------------------------------------------------------

// TestExplainHardwareFailure covers the failure the boot probe structurally
// cannot catch: the encoder is in the build (so the api booted) and the device is
// not in the container (so every job dies). The message must name the backend and
// the knob, and a MISSING device must be permanent — the next attempt finds the
// same empty /dev, so the backoff buys nothing and costs the operator a quarter
// of an hour to be told what was already known.
func TestExplainHardwareFailure(t *testing.T) {
	base := errors.New("media: ffmpeg hls ladder: exit status 1")
	software := videoCodecProfiles(true, true)

	// A software ladder's failure is returned untouched: nothing to explain, and
	// inventing a hardware hint would send the operator hunting for a GPU.
	if got := explainHardwareFailure(base, software, "", func(string) bool { return false }); got != base {
		t.Errorf("a software failure was rewritten: %v", got)
	}

	vaapi, err := applyHardware(videoCodecProfiles(false, false), HardwareVAAPI, "")
	if err != nil {
		t.Fatalf("applyHardware: %v", err)
	}

	missing := explainHardwareFailure(base, vaapi, "", func(string) bool { return false })
	if !IsPermanent(missing) {
		t.Error("a missing device node was left retryable; the retry finds the same empty /dev")
	}
	for _, want := range []string{"vaapi", "/dev/dri/renderD128", "TRANSCODING_HW=off", "devices"} {
		if !strings.Contains(missing.Error(), want) {
			t.Errorf("missing-device error %q does not mention %q", missing, want)
		}
	}

	// The device IS there and the encode still failed — a busy GPU, a wedged
	// driver, a session limit. Those recover, so the retry is correct behaviour.
	present := explainHardwareFailure(base, vaapi, "", func(string) bool { return true })
	if IsPermanent(present) {
		t.Error("a present device's failure was dead-lettered; a busy GPU recovers")
	}
	if !errors.Is(present, base) {
		t.Error("the underlying ffmpeg error was not wrapped, so its stderr tail is lost")
	}
	for _, want := range []string{"vaapi", "TRANSCODING_HW=off", "no automatic per-job fallback"} {
		if !strings.Contains(present.Error(), want) {
			t.Errorf("error %q does not mention %q", present, want)
		}
	}

	// A backend that names no device cannot have permanence established this way,
	// so it stays transient — but it still names itself.
	vt, err := applyHardware(videoCodecProfiles(false, false), HardwareVideoToolbox, "")
	if err != nil {
		t.Fatalf("applyHardware: %v", err)
	}
	got := explainHardwareFailure(base, vt, "", func(string) bool { return false })
	if IsPermanent(got) {
		t.Error("videotoolbox names no device, so its failure cannot be proven deterministic")
	}
	if !strings.Contains(got.Error(), "videotoolbox") {
		t.Errorf("error %q does not name the backend", got)
	}
}

// --- detection ----------------------------------------------------------------

// TestDetectHardware drives the detection helper with faked probes. The two facts
// it ANDs are both necessary and neither is sufficient: an ffmpeg build always
// has h264_vaapi whether or not the machine has a GPU (so encoders alone would
// recommend vaapi to every deployment on earth), and a render node says nothing
// about whether this binary can drive it.
func TestDetectHardware(t *testing.T) {
	encoders := func(names ...string) map[string]bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return m
	}
	verdict := func(all []HardwareAvailability, backend string) HardwareAvailability {
		for _, a := range all {
			if a.Backend == backend {
				return a
			}
		}
		t.Fatalf("no verdict for %q", backend)
		return HardwareAvailability{}
	}

	// The shipped image on a host with a GPU: vaapi is the ONLY backend that can
	// work, because its ffmpeg has no *_qsv and no *_nvenc.
	shipped := HardwareProbe{
		Encoders:    encoders("libx264", "h264_vaapi", "hevc_vaapi"),
		GOOS:        "linux",
		RenderNodes: []string{"/dev/dri/renderD128"},
	}
	got := DetectHardware(shipped)
	if v := verdict(got, HardwareVAAPI); !v.Available || v.Device != "/dev/dri/renderD128" {
		t.Errorf("vaapi verdict = %+v, want available on renderD128", v)
	}
	for _, absent := range []string{HardwareQSV, HardwareNVENC, HardwareVideoToolbox} {
		v := verdict(got, absent)
		if v.Available {
			t.Errorf("%s reported available on the shipped image: %+v", absent, v)
		}
		if v.Why == "" {
			t.Errorf("%s is unavailable with no reason given", absent)
		}
	}
	if a, ok := FirstAvailableHardware(shipped); !ok || a.Backend != HardwareVAAPI {
		t.Errorf("offer = %+v (%v), want vaapi", a, ok)
	}
	if offer := verdict(got, HardwareVAAPI).Offer(); !strings.Contains(offer, "TRANSCODING_HW=vaapi") ||
		!strings.Contains(offer, "/dev/dri/renderD128") {
		t.Errorf("offer %q must name both the knob and the device", offer)
	}

	// The encoder without the device: a GPU-less droplet running the shipped
	// image. This is the case that makes the device half of the AND necessary.
	noGPU := shipped
	noGPU.RenderNodes = nil
	if v := verdict(DetectHardware(noGPU), HardwareVAAPI); v.Available {
		t.Error("vaapi was offered to a host with no render node")
	} else if !strings.Contains(v.Why, "render node") {
		t.Errorf("reason %q does not say what is missing", v.Why)
	}

	// The device without the encoder: a GPU host whose ffmpeg was built without
	// vaapi. This is the case that makes the encoder half necessary.
	noEncoder := shipped
	noEncoder.Encoders = encoders("libx264")
	if v := verdict(DetectHardware(noEncoder), HardwareVAAPI); v.Available {
		t.Error("vaapi was offered by an ffmpeg that cannot encode it")
	} else if !strings.Contains(v.Why, "h264_vaapi") {
		t.Errorf("reason %q does not name the missing encoder", v.Why)
	}

	// macOS: videotoolbox needs no device at all, and the linux-only backends must
	// be refused on their platform rather than on their encoders.
	mac := HardwareProbe{Encoders: encoders("h264_videotoolbox", "hevc_videotoolbox"), GOOS: "darwin"}
	v := verdict(DetectHardware(mac), HardwareVideoToolbox)
	if !v.Available || v.Device != "" {
		t.Errorf("videotoolbox verdict = %+v, want available with no device", v)
	}
	if len(v.Encoders) != 2 {
		t.Errorf("encoders = %v, want both h264 and hevc listed", v.Encoders)
	}
	if v := verdict(DetectHardware(mac), HardwareVAAPI); !strings.Contains(v.Why, "linux") {
		t.Errorf("vaapi on darwin: %q, want the platform named", v.Why)
	}

	// A backend with H.264 but not HEVC is still AVAILABLE — H.264 is the codec
	// hardware is required for — and the encoder list is what tells an operator
	// that TRANSCODING_HEVC_ENABLED would not follow it onto the GPU.
	h264Only := shipped
	h264Only.Encoders = encoders("libx264", "h264_vaapi")
	v = verdict(DetectHardware(h264Only), HardwareVAAPI)
	if !v.Available {
		t.Error("a backend that can encode H.264 was reported unavailable")
	}
	if len(v.Encoders) != 1 || v.Encoders[0] != "h264_vaapi" {
		t.Errorf("encoders = %v, want h264_vaapi alone", v.Encoders)
	}

	// Nothing anywhere: the ordinary droplet. There must be no offer, and that is
	// not a problem — CPU encoding is the default and always works.
	if a, ok := FirstAvailableHardware(HardwareProbe{Encoders: encoders("libx264"), GOOS: "linux"}); ok {
		t.Errorf("a bare host was offered %+v", a)
	}

	// nvenc needs its own signal, and a render node is not it: an NVIDIA card
	// exposes /dev/nvidia*, and /dev/dri on such a host is usually the iGPU.
	nv := HardwareProbe{Encoders: encoders("h264_nvenc", "hevc_nvenc"), GOOS: "linux", RenderNodes: []string{"/dev/dri/renderD128"}}
	if v := verdict(DetectHardware(nv), HardwareNVENC); v.Available {
		t.Error("nvenc was offered on a render node alone")
	}
	nv.NVIDIA = true
	if v := verdict(DetectHardware(nv), HardwareNVENC); !v.Available || v.Device != "" {
		t.Errorf("nvenc verdict = %+v, want available with no named device", v)
	}
}

// TestRenderNodes pins the glob against a faked /dev/dri, since the machine
// running the tests may have one, several, or none.
func TestRenderNodes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"renderD128", "renderD129", "card0", "by-path"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	got := RenderNodes(dir)
	want := []string{dir + "/renderD128", dir + "/renderD129"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("RenderNodes = %v, want %v (card0 is not an encode device)", got, want)
	}
	if got := RenderNodes(filepath.Join(dir, "nope")); got != nil {
		t.Errorf("a missing /dev/dri returned %v, want nil", got)
	}
}

// TestHardwareNames pins the accepted set, which internal/config spells out
// independently. The two lists have to be changed together and this is the side
// that can say so.
func TestHardwareNames(t *testing.T) {
	want := []string{"off", "videotoolbox", "vaapi", "qsv", "nvenc"}
	if got := HardwareNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("HardwareNames = %v, want %v", got, want)
	}
	for _, n := range want {
		if !IsHardwareName(n) {
			t.Errorf("IsHardwareName(%q) = false", n)
		}
	}
	// "" is `off`: a Config assembled by hand has the same zero value as one whose
	// variable was never set, and both mean software.
	if !IsHardwareName("") {
		t.Error(`IsHardwareName("") = false; the empty value must resolve to off`)
	}
	for _, n := range []string{"auto", "cuda", "vappi", "OFF"} {
		if IsHardwareName(n) {
			t.Errorf("IsHardwareName(%q) = true", n)
		}
	}
}

// TestHardwareProfilesNameTheirKnob pins the field the boot probe reads to tell
// an operator how to stop asking for an encoder that is not there.
//
// It exists because of a gap the missing-binary branch made visible. That branch
// reports a plan the probe could not verify at all, and it reaches for EnvVar to
// name the exit — so a hardware H.264 profile carrying the registry's empty
// EnvVar fell through to the no-knob sentence, which says what is missing and not
// one word about how to stop needing it. Software H.264 genuinely has no knob;
// hardware H.264 does, and it is TRANSCODING_HW.
func TestHardwareProfilesNameTheirKnob(t *testing.T) {
	for _, hw := range []string{HardwareVideoToolbox, HardwareVAAPI, HardwareQSV, HardwareNVENC} {
		profiles := hwPlan(t, hw, true, true).profiles()
		h264, hevc, av1 := profiles[0], profiles[1], profiles[2]

		// H.264 had no knob and now has the hardware one.
		if h264.EnvVar != "TRANSCODING_HW" {
			t.Errorf("%s: hardware h264 EnvVar = %q, want TRANSCODING_HW", hw, h264.EnvVar)
		}
		// It is not an optional CODEC though, and the two questions are different:
		// codecKnob is what keeps the probe from writing "TRANSCODING_HW=true …
		// or set TRANSCODING_HW=false", a boolean sentence about an enum naming two
		// values that do not exist.
		if got := h264.codecKnob(); got != "" {
			t.Errorf("%s: hardware h264 codecKnob = %q, want empty — H.264 is not optional", hw, got)
		}

		// HEVC keeps its OWN knob. Overwriting it would lose the exit that keeps
		// the most of what was asked for: dropping to libx265 rather than dropping
		// HEVC altogether.
		if hevc.EnvVar != "TRANSCODING_HEVC_ENABLED" || hevc.codecKnob() != "TRANSCODING_HEVC_ENABLED" {
			t.Errorf("%s: hardware hevc EnvVar = %q / codecKnob = %q, want TRANSCODING_HEVC_ENABLED",
				hw, hevc.EnvVar, hevc.codecKnob())
		}
		// AV1 never went to hardware, so nothing about it moved.
		if av1.EnvVar != "TRANSCODING_AV1_ENABLED" || av1.Hardware != "" {
			t.Errorf("%s: av1 = %+v, want the untouched software profile", hw, av1)
		}
	}

	// And the software plan is untouched: H.264 still has no knob at all, which is
	// what the no-knob sentence is for.
	soft := videoCodecProfiles(true, true)
	if soft[0].EnvVar != "" || soft[0].codecKnob() != "" {
		t.Errorf("software h264 grew a knob: EnvVar=%q", soft[0].EnvVar)
	}
}

// TestHardwareVerdictsWithNoFFmpegAtAll drives the probe's decision table over
// hardware plans, which is the case the base's own table does not cover.
//
// The rule composes rather than being special-cased, and that is the point worth
// pinning: baselineEncoders is keyed on the ENCODER NAME, so swapping libx264 for
// h264_vaapi makes the plan non-baseline all by itself. A default install still
// boots with no ffmpeg on the machine; a hardware install must not, because
// "this host has a working h264_vaapi" is exactly the kind of claim that has to
// be proven before boot rather than discovered one dead-lettered upload at a time.
func TestHardwareVerdictsWithNoFFmpegAtAll(t *testing.T) {
	missing := &exec.Error{Name: "ffmpeg", Err: exec.ErrNotFound}
	hw := func(t *testing.T, name string, hevc, av1 bool) []codecProfile {
		t.Helper()
		return hwPlan(t, name, hevc, av1).profiles()
	}

	// The default software plan is baseline and boots. This is the base's rule and
	// it must survive the hardware work untouched.
	if err := verifiedAgainstEncoderListing("ffmpeg", defaultVideoCodecs, nil, missing); err != nil {
		t.Errorf("a default install refused to boot because ffmpeg is absent: %v", err)
	}

	for _, backend := range []string{HardwareVideoToolbox, HardwareVAAPI, HardwareQSV, HardwareNVENC} {
		t.Run(backend, func(t *testing.T) {
			// Hardware H.264 alone: fatal, and it names TRANSCODING_HW rather than
			// falling through to the sentence that offers no fix.
			err := verifiedAgainstEncoderListing("ffmpeg", hw(t, backend, false, false), nil, missing)
			if err == nil {
				t.Fatal("a hardware plan booted with no ffmpeg to prove the encoder exists")
			}
			for _, want := range []string{"TRANSCODING_HW=" + backend, "h264_" + backend, "TRANSCODING_HW=off", "ffmpeg"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			// The boolean phrasing must never appear: TRANSCODING_HW is an enum.
			for _, unwanted := range []string{"TRANSCODING_HW=true", "TRANSCODING_HW=false"} {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("error %q writes %q, which is not a value this knob takes", err, unwanted)
				}
			}

			// Hardware H.264 + hardware HEVC: the first non-baseline profile decides,
			// and both exits are offered.
			err = verifiedAgainstEncoderListing("ffmpeg", hw(t, backend, true, false), nil, missing)
			if err == nil {
				t.Fatal("a hardware hevc plan booted with no ffmpeg")
			}
			if !strings.Contains(err.Error(), "TRANSCODING_HW="+backend) {
				t.Errorf("error %q does not name the backend", err)
			}
		})
	}

	// TRANSCODING_HW=off is the software plan, so it keeps booting: turning the
	// knob off must undo the strictness it turned on.
	off, err := applyHardware(videoCodecProfiles(false, false), HardwareOff, "")
	if err != nil {
		t.Fatalf("applyHardware(off): %v", err)
	}
	if err := verifiedAgainstEncoderListing("ffmpeg", off, nil, missing); err != nil {
		t.Errorf("TRANSCODING_HW=off refused to boot without ffmpeg: %v", err)
	}
}

// TestSetHardwareWithNoFFmpegAtAll is the same decision at the seam a caller
// actually crosses — boot calls SetHardware then SetVideoCodecs, and the pure
// function above is only reached through them.
//
// It uses noSuchFFmpeg rather than asking PATH anything, for the reason that
// constant exists: a unit test must not have an opinion about what is installed
// on the machine running it. That is exactly how the probe's first version passed
// on every developer laptop and failed CI.
func TestSetHardwareWithNoFFmpegAtAll(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	newTC := func(t *testing.T, hw string) *HLSTranscoder {
		t.Helper()
		tc := NewHLSTranscoder(blobs)
		tc.bin = noSuchFFmpeg
		if err := tc.SetPackager(PackagerCMAF); err != nil {
			t.Fatalf("SetPackager: %v", err)
		}
		if err := tc.SetHardware(hw, ""); err != nil {
			t.Fatalf("SetHardware(%q): %v", hw, err)
		}
		return tc
	}

	// off is the software plan and still boots — the base's rule, unchanged by
	// this feature.
	if err := newTC(t, HardwareOff).SetVideoCodecs(false, false); err != nil {
		t.Errorf("TRANSCODING_HW=off refused to boot because ffmpeg is absent: %v", err)
	}

	// Every backend is the opposite: hardware availability is a per-HOST claim,
	// and a machine with no ffmpeg at all cannot be shown to support it.
	for _, hw := range []string{HardwareVideoToolbox, HardwareVAAPI, HardwareQSV, HardwareNVENC} {
		t.Run(hw, func(t *testing.T) {
			err := newTC(t, hw).SetVideoCodecs(false, false)
			if err == nil {
				t.Fatalf("TRANSCODING_HW=%s booted with no ffmpeg to check", hw)
			}
			for _, want := range []string{"TRANSCODING_HW=" + hw, "h264_" + hw, noSuchFFmpeg, "TRANSCODING_HW=off"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q — it has to name the missing binary AND the knob", err, want)
				}
			}
		})
	}
}
