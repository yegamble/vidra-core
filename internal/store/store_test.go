package store

import (
	"testing"
	"time"
)

// applyOptions is what New does before it touches pgxpool: start from the
// defaults, then let the Options move them. Asserting on the resolved struct
// keeps this a unit test — the alternative needs a live PostgreSQL, which is
// what the build-tagged integration tests are for.
func applyOptions(opts ...Option) options {
	o := options{
		maxConns:        DefaultMaxConns,
		minConns:        DefaultMinConns,
		maxConnLifetime: DefaultConnMaxLifetime,
		maxConnIdleTime: DefaultConnMaxIdleTime,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// A caller that passes no sizing Option — doctor's prober, a one-shot command,
// a test — must get exactly the pool the package always opened.
func TestNoOptionsIsTheHistoricalPool(t *testing.T) {
	o := applyOptions()
	if o.maxConns != 10 || o.minConns != 1 {
		t.Errorf("conns = %d/%d, want 10/1", o.maxConns, o.minConns)
	}
	if o.maxConnLifetime != time.Hour || o.maxConnIdleTime != 30*time.Minute {
		t.Errorf("lifetimes = %v/%v, want 1h/30m", o.maxConnLifetime, o.maxConnIdleTime)
	}
}

func TestSizingOptionsApply(t *testing.T) {
	o := applyOptions(
		WithMaxConns(4),
		WithMinConns(0),
		WithConnMaxLifetime(15*time.Minute),
		WithConnMaxIdleTime(2*time.Minute),
	)
	if o.maxConns != 4 {
		t.Errorf("maxConns = %d, want 4", o.maxConns)
	}
	// Zero is meaningful for MinConns — open nothing until asked — so it must
	// not be treated as "unset" the way the other three are.
	if o.minConns != 0 {
		t.Errorf("minConns = %d, want 0", o.minConns)
	}
	if o.maxConnLifetime != 15*time.Minute || o.maxConnIdleTime != 2*time.Minute {
		t.Errorf("lifetimes = %v/%v, want 15m/2m", o.maxConnLifetime, o.maxConnIdleTime)
	}
}

// A zero value reaching these Options means a caller passed a field it never
// set. Opening a pool of zero connections would hang every query forever, which
// is a far worse answer than "the default".
func TestNonPositiveSizingIsIgnored(t *testing.T) {
	o := applyOptions(
		WithMaxConns(0),
		WithMinConns(-1),
		WithConnMaxLifetime(0),
		WithConnMaxIdleTime(-time.Second),
	)
	if o.maxConns != DefaultMaxConns {
		t.Errorf("maxConns = %d, want the default %d", o.maxConns, DefaultMaxConns)
	}
	if o.minConns != DefaultMinConns {
		t.Errorf("minConns = %d, want the default %d", o.minConns, DefaultMinConns)
	}
	if o.maxConnLifetime != DefaultConnMaxLifetime || o.maxConnIdleTime != DefaultConnMaxIdleTime {
		t.Errorf("lifetimes = %v/%v, want the defaults", o.maxConnLifetime, o.maxConnIdleTime)
	}
}
