package qoe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/delivery"
)

func i32(v int32) *int32 { return &v }

func validStart() Event {
	return Event{
		Type:            EventStart,
		DeliverySource:  SourceCDN,
		Engine:          EngineHLSJS,
		PackagingFormat: FormatCMAF,
		VideoID:         uuid.New(),
		TTFFMs:          i32(812),
	}
}

// TestValidateAcceptsEachEventTypesOwnShape pins the per-type field contract in
// the direction that matters: the well-formed case must not be rejected.
func TestValidateAcceptsEachEventTypesOwnShape(t *testing.T) {
	class := ErrorNetwork
	cases := map[string]Event{
		"start": validStart(),
		"rebuffer": func() Event {
			e := validStart()
			e.Type, e.TTFFMs, e.RebufferMs = EventRebuffer, nil, i32(1400)
			return e
		}(),
		"bitrate switch": func() Event {
			e := validStart()
			e.Type, e.TTFFMs, e.RenditionHeight = EventBitrateSwitch, nil, i32(720)
			return e
		}(),
		"error": func() Event {
			e := validStart()
			e.Type, e.TTFFMs, e.ErrorClass = EventError, nil, &class
			return e
		}(),
		"native hls reports no rendition": func() Event {
			e := validStart()
			e.Engine = EngineNativeHLS
			return e
		}(),
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ev.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestValidateRejectsUnknownVocabularyValues is the bounded-cardinality rule at
// the edge. Every one of these would otherwise become a stored dimension value.
func TestValidateRejectsUnknownVocabularyValues(t *testing.T) {
	cases := map[string]func(*Event){
		"unknown event type":      func(e *Event) { e.Type = "playback.vibes" },
		"empty event type":        func(e *Event) { e.Type = "" },
		"unknown delivery source": func(e *Event) { e.DeliverySource = "my-cdn.example.com" },
		"empty delivery source":   func(e *Event) { e.DeliverySource = "" },
		"unknown engine":          func(e *Event) { e.Engine = "videojs" },
		"unknown packaging":       func(e *Event) { e.PackagingFormat = "hls" },
		"unknown error class": func(e *Event) {
			e.Type, e.TTFFMs = EventError, nil
			c := ErrorClass("EOF while reading https://cdn.example.com/x?sig=abc")
			e.ErrorClass = &c
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := validStart()
			mutate(&e)
			err := e.Validate()
			if err == nil {
				t.Fatal("Validate() = nil; an unknown vocabulary value must be rejected, never stored")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate() = %v, want an ErrInvalid", err)
			}
		})
	}
}

// TestValidateEnforcesPerTypeMeasurements: a measurement on the wrong event type
// is a rejection and not a silent drop, because a client sending rebuffer_ms on
// a start event is reporting something the rollup would never count.
func TestValidateEnforcesPerTypeMeasurements(t *testing.T) {
	cases := map[string]func(*Event){
		"start without ttff":       func(e *Event) { e.TTFFMs = nil },
		"start with rebuffer":      func(e *Event) { e.RebufferMs = i32(10) },
		"rebuffer without a value": func(e *Event) { e.Type, e.TTFFMs = EventRebuffer, nil },
		"error without a class":    func(e *Event) { e.Type, e.TTFFMs = EventError, nil },
		"class on a non-error": func(e *Event) {
			c := ErrorNetwork
			e.ErrorClass = &c
		},
		"ttff beyond an hour":     func(e *Event) { e.TTFFMs = i32(maxMeasurementMs + 1) },
		"negative ttff":           func(e *Event) { e.TTFFMs = i32(-1) },
		"native hls rendition":    func(e *Event) { e.Engine, e.RenditionHeight = EngineNativeHLS, i32(720) },
		"absurd rendition":        func(e *Event) { e.RenditionHeight = i32(99999) },
		"no subject":              func(e *Event) { e.VideoID = uuid.Nil },
		"two subjects":            func(e *Event) { e.LiveStreamID = uuid.New() },
		"zero rendition is not 0": func(e *Event) { e.RenditionHeight = i32(0) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := validStart()
			mutate(&e)
			if err := e.Validate(); err == nil {
				t.Error("Validate() = nil, want a rejection")
			}
		})
	}
}

// TestValidationMessagesNeverEchoInput: a rejection reaches a client, so it must
// name a field or a vocabulary and never quote what was sent — which is how a
// signed segment URL inside an error class would end up in a response body and
// an access log.
func TestValidationMessagesNeverEchoInput(t *testing.T) {
	e := validStart()
	e.Type, e.TTFFMs = EventError, nil
	c := ErrorClass("https://s3.example.net/bucket/key?X-Amz-Signature=deadbeef")
	e.ErrorClass = &c
	err := e.Validate()
	if err == nil {
		t.Fatal("Validate() = nil")
	}
	if strings.Contains(err.Error(), "X-Amz-Signature") || strings.Contains(err.Error(), "s3.example.net") {
		t.Errorf("validation error echoed client input: %v", err)
	}
}

// TestDeliverySourceMirrorsDeliveryPackage: this package copies four of
// delivery.SourceKind's values as strings rather than importing the package,
// which keeps internal/delivery's import purity intact. The copy is only safe
// while it cannot drift, so the equality is asserted rather than assumed.
func TestDeliverySourceMirrorsDeliveryPackage(t *testing.T) {
	pairs := []struct {
		mine  DeliverySource
		their delivery.SourceKind
	}{
		{SourceAPIProxy, delivery.SourceAPIProxy},
		{SourcePresigned, delivery.SourcePresigned},
		{SourceCDN, delivery.SourceCDN},
		{SourceIPFSGateway, delivery.SourceIPFSGateway},
	}
	for _, p := range pairs {
		if string(p.mine) != string(p.their) {
			t.Errorf("qoe %q != delivery %q — the vocabularies have drifted", p.mine, p.their)
		}
	}
}

// TestVocabulariesMatchTheSchemaChecks keeps the Go constants and the migration's
// CHECK constraints in lock-step. They are two independent statements of the same
// closed set, and a value in one but not the other is a write that fails at the
// database with a swallowed error — the exact failure a best-effort recorder
// cannot report.
func TestVocabulariesMatchTheSchemaChecks(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0109_qoe_telemetry.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(raw)
	check := func(label string, values []string) {
		for _, v := range values {
			if !strings.Contains(sql, "'"+v+"'") {
				t.Errorf("%s %q is in the Go vocabulary but not in the migration's CHECK", label, v)
			}
		}
	}
	types := make([]string, 0, len(EventTypes))
	for _, v := range EventTypes {
		types = append(types, string(v))
	}
	check("event type", types)

	sources := make([]string, 0, len(DeliverySources))
	for _, v := range DeliverySources {
		sources = append(sources, string(v))
	}
	check("delivery source", sources)

	engines := make([]string, 0, len(Engines))
	for _, v := range Engines {
		engines = append(engines, string(v))
	}
	check("engine", engines)

	formats := make([]string, 0, len(PackagingFormats))
	for _, v := range PackagingFormats {
		formats = append(formats, string(v))
	}
	check("packaging format", formats)

	classes := make([]string, 0, len(ErrorClasses))
	for _, v := range ErrorClasses {
		classes = append(classes, string(v))
	}
	check("error class", classes)
}

// TestRenditionReportingSupported states the permanent native-HLS gap as code.
func TestRenditionReportingSupported(t *testing.T) {
	if RenditionReportingSupported(EngineNativeHLS) {
		t.Error("native-hls must report rendition support as false; the browser owns variant selection and exposes no hook")
	}
	for _, e := range []Engine{EngineHLSJS, EngineProgressive, EngineShaka} {
		if !RenditionReportingSupported(e) {
			t.Errorf("%q should support rendition reporting", e)
		}
	}
}

// TestSafeMetadataIsDenyByDefault mirrors jobstatus's discipline: only the
// allowlist survives, and a URL never does.
func TestSafeMetadataIsDenyByDefault(t *testing.T) {
	got := string(SafeMetadata(map[string]any{
		"network":      "4g",
		"fatal":        true,
		"visible":      false,
		"segment":      "https://s3.example.net/bucket/key?X-Amz-Signature=deadbeef",
		"rendition":    "https://cdn.example.com/media/720p.m3u8",
		"note":         "anything at all",
		"switch_count": float64(3),
	}))
	for _, banned := range []string{"segment", "note", "X-Amz-Signature", "cdn.example.com"} {
		if strings.Contains(got, banned) {
			t.Errorf("SafeMetadata kept %q: %s", banned, got)
		}
	}
	for _, kept := range []string{"network", "fatal", "visible", "switch_count"} {
		if !strings.Contains(got, kept) {
			t.Errorf("SafeMetadata dropped the allowlisted key %q: %s", kept, got)
		}
	}
	if s := string(SafeMetadata(nil)); s != "{}" {
		t.Errorf("SafeMetadata(nil) = %s, want {}", s)
	}
}
