package media

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"
)

// --- the video codec registry ------------------------------------------------
//
// A ladder rung is a RESOLUTION. A codec profile is what that resolution is
// encoded WITH. Before this file there was exactly one answer — libx264, spelled
// out inline — and the two ideas were the same thing. They are not: a CMAF tree
// can carry the same rung encoded several ways, as several representations a
// player picks between on the CODECS string alone.
//
// WHY A REGISTRY AND NOT A FLAG. Each codec differs from H.264 in more than its
// encoder name: HEVC needs a container tag most tooling forgets, AV1's preset is
// a NUMBER rather than a word and its encoder wants its rate cap expressed as
// capped CRF rather than the capped VBR the other two take, each earns its own
// bitrate budget, and each ranks differently for the clients that read SCORE.
// Spreading those differences across the argument builders as conditionals is how
// a codec ends up half-added. They are stated once, here, and the builders read
// them.
//
// WHAT IS DELIBERATELY NOT HERE: hardware encoders (they are a different
// decision — availability is per HOST, not per build, and a fallback policy has
// to exist before one can be offered), PER-TITLE analysis (the CRF below is a
// fixed quality target, not one derived from the source's own complexity), and
// any MPEG-TS spelling of any of this. TRANSCODING_PACKAGER=ts is the frozen
// compatibility/rollback path and stays single-codec H.264 by construction.

// The operator-facing codec names. They are also what a profile reports as its
// own Name, so a log line or an error message says "hevc" and not "libx265".
const (
	VideoCodecH264 = "h264"
	VideoCodecHEVC = "hevc"
	VideoCodecAV1  = "av1"
)

// codecProfile is one entry of the registry: everything the encode-argument
// builders and the manifest writers need to emit and then VERIFY one codec's
// representations.
type codecProfile struct {
	// Name is the operator-facing codec name (VideoCodecH264 …).
	Name string

	// Encoder is the ffmpeg encoder this profile uses — the -c:v value, and the
	// exact name the availability probe looks for in `ffmpeg -encoders`.
	Encoder string

	// Tag is the ISOBMFF sample-entry code forced with -tag:v, empty when the
	// muxer's own default is already right.
	//
	// It is load-bearing for exactly one codec and the failure it prevents is
	// silent: given no tag, ffmpeg writes HEVC into fMP4 as `hev1` (parameter
	// sets in-band) rather than `hvc1` (parameter sets in the sample entry),
	// warns about it, and carries on. Safari plays hvc1 and refuses hev1 — and
	// worse, ffmpeg's own HLS master then computes an EMPTY codec string for that
	// variant, so the manifest is unplayable everywhere rather than only on
	// Safari. Measured on ffmpeg 8.1: with the tag, CODECS="hvc1.1.6.L63.90";
	// without it, CODECS=",mp4a.40.2".
	Tag string

	// Profile is the -profile:v value, empty when the encoder exposes no profile
	// option. libx264 and libx265 both take "main"; libsvtav1 has no `profile`
	// private option at all and defaults to AV1 Main for 8-bit 4:2:0, which is
	// what this pipeline emits.
	Profile string

	// Preset is the -preset value. Note the type collision the registry exists to
	// absorb: x264/x265 presets are WORDS ("veryfast"), SVT-AV1's is an INTEGER
	// 0–13 where higher is faster.
	Preset string

	// Rate is how this encoder is told what to spend. See rateControl: the two
	// modes exist because the encoders genuinely disagree about what they accept,
	// and getting it wrong here is what makes a manifest lie.
	Rate rateControl

	// CRF is the quality target for rateCappedCRF, ignored otherwise.
	CRF int

	// ScoreBonus is this codec's contribution to the SCORE attribute, added to a
	// rung's own rank so resolution dominates and codec breaks the tie. See
	// cmafVariantScore for the values and the reasoning.
	ScoreBonus float64

	// CodecsPrefix is the RFC 6381 prefix ffmpeg must write for this encoder in
	// the CODECS attribute of every one of its variants. It is what turns "the
	// rung → representation mapping is verified" into "the rung → representation
	// → CODEC mapping is verified": a tree whose HEVC representations were
	// silently encoded as H.264 (or tagged hev1) plays, it just plays the wrong
	// thing on the wrong clients.
	CodecsPrefix string

	// BitrateMultiplier scales the H.264 ladder budget (hlsRungBitrates, already
	// fps-adjusted) into this codec's own. See the constants below for where the
	// numbers come from.
	BitrateMultiplier float64

	// EnvVar is the boot-baked knob that turns this codec on, named in the
	// availability probe's failure so an operator is told what to switch off
	// rather than left to guess. Empty for H.264, which cannot be turned off.
	EnvVar string

	// Compat and Cost are the operator-facing notes: which clients can play this,
	// and what emitting it costs. They are documentation that travels with the
	// profile instead of drifting in a wiki.
	Compat string
	Cost   string
}

// rateControl is HOW an encoder is told what to spend — and it is a per-codec
// property because a manifest's BANDWIDTH is a PEAK, and only rate control makes
// a peak bounded.
//
// The failure this type exists to prevent was measured, not imagined. AV1 was
// first shipped here as plain VBR (`-b:v` alone) because SVT-AV1 rejects
// `-b:v` + `-maxrate` outright ("Max Bitrate only supported with CRF mode").
// Plain VBR has no ceiling at all: on a 54s-flat-then-6s-hard clip the AV1 rung
// delivered a segment at 3.4x its declared BANDWIDTH (6,194,572 bps against
// 1,834,800), which is worse than the H.264 rung at the same moment. An ABR
// player picks on the declared number, cannot un-pick a segment already in
// flight, and drains its buffer — so the manifest was not merely imprecise, it
// was actively harmful.
//
// SVT-AV1 does accept a cap; it just wants it expressed as CAPPED CRF rather
// than capped VBR — `-crf <av1CRF> -maxrate <budget> -bufsize <2x budget>`, with no
// -b:v. That turns the 3.4x into 0.842x of the declared value on the same clip,
// against H.264's own 1.031x. See TestCMAFDeclaredBandwidthBoundsWhatIsDelivered,
// which re-measures both, and the counterfactual inside it, which re-measures the
// uncapped encode so the comparison cannot rot into folklore.
type rateControl int

const (
	// rateCappedVBR targets the budget with -b:v and bounds it with -maxrate at
	// the budget and -bufsize at twice it. x264 and x265, and byte-for-byte what
	// this package has always emitted.
	rateCappedVBR rateControl = iota

	// rateCappedCRF targets a QUALITY with -crf and bounds the rate with the same
	// -maxrate/-bufsize pair, emitting no -b:v at all.
	//
	// It is a better mode than capped VBR, not a workaround for a limitation:
	// easy content costs what it costs instead of being padded up to a budget, so
	// the budget becomes a CEILING rather than a target and the average lands well
	// under it. That is why the AV1 multiplier below is a bound on the worst case
	// rather than a prediction of the mean, and why AVERAGE-BANDWIDTH in the
	// master is computed from the bytes actually stored.
	rateCappedCRF
)

// Peak allowances, in permille of a rung's budget: how far above that budget one
// segment may actually land, and therefore what the master playlist has to
// DECLARE if BANDWIDTH is to be a bound rather than a wish.
//
// There is ONE number because, once each codec's own target is chosen to fit its
// own budget (see av1CRF), all three land inside the same ~10% container
// allowance this package has always applied. Measured worst segments on the
// flat-then-hard fixture: H.264 1.031x, HEVC 0.945x, AV1 0.842x.
//
// It very nearly was not one number. At CRF 35 the AV1 rung needed 1.25x, and
// declaring that would have been the honest thing to do — but it would also have
// priced AV1's 720p variant level with HEVC's, throwing away the reason to emit
// it. Fixing the encoder's target was the better trade, and it is why the CRF
// constant is documented as a consequence of the multiplier rather than as taste.
const (
	peakPermille = 1100
	// audioPeakPermille is the same allowance on the shared audio
	// representation, which is CBR AAC and needs nothing more.
	audioPeakPermille = 1100
)

// The bitrate multipliers, and where they come from.
//
// Both are conservative readings of the same literature, on purpose: an
// over-optimistic multiplier does not show up as a bigger bill, it shows up as a
// worse-looking video than the H.264 rung sitting next to it in the same
// manifest, which is the one outcome a second codec must not produce.
const (
	// hevcBitrateMultiplier is 0.65 — a 35% cut.
	//
	// The original JCT-VC subjective study (Ohm et al., "Comparison of the Coding
	// Efficiency of Video Coding Standards", IEEE TCSVT 22(12), 2012) measured
	// ~50% BD-rate saving for HEVC over H.264 at equal quality, but at very slow
	// settings. Practical VOD ladders assume far less: Apple's HLS Authoring
	// Specification publishes an HEVC ladder roughly 30–40% below its H.264 one at
	// the same resolutions, and Bitmovin's and Netflix's published per-title
	// results land in the same band for real-time-ish presets. 0.65 is the
	// conservative end of that band, which is the right end for a `veryfast`
	// preset.
	hevcBitrateMultiplier = 0.65

	// av1BitrateMultiplier is 0.55 — a 45% cut.
	//
	// AOM's and Netflix's AV1-vs-H.264 comparisons, and the Moscow State
	// University codec comparisons, put AV1 around 45–50% below H.264 in BD-rate
	// (and ~25–30% below HEVC). SVT-AV1 at a fast preset gives part of that back.
	// 0.55 keeps AV1 visibly cheaper than HEVC without betting the picture on the
	// headline number. Under capped CRF it is a CEILING rather than a target, so
	// easy content lands well below it and AVERAGE-BANDWIDTH says so.
	av1BitrateMultiplier = 0.55

	// av1CRF is the quality target SVT-AV1 encodes to underneath its rate cap: 40
	// on its 0–63 scale.
	//
	// It is CHOSEN TO FIT THE BUDGET, and that is the whole reasoning. SVT-AV1's
	// max_bit_rate is a sliding-window cap rather than a hard per-segment VBV, so
	// it does not rescue a quality target that is simply richer than the budget —
	// it only trims the edges. Measured at 720p against a 1540k cap on uniformly
	// hard content, the worst segment lands at 1.279x the cap at CRF 35, 1.120x at
	// CRF 38 and 0.985x at CRF 40. Only at 40 is the encoder's own appetite inside
	// the budget, which is what makes the cap a backstop instead of the primary
	// constraint — and what makes the declared BANDWIDTH true.
	//
	// It costs nothing on easy content, which is the point of capped CRF: on the
	// flat-then-hard fixture the same rung averages 156,966 bps at CRF 40 against
	// 204,014 at CRF 35, both a tiny fraction of the ceiling.
	av1CRF = 40
)

// SCORE bonuses, one per codec, added to a rung's own rank.
//
// SCORE (RFC 8216bis §4.4.6.2 / Apple's HLS Authoring Specification) is "an
// abstract, relative measure of the playback quality-of-experience"; a client
// filters variants to the ones its bandwidth supports and then SHOULD take the
// highest SCORE among them. Without it, AVFoundation's fallback is closer to
// "the highest BANDWIDTH it can play" — and because an efficient codec DECLARES
// LESS, that fallback picks H.264 and inverts the entire feature.
//
// The order is hevc > av1 > h264, and it is an Apple-platform judgement because
// SCORE is an Apple-platform mechanism: HEVC is hardware-decoded on every Mac,
// iPhone and Apple TV that runs a Safari able to reach these manifests, while
// AV1 hardware decode arrived only with the A17 Pro and M3. A software AV1
// decode outranking a hardware HEVC one would cost battery and drop frames to
// save bytes the device was not short of. Clients that ignore SCORE entirely —
// hls.js does — are unaffected either way.
const (
	h264ScoreBonus = 0.1
	av1ScoreBonus  = 0.2
	hevcScoreBonus = 0.3
)

// The registry itself. H.264 first is not cosmetic: it is the order every
// representation index, every adaptation set and every HLS variant is emitted
// in, and H.264-first is what a naive player that takes the first variant it
// understands needs.
var (
	// h264Profile is the COMPATIBILITY FLOOR. It is exactly what this package has
	// always emitted, argument for argument, and it cannot be turned off: it is
	// the source of the progressive downloads, the derived web videos, the
	// audio-only extraction and the trick-play renditions, and it is the only
	// codec every client in existence can play.
	h264Profile = codecProfile{
		Name:              VideoCodecH264,
		Encoder:           "libx264",
		Profile:           "main",
		Preset:            "veryfast",
		Rate:              rateCappedVBR,
		ScoreBonus:        h264ScoreBonus,
		CodecsPrefix:      "avc1.",
		BitrateMultiplier: 1,
		Compat:            "every browser, every mobile OS, every set-top box made this century; the only codec safe to ship alone",
		Cost:              "the baseline this pipeline is budgeted and tuned for",
	}

	hevcProfile = codecProfile{
		Name:              VideoCodecHEVC,
		Encoder:           "libx265",
		Tag:               "hvc1",
		Profile:           "main",
		Preset:            "veryfast",
		Rate:              rateCappedVBR,
		ScoreBonus:        hevcScoreBonus,
		CodecsPrefix:      "hvc1.",
		BitrateMultiplier: hevcBitrateMultiplier,
		EnvVar:            "TRANSCODING_HEVC_ENABLED",
		Compat:            "Safari (macOS/iOS/tvOS), Edge and Chrome on hardware-decoding devices, most 2016+ smart TVs; NOT Firefox on desktop",
		Cost:              "roughly 3–5x H.264's encode time at the same preset, for ~35% fewer delivered bytes",
	}

	av1Profile = codecProfile{
		Name:    VideoCodecAV1,
		Encoder: "libsvtav1",
		// SVT-AV1 preset 8 of 0–13. 0 is glacial and 13 is a proof of concept; 8
		// is the usual VOD compromise and is roughly comparable in wall-clock to
		// x265's veryfast on the same content.
		Preset:            "8",
		Rate:              rateCappedCRF,
		CRF:               av1CRF,
		ScoreBonus:        av1ScoreBonus,
		CodecsPrefix:      "av01.",
		BitrateMultiplier: av1BitrateMultiplier,
		EnvVar:            "TRANSCODING_AV1_ENABLED",
		Compat:            "Chrome/Edge 70+, Firefox 67+, Safari 17+ on hardware that decodes it; falls back to the H.264 variant elsewhere",
		Cost:              "the most expensive of the three to encode, for ~45% fewer delivered bytes",
	}

	// defaultVideoCodecs is the single-codec ladder every ordinary install
	// produces. A plan with no codec list resolves to this, so nothing that
	// predates the registry has to be told about it.
	defaultVideoCodecs = []codecProfile{h264Profile}
)

// videoCodecProfiles resolves the boot-baked codec knobs into the ladder's codec
// plan, in emission order. H.264 is always first and always present.
func videoCodecProfiles(hevc, av1 bool) []codecProfile {
	profiles := []codecProfile{h264Profile}
	if hevc {
		profiles = append(profiles, hevcProfile)
	}
	if av1 {
		profiles = append(profiles, av1Profile)
	}
	return profiles
}

// videoKbps is one rung's budget for THIS codec: the H.264 table value (already
// scaled for the ladder's effective frame rate) times this profile's multiplier.
//
// A multiplier of 1 returns the rung's own number bit for bit rather than
// round-tripping it through float64 — the H.264 ladder must be pin-identical to
// what it was before a second codec existed, and "the arithmetic happens to come
// out the same" is a weaker guarantee than "the arithmetic does not happen".
func (p codecProfile) videoKbps(r HLSRung) int {
	if p.BitrateMultiplier == 1 {
		return r.VideoKbps
	}
	kbps := int(math.Round(float64(r.VideoKbps) * p.BitrateMultiplier))
	if kbps < 1 {
		return 1
	}
	return kbps
}

// --- encoder availability ----------------------------------------------------

// videoEncoderProbeTimeout bounds the one `ffmpeg -encoders` this package runs.
// It is generous for a subprocess that only prints a table, and it exists
// because this probe sits on the BOOT path: an ffmpeg wedged on a broken shared
// library or a stalled filesystem would otherwise hang the api forever, with no
// log line to say why. A timeout turns that into a startup error naming the
// binary.
const videoEncoderProbeTimeout = 10 * time.Second

// verifyVideoEncoders asks the ffmpeg that will actually run the transcodes
// whether it HAS the encoders this deployment's codec plan needs, and returns an
// operator-actionable error when it does not.
//
// It runs at transcoder construction — boot — rather than at the first
// transcode, because the alternative is a deployment that starts cleanly, looks
// healthy, and then dead-letters every video with an ffmpeg stderr tail. The
// probe is one `ffmpeg -encoders` for the whole process lifetime; the encoder set
// is a property of the BUILD and cannot change under a running binary.
//
// It runs for the H.264-ONLY plan too, which is every ordinary install. That
// costs one subprocess at boot and buys the same guarantee for the codec every
// deployment actually depends on: an image built without libx264 currently
// passes the ffmpeg-is-on-PATH check and then fails every single transcode.
func verifyVideoEncoders(ctx context.Context, ffmpegBin string, profiles []codecProfile) error {
	var want []codecProfile
	for _, p := range profiles {
		if p.Encoder == "" {
			continue
		}
		want = append(want, p)
	}
	if len(want) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, videoEncoderProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegBin, "-hide_banner", "-encoders")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("media: listing %s encoders: %w: %s", ffmpegBin, err, tailOf(stderr.String()))
	}
	have := ffmpegEncoderNames(out)
	for _, p := range want {
		if have[p.Encoder] {
			continue
		}
		knob := p.EnvVar
		if knob == "" {
			// H.264 has no knob to turn off, and an ffmpeg without libx264 cannot
			// transcode anything at all — say that rather than offering a fix that
			// does not exist.
			return fmt.Errorf("media: this ffmpeg (%s) has no %q encoder, which every transcode needs; "+
				"install an ffmpeg built with %s", ffmpegBin, p.Encoder, p.Encoder)
		}
		return fmt.Errorf("media: %s=true needs the %q encoder and this ffmpeg (%s) does not have it; "+
			"install an ffmpeg built with %s, or set %s=false",
			knob, p.Encoder, ffmpegBin, p.Encoder, knob)
	}
	return nil
}

// ffmpegEncoderNames is the set of encoder names in `ffmpeg -encoders` output.
//
// The listing is a legend, a rule of dashes, and then one encoder per line as
// "<six flag characters> <name> <description>". Only the second field is read.
// The legend's own rows carry the same flag block (" V..... = Video"), so they
// are told apart by that second field being the "=" no encoder is named.
func ffmpegEncoderNames(out []byte) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, " ") {
			// Encoder rows are indented under the legend; "Encoders:" and the
			// dashed rule are not.
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != 6 || fields[1] == "=" {
			continue
		}
		names[fields[1]] = true
	}
	return names
}

// VideoCodecEncoders maps each extra-codec setting to the ffmpeg encoder that
// setting requires.
//
// It is exported for DIAGNOSTICS. `vidra doctor` checks the same fact against a
// deployment's own ffmpeg and deliberately does not import this package at build
// time — it diagnoses a container and an env file, and pulling the media
// pipeline in to learn two strings would tie the diagnostic to the thing it
// diagnoses. This is what lets its test pin the two lists together anyway, so a
// rename here cannot leave doctor quietly checking the wrong encoder.
func VideoCodecEncoders() map[string]string {
	out := map[string]string{}
	for _, p := range videoCodecProfiles(true, true) {
		if p.EnvVar != "" {
			out[p.EnvVar] = p.Encoder
		}
	}
	return out
}
