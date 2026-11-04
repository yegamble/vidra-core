package catalog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseBootstrapRequestPreservesExactNullableRetryTuple(t *testing.T) {
	epoch, previous, receiver := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"), uuid.New(), uuid.New()
	for _, nullable := range []bool{false, true} {
		a, b := `"`+previous.String()+`"`, `"`+receiver.String()+`"`
		if nullable {
			a, b = "null", "null"
		}
		raw := fmt.Sprintf(`{"scope":"local","epoch":%q,"expected_epoch":%s,"expected_receiver_epoch":%s}`, epoch, a, b)
		got, err := ParseBootstrapRequest([]byte(raw))
		if err != nil || got.Scope != ScopeLocal || got.Epoch != epoch || (got.ExpectedEpoch == nil) != nullable || (got.ExpectedReceiverEpoch == nil) != nullable {
			t.Fatalf("lost explicit bootstrap tuple: %+v/%v", got, err)
		}
		if !nullable && (*got.ExpectedEpoch != previous || *got.ExpectedReceiverEpoch != receiver) {
			t.Fatal("changed original CAS predecessor")
		}
	}
	valid := fmt.Sprintf(`{"scope":"local","epoch":%q,"expected_epoch":null,"expected_receiver_epoch":null}`, epoch)
	for _, raw := range []string{
		`null`, `[]`, valid + `{}`,
		strings.Replace(valid, `"local"`, `"remote"`, 1),
		strings.Replace(valid, `"scope":"local",`, ``, 1),
		strings.Replace(valid, `"epoch":`, `"Epoch":`, 1),
		strings.Replace(valid, `"scope":`, `"extra":0,"scope":`, 1),
		strings.Replace(valid, `"epoch":`, `"epoch":"`+uuid.NewString()+`","epoch":`, 1),
		strings.Replace(valid, epoch.String(), strings.ToUpper(epoch.String()), 1),
		strings.Replace(valid, epoch.String(), uuid.Nil.String(), 1),
		strings.Replace(valid, `"expected_epoch":null,`, ``, 1),
		strings.Replace(valid, `"expected_epoch":null`, `"expected_epoch":""`, 1),
		strings.Replace(valid, `"expected_receiver_epoch":null`, `"expected_receiver_epoch":"`+uuid.Nil.String()+`"`, 1),
	} {
		if got, err := ParseBootstrapRequest([]byte(raw)); err == nil || got != (BootstrapRequest{}) {
			t.Fatalf("ambiguous/invalid bootstrap accepted: %+v/%v for %s", got, err, raw)
		}
	}
}

func TestParseFullRequestKeepsZeroAndLargeAllocatorStringsExact(t *testing.T) {
	epoch := uuid.New()
	for _, generation := range []int64{0, 1, 9007199254740993, 9223372036854775807} {
		raw := fmt.Sprintf(`{"scope":"local","expected_epoch":%q,"expected_allocated_generation":"%d"}`, epoch, generation)
		got, err := ParseFullRequest([]byte(raw))
		if err != nil || got.Scope != ScopeLocal || got.ExpectedEpoch != epoch || got.ExpectedAllocatedGeneration != generation {
			t.Fatalf("lost exact allocator request: %+v/%v", got, err)
		}
	}
	valid := fmt.Sprintf(`{"scope":"local","expected_epoch":%q,"expected_allocated_generation":"0"}`, epoch)
	for _, raw := range []string{
		`null`, `[]`, valid + `{}`,
		strings.Replace(valid, `"local"`, `"remote"`, 1),
		strings.Replace(valid, `"expected_epoch":`, `"Expected_epoch":`, 1),
		strings.Replace(valid, epoch.String(), uuid.Nil.String(), 1),
		strings.Replace(valid, `"expected_allocated_generation":`, `"extra":0,"expected_allocated_generation":`, 1),
		strings.Replace(valid, `"expected_allocated_generation":`, `"expected_allocated_generation":"1","expected_allocated_generation":`, 1),
	} {
		if got, err := ParseFullRequest([]byte(raw)); err == nil || got != (FullRequest{}) {
			t.Fatalf("ambiguous full request accepted: %+v/%v", got, err)
		}
	}
	for _, generation := range []string{`0`, `1.0`, `null`, `""`, `"01"`, `"-1"`, `"+1"`, `"1e0"`, `"1.0"`, `"9223372036854775808"`} {
		raw := strings.Replace(valid, `"0"`, generation, 1)
		if got, err := ParseFullRequest([]byte(raw)); err == nil || got != (FullRequest{}) {
			t.Fatalf("noncanonical allocator accepted: %+v/%v for %s", got, err, raw)
		}
	}
}
