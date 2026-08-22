package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// --- phase-3 item 5: codec profiles ------------------------------------------

// TestH264LadderIsUnchangedByTheRegistry is the pin that matters most here, and
// it is stated as an EQUALITY rather than a list of expected strings: the
// argument vector an ordinary install emits must be the one it emitted when
// libx264 was spelled inline, and a plan that names H.264 explicitly must be
// indistinguishable from one that names no codec at all.
func TestH264LadderIsUnchangedByTheRegistry(t *testing.T) {
	src := localSource("/in/src.mp4")
	for _, rungs := range [][]HLSRung{
		cmafTestRungs(),
		PlanHLSLadder(1920, 1080),
		PlanHLSLadder(320, 240),
	} {
		implicit := hlsLadderArgsWith(cmafPackager{}, src, localOutput("/out"), ladderPlan{rungs: rungs, hasAudio: true})
		explicit := hlsLadderArgsWith(cmafPackager{}, src, localOutput("/out"),
			ladderPlan{rungs: rungs, codecs: videoCodecProfiles(false, false), hasAudio: true})
		if strings.Join(implicit, "\x00") != strings.Join(explicit, "\x00") {
			t.Errorf("naming H.264 changes the vector:\n implicit %s\n explicit %s",
				strings.Join(implicit, " "), strings.Join(explicit, " "))
		}
		// And the encoder half is exactly the four options plus capped VBR this
		// package has always emitted, in that order.
		got := strings.Join(hlsRungVideoEncodeArgs(rungs[0], h264Profile, soleVideoStream, ""), " ")
		want := "-c:v libx264 -profile:v main -preset veryfast -pix_fmt yuv420p" +
			" -b:v " + strconv.Itoa(rungs[0].VideoKbps) + "k" +
			" -maxrate " + strconv.Itoa(rungs[0].VideoKbps) + "k" +
			" -bufsize " + strconv.Itoa(2*rungs[0].VideoKbps) + "k" +
			" -force_key_frames expr:gte(t,n_forced*6)"
		if got != want {
			t.Errorf("H.264 encoder args:\n got %s\nwant %s", got, want)
		}
		// H.264 carries no container tag: forcing avc1 would be a no-op that
		// invited someone to "fix" the HEVC one by removing it.
		if strings.Contains(got, "-tag") {
			t.Errorf("H.264 emits a container tag it does not need: %s", got)
		}
	}
}

// TestVideoCodecProfileOrder pins H.264-first. It is the order of representation
// indices, of adaptation sets and of HLS variants, and everything downstream —
// the progressive downloads, the derived web videos, trick-play — addresses the
// H.264 representations as indices 0..len(rungs)-1.
func TestVideoCodecProfileOrder(t *testing.T) {
	cases := []struct {
		hevc, av1 bool
		want      []string
	}{
		{false, false, []string{VideoCodecH264}},
		{true, false, []string{VideoCodecH264, VideoCodecHEVC}},
		{false, true, []string{VideoCodecH264, VideoCodecAV1}},
		{true, true, []string{VideoCodecH264, VideoCodecHEVC, VideoCodecAV1}},
	}
	for _, tc := range cases {
		var got []string
		for _, p := range videoCodecProfiles(tc.hevc, tc.av1) {
			got = append(got, p.Name)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("videoCodecProfiles(%v, %v) = %v, want %v", tc.hevc, tc.av1, got, tc.want)
		}
	}
	// H.264 is never absent, whatever the knobs say.
	for _, p := range []struct{ hevc, av1 bool }{{true, true}, {false, true}, {true, false}} {
		if videoCodecProfiles(p.hevc, p.av1)[0].Name != VideoCodecH264 {
			t.Errorf("H.264 is not first for (hevc %v, av1 %v)", p.hevc, p.av1)
		}
	}
}

// TestCodecBitrateMultipliers checks the budget arithmetic, and specifically
// that H.264's is not arithmetic at all: the compatibility ladder's numbers must
// come through untouched rather than survive a float round-trip.
func TestCodecBitrateMultipliers(t *testing.T) {
	r := HLSRung{Height: 1080, Width: 1920, VideoKbps: 5000, AudioKbps: 160}
	if got := h264Profile.videoKbps(r); got != 5000 {
		t.Errorf("h264 budget = %d, want the rung's own %d", got, r.VideoKbps)
	}
	if got := hevcProfile.videoKbps(r); got != 3250 {
		t.Errorf("hevc budget = %d, want 3250 (0.65x)", got)
	}
	if got := av1Profile.videoKbps(r); got != 2750 {
		t.Errorf("av1 budget = %d, want 2750 (0.55x)", got)
	}
	// The multipliers stack on top of the fps-aware budget rather than replacing
	// it: a 60fps 1080p rung is planned at 8000k, and HEVC's share is of THAT.
	hfr := HLSRung{Height: 1080, Width: 1920, VideoKbps: 8000, AudioKbps: 160}
	if got := hevcProfile.videoKbps(hfr); got != 5200 {
		t.Errorf("hevc budget for a high-frame-rate rung = %d, want 5200 (0.65 x 8000)", got)
	}
	// A rung small enough to round to nothing still gets a positive budget: an
	// encoder handed -b:v 0k does not fail, it produces garbage.
	if got := av1Profile.videoKbps(HLSRung{VideoKbps: 1}); got < 1 {
		t.Errorf("a tiny rung budgets to %d", got)
	}
}

// TestHEVCCarriesTheHVC1Tag is the single most consequential line in the
// registry. Without it ffmpeg writes hev1, warns, and carries on — and the
// measured consequence is not merely "Safari refuses it": ffmpeg's own HLS
// master then computes an EMPTY video codec string for that variant, so the
// manifest is unplayable everywhere.
func TestHEVCCarriesTheHVC1Tag(t *testing.T) {
	got := strings.Join(hlsRungVideoEncodeArgs(HLSRung{VideoKbps: 800}, hevcProfile, sharedVideoStream(2), ""), " ")
	if !strings.Contains(got, "-tag:v:2 hvc1") {
		t.Fatalf("HEVC vector has no hvc1 tag, so it will be written as Safari-breaking hev1: %s", got)
	}
	if strings.Contains(got, "hev1") {
		t.Errorf("HEVC vector asks for hev1: %s", got)
	}
	// The tag has to be stream-qualified like everything else on a shared output,
	// or it would be applied to every video stream including the H.264 ones.
	if strings.Contains(got, "-tag:v ") {
		t.Errorf("the tag is not stream-qualified and would retag the whole ladder: %s", got)
	}
	if !strings.Contains(got, "-c:v:2 libx265") || !strings.Contains(got, "-profile:v:2 main") {
		t.Errorf("HEVC vector: %s", got)
	}
}

// TestAV1IsRateCappedWithCRF pins the rate control that makes AV1's declared
// BANDWIDTH mean anything.
//
// The shape is not a preference. SVT-AV1 refuses `-b:v` together with `-maxrate`
// outright ("Max Bitrate only supported with CRF mode") and the whole ffmpeg
// process dies before writing a byte — which is why AV1 first shipped as plain
// VBR, and why plain VBR was a bug: with no ceiling at all it delivered a segment
// at 3.4x its declared BANDWIDTH on a flat-then-hard clip. Capped CRF is the same
// ceiling expressed the way this encoder accepts it.
func TestAV1IsRateCappedWithCRF(t *testing.T) {
	got := strings.Join(hlsRungVideoEncodeArgs(HLSRung{VideoKbps: 800}, av1Profile, sharedVideoStream(3), ""), " ")
	// The CEILING, which is what BANDWIDTH promises a player.
	for _, want := range []string{"-maxrate:v:3 440k", "-bufsize:v:3 880k"} {
		if !strings.Contains(got, want) {
			t.Errorf("AV1 vector has no rate ceiling (%s): %s", want, got)
		}
	}
	// A QUALITY target underneath it, and deliberately no bitrate target: the two
	// together are what libsvtav1 rejects.
	if !strings.Contains(got, "-crf:v:3 "+strconv.Itoa(av1CRF)) {
		t.Errorf("AV1 vector has no CRF target: %s", got)
	}
	if strings.Contains(got, "-b:v") {
		t.Errorf("AV1 vector sets a bitrate target beside its cap, which libsvtav1 refuses outright: %s", got)
	}
	// SVT-AV1's preset is a NUMBER; "veryfast" is not a value it has.
	if !strings.Contains(got, "-preset:v:3 8") {
		t.Errorf("AV1 preset is not the numeric one libsvtav1 takes: %s", got)
	}
	// libsvtav1 has no `profile` private option at all, so asking for one is an
	// error rather than a hint.
	if strings.Contains(got, "-profile") {
		t.Errorf("AV1 vector sets a profile libsvtav1 does not have: %s", got)
	}
	// Key frames are forced through ffmpeg's own frame flag, which is
	// codec-independent — and without it the segments would not align.
	if !strings.Contains(got, "-force_key_frames:v:3 expr:gte(t,n_forced*6)") {
		t.Errorf("AV1 vector does not force key frames on segment boundaries: %s", got)
	}
}

// TestEveryCodecIsRateCapped is the property that makes the master playlist
// truthful, stated once for all three: whatever mode a codec uses underneath,
// every one of them ends at -maxrate on its own budget and -bufsize at twice it.
// A codec added here without a ceiling would declare a BANDWIDTH it can exceed
// without limit, which is the AV1 bug this exists to prevent a repeat of.
func TestEveryCodecIsRateCapped(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	for _, prof := range videoCodecProfiles(true, true) {
		got := strings.Join(hlsRungVideoEncodeArgs(r, prof, soleVideoStream, ""), " ")
		kbps := prof.videoKbps(r)
		for _, want := range []string{
			"-maxrate " + strconv.Itoa(kbps) + "k",
			"-bufsize " + strconv.Itoa(2*kbps) + "k",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s is not rate-capped (%s missing): %s", prof.Name, want, got)
			}
		}
		// Exactly one of the two targets, never both and never neither.
		bitrate := strings.Contains(got, "-b:v ")
		quality := strings.Contains(got, "-crf ")
		if bitrate == quality {
			t.Errorf("%s sets %s targets: %s", prof.Name,
				map[bool]string{true: "both bitrate and quality", false: "neither a bitrate nor a quality"}[bitrate], got)
		}
	}
}

// TestLadderPlanRepIndexing pins the tree layout every other part of this change
// depends on: each codec's whole ladder in rung order, H.264 first, so the H.264
// representations are 0..len(rungs)-1.
func TestLadderPlanRepIndexing(t *testing.T) {
	rungs := cmafTestRungs()
	plan := ladderPlan{rungs: rungs, codecs: videoCodecProfiles(true, true)}
	if got := plan.videoReps(); got != 6 {
		t.Errorf("videoReps = %d, want 2 rungs x 3 codecs", got)
	}
	want := map[[2]int]int{
		{0, 0}: 0, {0, 1}: 1, // h264
		{1, 0}: 2, {1, 1}: 3, // hevc
		{2, 0}: 4, {2, 1}: 5, // av1
	}
	for k, v := range want {
		if got := plan.repIndex(k[0], k[1]); got != v {
			t.Errorf("repIndex(codec %d, rung %d) = %d, want %d", k[0], k[1], got, v)
		}
	}
	// The single-codec plan keeps rung i at representation i, which is what the
	// MPEG-TS packager and every derived asset read.
	single := ladderPlan{rungs: rungs}
	for i := range rungs {
		if got := single.repIndex(0, i); got != i {
			t.Errorf("single-codec repIndex(0, %d) = %d", i, got)
		}
	}
	if single.videoReps() != len(rungs) {
		t.Errorf("single-codec videoReps = %d, want %d", single.videoReps(), len(rungs))
	}
	// An audio-only plan has no video representations at all.
	if got := (ladderPlan{codecs: videoCodecProfiles(true, true)}).videoReps(); got != 0 {
		t.Errorf("audio-only videoReps = %d, want 0", got)
	}
}

// multiCodecPlan is a 2-rung ladder with all three codecs and audio.
func multiCodecPlan() ladderPlan {
	return ladderPlan{rungs: cmafTestRungs(), codecs: videoCodecProfiles(true, true), hasAudio: true}
}

// TestMultiCodecLadderIsOnePassWithOneScalePerRung pins the shape of the
// multi-codec encode: still ONE input and one dash output, still one scale per
// rung, with the scaled frames forked to that rung's encoders. Adding a codec
// must add encoders, not decodes and not scalers.
func TestMultiCodecLadderIsOnePassWithOneScalePerRung(t *testing.T) {
	plan := multiCodecPlan()
	args := hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), plan)
	joined := strings.Join(args, " ")

	if n := countArg(args, "-i"); n != 1 {
		t.Errorf("-i appears %d times, want 1 (one decode for every codec)", n)
	}
	if n := countArg(args, "-f"); n != 1 {
		t.Errorf("-f appears %d times, want 1 (one dash output)", n)
	}
	graph := argValue(t, args, "-filter_complex")
	if n := strings.Count(graph, "scale="); n != len(plan.rungs) {
		t.Errorf("graph scales %d times, want once per rung (%d): %s", n, len(plan.rungs), graph)
	}
	want := "[0:v]split=2[b0][b1];" +
		"[b0]scale=480:360,split=3[v0][v2][v4];" +
		"[b1]scale=320:240,split=3[v1][v3][v5]"
	if graph != want {
		t.Errorf("graph:\n got %s\nwant %s", graph, want)
	}
	// One map per representation, then the audio.
	if n := countArg(args, "-map"); n != 7 {
		t.Errorf("%d maps, want 6 representations + 1 audio", n)
	}
	// Audio is STILL encoded once, for all three codecs' representations to
	// reference — a three-codec tree stores its audio exactly once.
	if n := strings.Count(joined, "-c:a:"); n != 1 {
		t.Errorf("%d audio encoders, want 1:\n%s", n, joined)
	}
}

// TestMultiCodecLadderEncodersAreGroupedByCodec walks the whole vector and
// asserts each representation index gets its own codec's encoder at its own
// codec's budget — the mapping a wrong index would silently invert.
func TestMultiCodecLadderEncodersAreGroupedByCodec(t *testing.T) {
	plan := multiCodecPlan()
	joined := strings.Join(hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), plan), " ")
	for c, prof := range plan.profiles() {
		for i, r := range plan.rungs {
			rep := strconv.Itoa(plan.repIndex(c, i))
			// Every codec is capped at its OWN budget for this rung, whichever
			// target it uses underneath (see TestEveryCodecIsRateCapped).
			for _, want := range []string{
				"-c:v:" + rep + " " + prof.Encoder,
				"-maxrate:v:" + rep + " " + strconv.Itoa(prof.videoKbps(r)) + "k",
				"-force_key_frames:v:" + rep + " expr:gte(t,n_forced*6)",
			} {
				if !strings.Contains(joined, want) {
					t.Errorf("%s rung %s missing %q:\n%s", prof.Name, r.Name(), want, joined)
				}
			}
		}
	}
	// Nothing unqualified: on a shared output that would be applied to every
	// representation of every codec at once.
	for _, leaked := range []string{" -c:v ", " -tag:v ", " -profile:v ", " -preset ", " -b:v ", " -maxrate "} {
		if strings.Contains(" "+joined+" ", leaked) {
			t.Errorf("unqualified%soption on a shared output:\n%s", leaked, joined)
		}
	}
}

// TestMultiCodecAdaptationSets: one video adaptation set PER CODEC. A set is
// what a player switches within, so an H.264 rung and an AV1 rung in one set
// would claim to be interchangeable mid-playback for a decoder that only has
// one of them.
func TestMultiCodecAdaptationSets(t *testing.T) {
	rungs := cmafTestRungs()
	cases := []struct {
		name string
		plan ladderPlan
		want string
	}{
		{"single codec keeps the shorthand", ladderPlan{rungs: rungs, hasAudio: true}, "id=0,streams=v id=1,streams=a"},
		{"single codec, silent", ladderPlan{rungs: rungs}, "id=0,streams=v"},
		{"audio-only", ladderPlan{codecs: videoCodecProfiles(true, true), hasAudio: true}, "id=0,streams=a"},
		{"h264+hevc", ladderPlan{rungs: rungs, codecs: videoCodecProfiles(true, false), hasAudio: true},
			"id=0,streams=0,1 id=1,streams=2,3 id=2,streams=a"},
		{"all three", multiCodecPlan(), "id=0,streams=0,1 id=1,streams=2,3 id=2,streams=4,5 id=3,streams=a"},
		{"all three, silent", ladderPlan{rungs: rungs, codecs: videoCodecProfiles(true, true)},
			"id=0,streams=0,1 id=1,streams=2,3 id=2,streams=4,5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cmafAdaptationSets(tc.plan); got != tc.want {
				t.Errorf("-adaptation_sets = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMultiCodecThreadsAreSplitAcrossEveryEncoder: transcoding_threads is a
// PER-JOB budget, and with three codecs there are three times as many encoders
// running concurrently in the one process. Dividing by the rung count would
// triple the setting.
func TestMultiCodecThreadsAreSplitAcrossEveryEncoder(t *testing.T) {
	plan := multiCodecPlan()
	plan.threads = 12
	joined := strings.Join(hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), plan), " ")
	// 12 threads across 6 representations is 2 each — not 6 each, which is what
	// dividing by the rung count would have given.
	for rep := 0; rep < 6; rep++ {
		if !strings.Contains(joined, "-threads:v:"+strconv.Itoa(rep)+" 2") {
			t.Errorf("representation %d does not get its share of the thread budget:\n%s", rep, joined)
		}
	}
	if strings.Contains(joined, "-threads:v:0 6") {
		t.Errorf("the budget was divided by the rung count, tripling a deliberately-restrained setting:\n%s", joined)
	}
}

// --- multi-codec manifests ---------------------------------------------------

// ffmpegCMAFMasterMultiCodec is a verbatim capture of what ffmpeg 8.1's dash
// muxer writes for a 2-rung H.264+HEVC ladder with audio under the arguments
// this package builds — the hvc1 tag included, which is why the HEVC variants
// have a codec string at all.
const ffmpegCMAFMasterMultiCodec = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="group_A1",NAME="audio_4",DEFAULT=YES,CHANNELS="2",URI="media_4.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=899485,RESOLUTION=480x360,CODECS="avc1.4d401e,mp4a.40.2",AUDIO="group_A1"
media_0.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=599485,RESOLUTION=320x240,CODECS="avc1.4d400d,mp4a.40.2",AUDIO="group_A1"
media_1.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=619484,RESOLUTION=480x360,CODECS="hvc1.1.6.L63.90,mp4a.40.2",AUDIO="group_A1"
media_2.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=424484,RESOLUTION=320x240,CODECS="hvc1.1.6.L60.90,mp4a.40.2",AUDIO="group_A1"
media_3.m3u8

`

func multiCodecLayout(t *testing.T) cmafLayout {
	t.Helper()
	layout, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMasterMultiCodec), 4)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	layout.codecs = videoCodecProfiles(true, false)
	return layout
}

// TestVerifyCMAFLayoutAgainstPlan covers both halves of the mapping check: which
// CODEC each representation holds, and which RUNG. Every failure here produces a
// tree that plays perfectly and delivers the wrong thing, which is why it is
// checked against ffmpeg's own manifest rather than assumed.
func TestVerifyCMAFLayoutAgainstPlan(t *testing.T) {
	rungs := cmafTestRungs()
	if err := multiCodecLayout(t).verifyAgainstPlan(rungs); err != nil {
		t.Fatalf("a correct layout was rejected: %v", err)
	}
	// H.264-only layouts are verified too, and the shipped ladder passes.
	plain, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMaster), 2)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := plain.verifyAgainstPlan(rungs); err != nil {
		t.Errorf("the H.264 ladder failed its own layout check: %v", err)
	}
	// Audio-only has no video representations to attribute.
	if err := (cmafLayout{hasAudio: true}).verifyAgainstPlan(nil); err != nil {
		t.Errorf("audio-only layout: %v", err)
	}

	t.Run("a representation encoded with the wrong codec is refused", func(t *testing.T) {
		bad := multiCodecLayout(t)
		// The HEVC half came out H.264: the tree plays, at the HEVC rung's
		// smaller budget, and looks like a quality regression.
		bad.videoCodecs[2] = "avc1.4d401e,mp4a.40.2"
		if err := bad.verifyAgainstPlan(rungs); err == nil {
			t.Error("an H.264 stream in an HEVC representation was accepted")
		}
	})

	t.Run("the empty codec string a missing hvc1 tag produces is refused", func(t *testing.T) {
		bad := multiCodecLayout(t)
		// This is the literal string ffmpeg writes when HEVC is muxed without the
		// tag: no video codec at all, and a master no client will start.
		bad.videoCodecs[2] = ",mp4a.40.2"
		if err := bad.verifyAgainstPlan(rungs); err == nil {
			t.Error("a variant with no video codec was accepted")
		}
	})

	// Different ffmpeg builds write different amounts of RFC 6381 detail for the
	// same stream: 8.1 writes "hvc1.1.6.L93.90" where 6.1 writes a bare "hvc1".
	// Both name HEVC, which is the only question this check asks. Demanding the
	// suffix rejected correct trees on every older build — found by running this
	// suite against the ffmpeg the CI runner actually has.
	t.Run("a bare codec identifier with no profile suffix is accepted", func(t *testing.T) {
		ok := multiCodecLayout(t)
		ok.videoCodecs[2] = "hvc1,mp4a.40.2"
		ok.videoCodecs[3] = "hvc1,mp4a.40.2"
		if err := ok.verifyAgainstPlan(rungs); err != nil {
			t.Errorf("an older ffmpeg's abbreviated CODECS was rejected: %v", err)
		}
	})

	// But a codec identifier that merely STARTS THE SAME is not the same codec.
	t.Run("a different codec with a similar name is refused", func(t *testing.T) {
		for _, wrong := range []string{"hvc10,mp4a.40.2", "hev1.1.6.L93.90,mp4a.40.2", "avc1,mp4a.40.2"} {
			bad := multiCodecLayout(t)
			bad.videoCodecs[2] = wrong
			if err := bad.verifyAgainstPlan(rungs); err == nil {
				t.Errorf("CODECS %q was accepted as HEVC", wrong)
			}
		}
	})

	t.Run("a short layout is refused", func(t *testing.T) {
		bad := multiCodecLayout(t)
		bad.videoCodecs = bad.videoCodecs[:3]
		if err := bad.verifyAgainstPlan(rungs); err == nil {
			t.Error("a layout missing a representation was accepted")
		}
	})

	// THE TRANSPOSITION ATTACK. Two same-codec rungs swapped between each other:
	// every index exists, every index is unique, every index carries the right
	// codec. Only the RESOLUTION says anything is wrong — and what is wrong is
	// that the master labels the 480p variant 720p (so an ABR player fetches a
	// rung at a bandwidth it budgeted for a different one), the per-rung
	// progressive downloads are remuxed from the wrong representation, and
	// trick-play indexes the wrong rung.
	t.Run("two same-codec rungs swapped are refused", func(t *testing.T) {
		for _, codec := range []int{0, 1} {
			bad := multiCodecLayout(t)
			a, b := codec*len(rungs), codec*len(rungs)+1
			bad.videoResolutions[a], bad.videoResolutions[b] = bad.videoResolutions[b], bad.videoResolutions[a]
			err := bad.verifyAgainstPlan(rungs)
			if err == nil {
				t.Fatalf("codec %d: transposing representations %d and %d was accepted", codec, a, b)
			}
			if !strings.Contains(err.Error(), "RESOLUTION") {
				t.Errorf("codec %d: error %q does not say what disagreed", codec, err)
			}
		}
	})

	t.Run("a representation with no RESOLUTION is refused", func(t *testing.T) {
		bad := multiCodecLayout(t)
		bad.videoResolutions[1] = ""
		if err := bad.verifyAgainstPlan(rungs); err == nil {
			t.Error("a variant whose rung cannot be verified was accepted")
		}
	})

	t.Run("a resolution that is no rung at all is refused", func(t *testing.T) {
		bad := multiCodecLayout(t)
		bad.videoResolutions[0] = "1920x1080"
		if err := bad.verifyAgainstPlan(rungs); err == nil {
			t.Error("a variant at a resolution the ladder never planned was accepted")
		}
	})
}

// TestCMAFVariantScore pins the SCORE attribute's two properties: resolution
// dominates, and the codec breaks the tie in favour of the efficient one. Without
// SCORE, an Apple client's fallback is close to "the highest BANDWIDTH it can
// play" — and because an efficient codec DECLARES LESS, that fallback picks
// H.264 and inverts the entire feature.
func TestCMAFVariantScore(t *testing.T) {
	const rungs = 3 // 720p, 480p, 360p — index 0 is the tallest
	// Resolution dominates: every 720p variant outranks every 480p one, whatever
	// the codecs are.
	for _, tall := range videoCodecProfiles(true, true) {
		for _, short := range videoCodecProfiles(true, true) {
			if cmafVariantScore(0, rungs, tall) <= cmafVariantScore(1, rungs, short) {
				t.Errorf("720p %s scores no higher than 480p %s — a codec bonus must never outweigh a rung",
					tall.Name, short.Name)
			}
		}
	}
	// Within one rung, hevc > av1 > h264.
	same := func(p codecProfile) float64 { return cmafVariantScore(0, rungs, p) }
	if !(same(hevcProfile) > same(av1Profile) && same(av1Profile) > same(h264Profile)) {
		t.Errorf("codec preference is not hevc > av1 > h264: hevc %v av1 %v h264 %v",
			same(hevcProfile), same(av1Profile), same(h264Profile))
	}
	// No ties anywhere in a full three-codec ladder: a tie is a coin flip the
	// client makes for us.
	seen := map[string]string{}
	for _, prof := range videoCodecProfiles(true, true) {
		for i := 0; i < rungs; i++ {
			key := formatScore(cmafVariantScore(i, rungs, prof))
			if prev, dup := seen[key]; dup {
				t.Errorf("SCORE %s is shared by %s and %s rung %d", key, prev, prof.Name, i)
			}
			seen[key] = prof.Name
		}
	}
	// Rendered as the shortest exact decimal, because it is a decimal attribute
	// and "3.3" is what an operator reading the manifest expects to see.
	if got := formatScore(cmafVariantScore(0, rungs, hevcProfile)); got != "3.3" {
		t.Errorf("top HEVC score renders as %q, want 3.3", got)
	}
}

// TestCMAFAverageBandwidthIsMeasuredNotGuessed: AVERAGE-BANDWIDTH describes the
// tree that exists. It counts the shared audio for the same reason BANDWIDTH
// does — a variant PLAYS the rendition it references — and it is OMITTED rather
// than invented when the tree could not be measured, because a made-up average is
// worse than none.
func TestCMAFAverageBandwidthIsMeasuredNotGuessed(t *testing.T) {
	layout := multiCodecLayout(t)
	if got := cmafVariantAverageBandwidth(layout, 0); got != 0 {
		t.Errorf("an unmeasured layout produced AVERAGE-BANDWIDTH=%d", got)
	}
	// 6 MB of video over 60s = 800,000 bps; 0.9 MB of audio over 60s = 120,000.
	layout.measured = make([]cmafRepMeasure, layout.audioRep+1)
	layout.measured[0] = cmafRepMeasure{bytes: 6_000_000, seconds: 60}
	layout.measured[layout.audioRep] = cmafRepMeasure{bytes: 900_000, seconds: 60}
	if got := cmafVariantAverageBandwidth(layout, 0); got != 920_000 {
		t.Errorf("AVERAGE-BANDWIDTH = %d, want 920000 (video 800000 + shared audio 120000)", got)
	}
	// A variant whose own representation was not measured has no average, even
	// though the audio one does.
	if got := cmafVariantAverageBandwidth(layout, 1); got != 0 {
		t.Errorf("an unmeasured video representation produced AVERAGE-BANDWIDTH=%d", got)
	}
	// A silent tree's average is the video alone.
	silent := layout
	silent.hasAudio = false
	if got := cmafVariantAverageBandwidth(silent, 0); got != 800_000 {
		t.Errorf("silent AVERAGE-BANDWIDTH = %d, want 800000", got)
	}
}

// TestPlaylistDurationSeconds: the duration AVERAGE-BANDWIDTH divides by comes
// from the playlist's own EXTINF values — what the client itself will add up,
// short final segment included.
func TestPlaylistDurationSeconds(t *testing.T) {
	playlist := strings.Join([]string{
		"#EXTM3U",
		`#EXT-X-MAP:URI="init-0.mp4"`,
		"#EXTINF:6.000000,", "chunk-0-00001.m4s",
		"#EXTINF:6.000000,", "chunk-0-00002.m4s",
		"#EXTINF:2.500000,", "chunk-0-00003.m4s",
		"#EXT-X-ENDLIST", "",
	}, "\n")
	if got := playlistDurationSeconds([]byte(playlist)); got != 14.5 {
		t.Errorf("duration = %v, want 14.5", got)
	}
	if got := playlistDurationSeconds([]byte("#EXTM3U\n")); got != 0 {
		t.Errorf("a playlist with no segments measured %v seconds", got)
	}
}

// TestCMAFRepFilePrefixesDoNotCollide is the off-by-one that would silently
// inflate every average: representation 1's segment prefix must not match
// representation 10's, 11's and so on.
func TestCMAFRepFilePrefixesDoNotCollide(t *testing.T) {
	one := cmafRepFilePrefixes(1)
	for _, name := range []string{"chunk-10-00001.m4s", "chunk-11-00002.m4s", "init-10.mp4"} {
		for _, prefix := range one {
			if strings.HasPrefix(name, prefix) {
				t.Errorf("representation 1's prefix %q swallows %q", prefix, name)
			}
		}
	}
	for _, name := range []string{"chunk-1-00001.m4s", "init-1.mp4"} {
		matched := false
		for _, prefix := range one {
			matched = matched || strings.HasPrefix(name, prefix)
		}
		if !matched {
			t.Errorf("representation 1's own file %q is not counted", name)
		}
	}
}

// TestRenderMultiCodecMasterPlaylist pins the master a multi-codec tree serves:
// every codec's variants, H.264 FIRST, each declaring its own codec's budget,
// and trick-play advertised only for the H.264 ones.
func TestRenderMultiCodecMasterPlaylist(t *testing.T) {
	rungs := cmafTestRungs()
	layout := multiCodecLayout(t)
	got, err := renderCMAFMasterPlaylist(rungs, rungs[0].AudioKbps, layout, map[int]hlsTrickPlayInfo{
		360: {Bandwidth: 120000, Codec: "avc1.4d4015"},
		240: {Bandwidth: 90000, Codec: "avc1.4d400d"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Variant order, exactly: the two H.264 rungs, then the two HEVC ones. A
	// client that takes the first variant it understands must land on H.264.
	var variants []string
	for _, line := range strings.Split(got, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			variants = append(variants, line)
		}
	}
	want := []string{"cmaf/media_0.m3u8", "cmaf/media_1.m3u8", "cmaf/media_2.m3u8", "cmaf/media_3.m3u8"}
	if strings.Join(variants, ",") != strings.Join(want, ",") {
		t.Errorf("variant order = %v, want %v (H.264 first)", variants, want)
	}
	first := strings.Index(got, `CODECS="hvc1.`)
	last := strings.LastIndex(got, `CODECS="avc1.4d400d,mp4a.40.2"`)
	if first < 0 || last < 0 || last > first {
		t.Errorf("an HEVC variant precedes an H.264 one:\n%s", got)
	}

	// Each variant declares ITS codec's budget plus the shared audio.
	for c, prof := range layout.profiles() {
		for _, r := range rungs {
			bw := (prof.videoKbps(r) + rungs[0].AudioKbps) * 1000 * 11 / 10
			line := "#EXT-X-STREAM-INF:BANDWIDTH=" + strconv.Itoa(bw) + ",RESOLUTION=" +
				strconv.Itoa(r.Width) + "x" + strconv.Itoa(r.Height)
			if !strings.Contains(got, line) {
				t.Errorf("%s %s does not declare its own budget (%d):\n%s", prof.Name, r.Name(), bw, got)
			}
			if c > 0 && prof.videoKbps(r) == r.VideoKbps {
				t.Errorf("%s is budgeted identically to H.264, so the multiplier is not applied", prof.Name)
			}
		}
	}

	// Trick-play is H.264-only, so exactly one I-frame entry per rung — not one
	// per rung per codec, and none pointing at an HEVC representation.
	if n := strings.Count(got, "#EXT-X-I-FRAME-STREAM-INF:"); n != len(rungs) {
		t.Errorf("%d I-frame entries, want one per rung (%d):\n%s", n, len(rungs), got)
	}
	for i := 2; i < 4; i++ {
		if strings.Contains(got, "iframe-"+strconv.Itoa(i)+".m3u8") {
			t.Errorf("trick-play advertised for a non-H.264 representation:\n%s", got)
		}
	}
	// One audio rendition group for the whole tree, at the representation after
	// the last video one.
	if !strings.Contains(got, `URI="cmaf/media_4.m3u8"`) {
		t.Errorf("the shared audio rendition is not at representation 4:\n%s", got)
	}
	if n := strings.Count(got, "#EXT-X-MEDIA:"); n != 1 {
		t.Errorf("%d audio rendition declarations, want 1:\n%s", n, got)
	}
}

// --- packager capability and the boot-time refusals ---------------------------

// TestPackagerMultiCodecCapability: MPEG-TS is the frozen rollback path and says
// so, rather than a call site inferring it.
func TestPackagerMultiCodecCapability(t *testing.T) {
	var cmaf, ts Packager = cmafPackager{}, tsPackager{}
	if !cmaf.SupportsMultiCodec() {
		t.Error("CMAF must support several codecs: a representation is an index in a shared directory")
	}
	if ts.SupportsMultiCodec() {
		t.Error("MPEG-TS must not claim multi-codec support: a variant there is a directory named for a height")
	}
}

// noSuchFFmpeg is a path that cannot exist, used to make "there is no ffmpeg"
// a DETERMINISTIC condition instead of a property of the machine.
//
// The alternative — leaving the binary as "ffmpeg" and letting PATH decide — is
// exactly the bug this constant exists because of: the probe was made
// unconditional, every developer machine had Homebrew ffmpeg, and the build
// runner did not. A unit test must not have an opinion about what is installed.
const noSuchFFmpeg = "/nonexistent/vidra-test/ffmpeg"

// TestSetVideoCodecsRefusesTheMPEGTSPackager is the in-process second gate on the
// combination config already refuses at boot. It matters because the two settings
// are independent variables an operator can change one at a time — rolling back
// to `ts` while HEVC is still on must stop the process, not silently produce
// H.264 trees while the operator believes they are shipping H.265.
//
// It also pins the ORDER of the two checks. The packager verdict does not depend
// on any binary, so it must be reached without one: with no ffmpeg present, a
// probe running first would report the wrong problem entirely.
func TestSetVideoCodecsRefusesTheMPEGTSPackager(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	tc := NewHLSTranscoder(blobs)
	tc.bin = noSuchFFmpeg
	if err := tc.SetPackager(PackagerTS); err != nil {
		t.Fatalf("SetPackager: %v", err)
	}
	for _, codec := range []struct{ hevc, av1 bool }{{true, false}, {false, true}, {true, true}} {
		err := tc.SetVideoCodecs(codec.hevc, codec.av1)
		if err == nil {
			t.Fatalf("the MPEG-TS packager accepted (hevc %v, av1 %v)", codec.hevc, codec.av1)
		}
		if !strings.Contains(err.Error(), PackagerCMAF) {
			t.Errorf("error %q does not name the packager an operator must switch to", err)
		}
	}
	// H.264-only is always fine, on every packager, and leaves the shipped
	// ladder — with or without an ffmpeg to ask about it.
	if err := tc.SetVideoCodecs(false, false); err != nil {
		t.Fatalf("SetVideoCodecs(false, false) on MPEG-TS: %v", err)
	}
	if got := tc.videoCodecs(); len(got) != 1 || got[0].Name != VideoCodecH264 {
		t.Errorf("codecs = %v, want H.264 alone", got)
	}
}

// TestSetVideoCodecsWithNoFFmpegAtAll pins the semantics that keep this probe
// from being an environment dependency, on the transcoder rather than on the
// helper — because that is the seam a caller actually crosses.
func TestSetVideoCodecsWithNoFFmpegAtAll(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	newTC := func(t *testing.T) *HLSTranscoder {
		t.Helper()
		tc := NewHLSTranscoder(blobs)
		tc.bin = noSuchFFmpeg
		if err := tc.SetPackager(PackagerCMAF); err != nil {
			t.Fatalf("SetPackager: %v", err)
		}
		return tc
	}

	// The DEFAULT plan asks nothing of ffmpeg that this probe has to prove, so a
	// missing binary is not a boot failure. Whether ffmpeg exists at all is
	// checkFFmpeg's and doctor's question, and a transcode that cannot find it
	// fails with a message nobody can misread.
	if err := newTC(t).SetVideoCodecs(false, false); err != nil {
		t.Errorf("a default install refused to boot because ffmpeg is absent: %v", err)
	}

	// An ENABLED extra codec is the opposite: the operator asked for something
	// optional and would otherwise never learn it silently did not happen.
	for _, tc := range []struct {
		name      string
		hevc, av1 bool
		knob      string
	}{
		{"hevc", true, false, "TRANSCODING_HEVC_ENABLED"},
		{"av1", false, true, "TRANSCODING_AV1_ENABLED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newTC(t).SetVideoCodecs(tc.hevc, tc.av1)
			if err == nil {
				t.Fatalf("%s was enabled with no ffmpeg to encode it, and boot continued", tc.name)
			}
			for _, want := range []string{tc.knob, noSuchFFmpeg} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q — it has to name both the missing binary and the knob", err, want)
				}
			}
		})
	}
}

// TestEncoderListingVerdicts is the probe's whole decision table, driven
// directly so every branch is deterministic — including the ones that depend on
// there being no ffmpeg, which is precisely the case a test must never leave to
// the machine it runs on.
func TestEncoderListingVerdicts(t *testing.T) {
	missing := &exec.Error{Name: "ffmpeg", Err: exec.ErrNotFound}
	broken := errors.New("media: listing ffmpeg encoders: exit status 1")
	full := map[string]bool{"libx264": true, "libx265": true, "libsvtav1": true}
	h264Only := map[string]bool{"libx264": true}

	cases := []struct {
		name    string
		want    []codecProfile
		have    map[string]bool
		listErr error
		wantErr string // "" means it must succeed
	}{
		{"everything present", videoCodecProfiles(true, true), full, nil, ""},
		{"default plan, everything present", defaultVideoCodecs, full, nil, ""},
		{"default plan, no ffmpeg at all", defaultVideoCodecs, nil, missing, ""},
		{"hevc enabled, no ffmpeg at all", videoCodecProfiles(true, false), nil, missing, "TRANSCODING_HEVC_ENABLED"},
		{"av1 enabled, no ffmpeg at all", videoCodecProfiles(false, true), nil, missing, "TRANSCODING_AV1_ENABLED"},
		// A binary that EXISTS and misbehaves is a real fault whatever the plan
		// is: something is wrong with the deployment's ffmpeg, not with its
		// configuration, and no leniency applies.
		{"default plan, ffmpeg present but broken", defaultVideoCodecs, nil, broken, "exit status 1"},
		{"hevc enabled, ffmpeg present but broken", videoCodecProfiles(true, false), nil, broken, "exit status 1"},
		// A listing that SUCCEEDS and lacks the encoder keeps the behaviour it
		// has always had, for the baseline codec as much as the optional ones.
		{"hevc enabled, listing lacks libx265", videoCodecProfiles(true, false), h264Only, nil, "TRANSCODING_HEVC_ENABLED"},
		{"default plan, listing lacks libx264", defaultVideoCodecs, map[string]bool{}, nil, "every transcode needs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifiedAgainstEncoderListing("ffmpeg", tc.want, tc.have, tc.listErr)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error mentioning %q, got success", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestBaselineEncodersAreTheOnesEveryBuildHas pins WHICH encoders a missing
// binary is forgiven for, because that set is the whole leniency rule — and
// because a hardware backend swapping an encoder in must fall outside it. A
// hardware encoder's availability is a property of the HOST, which is exactly the
// kind of claim that has to be proven before boot rather than discovered per job.
func TestBaselineEncodersAreTheOnesEveryBuildHas(t *testing.T) {
	if !baselineEncoders[h264Profile.Encoder] {
		t.Errorf("%q is the compatibility floor and must be baseline", h264Profile.Encoder)
	}
	for _, p := range []codecProfile{hevcProfile, av1Profile} {
		if baselineEncoders[p.Encoder] {
			t.Errorf("%s is opt-in, so %q must not be baseline", p.Name, p.Encoder)
		}
	}
	for _, hw := range []string{"h264_vaapi", "hevc_videotoolbox", "h264_nvenc", "av1_qsv"} {
		if baselineEncoders[hw] {
			t.Errorf("%q is a hardware encoder; its availability is per host and must be proven, not assumed", hw)
		}
	}
}

// TestVerifyVideoEncoders drives the boot probe against a stub ffmpeg, so the
// failure an operator would otherwise meet as "every upload dead-lettered" is
// exercised without needing a specially-built binary to hand.
func TestVerifyVideoEncoders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	stub := func(t *testing.T, listing string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "ffmpeg-stub")
		body := "#!/bin/sh\ncat <<'EOF'\n" + listing + "EOF\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write stub: %v", err)
		}
		return path
	}
	const full = `Encoders:
 V..... = Video
 ------
 V....D libx264              libx264 H.264 / AVC (codec h264)
 V....D libx265              libx265 H.265 / HEVC (codec hevc)
 V..... libsvtav1            SVT-AV1 encoder (codec av1)
`
	ctx := context.Background()
	all := videoCodecProfiles(true, true)

	if err := verifyVideoEncoders(ctx, stub(t, full), all); err != nil {
		t.Fatalf("a complete ffmpeg was rejected: %v", err)
	}

	// The realistic failure: a distro or slim-image ffmpeg without AV1.
	noAV1 := strings.Replace(full, " V..... libsvtav1            SVT-AV1 encoder (codec av1)\n", "", 1)
	err := verifyVideoEncoders(ctx, stub(t, noAV1), all)
	if err == nil {
		t.Fatal("a missing libsvtav1 was accepted")
	}
	for _, want := range []string{"libsvtav1", "TRANSCODING_AV1_ENABLED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — it has to name the encoder AND the knob to turn off", err, want)
		}
	}

	noHEVC := strings.Replace(full, " V....D libx265              libx265 H.265 / HEVC (codec hevc)\n", "", 1)
	err = verifyVideoEncoders(ctx, stub(t, noHEVC), all)
	if err == nil || !strings.Contains(err.Error(), "TRANSCODING_HEVC_ENABLED") {
		t.Errorf("missing libx265 = %v, want it attributed to TRANSCODING_HEVC_ENABLED", err)
	}

	// A binary that cannot be run at all is an error, not a pass: "we could not
	// check" must never be reported as "it is fine".
	if err := verifyVideoEncoders(ctx, filepath.Join(t.TempDir(), "nope"), all); err == nil {
		t.Error("an unrunnable ffmpeg was accepted")
	}
}

// TestFFmpegEncoderNamesParsesTheRealListing pins the parser against the layout
// ffmpeg prints, including the legend rows that carry the same flag block as the
// encoder rows.
func TestFFmpegEncoderNamesParsesTheRealListing(t *testing.T) {
	got := ffmpegEncoderNames([]byte(`Encoders:
 V..... = Video
 A..... = Audio
 ------
 V....D libx264              libx264 H.264 / AVC (codec h264)
 A....D aac                  AAC (Advanced Audio Coding)
`))
	if !got["libx264"] || !got["aac"] {
		t.Errorf("encoders = %v", got)
	}
	for _, unwanted := range []string{"=", "Encoders:", "------", "Video"} {
		if got[unwanted] {
			t.Errorf("parser took %q for an encoder name", unwanted)
		}
	}
}
