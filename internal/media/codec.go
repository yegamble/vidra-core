package media

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
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
// a NUMBER rather than a word and its encoder rejects the capped-VBR rate control
// the other two use, and each earns its own bitrate budget. Spreading those
// differences across the argument builders as conditionals is how a codec ends up
// half-added. They are stated once, here, and the builders read them.
//
// WHAT IS DELIBERATELY NOT HERE: hardware encoders (they are a different
// decision — availability is per HOST, not per build, and a fallback policy has
// to exist before one can be offered), per-title or CRF rate control, and any
// MPEG-TS spelling of any of this. TRANSCODING_PACKAGER=ts is the frozen
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

	// CappedRate selects capped-VBR rate control: -maxrate at the rung's budget
	// and -bufsize at twice it, alongside -b:v. Off for AV1, and not as a
	// preference — SVT-AV1 refuses the combination outright ("Max Bitrate only
	// supported with CRF mode"), so an AV1 rung is plain VBR at its target.
	CappedRate bool

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
	// Specification publishes an HEVC ladder roughly 30–40% below its H.264 one
	// at the same resolutions, and Bitmovin's and Netflix's published per-title
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
	// headline number.
	av1BitrateMultiplier = 0.55
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
		CappedRate:        true,
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
		CappedRate:        true,
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
		CappedRate:        false,
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

// verifyVideoEncoders asks the ffmpeg that will actually run the transcodes
// whether it HAS the encoders the enabled codecs need, and returns an
// operator-actionable error when it does not.
//
// It runs at transcoder construction — boot — rather than at the first
// transcode, because the alternative is a deployment that starts cleanly, looks
// healthy, and then dead-letters every video with an ffmpeg stderr tail. The
// probe is one `ffmpeg -encoders` for the whole process lifetime; the encoder set
// is a property of the BUILD and cannot change under a running binary.
func verifyVideoEncoders(ctx context.Context, ffmpegBin string, profiles []codecProfile) error {
	var missing []codecProfile
	for _, p := range profiles {
		if p.Encoder == "" {
			continue
		}
		missing = append(missing, p)
	}
	if len(missing) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, ffmpegBin, "-hide_banner", "-encoders")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("media: listing %s encoders: %w: %s", ffmpegBin, err, tailOf(stderr.String()))
	}
	have := ffmpegEncoderNames(out)
	for _, p := range missing {
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
