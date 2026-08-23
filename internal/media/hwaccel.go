package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// --- hardware transcoding (phase-3 item 7) -----------------------------------
//
// A hardware encoder is not a faster libx264. It is a different encoder with a
// different option surface, a different rate-control model, a different idea of
// where the frames live, and — the part that makes it a DEPLOYMENT decision
// rather than a build one — a dependency on a device node that the process may
// or may not be able to open. The same image runs on a laptop with an Intel iGPU,
// a droplet with no GPU at all, and a GPU instance whose /dev/nvidia* nodes are
// only present if somebody remembered the nvidia runtime.
//
// That is why there is NO `auto`. Availability is a property of the HOST, and a
// pipeline that silently switched encoders because a device node appeared would
// change the picture quality of a whole deployment on a kernel upgrade. Hardware
// is opt-in, spelled out, and boot-verified; `off` is the default and is the only
// value that is guaranteed to work everywhere. The detection helper below exists
// to make the opt-in EASY (doctor and `vidra setup` say "vaapi looks available
// here, enable it with TRANSCODING_HW=vaapi"), never to make it automatic.
//
// WHAT A BACKEND IS ALLOWED TO CHANGE. A backend is a TRANSFORM of the codec
// profiles in codec.go, not a new registry row, and the line is exact: it may
// change how a representation is PRODUCED and it may not change what that
// representation IS. Name, CodecsID, Tag, BitrateMultiplier and the ladder
// budgets are carried through untouched, so an H.264 rung encoded by
// h264_videotoolbox is still `avc1.*` in the same adaptation set at the same
// bitrate, and every manifest, every serving route and every stored row is
// unaffected. What changes is the -c:v value, the encoder-private options, the
// pixel format, and — for the backends whose encoders read GPU memory — a filter
// tail that uploads the scaled frames.
//
// WHY AV1 IS NEVER HARDWARE HERE. Hardware AV1 ENCODE exists on Arc, Ada and
// RDNA3 and nowhere else; on every other device the encoder either is not built,
// is built and fails at runtime, or silently produces something the rate control
// was never tuned for. TRANSCODING_AV1_ENABLED already means "spend a lot of CPU
// for ~45% fewer bytes", and quietly turning that into "spend a little GPU for an
// unknown number of bytes on the third of hosts that can" is not the same
// setting. AV1 stays libsvtav1 under every backend. An operator with an Arc card
// who wants av1_vaapi is asking for a fourth codec plan, not for this knob.
//
// WHY THERE IS NO PER-JOB FALLBACK TO CPU. When a hardware encode fails, the
// tempting move is to re-run the job with libx264 and log it. This pipeline
// deliberately does not. A deployment that quietly falls back has no way to tell
// "my GPU is working" from "my GPU has been broken for a month and every upload
// costs 8x what I budgeted"; the failure mode is a performance cliff nobody sees
// until the queue backs up. The job fails, the error NAMES the backend and the
// knob that turns it off, and the operator decides. Turning TRANSCODING_HW=off
// and restarting is a config change with no data migration — the already-encoded
// trees are ordinary H.264 and keep serving — so the fix is cheap and explicit.

// The operator-facing TRANSCODING_HW values.
const (
	// HardwareOff is the default and the compatibility floor: every rung is
	// encoded by the software encoders in codec.go, which every ffmpeg build has
	// and which need no device at all.
	HardwareOff = "off"
	// HardwareVideoToolbox is Apple's encoder, present on every macOS host and on
	// no other platform. It needs no device node and no init: the framework is
	// part of the OS.
	HardwareVideoToolbox = "videotoolbox"
	// HardwareVAAPI is the Linux/Mesa/Intel-media path, through a DRM render node.
	// It is the ONLY backend the shipped image can use without a rebuild.
	HardwareVAAPI = "vaapi"
	// HardwareQSV is Intel Quick Sync through the oneVPL/MSDK runtime. It reaches
	// the same silicon as vaapi on an Intel host but through Intel's own stack,
	// and it is NOT in the shipped image's ffmpeg (see the doc on hwBackend).
	HardwareQSV = "qsv"
	// HardwareNVENC is NVIDIA's encoder. Also not in the shipped image.
	HardwareNVENC = "nvenc"
)

// hardwareEnvVar is the knob this whole file hangs off, spelled once so the
// profiles, the probe's error messages and the operator-facing offers cannot
// drift from each other.
const hardwareEnvVar = "TRANSCODING_HW"

// defaultRenderNode is the DRM render node vaapi and qsv use when
// TRANSCODING_HW_DEVICE is unset. renderD128 is the first render node the kernel
// hands out and is the right answer on every single-GPU host; a machine with a
// discrete card beside an iGPU has renderD129 too and the operator has to say
// which one, because "the first one" is not a preference the pipeline can hold
// on their behalf.
const defaultRenderNode = "/dev/dri/renderD128"

// encodeOpt is one encoder-private option: a flag and its value, stream-qualified
// at render time the way -preset and -pix_fmt already are.
type encodeOpt struct{ flag, value string }

// hwBackend is one hardware encoding path: which encoders it names, how its
// encoders want to be configured, and what it needs from the host to work at all.
//
// A NOTE ON WHAT THE SHIPPED IMAGE ACTUALLY HAS. Measured on the image's own base
// (alpine 3.24, `apk add ffmpeg`, ffmpeg 8.1.2): the build contains h264_vaapi,
// hevc_vaapi, av1_vaapi, vp8/vp9/mjpeg/mpeg2_vaapi, the vulkan encoders, and the
// v4l2m2m wrappers. It contains NO h264_qsv, NO hevc_qsv and NO *_nvenc. So on a
// stock deployment vaapi is the only backend that can succeed, and qsv/nvenc need
// a differently-built ffmpeg (Intel's oneVPL-enabled build, or an NVIDIA CUDA
// base image with a CUDA-enabled ffmpeg). They are implemented here anyway
// because the operator who builds that image should not also have to patch this
// package, and because the boot probe's whole job is to say "this ffmpeg has no
// h264_nvenc" on the day it is asked to.
type hwBackend struct {
	// Name is the TRANSCODING_HW value.
	Name string

	// H264Encoder and HEVCEncoder are the ffmpeg encoders this backend supplies
	// for the two codecs it is allowed to touch. An empty HEVCEncoder would mean
	// "this backend cannot do HEVC"; every backend here can, and the boot probe is
	// what decides whether this BUILD can.
	H264Encoder string
	HEVCEncoder string

	// Preset is the -preset value for this backend's encoders, or "" when they
	// have no such option. The software profiles' presets are words on a scale
	// libx264 defined; a hardware encoder either borrowed that scale (qsv), made
	// its own (nvenc's p1..p7), or has no preset at all and exposes quality as a
	// rate-control mode instead (videotoolbox, vaapi). Passing an option an
	// encoder does not have is a hard ffmpeg error, not a warning, so "" is a real
	// answer and not a missing one.
	Preset string

	// PixFmt is the -pix_fmt value, or "" to omit it entirely.
	//
	// "" is the load-bearing case. vaapi and qsv encoders read frames that are
	// ALREADY in GPU memory (see FilterChain), and their pixel format is a hardware
	// frame format the filter graph has already established. Naming a software
	// format there does not convert anything — it tells ffmpeg the encoder wants
	// software frames, which contradicts the graph and fails the encode.
	PixFmt string

	// FilterChain is the filter tail appended to each of this backend's
	// representations, without a leading comma, or "" when the encoder takes
	// ordinary software frames.
	//
	// This is the deepest difference between the backends. videotoolbox and nvenc
	// accept system-memory frames and do their own upload inside the encoder;
	// vaapi and qsv do not, and the upload has to be a filter, which means the
	// filter graph grows a per-codec tail. That is why the graph forks per CODEC
	// and not just per rung when a chain is present.
	FilterChain string

	// ExtraArgs are encoder-private options beyond preset/profile/rate control.
	ExtraArgs []encodeOpt

	// Device is what this backend needs from the host, and how to ask for it.
	//
	// DeviceKind is "drm" (a /dev/dri render node, which the operator may override
	// with TRANSCODING_HW_DEVICE), "nvidia" (the nvidia runtime's device nodes,
	// which are not named on the command line), or "" (nothing — the framework is
	// the OS).
	DeviceKind string
	// InitArgs renders the global -init_hw_device / -filter_hw_device pair this
	// backend needs before the input, or nil when it needs none.
	InitArgs func(device string) []string

	// Requires is the operator-facing prose for what must be true on the host, and
	// GOOS is the platform this backend can exist on at all ("" = any).
	Requires string
	GOOS     string
}

// hwDeviceAlias is the name the init'd hardware device is bound to inside one
// ffmpeg invocation. It is namespaced rather than the conventional bare "hw" so
// it cannot collide with a device an operator's own ffmpeg wrapper set up.
const hwDeviceAlias = "vidra_hw"

// The backends, in the order the detection helper reports them: the two that
// need no extra runtime first, then the two that need a rebuilt ffmpeg.
var hwBackends = []hwBackend{
	{
		Name:        HardwareVideoToolbox,
		H264Encoder: "h264_videotoolbox",
		HEVCEncoder: "hevc_videotoolbox",
		// VideoToolbox has no preset. Its speed/quality lever is -realtime, and
		// the default (off) is the batch-VOD setting this pipeline wants.
		Preset: "",
		// Software frames: the framework uploads them itself. yuv420p keeps the
		// pixel format identical to the software path, so nothing downstream of
		// the encoder sees a difference.
		PixFmt:   "yuv420p",
		Requires: "macOS — VideoToolbox is part of the OS and needs no device node, but it is unreachable from a Linux container, which is where this pipeline normally runs",
		GOOS:     "darwin",
	},
	{
		Name:        HardwareVAAPI,
		H264Encoder: "h264_vaapi",
		HEVCEncoder: "hevc_vaapi",
		// VAAPI encoders have no -preset either; -rc_mode and -quality are the
		// levers, and with -b:v + -maxrate present ffmpeg selects VBR, which is
		// the capped-VBR shape the ladder is budgeted for.
		Preset: "",
		// Hardware frames after the upload below — see PixFmt's doc.
		PixFmt:      "",
		FilterChain: "format=nv12,hwupload",
		DeviceKind:  "drm",
		InitArgs: func(device string) []string {
			return []string{
				"-init_hw_device", "vaapi=" + hwDeviceAlias + ":" + device,
				"-filter_hw_device", hwDeviceAlias,
			}
		},
		Requires: "a DRM render node (/dev/dri/renderD*) readable by the container's user, and a VA-API driver for the GPU (mesa's radeonsi/nouveau, or intel-media-driver)",
		GOOS:     "linux",
	},
	{
		Name:        HardwareQSV,
		H264Encoder: "h264_qsv",
		HEVCEncoder: "hevc_qsv",
		// QSV is the one hardware backend that borrowed libx264's preset words, so
		// the software profiles' "veryfast" carries over meaning the same thing.
		Preset: "veryfast",
		PixFmt: "",
		// extra_hw_frames is not optional decoration: the QSV encoder holds a
		// window of surfaces, and an upload that does not allocate for it fails
		// mid-encode with "no free surfaces" on exactly the long sources a VOD
		// pipeline is made of.
		FilterChain: "format=nv12,hwupload=extra_hw_frames=64",
		DeviceKind:  "drm",
		InitArgs: func(device string) []string {
			return []string{
				"-init_hw_device", "qsv=" + hwDeviceAlias + ":" + device,
				"-filter_hw_device", hwDeviceAlias,
			}
		},
		Requires: "an Intel GPU with the oneVPL/iHD runtime installed, a DRM render node, AND an ffmpeg built with --enable-libvpl; the shipped image's ffmpeg is NOT",
		GOOS:     "linux",
	},
	{
		Name:        HardwareNVENC,
		H264Encoder: "h264_nvenc",
		HEVCEncoder: "hevc_nvenc",
		// NVENC's presets are p1..p7 (p1 fastest, p7 slowest) and are NOT libx264's
		// words — the old spellings are deprecated aliases that map unpredictably
		// across driver versions. p4 is NVIDIA's own balanced default and is still
		// several times faster than libx264 veryfast on any NVENC-capable card, so
		// matching the software preset's SPEED would be spending quality on a
		// margin nobody asked for.
		Preset: "p4",
		// NVENC takes system-memory frames and uploads them itself, so there is no
		// filter tail and no device to name: the CUDA runtime finds the card.
		PixFmt: "yuv420p",
		// Explicit VBR. NVENC's default rate-control depends on the driver and the
		// preset, and the one thing the ladder cannot tolerate is a rung that
		// ignores -maxrate: a variant that overshoots the bandwidth it declared is
		// picked by an ABR player that then cannot carry it.
		ExtraArgs:  []encodeOpt{{flag: "-rc", value: "vbr"}},
		DeviceKind: "nvidia",
		Requires:   "an NVIDIA GPU with the driver + container runtime exposing /dev/nvidia*, AND an ffmpeg built with --enable-nvenc; the shipped image's ffmpeg is NOT",
		GOOS:       "linux",
	},
}

// hardwareBackend resolves a TRANSCODING_HW value. off (and the empty value it
// stands in for) resolves to no backend, which is the software pipeline.
func hardwareBackend(name string) (hwBackend, bool) {
	if name == "" || name == HardwareOff {
		return hwBackend{}, true
	}
	for _, b := range hwBackends {
		if b.Name == name {
			return b, true
		}
	}
	return hwBackend{}, false
}

// IsHardwareName reports whether name is a TRANSCODING_HW value this package
// implements. It is the second gate behind internal/config's own enum, the same
// way IsPackagerName is: config spells the names out because it imports nothing
// of the codebase, and this is where that list is checked against the one that
// actually has to build an argument vector.
func IsHardwareName(name string) bool {
	_, ok := hardwareBackend(name)
	return ok
}

// HardwareNames is every accepted TRANSCODING_HW value, off first, for the error
// messages and the operator-facing listings that have to enumerate them.
func HardwareNames() []string {
	names := make([]string, 0, len(hwBackends)+1)
	names = append(names, HardwareOff)
	for _, b := range hwBackends {
		names = append(names, b.Name)
	}
	return names
}

// hardwareNeedsDevice reports whether a backend reads a device PATH the operator
// can name (TRANSCODING_HW_DEVICE). nvenc needs a device and does not take a
// path — the CUDA runtime finds the card — so it answers false.
func (b hwBackend) needsDevicePath() bool { return b.DeviceKind == "drm" }

// device resolves the device path this backend will use: the operator's
// TRANSCODING_HW_DEVICE when set, otherwise the default render node.
func (b hwBackend) device(configured string) string {
	if !b.needsDevicePath() {
		return ""
	}
	if configured != "" {
		return configured
	}
	return defaultRenderNode
}

// encoderFor is the encoder this backend supplies for a codec, or "" when it
// supplies none — which for AV1 is always, deliberately (see the file's doc).
func (b hwBackend) encoderFor(codec string) string {
	switch codec {
	case VideoCodecH264:
		return b.H264Encoder
	case VideoCodecHEVC:
		return b.HEVCEncoder
	default:
		return ""
	}
}

// withHardware returns p re-pointed at a hardware encoder, or p unchanged when
// this backend has nothing for p's codec.
//
// Everything that identifies the representation survives: Name, Tag,
// CodecsID, Rate, CRF and ScoreBonus are p's own,
// because this copies p and overwrites only the fields below. That is the
// invariant the whole feature rests on — a hardware H.264 rung is an H.264 rung,
// so the manifests, the adaptation sets, the derived downloads and the stored
// rows are the ones a software install produces. Rate and ScoreBonus matter most:
// the ceiling BANDWIDTH promises and the rank SCORE publishes are properties of
// the rung and the codec, never of the silicon.
func (p codecProfile) withHardware(b hwBackend, device string) codecProfile {
	enc := b.encoderFor(p.Name)
	if enc == "" {
		return p
	}
	p.Encoder = enc
	p.Preset = b.Preset
	p.PixFmt = b.PixFmt
	p.FilterChain = b.FilterChain
	p.ExtraArgs = b.ExtraArgs
	p.Hardware = b.Name
	// EnvVar is the knob the boot probe names when this encoder turns out to be
	// missing, and a hardware profile must never leave it empty. H.264's is empty
	// in the registry because software H.264 cannot be turned off — but once it is
	// h264_vaapi there IS a knob, and it is this one. Without this line the probe
	// falls through to its no-knob sentence ("the %q encoder is needed but there
	// is no ffmpeg…"), which tells an operator what is missing and not one word
	// about how to stop asking for it.
	//
	// HEVC keeps TRANSCODING_HEVC_ENABLED rather than being overwritten, because
	// that is genuinely the knob that put HEVC in the plan and turning it off is
	// one of the two real exits. TRANSCODING_HW is named ALONGSIDE it by the
	// hardware arms in verifiedAgainstEncoderListing, which is what lets the
	// message offer both fixes in the order that keeps the most of what was asked
	// for — dropping to libx265 before dropping HEVC altogether.
	if p.EnvVar == "" {
		p.EnvVar = hardwareEnvVar
	}
	if b.InitArgs != nil {
		p.InitArgs = b.InitArgs(device)
	}
	return p
}

// applyHardware transforms a whole codec plan for a backend. An empty backend
// name returns the plan untouched, which is what every ordinary install gets.
func applyHardware(profiles []codecProfile, name, configuredDevice string) ([]codecProfile, error) {
	b, ok := hardwareBackend(name)
	if !ok {
		return nil, fmt.Errorf("media: unknown TRANSCODING_HW %q (want %s)", name, strings.Join(HardwareNames(), "|"))
	}
	if b.Name == "" {
		return profiles, nil
	}
	device := b.device(configuredDevice)
	out := make([]codecProfile, len(profiles))
	for i, p := range profiles {
		out[i] = p.withHardware(b, device)
	}
	return out, nil
}

// hwInitArgs are the GLOBAL arguments a codec plan needs before the input: the
// hardware device initialisation, emitted ONCE however many of the plan's codecs
// share it. Two representations of the same rung encoded by h264_vaapi and
// hevc_vaapi want the same VA display, and initialising it twice under the same
// alias is an error.
func hwInitArgs(profiles []codecProfile) []string {
	var args []string
	seen := map[string]bool{}
	for _, p := range profiles {
		if len(p.InitArgs) == 0 {
			continue
		}
		key := strings.Join(p.InitArgs, " ")
		if seen[key] {
			continue
		}
		seen[key] = true
		args = append(args, p.InitArgs...)
	}
	return args
}

// hardwareBackendOf reports the backend name a codec plan is encoding with, or ""
// when the plan is entirely software. It is what the job-time failure message
// reads to name the backend the operator has to switch off.
func hardwareBackendOf(profiles []codecProfile) string {
	for _, p := range profiles {
		if p.Hardware != "" {
			return p.Hardware
		}
	}
	return ""
}

// --- job-time failure semantics ----------------------------------------------

// explainHardwareFailure turns an ffmpeg failure on a hardware-accelerated ladder
// into an error that says which backend was in play and how to stop using it, and
// decides whether retrying could possibly help.
//
// The boot probe (verifyVideoEncoders) proves the ENCODER exists in the build. It
// cannot prove the DEVICE is reachable, and that is the failure this deployment
// will actually hit: the image ships h264_vaapi, so the api boots happily, and
// then every job dies because nobody added `devices: [/dev/dri:/dev/dri]` to the
// compose service. The stderr tail already contains ffmpeg's own words for that;
// what it does not contain is the name of the knob, which is the only thing the
// operator needs.
//
// PERMANENCE IS DECIDED BY THE DEVICE, NOT BY THE MESSAGE. A missing device node
// is deterministic — the next attempt, and the one after the backoff, find the
// same empty /dev — so it is a PermanentError and dead-letters immediately rather
// than costing a quarter of an hour to say what it already knew. Everything else
// (a busy GPU, a driver that wedged, an encoder session limit) is transient by
// default, because those DO recover and the retry is the correct behaviour. When
// the backend names no device path at all, permanence cannot be established this
// way and the failure stays transient.
func explainHardwareFailure(err error, profiles []codecProfile, configuredDevice string, exists func(string) bool) error {
	name := hardwareBackendOf(profiles)
	if name == "" {
		return err
	}
	b, ok := hardwareBackend(name)
	if !ok {
		return err
	}
	device := b.device(configuredDevice)
	hint := fmt.Sprintf(
		"this ladder was encoded with the %s hardware backend (TRANSCODING_HW=%s); "+
			"set TRANSCODING_HW=off and restart to fall back to CPU encoding, which always works. "+
			"There is no automatic per-job fallback on purpose: a deployment that silently encodes on the CPU "+
			"looks healthy while costing several times the budgeted time per video",
		name, name)
	if device != "" {
		if exists != nil && !exists(device) {
			return permanentf("%v: %s is not present in this container — %s. Map the device in (docker-compose: `devices: [\"/dev/dri:/dev/dri\"]`) or %s",
				err, device, b.Requires, hint)
		}
		return fmt.Errorf("%w: %s is present but the encode failed — %s. %s", err, device, b.Requires, hint)
	}
	return fmt.Errorf("%w: %s", err, hint)
}

// --- detection ---------------------------------------------------------------
//
// Detection answers ONE question — "would this backend plausibly work here?" —
// and answers it for a report, never for a decision. It is two independent facts
// AND-ed together, because either one alone is a lie an operator would act on:
//
//   - the ffmpeg that runs the transcodes has the encoder. A host ffmpeg with
//     h264_vaapi says nothing about the container's, which is why the callers all
//     take the encoder set from the binary that would do the work.
//   - the DEVICE is plausibly there. An ffmpeg build always has h264_vaapi
//     whether or not the machine has a GPU, so "the encoder exists" on its own
//     recommends vaapi to every deployment on earth.
//
// Plausibly, not certainly. The device check the callers can make is on the
// filesystem they can see; a doctor run on the host sees /dev/dri even when the
// container has not been given it. That gap is real and is exactly why this is an
// informational offer and why the job-time failure above spells out the device
// mapping.

// HardwareProbe is what a caller measured about one machine. It is a plain value
// so the detection logic is pure and testable without a GPU, a container or a
// filesystem.
type HardwareProbe struct {
	// Encoders is the encoder-name set from `ffmpeg -encoders` on the binary that
	// would run the transcodes.
	Encoders map[string]bool
	// GOOS is the platform ("darwin", "linux"), as runtime.GOOS spells it.
	GOOS string
	// RenderNodes are the DRM render nodes found (/dev/dri/renderD*), in path
	// order. Empty on a host with no GPU exposed.
	RenderNodes []string
	// NVIDIA reports that NVIDIA devices or tooling were found (an /dev/nvidia*
	// node, or nvidia-smi on PATH).
	NVIDIA bool
}

// HardwareAvailability is one backend's verdict.
type HardwareAvailability struct {
	// Backend is the TRANSCODING_HW value.
	Backend string
	// Available is the AND of "the encoder is in this ffmpeg" and "the device is
	// plausibly here".
	Available bool
	// Encoders are the backend's encoders this ffmpeg actually has, in h264-then-
	// hevc order. A backend with the H.264 encoder and not the HEVC one is still
	// available — H.264 is the only codec hardware is REQUIRED for — and the list
	// is what tells an operator that TRANSCODING_HEVC_ENABLED would not follow.
	Encoders []string
	// Device is the device node that made it plausible ("" for a backend that
	// needs none).
	Device string
	// Why is the reason it is unavailable, in operator prose. Empty when it is.
	Why string
	// Requires is the backend's host requirement, carried so a report can explain
	// an unavailable backend without a second lookup.
	Requires string
}

// DetectHardware reports every backend's verdict for one probed machine, in a
// fixed order (the two that need no rebuilt ffmpeg first).
func DetectHardware(p HardwareProbe) []HardwareAvailability {
	out := make([]HardwareAvailability, 0, len(hwBackends))
	for _, b := range hwBackends {
		a := HardwareAvailability{Backend: b.Name, Requires: b.Requires}
		for _, enc := range []string{b.H264Encoder, b.HEVCEncoder} {
			if p.Encoders[enc] {
				a.Encoders = append(a.Encoders, enc)
			}
		}
		switch {
		case b.GOOS != "" && p.GOOS != "" && b.GOOS != p.GOOS:
			a.Why = fmt.Sprintf("%s is %s-only and this is %s", b.Name, b.GOOS, p.GOOS)
		case !p.Encoders[b.H264Encoder]:
			// The H.264 encoder is the one that decides: it is the compatibility
			// floor every ladder emits, so a backend that cannot encode H.264 has
			// nothing to contribute whatever else it has.
			a.Why = fmt.Sprintf("this ffmpeg has no %q encoder", b.H264Encoder)
		case b.DeviceKind == "drm" && len(p.RenderNodes) == 0:
			a.Why = "no DRM render node (/dev/dri/renderD*) is visible here"
		case b.DeviceKind == "nvidia" && !p.NVIDIA:
			a.Why = "no NVIDIA device or nvidia-smi was found here"
		default:
			a.Available = true
		}
		if a.Available && b.DeviceKind == "drm" {
			a.Device = p.RenderNodes[0]
		}
		out = append(out, a)
	}
	return out
}

// FirstAvailableHardware is the backend a report should OFFER: the first
// available one in DetectHardware's order, or a zero value when there is none.
// The order is not a quality ranking — it is "what can work without rebuilding
// ffmpeg" first, which is the only ordering that makes the offer actionable.
func FirstAvailableHardware(p HardwareProbe) (HardwareAvailability, bool) {
	for _, a := range DetectHardware(p) {
		if a.Available {
			return a, true
		}
	}
	return HardwareAvailability{}, false
}

// HardwareOffer is the one line a report prints when a backend looks usable and
// the deployment is not using it.
func (a HardwareAvailability) Offer() string {
	where := ""
	if a.Device != "" {
		where = " (" + a.Device + ")"
	}
	return fmt.Sprintf("hardware transcode available: %s%s — enable it with TRANSCODING_HW=%s. CPU encoding is the default and always works; hardware is faster and cheaper but depends on this host keeping that device",
		a.Backend, where, a.Backend)
}

// ProbeHardware measures THIS machine: which encoders ffmpegBin has, which DRM
// render nodes exist, and whether NVIDIA hardware is in evidence.
//
// It is the host-facing companion to DetectHardware, which stays pure. Callers
// that can reach a container's ffmpeg (doctor) build their own probe instead —
// this one is for the tools that run on the host with nothing else up, and it is
// deliberately total: an ffmpegBin that is absent or will not run yields an empty
// encoder set rather than an error, because "we could not ask" and "there is
// nothing here" lead to the same report, which is silence.
func ProbeHardware(ctx context.Context, ffmpegBin string) HardwareProbe {
	p := HardwareProbe{
		Encoders:    map[string]bool{},
		GOOS:        runtime.GOOS,
		RenderNodes: RenderNodes("/dev/dri"),
	}
	for _, node := range []string{"/dev/nvidiactl", "/dev/nvidia0"} {
		if _, err := os.Stat(node); err == nil {
			p.NVIDIA = true
		}
	}
	if !p.NVIDIA {
		if _, err := exec.LookPath("nvidia-smi"); err == nil {
			p.NVIDIA = true
		}
	}
	if ffmpegBin == "" {
		return p
	}
	out, err := exec.CommandContext(ctx, ffmpegBin, "-hide_banner", "-encoders").Output()
	if err != nil {
		return p
	}
	p.Encoders = ffmpegEncoderNames(out)
	return p
}

// deviceExists is explainHardwareFailure's real filesystem probe, split out so
// the permanence decision can be tested without a /dev.
func deviceExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// RenderNodes lists the DRM render nodes present under dir (normally /dev/dri).
// It is here rather than in the callers because both of them — doctor and `vidra
// setup` — need the same answer and a glob written twice is a glob that drifts.
func RenderNodes(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "renderD") {
			out = append(out, dir+"/"+e.Name())
		}
	}
	return out
}
