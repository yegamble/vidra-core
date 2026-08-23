package config

import (
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store"
)

// poolCandidate is a minimal development env with the pool keys overlaid, read
// through LoadFrom so these tests exercise the same parse-then-validate path
// that boots the process.
func poolCandidate(overrides map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := overrides[key]
		return v, ok
	}
}

// TestPoolDefaultsMatchStore pins the one thing the duplication between this
// package and internal/store can get wrong.
//
// The four DB_* pool defaults are declared twice on purpose: internal/config is
// the validation engine the setup wizard and `vidra doctor` link against, and it
// must not drag in the database driver to state a number. The cost of that is a
// pair of constants that could drift, and a drift here is invisible — the api
// would open one pool and a bare store.New() (doctor's prober, a one-shot
// command) another, while .env.example documented a third.
func TestPoolDefaultsMatchStore(t *testing.T) {
	if DefaultDBMaxConns != store.DefaultMaxConns {
		t.Errorf("DefaultDBMaxConns = %d, store.DefaultMaxConns = %d", DefaultDBMaxConns, store.DefaultMaxConns)
	}
	if DefaultDBMinConns != store.DefaultMinConns {
		t.Errorf("DefaultDBMinConns = %d, store.DefaultMinConns = %d", DefaultDBMinConns, store.DefaultMinConns)
	}
	if DefaultDBConnMaxLifetime != store.DefaultConnMaxLifetime {
		t.Errorf("DefaultDBConnMaxLifetime = %v, store.DefaultConnMaxLifetime = %v", DefaultDBConnMaxLifetime, store.DefaultConnMaxLifetime)
	}
	if DefaultDBConnMaxIdleTime != store.DefaultConnMaxIdleTime {
		t.Errorf("DefaultDBConnMaxIdleTime = %v, store.DefaultConnMaxIdleTime = %v", DefaultDBConnMaxIdleTime, store.DefaultConnMaxIdleTime)
	}
}

// TestPoolDefaultsAreTheHistoricalValues states the numbers literally, so the
// pool sizing of every existing install cannot change as a side-effect of
// editing one constant. 10/1/1h/30m is what the pool was hardcoded to before
// these keys existed, and HTTP_DRAIN_DELAY of 0 is the shutdown sequence that
// shipped before the drain phase existed.
func TestPoolDefaultsAreTheHistoricalValues(t *testing.T) {
	cfg, err := LoadFrom(poolCandidate(nil))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.DBMaxConns != 10 {
		t.Errorf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
	if cfg.DBMinConns != 1 {
		t.Errorf("DBMinConns = %d, want 1", cfg.DBMinConns)
	}
	if cfg.DBConnMaxLifetime != time.Hour {
		t.Errorf("DBConnMaxLifetime = %v, want 1h", cfg.DBConnMaxLifetime)
	}
	if cfg.DBConnMaxIdleTime != 30*time.Minute {
		t.Errorf("DBConnMaxIdleTime = %v, want 30m", cfg.DBConnMaxIdleTime)
	}
	if cfg.HTTPDrainDelay != 0 {
		t.Errorf("HTTPDrainDelay = %v, want 0", cfg.HTTPDrainDelay)
	}
}

// TestPoolSizingRefusals covers the values that must not boot. The first two are
// the leader-elector floor: a pool of 1 on a role that runs workers hands the
// elector the only connection there is, and the symptom is every request hanging
// on Acquire rather than an error anybody can read.
func TestPoolSizingRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"max conns of one", map[string]string{"DB_MAX_CONNS": "1"}, "DB_MAX_CONNS"},
		{"max conns of zero", map[string]string{"DB_MAX_CONNS": "0"}, "DB_MAX_CONNS"},
		{"min above max", map[string]string{"DB_MAX_CONNS": "4", "DB_MIN_CONNS": "5"}, "DB_MIN_CONNS"},
		{"negative min", map[string]string{"DB_MIN_CONNS": "-1"}, "DB_MIN_CONNS"},
		{"zero lifetime", map[string]string{"DB_CONN_MAX_LIFETIME": "0s"}, "DB_CONN_MAX_LIFETIME"},
		{"zero idle time", map[string]string{"DB_CONN_MAX_IDLE_TIME": "0s"}, "DB_CONN_MAX_IDLE_TIME"},
		{"negative drain delay", map[string]string{"HTTP_DRAIN_DELAY": "-1s"}, "HTTP_DRAIN_DELAY"},
		{"unparseable max conns", map[string]string{"DB_MAX_CONNS": "lots"}, "DB_MAX_CONNS"},
		{"unparseable drain delay", map[string]string{"HTTP_DRAIN_DELAY": "soon"}, "HTTP_DRAIN_DELAY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFrom(poolCandidate(tc.env))
			if err == nil {
				t.Fatalf("LoadFrom(%v) = nil error, want a refusal naming %s", tc.env, tc.want)
			}
			// Attributed, because the wizard and doctor render these against the
			// field an operator has to edit.
			if _, ok := collectVarErrors(err)[tc.want]; !ok {
				t.Errorf("no VarError for %s; error tree = %v", tc.want, err)
			}
		})
	}
}

// TestPoolSizingAccepts checks the sizes an operator on a small managed plan
// would actually write: a pool well under the default, no warm connections, and
// a drain long enough for a load balancer to notice.
func TestPoolSizingAccepts(t *testing.T) {
	cfg, err := LoadFrom(poolCandidate(map[string]string{
		"DB_MAX_CONNS":          "4",
		"DB_MIN_CONNS":          "0",
		"DB_CONN_MAX_LIFETIME":  "15m",
		"DB_CONN_MAX_IDLE_TIME": "2m",
		"HTTP_DRAIN_DELAY":      "10s",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.DBMaxConns != 4 || cfg.DBMinConns != 0 {
		t.Errorf("conns = %d/%d, want 4/0", cfg.DBMaxConns, cfg.DBMinConns)
	}
	if cfg.DBConnMaxLifetime != 15*time.Minute || cfg.DBConnMaxIdleTime != 2*time.Minute {
		t.Errorf("lifetimes = %v/%v, want 15m/2m", cfg.DBConnMaxLifetime, cfg.DBConnMaxIdleTime)
	}
	if cfg.HTTPDrainDelay != 10*time.Second {
		t.Errorf("HTTPDrainDelay = %v, want 10s", cfg.HTTPDrainDelay)
	}
}
