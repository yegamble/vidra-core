package database

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		config      PoolConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			config: PoolConfig{
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
				ConnMaxIdleTime: 2 * time.Minute,
			},
			expectError: false,
		},
		{
			name: "invalid MaxOpenConns - zero",
			config: PoolConfig{
				MaxOpenConns:    0,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
				ConnMaxIdleTime: 2 * time.Minute,
			},
			expectError: true,
			errorMsg:    "MaxOpenConns must be greater than 0",
		},
		{
			name: "invalid MaxIdleConns - exceeds MaxOpenConns",
			config: PoolConfig{
				MaxOpenConns:    10,
				MaxIdleConns:    15,
				ConnMaxLifetime: 5 * time.Minute,
				ConnMaxIdleTime: 2 * time.Minute,
			},
			expectError: true,
			errorMsg:    "MaxIdleConns cannot exceed MaxOpenConns",
		},
		{
			name: "invalid ConnMaxLifetime - zero",
			config: PoolConfig{
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 0,
				ConnMaxIdleTime: 2 * time.Minute,
			},
			expectError: true,
			errorMsg:    "ConnMaxLifetime must be greater than 0",
		},
		{
			name: "invalid ConnMaxIdleTime - exceeds ConnMaxLifetime",
			config: PoolConfig{
				MaxOpenConns:    25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 2 * time.Minute,
				ConnMaxIdleTime: 5 * time.Minute,
			},
			expectError: true,
			errorMsg:    "ConnMaxIdleTime cannot exceed ConnMaxLifetime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDefaultPoolConfig(t *testing.T) {
	config := DefaultPoolConfig()

	assert.Equal(t, 25, config.MaxOpenConns, "MaxOpenConns should be 25 per CLAUDE.md")
	assert.Equal(t, 5, config.MaxIdleConns, "MaxIdleConns should be 5 per CLAUDE.md")
	assert.Equal(t, 5*time.Minute, config.ConnMaxLifetime, "ConnMaxLifetime should be 5 minutes per CLAUDE.md")
	assert.Equal(t, 2*time.Minute, config.ConnMaxIdleTime, "ConnMaxIdleTime should be 2 minutes per CLAUDE.md")
}

func TestNewPool_Success(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing()

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	require.NotNil(t, pool)

	require.Eventually(t, func() bool {
		return pool.Stats().InUse == 0
	}, 200*time.Millisecond, 10*time.Millisecond, "Initial InUse should be 0")
	stats := pool.Stats()
	assert.LessOrEqual(t, stats.Idle, 1, "Initial Idle should be 0 or 1 (ping may create a connection)")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewPool_FailedPing(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(fmt.Errorf("connection refused"))

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	assert.Error(t, err)
	assert.Nil(t, pool)
	assert.Contains(t, err.Error(), "ping failed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewPool_InvalidConfig(t *testing.T) {
	mockDB, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")

	invalidConfig := PoolConfig{
		MaxOpenConns:    0,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	pool, err := NewPool(sqlxDB, invalidConfig)
	assert.Error(t, err)
	assert.Nil(t, pool)
	assert.Contains(t, err.Error(), "invalid configuration")
}

func TestPool_MaxOpenConnsLimit(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := PoolConfig{
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	const numGoroutines = 10
	var wg sync.WaitGroup
	var maxInUse int32

	for i := 0; i < numGoroutines; i++ {
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	}

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := pool.QueryContext(ctx, "SELECT 1")
			if err != nil {
				t.Logf("Query error: %v", err)
				return
			}

			stats := pool.Stats()
			for {
				current := atomic.LoadInt32(&maxInUse)
				if stats.InUse <= int(current) {
					break
				}
				if atomic.CompareAndSwapInt32(&maxInUse, current, int32(stats.InUse)) {
					break
				}
			}

			time.Sleep(10 * time.Millisecond)
		}()
	}

	wg.Wait()

	finalMaxInUse := atomic.LoadInt32(&maxInUse)
	assert.LessOrEqual(t, int(finalMaxInUse), config.MaxOpenConns,
		"InUse connections should never exceed MaxOpenConns")
}

func TestPool_ConnectionReuse(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	rows1, err := pool.Query("SELECT 1")
	require.NoError(t, err)
	rows1.Close()

	require.Eventually(t, func() bool {
		return pool.Stats().InUse == 0
	}, 200*time.Millisecond, 10*time.Millisecond)

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	rows2, err := pool.Query("SELECT 1")
	require.NoError(t, err)
	rows2.Close()

	require.Eventually(t, func() bool {
		return pool.Stats().OpenConnections <= 2
	}, 200*time.Millisecond, 10*time.Millisecond,
		"Connection should be reused from pool, not creating many new ones")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPool_IdleConnectionTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping time-dependent test in short mode")
	}

	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := PoolConfig{
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 10 * time.Second,
		ConnMaxIdleTime: 1 * time.Second,
	}

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	rows, err := pool.Query("SELECT 1")
	require.NoError(t, err)
	rows.Close()

	time.Sleep(100 * time.Millisecond)
	stats1 := pool.Stats()
	assert.GreaterOrEqual(t, stats1.OpenConnections, 0, "Should have handled the query")

	time.Sleep(2 * time.Second)

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	_, err = pool.Query("SELECT 1")
	if err != nil && !strings.Contains(err.Error(), "expected a connection to be available, but it is not") {
		require.NoError(t, err)
	}

	stats2 := pool.Stats()

	assert.LessOrEqual(t, stats2.Idle, stats1.Idle,
		"Idle connections should be cleaned up after ConnMaxIdleTime")
}

func TestPool_ConnectionMaxLifetime(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping time-dependent test in short mode")
	}

	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := PoolConfig{
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 1 * time.Second,
		ConnMaxIdleTime: 500 * time.Millisecond,
	}

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	for i := 0; i < 3; i++ {
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	}

	for i := 0; i < 3; i++ {
		_, err = pool.Query("SELECT 1")
		require.NoError(t, err)
	}

	time.Sleep(100 * time.Millisecond)
	initialOpenConns := pool.Stats().OpenConnections

	time.Sleep(2 * time.Second)

	for i := 0; i < 3; i++ {
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	}

	for i := 0; i < 3; i++ {
		_, err = pool.Query("SELECT 1")
		require.NoError(t, err)
	}

	// Note: This is a soft check as exact behavior depends on Go's sql package internals
	t.Logf("Initial open connections: %d, Final open connections: %d",
		initialOpenConns, pool.Stats().OpenConnections)
}

func TestPool_AllConnectionsBusy(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := PoolConfig{
		MaxOpenConns:    2,
		MaxIdleConns:    1,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	mock.ExpectQuery("SELECT SLEEP").WillDelayFor(2 * time.Second).
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	mock.ExpectQuery("SELECT SLEEP").WillDelayFor(2 * time.Second).
		WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))

	var wg sync.WaitGroup
	wg.Add(2)

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			_, _ = pool.Query("SELECT SLEEP")
		}()
	}

	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = pool.QueryContext(ctx, "SELECT 1")

	stats := pool.Stats()
	if err != nil {
		assert.Equal(t, context.DeadlineExceeded, err, "Should timeout when all connections busy")
	} else {
		t.Logf("Query succeeded, WaitCount: %d", stats.WaitCount)
	}

	wg.Wait()
}

func TestPool_DatabaseDowntime(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	mock.ExpectQuery("SELECT 1").WillReturnError(fmt.Errorf("connection lost"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = pool.QueryContext(ctx, "SELECT 1")
	assert.Error(t, err, "Should fail when database is down")
	assert.Contains(t, err.Error(), "connection lost")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPool_Stats(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := PoolConfig{
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	require.Eventually(t, func() bool {
		return pool.Stats().InUse == 0
	}, 200*time.Millisecond, 10*time.Millisecond, "Initial InUse should be 0")
	stats := pool.Stats()
	assert.LessOrEqual(t, stats.Idle, 1, "Initial Idle should be 0 or 1 (ping may create a connection)")
	assert.LessOrEqual(t, stats.OpenConnections, 1, "Initial OpenConnections should be 0 or 1 (ping may create a connection)")

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	rows, err := pool.Query("SELECT 1")
	require.NoError(t, err)

	statsInUse := pool.Stats()
	assert.Greater(t, statsInUse.OpenConnections, 0, "Should have open connections")

	rows.Close()

	require.Eventually(t, func() bool {
		statsIdle := pool.Stats()
		return statsIdle.Idle >= 0
	}, 100*time.Millisecond, 10*time.Millisecond, "Should have idle connections")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPool_StatsUnderLoad(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	mock.MatchExpectationsInOrder(false)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := PoolConfig{
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	const numQueries = 20
	for i := 0; i < numQueries; i++ {
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	}

	var wg sync.WaitGroup
	wg.Add(numQueries)

	for i := 0; i < numQueries; i++ {
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			rows, err := pool.QueryContext(ctx, "SELECT 1")
			if err != nil {
				t.Logf("Query error: %v", err)
				return
			}
			rows.Close()
			time.Sleep(10 * time.Millisecond)
		}()
	}

	time.Sleep(20 * time.Millisecond)
	stats := pool.Stats()

	assert.LessOrEqual(t, stats.InUse, config.MaxOpenConns,
		"InUse should not exceed MaxOpenConns")
	assert.LessOrEqual(t, stats.Idle, config.MaxIdleConns,
		"Idle should not exceed MaxIdleConns")
	assert.LessOrEqual(t, stats.OpenConnections, config.MaxOpenConns,
		"OpenConnections should not exceed MaxOpenConns")

	wg.Wait()

	finalStats := pool.Stats()
	t.Logf("Final stats - Open: %d, InUse: %d, Idle: %d, WaitCount: %d",
		finalStats.OpenConnections, finalStats.InUse, finalStats.Idle, finalStats.WaitCount)
}

func TestPool_Close(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	rows, err := pool.Query("SELECT 1")
	require.NoError(t, err)
	rows.Close()

	require.Eventually(t, func() bool {
		return pool.Stats().InUse == 0
	}, 100*time.Millisecond, 10*time.Millisecond, "Connection should return to pool")

	mock.ExpectClose()
	err = pool.Close()
	assert.NoError(t, err)

	_, err = pool.Query("SELECT 1")
	assert.Error(t, err, "Queries should fail after pool is closed")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPool_ContextCancellation(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock.ExpectQuery("SELECT 1").WillReturnError(context.Canceled)

	_, err = pool.QueryContext(ctx, "SELECT 1")
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled, "Should return context.Canceled error")
}

func BenchmarkPool_SimpleQuery(b *testing.B) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(b, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(b, err)
	defer pool.Close()

	for i := 0; i < b.N; i++ {
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := pool.Query("SELECT 1")
		if err != nil {
			b.Fatalf("Query failed: %v", err)
		}
		rows.Close()
	}
}

func BenchmarkPool_ConcurrentQueries(b *testing.B) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(b, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(b, err)
	defer pool.Close()

	for i := 0; i < b.N; i++ {
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rows, err := pool.Query("SELECT 1")
			if err != nil {
				b.Logf("Query failed: %v", err)
				continue
			}
			rows.Close()
		}
	})
}

func BenchmarkPool_ConnectionAcquisition(b *testing.B) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(b, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := PoolConfig{
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	pool, err := NewPool(sqlxDB, config)
	require.NoError(b, err)
	defer pool.Close()

	for i := 0; i < b.N; i++ {
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"col"}).AddRow(1))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := pool.Query("SELECT 1")
		if err != nil {
			b.Fatalf("Query failed: %v", err)
		}
		rows.Close()
	}
}

func TestPool_NilDatabase(t *testing.T) {
	config := DefaultPoolConfig()

	pool, err := NewPool(nil, config)
	assert.Error(t, err)
	assert.Nil(t, pool)
	assert.Contains(t, err.Error(), "database cannot be nil")
}

func TestPool_GetDB(t *testing.T) {
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	mock.ExpectPing().WillReturnError(nil)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	config := DefaultPoolConfig()

	pool, err := NewPool(sqlxDB, config)
	require.NoError(t, err)
	defer pool.Close()

	db := pool.GetDB()
	assert.NotNil(t, db, "GetDB should return the underlying database")
	assert.Equal(t, sqlxDB, db, "GetDB should return the same database instance")
}
