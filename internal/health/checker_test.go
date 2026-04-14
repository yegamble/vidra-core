package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

func newMockSQLXDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	return sqlx.NewDb(sqlDB, "sqlmock"), mock
}

func TestDatabaseChecker_Check(t *testing.T) {
	t.Run("ping failure", func(t *testing.T) {
		db, mock := newMockSQLXDB(t)
		mock.ExpectPing().WillReturnError(errors.New("db down"))

		checker := &DatabaseChecker{
			DB:               db,
			MaxPingTime:      50 * time.Millisecond,
			CheckConnections: false,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "database ping failed") {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sqlmock expectations: %v", err)
		}
	})

	t.Run("read-only database", func(t *testing.T) {
		db, mock := newMockSQLXDB(t)
		mock.ExpectPing()
		mock.ExpectQuery(`SELECT pg_is_in_recovery\(\)`).
			WillReturnRows(sqlmock.NewRows([]string{"pg_is_in_recovery"}).AddRow(true))

		checker := &DatabaseChecker{
			DB:               db,
			MaxPingTime:      50 * time.Millisecond,
			CheckConnections: false,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "read-only mode") {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sqlmock expectations: %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		db, mock := newMockSQLXDB(t)
		mock.ExpectPing()
		mock.ExpectQuery(`SELECT pg_is_in_recovery\(\)`).
			WillReturnRows(sqlmock.NewRows([]string{"pg_is_in_recovery"}).AddRow(false))

		checker := &DatabaseChecker{
			DB:               db,
			MaxPingTime:      50 * time.Millisecond,
			CheckConnections: false,
		}

		if err := checker.Check(context.Background()); err != nil {
			t.Fatalf("checker.Check() error = %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sqlmock expectations: %v", err)
		}
	})
}

func TestNewDatabaseCheckerDefaultsAndName(t *testing.T) {
	db, _ := newMockSQLXDB(t)
	checker := NewDatabaseChecker(db)

	if checker.Name() != "database" {
		t.Fatalf("checker.Name() = %q, want %q", checker.Name(), "database")
	}
	if checker.DB != db {
		t.Fatal("checker.DB was not set from constructor")
	}
	if checker.MaxPingTime != 2*time.Second {
		t.Fatalf("checker.MaxPingTime = %v, want %v", checker.MaxPingTime, 2*time.Second)
	}
	if !checker.CheckConnections {
		t.Fatal("checker.CheckConnections = false, want true")
	}
}

func TestRedisChecker_CheckPingFailure(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:0",
		DialTimeout:  20 * time.Millisecond,
		ReadTimeout:  20 * time.Millisecond,
		WriteTimeout: 20 * time.Millisecond,
		PoolTimeout:  20 * time.Millisecond,
	})
	defer func() { _ = client.Close() }()

	checker := &RedisChecker{
		Client:      client,
		MaxPingTime: 50 * time.Millisecond,
	}

	err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "redis ping failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRedisCheckerDefaultsAndName(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	defer func() { _ = client.Close() }()

	checker := NewRedisChecker(client)
	if checker.Name() != "redis" {
		t.Fatalf("checker.Name() = %q, want %q", checker.Name(), "redis")
	}
	if checker.Client != client {
		t.Fatal("checker.Client was not set from constructor")
	}
	if checker.MaxPingTime != time.Second {
		t.Fatalf("checker.MaxPingTime = %v, want %v", checker.MaxPingTime, time.Second)
	}
	if !checker.CheckMemory {
		t.Fatal("checker.CheckMemory = false, want true")
	}
	if checker.MaxMemoryUsage != 0.9 {
		t.Fatalf("checker.MaxMemoryUsage = %v, want %v", checker.MaxMemoryUsage, 0.9)
	}
}

func TestIPFSChecker_Check(t *testing.T) {
	t.Run("invalid API endpoint", func(t *testing.T) {
		checker := &IPFSChecker{
			APIEndpoint:     "://bad-endpoint",
			MaxResponseTime: 100 * time.Millisecond,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to create IPFS request") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("IPFS API non-200", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fail", http.StatusInternalServerError)
		}))
		defer api.Close()

		checker := &IPFSChecker{
			APIEndpoint:     api.URL,
			MaxResponseTime: time.Second,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "IPFS API returned status 500") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("cluster non-200", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v0/version" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.NotFound(w, r)
		}))
		defer api.Close()

		cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "cluster fail", http.StatusServiceUnavailable)
		}))
		defer cluster.Close()

		checker := &IPFSChecker{
			APIEndpoint:     api.URL,
			ClusterEndpoint: cluster.URL,
			MaxResponseTime: time.Second,
			CheckCluster:    true,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "IPFS Cluster returned status 503") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("success with cluster", func(t *testing.T) {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v0/version" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.NotFound(w, r)
		}))
		defer api.Close()

		cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/id" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.NotFound(w, r)
		}))
		defer cluster.Close()

		checker := &IPFSChecker{
			APIEndpoint:     api.URL,
			ClusterEndpoint: cluster.URL,
			MaxResponseTime: time.Second,
			CheckCluster:    true,
		}

		if err := checker.Check(context.Background()); err != nil {
			t.Fatalf("checker.Check() error = %v", err)
		}
	})
}

func TestNewIPFSCheckerDefaultsAndName(t *testing.T) {
	apiEndpoint := "http://127.0.0.1:5001"
	clusterEndpoint := "http://127.0.0.1:9094"

	checker := NewIPFSChecker(apiEndpoint)
	if checker.Name() != "ipfs" {
		t.Fatalf("checker.Name() = %q, want %q", checker.Name(), "ipfs")
	}
	if checker.APIEndpoint != apiEndpoint {
		t.Fatalf("checker.APIEndpoint = %q, want %q", checker.APIEndpoint, apiEndpoint)
	}
	if checker.MaxResponseTime != 5*time.Second {
		t.Fatalf("checker.MaxResponseTime = %v, want %v", checker.MaxResponseTime, 5*time.Second)
	}
	if checker.CheckCluster {
		t.Fatal("checker.CheckCluster = true, want false")
	}

	clusterChecker := NewIPFSCheckerWithCluster(apiEndpoint, clusterEndpoint)
	if clusterChecker.APIEndpoint != apiEndpoint {
		t.Fatalf("clusterChecker.APIEndpoint = %q, want %q", clusterChecker.APIEndpoint, apiEndpoint)
	}
	if clusterChecker.ClusterEndpoint != clusterEndpoint {
		t.Fatalf("clusterChecker.ClusterEndpoint = %q, want %q", clusterChecker.ClusterEndpoint, clusterEndpoint)
	}
	if !clusterChecker.CheckCluster {
		t.Fatal("clusterChecker.CheckCluster = false, want true")
	}
}

func TestQueueDepthChecker_Check(t *testing.T) {
	t.Run("encoding depth error", func(t *testing.T) {
		checker := &QueueDepthChecker{
			GetEncodingQueueDepth: func() (int, error) { return 0, errors.New("encoding queue unavailable") },
			MaxEncodingQueue:      10,
			MaxActivityQueue:      10,
			WarningThreshold:      0.8,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to get encoding queue depth") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("encoding queue saturated", func(t *testing.T) {
		checker := &QueueDepthChecker{
			GetEncodingQueueDepth: func() (int, error) { return 10, nil },
			MaxEncodingQueue:      10,
			MaxActivityQueue:      10,
			WarningThreshold:      0.8,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "encoding queue saturated") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("activity queue high threshold", func(t *testing.T) {
		checker := &QueueDepthChecker{
			GetEncodingQueueDepth: func() (int, error) { return 1, nil },
			GetActivityQueueDepth: func() (int, error) { return 8, nil },
			MaxEncodingQueue:      10,
			MaxActivityQueue:      10,
			WarningThreshold:      0.8,
		}

		err := checker.Check(context.Background())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "activity queue high") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		checker := &QueueDepthChecker{
			GetEncodingQueueDepth: func() (int, error) { return 1, nil },
			GetActivityQueueDepth: func() (int, error) { return 1, nil },
			MaxEncodingQueue:      10,
			MaxActivityQueue:      10,
			WarningThreshold:      0.8,
		}

		if err := checker.Check(context.Background()); err != nil {
			t.Fatalf("checker.Check() error = %v", err)
		}
	})
}

func TestNewQueueDepthCheckerDefaultsAndName(t *testing.T) {
	checker := NewQueueDepthChecker(
		func() (int, error) { return 0, nil },
		func() (int, error) { return 0, nil },
	)

	if checker.Name() != "queue" {
		t.Fatalf("checker.Name() = %q, want %q", checker.Name(), "queue")
	}
	if checker.MaxEncodingQueue != 1000 {
		t.Fatalf("checker.MaxEncodingQueue = %d, want 1000", checker.MaxEncodingQueue)
	}
	if checker.MaxActivityQueue != 5000 {
		t.Fatalf("checker.MaxActivityQueue = %d, want 5000", checker.MaxActivityQueue)
	}
	if checker.WarningThreshold != 0.8 {
		t.Fatalf("checker.WarningThreshold = %v, want 0.8", checker.WarningThreshold)
	}
}

func TestHealthService_CheckLivenessAndReadiness(t *testing.T) {
	okChecker := &MockChecker{NameValue: "ok-check"}
	failChecker := &MockChecker{
		NameValue: "fail-check",
		CheckFunc: func(_ context.Context) error { return errors.New("boom") },
	}

	service := NewHealthService("v1.2.3", okChecker, failChecker)

	liveness := service.CheckLiveness()
	if liveness.Name != "liveness" {
		t.Fatalf("liveness.Name = %q, want %q", liveness.Name, "liveness")
	}
	if liveness.Status != "ok" {
		t.Fatalf("liveness.Status = %q, want %q", liveness.Status, "ok")
	}
	details, ok := liveness.Details.(map[string]interface{})
	if !ok {
		t.Fatalf("liveness.Details type = %T, want map[string]interface{}", liveness.Details)
	}
	if details["version"] != "v1.2.3" {
		t.Fatalf("liveness version = %v, want %q", details["version"], "v1.2.3")
	}
	if _, ok := details["uptime"]; !ok {
		t.Fatal("liveness missing uptime detail")
	}

	results, allHealthy := service.CheckReadiness(context.Background())
	if allHealthy {
		t.Fatal("allHealthy = true, want false")
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}

	resultByName := make(map[string]CheckResult, len(results))
	for _, result := range results {
		resultByName[result.Name] = result
	}

	if resultByName["ok-check"].Status != "ok" {
		t.Fatalf("ok-check status = %q, want %q", resultByName["ok-check"].Status, "ok")
	}
	if resultByName["fail-check"].Status != "fail" {
		t.Fatalf("fail-check status = %q, want %q", resultByName["fail-check"].Status, "fail")
	}
	if !strings.Contains(resultByName["fail-check"].Error, "boom") {
		t.Fatalf("fail-check error = %q, want substring %q", resultByName["fail-check"].Error, "boom")
	}
}

func TestMockChecker_DefaultAndName(t *testing.T) {
	checker := &MockChecker{NameValue: "test-checker"}
	if checker.Name() != "test-checker" {
		t.Fatalf("checker.Name() = %q, want %q", checker.Name(), "test-checker")
	}

	if err := checker.Check(context.Background()); err != nil {
		t.Fatalf("checker.Check() error = %v", err)
	}
}

func TestGetDBStats(t *testing.T) {
	db, _ := newMockSQLXDB(t)
	db.DB.SetMaxOpenConns(17)

	stats := GetDBStats(db.DB)
	if stats.MaxOpenConnections != 17 {
		t.Fatalf("stats.MaxOpenConnections = %d, want 17", stats.MaxOpenConnections)
	}
	if stats.OpenConnections < 0 {
		t.Fatalf("stats.OpenConnections = %d, want >= 0", stats.OpenConnections)
	}
	if stats.InUse < 0 {
		t.Fatalf("stats.InUse = %d, want >= 0", stats.InUse)
	}
	if stats.Idle < 0 {
		t.Fatalf("stats.Idle = %d, want >= 0", stats.Idle)
	}
}

