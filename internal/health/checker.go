package health

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

type CheckResult struct {
	Name     string        `json:"name"`
	Status   string        `json:"status"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
	Details  interface{}   `json:"details,omitempty"`
}

type Checker interface {
	Check(ctx context.Context) error
	Name() string
}

type DatabaseChecker struct {
	DB               *sqlx.DB
	MaxPingTime      time.Duration
	CheckConnections bool
}

func (d *DatabaseChecker) Check(ctx context.Context) error {
	if d.MaxPingTime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.MaxPingTime)
		defer cancel()
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}

	if err := d.DB.PingContext(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	var isReadOnly bool
	err := d.DB.GetContext(ctx, &isReadOnly, "SELECT pg_is_in_recovery()")
	if err != nil {
		return fmt.Errorf("failed to check read-only status: %w", err)
	}
	if isReadOnly {
		return fmt.Errorf("database is in read-only mode")
	}

	if d.CheckConnections {
		stats := d.DB.Stats()

		if stats.InUse >= stats.MaxOpenConnections && stats.MaxOpenConnections > 0 {
			return fmt.Errorf("connection pool exhausted: %d/%d connections in use",
				stats.InUse, stats.MaxOpenConnections)
		}

		if stats.MaxOpenConnections > 0 {
			usagePercent := float64(stats.InUse) / float64(stats.MaxOpenConnections)
			if usagePercent > 0.8 {
				return fmt.Errorf("high connection pool usage: %d/%d (%.0f%%)",
					stats.InUse, stats.MaxOpenConnections, usagePercent*100)
			}
		}
	}

	return nil
}

func (d *DatabaseChecker) Name() string {
	return "database"
}

func NewDatabaseChecker(db *sqlx.DB) *DatabaseChecker {
	return &DatabaseChecker{
		DB:               db,
		MaxPingTime:      2 * time.Second,
		CheckConnections: true,
	}
}

type RedisChecker struct {
	Client         *redis.Client
	MaxPingTime    time.Duration
	CheckMemory    bool
	MaxMemoryUsage float64
}

func (r *RedisChecker) Check(ctx context.Context) error {
	if r.MaxPingTime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.MaxPingTime)
		defer cancel()
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 1*time.Second)
		defer cancel()
	}

	if err := r.Client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}

	if r.CheckMemory {
		info, err := r.Client.Info(ctx, "memory").Result()
		if err != nil {
			return fmt.Errorf("failed to get redis info: %w", err)
		}

		var usedMemory int64
		var maxMemory int64

		lines := strings.Split(info, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "used_memory:") {
				parts := strings.Split(line, ":")
				if len(parts) == 2 {
					usedMemory, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
				}
			}
			if strings.HasPrefix(line, "maxmemory:") {
				parts := strings.Split(line, ":")
				if len(parts) == 2 {
					maxMemory, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
				}
			}
		}

		if maxMemory > 0 && usedMemory > 0 {
			usagePercent := float64(usedMemory) / float64(maxMemory)
			if usagePercent > r.MaxMemoryUsage {
				return fmt.Errorf("redis memory usage too high: %.0f%% (threshold: %.0f%%)",
					usagePercent*100, r.MaxMemoryUsage*100)
			}
		}
	}

	_, err := r.Client.Set(ctx, "health:check", "ok", 1*time.Second).Result()
	if err != nil {
		return fmt.Errorf("redis write test failed: %w", err)
	}

	return nil
}

func (r *RedisChecker) Name() string {
	return "redis"
}

func NewRedisChecker(client *redis.Client) *RedisChecker {
	return &RedisChecker{
		Client:         client,
		MaxPingTime:    1 * time.Second,
		CheckMemory:    true,
		MaxMemoryUsage: 0.9,
	}
}

type IPFSChecker struct {
	APIEndpoint     string
	ClusterEndpoint string
	MaxResponseTime time.Duration
	CheckCluster    bool
}

func (i *IPFSChecker) Check(ctx context.Context) error {
	timeout := i.MaxResponseTime
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", i.APIEndpoint+"/api/v0/version", nil)
	if err != nil {
		return fmt.Errorf("failed to create IPFS request: %w", err)
	}

	client := &http.Client{
		Timeout: timeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("IPFS API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("IPFS API returned status %d", resp.StatusCode)
	}

	responseTime := time.Since(start)
	if responseTime > i.MaxResponseTime && i.MaxResponseTime > 0 {
		return fmt.Errorf("IPFS API response too slow: %v (max: %v)",
			responseTime, i.MaxResponseTime)
	}

	if i.CheckCluster && i.ClusterEndpoint != "" {
		clusterStart := time.Now()
		clusterReq, err := http.NewRequestWithContext(ctx, "GET", i.ClusterEndpoint+"/id", nil)
		if err != nil {
			return fmt.Errorf("failed to create cluster request: %w", err)
		}

		clusterResp, err := client.Do(clusterReq)
		if err != nil {
			return fmt.Errorf("IPFS Cluster request failed: %w", err)
		}
		defer func() { _ = clusterResp.Body.Close() }()

		if clusterResp.StatusCode != http.StatusOK {
			return fmt.Errorf("IPFS Cluster returned status %d", clusterResp.StatusCode)
		}

		clusterTime := time.Since(clusterStart)
		if clusterTime > i.MaxResponseTime && i.MaxResponseTime > 0 {
			return fmt.Errorf("IPFS Cluster response too slow: %v (max: %v)",
				clusterTime, i.MaxResponseTime)
		}
	}

	return nil
}

func (i *IPFSChecker) Name() string {
	return "ipfs"
}

func NewIPFSChecker(apiEndpoint string) *IPFSChecker {
	return &IPFSChecker{
		APIEndpoint:     apiEndpoint,
		MaxResponseTime: 5 * time.Second,
		CheckCluster:    false,
	}
}

func NewIPFSCheckerWithCluster(apiEndpoint, clusterEndpoint string) *IPFSChecker {
	return &IPFSChecker{
		APIEndpoint:     apiEndpoint,
		ClusterEndpoint: clusterEndpoint,
		MaxResponseTime: 5 * time.Second,
		CheckCluster:    true,
	}
}

type QueueDepthChecker struct {
	GetEncodingQueueDepth func() (int, error)
	GetActivityQueueDepth func() (int, error)
	MaxEncodingQueue      int
	MaxActivityQueue      int
	WarningThreshold      float64
}

func (q *QueueDepthChecker) Check(ctx context.Context) error {
	if q.GetEncodingQueueDepth != nil {
		encodingDepth, err := q.GetEncodingQueueDepth()
		if err != nil {
			return fmt.Errorf("failed to get encoding queue depth: %w", err)
		}

		if encodingDepth >= q.MaxEncodingQueue {
			return fmt.Errorf("encoding queue saturated: %d/%d",
				encodingDepth, q.MaxEncodingQueue)
		}

		if q.WarningThreshold > 0 {
			thresholdDepth := int(float64(q.MaxEncodingQueue) * q.WarningThreshold)
			if encodingDepth >= thresholdDepth {
				return fmt.Errorf("encoding queue high: %d/%d (%.0f%% of max)",
					encodingDepth, q.MaxEncodingQueue,
					float64(encodingDepth)/float64(q.MaxEncodingQueue)*100)
			}
		}
	}

	if q.GetActivityQueueDepth != nil {
		activityDepth, err := q.GetActivityQueueDepth()
		if err != nil {
			return fmt.Errorf("failed to get activity queue depth: %w", err)
		}

		if activityDepth >= q.MaxActivityQueue {
			return fmt.Errorf("activity queue saturated: %d/%d",
				activityDepth, q.MaxActivityQueue)
		}

		if q.WarningThreshold > 0 {
			thresholdDepth := int(float64(q.MaxActivityQueue) * q.WarningThreshold)
			if activityDepth >= thresholdDepth {
				return fmt.Errorf("activity queue high: %d/%d (%.0f%% of max)",
					activityDepth, q.MaxActivityQueue,
					float64(activityDepth)/float64(q.MaxActivityQueue)*100)
			}
		}
	}

	return nil
}

func (q *QueueDepthChecker) Name() string {
	return "queue"
}

func NewQueueDepthChecker(
	getEncodingDepth func() (int, error),
	getActivityDepth func() (int, error),
) *QueueDepthChecker {
	return &QueueDepthChecker{
		GetEncodingQueueDepth: getEncodingDepth,
		GetActivityQueueDepth: getActivityDepth,
		MaxEncodingQueue:      1000,
		MaxActivityQueue:      5000,
		WarningThreshold:      0.8,
	}
}

type HealthService struct {
	checkers        []Checker
	startTime       time.Time
	version         string
	readinessProbes map[string]Checker
}

func NewHealthService(version string, checkers ...Checker) *HealthService {
	probes := make(map[string]Checker)
	for _, checker := range checkers {
		probes[checker.Name()] = checker
	}

	return &HealthService{
		checkers:        checkers,
		startTime:       time.Now(),
		version:         version,
		readinessProbes: probes,
	}
}

func (h *HealthService) CheckLiveness() CheckResult {
	return CheckResult{
		Name:     "liveness",
		Status:   "ok",
		Duration: time.Since(h.startTime),
		Details: map[string]interface{}{
			"version": h.version,
			"uptime":  time.Since(h.startTime).String(),
		},
	}
}

func (h *HealthService) CheckReadiness(ctx context.Context) ([]CheckResult, bool) {
	results := make([]CheckResult, 0, len(h.checkers))
	allHealthy := true

	for _, checker := range h.checkers {
		start := time.Now()
		err := checker.Check(ctx)
		duration := time.Since(start)

		result := CheckResult{
			Name:     checker.Name(),
			Duration: duration,
		}

		if err != nil {
			result.Status = "fail"
			result.Error = err.Error()
			allHealthy = false
		} else {
			result.Status = "ok"
		}

		results = append(results, result)
	}

	return results, allHealthy
}

type MockChecker struct {
	CheckFunc func(ctx context.Context) error
	NameValue string
}

func (m *MockChecker) Check(ctx context.Context) error {
	if m.CheckFunc != nil {
		return m.CheckFunc(ctx)
	}
	return nil
}

func (m *MockChecker) Name() string {
	return m.NameValue
}

type ConnectionPoolStats struct {
	MaxOpenConnections int `json:"max_open_connections"`
	OpenConnections    int `json:"open_connections"`
	InUse              int `json:"in_use"`
	Idle               int `json:"idle"`
}

func GetDBStats(db *sql.DB) ConnectionPoolStats {
	stats := db.Stats()
	return ConnectionPoolStats{
		MaxOpenConnections: stats.MaxOpenConnections,
		OpenConnections:    stats.OpenConnections,
		InUse:              stats.InUse,
		Idle:               stats.Idle,
	}
}

type RedisInfo struct {
	UsedMemory        int64   `json:"used_memory"`
	UsedMemoryPeak    int64   `json:"used_memory_peak"`
	UsedMemoryPercent float64 `json:"used_memory_percent"`
	ConnectedClients  int     `json:"connected_clients"`
	Role              string  `json:"role"`
}

