# Defense-in-Depth Security Recommendations

## Beyond Virus Scanning: Comprehensive Upload Security Architecture

**Date**: 2025-11-16
**Related**: SECURITY_ASSESSMENT_VIRUS_SCANNER_P1.md
**Scope**: System-wide upload security hardening

---

## Executive Summary

While fixing the P1 virus scanner vulnerability is critical, this document addresses the broader security architecture needed to protect against sophisticated attacks that may bypass single-layer defenses. A defense-in-depth strategy ensures that even if one security control fails, multiple fallback mechanisms prevent compromise.

**Key Principle**: **Assume Breach** - Design systems assuming that attackers WILL bypass some controls, and ensure failures don't cascade.

---

## Layer 1: Network & Transport Security

### 1.1 TLS Configuration Hardening

**Current Gap**: Standard TLS may be vulnerable to downgrade attacks

**Recommendation**:

```nginx
# NGINX configuration for upload endpoints
ssl_protocols TLSv1.3 TLSv1.2;
ssl_ciphers 'ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384';
ssl_prefer_server_ciphers on;
ssl_session_cache shared:SSL:10m;
ssl_session_timeout 10m;

# HSTS (Force HTTPS)
add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;

# Certificate pinning for API clients
add_header Public-Key-Pins 'pin-sha256="base64+primary=="; pin-sha256="base64+backup=="; max-age=5184000; includeSubDomains' always;
```

### 1.2 Rate Limiting (Defense Against Bypass Attempts)

**Threat**: Attackers flood system with malicious uploads hoping some bypass scanning

**Solution**: Multi-tier rate limiting

```go
type RateLimiter struct {
    // Tier 1: IP-based limits
    ipLimiter *redis.RateLimiter

    // Tier 2: User-based limits
    userLimiter *redis.RateLimiter

    // Tier 3: Global system limits
    globalLimiter *redis.RateLimiter
}

type UploadRateLimits struct {
    IPPerMinute      int // e.g., 10 uploads/min per IP
    UserPerHour      int // e.g., 100 uploads/hour per user
    GlobalPerSecond  int // e.g., 50 uploads/sec globally
    ScanFailureLimit int // e.g., 5 scan failures before temporary ban
}

func (r *RateLimiter) CheckUploadAllowed(ctx context.Context, ip, userID string) error {
    // Check IP rate limit
    if !r.ipLimiter.Allow(ip, 10, time.Minute) {
        log.Warn().Str("ip", ip).Msg("IP rate limit exceeded")
        return ErrRateLimitExceeded
    }

    // Check user rate limit
    if !r.userLimiter.Allow(userID, 100, time.Hour) {
        log.Warn().Str("user", userID).Msg("User rate limit exceeded")
        return ErrRateLimitExceeded
    }

    // Check global rate limit (prevent DoS)
    if !r.globalLimiter.Allow("global", 50, time.Second) {
        log.Warn().Msg("Global rate limit exceeded - system under load")
        return ErrSystemOverloaded
    }

    return nil
}

// Track scan failures per user
func (r *RateLimiter) RecordScanFailure(ctx context.Context, userID string) error {
    key := fmt.Sprintf("scan_failures:%s", userID)

    failures, err := r.redis.Incr(ctx, key).Result()
    if err != nil {
        return err
    }

    // Set TTL on first failure
    if failures == 1 {
        r.redis.Expire(ctx, key, 10*time.Minute)
    }

    // Temporary ban after threshold
    if failures >= 5 {
        log.Error().
            Str("user_id", userID).
            Int64("failures", failures).
            Msg("SECURITY: User exceeded scan failure threshold - temporary ban")

        banKey := fmt.Sprintf("banned_uploads:%s", userID)
        r.redis.Set(ctx, banKey, "scan_failures", 1*time.Hour)

        // Trigger security alert
        r.alertSecurityTeam(userID, "excessive_scan_failures", failures)

        return ErrTooManyScanFailures
    }

    return nil
}
```

### 1.3 DDoS Protection for ClamAV

**Threat**: Attackers could DoS ClamAV to force fallback mode

**Solution**: ClamAV connection pooling with circuit breaker

```go
type ClamAVConnectionPool struct {
    pool          *redis.Pool
    circuitBreaker *gobreaker.CircuitBreaker
    healthCheck    *time.Ticker
}

func NewClamAVPool(config *Config) *ClamAVConnectionPool {
    cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
        Name:        "clamav",
        MaxRequests: 3,
        Interval:    10 * time.Second,
        Timeout:     30 * time.Second,
        ReadyToTrip: func(counts gobreaker.Counts) bool {
            failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
            return counts.Requests >= 10 && failureRatio >= 0.6
        },
        OnStateChange: func(name string, from, to gobreaker.State) {
            log.Warn().
                Str("circuit", name).
                Str("from", from.String()).
                Str("to", to.String()).
                Msg("ClamAV circuit breaker state change")

            if to == gobreaker.StateOpen {
                // SECURITY ALERT: ClamAV circuit open - scanning degraded
                alertSecurityTeam("clamav_circuit_open")
            }
        },
    })

    return &ClamAVConnectionPool{
        circuitBreaker: cb,
        healthCheck:    time.NewTicker(30 * time.Second),
    }
}

func (p *ClamAVConnectionPool) ScanWithCircuitBreaker(ctx context.Context, data []byte) (*ScanResult, error) {
    result, err := p.circuitBreaker.Execute(func() (interface{}, error) {
        // Acquire connection from pool with timeout
        conn, err := p.pool.GetContext(ctx)
        if err != nil {
            return nil, fmt.Errorf("failed to acquire ClamAV connection: %w", err)
        }
        defer conn.Close()

        // Perform scan with deadline
        scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
        defer cancel()

        return p.scanStream(scanCtx, conn, data)
    })

    if err != nil {
        log.Error().Err(err).Msg("ClamAV scan failed through circuit breaker")
        return nil, err
    }

    return result.(*ScanResult), nil
}
```

---

## Layer 2: Application Security

### 2.1 MIME Type Validation (Multi-Stage)

**Threat**: Polyglot files (e.g., file is both JPEG and executable)

**Solution**: Multi-stage MIME validation

```go
type MIMEValidator struct {
    allowedTypes map[string][]string // extension -> allowed MIME types
}

func (m *MIMEValidator) ValidateFile(filePath, declaredExt string, reader io.Reader) error {
    // Stage 1: Check declared extension
    if !m.isExtensionAllowed(declaredExt) {
        return fmt.Errorf("file extension not allowed: %s", declaredExt)
    }

    // Stage 2: Magic byte detection
    buffer := make([]byte, 512)
    n, err := reader.Read(buffer)
    if err != nil && err != io.EOF {
        return fmt.Errorf("failed to read file header: %w", err)
    }

    detectedMIME := http.DetectContentType(buffer[:n])

    // Stage 3: Deep content inspection with library
    file, err := os.Open(filePath)
    if err != nil {
        return err
    }
    defer file.Close()

    deepMIME, err := mimetype.DetectReader(file)
    if err != nil {
        return fmt.Errorf("deep MIME detection failed: %w", err)
    }

    // Stage 4: Cross-validate all MIME detections
    allowedMIMEs := m.allowedTypes[declaredExt]
    if !contains(allowedMIMEs, detectedMIME) {
        log.Warn().
            Str("declared_ext", declaredExt).
            Str("detected_mime", detectedMIME).
            Str("deep_mime", deepMIME.String()).
            Msg("MIME type mismatch detected")

        return fmt.Errorf("MIME type mismatch: extension=%s, detected=%s", declaredExt, detectedMIME)
    }

    if detectedMIME != deepMIME.String() {
        log.Warn().
            Str("magic_bytes_mime", detectedMIME).
            Str("deep_scan_mime", deepMIME.String()).
            Msg("Inconsistent MIME detection - possible polyglot file")

        return fmt.Errorf("inconsistent MIME detection - possible polyglot attack")
    }

    // Stage 5: Format-specific validation
    switch declaredExt {
    case ".mp4", ".mov", ".avi":
        return m.validateVideoFile(filePath)
    case ".jpg", ".jpeg", ".png":
        return m.validateImageFile(filePath)
    case ".pdf":
        return m.validatePDFFile(filePath)
    }

    return nil
}

func (m *MIMEValidator) validateVideoFile(filePath string) error {
    // Use ffprobe to validate video structure
    cmd := exec.Command("ffprobe",
        "-v", "error",
        "-show_entries", "format=format_name,duration,size",
        "-of", "json",
        filePath,
    )

    output, err := cmd.Output()
    if err != nil {
        return fmt.Errorf("video validation failed: %w", err)
    }

    var probe struct {
        Format struct {
            FormatName string  `json:"format_name"`
            Duration   string  `json:"duration"`
            Size       string  `json:"size"`
        } `json:"format"`
    }

    if err := json.Unmarshal(output, &probe); err != nil {
        return fmt.Errorf("invalid video metadata: %w", err)
    }

    // Validate format
    validFormats := []string{"mov,mp4,m4a,3gp,3g2,mj2", "avi", "matroska,webm"}
    if !contains(validFormats, probe.Format.FormatName) {
        return fmt.Errorf("unsupported video format: %s", probe.Format.FormatName)
    }

    return nil
}

func (m *MIMEValidator) validateImageFile(filePath string) error {
    file, err := os.Open(filePath)
    if err != nil {
        return err
    }
    defer file.Close()

    // Attempt to decode image
    _, format, err := image.Decode(file)
    if err != nil {
        return fmt.Errorf("image decode failed: %w", err)
    }

    // Validate format matches extension
    validFormats := map[string]string{
        ".jpg":  "jpeg",
        ".jpeg": "jpeg",
        ".png":  "png",
        ".gif":  "gif",
    }

    expectedFormat := validFormats[filepath.Ext(filePath)]
    if format != expectedFormat {
        return fmt.Errorf("image format mismatch: expected %s, got %s", expectedFormat, format)
    }

    return nil
}
```

### 2.2 File Size Limits (Graduated)

**Threat**: Large file uploads exhaust resources, enable DoS

**Solution**: Size limits based on file type and user trust level

```go
type FileSizeLimits struct {
    limits map[string]map[string]int64 // userTier -> fileType -> maxBytes
}

func (f *FileSizeLimits) GetLimit(userTier, fileType string) int64 {
    tierLimits, exists := f.limits[userTier]
    if !exists {
        tierLimits = f.limits["default"]
    }

    limit, exists := tierLimits[fileType]
    if !exists {
        limit = tierLimits["default"]
    }

    return limit
}

var DefaultSizeLimits = FileSizeLimits{
    limits: map[string]map[string]int64{
        "unverified": { // New users, email not verified
            "video":  2 * GB,
            "image":  10 * MB,
            "document": 25 * MB,
            "default": 100 * MB,
        },
        "verified": { // Email verified users
            "video":  10 * GB,
            "image":  25 * MB,
            "document": 100 * MB,
            "default": 512 * MB,
        },
        "premium": { // Paid users
            "video":  50 * GB,
            "image":  100 * MB,
            "document": 500 * MB,
            "default": 5 * GB,
        },
    },
}
```

### 2.3 Archive Bomb Protection

**Threat**: Zip bombs (small compressed file extracts to massive size)

**Solution**: Nested archive detection and expansion limits

```go
type ArchiveValidator struct {
    maxNestingDepth   int
    maxExtractedSize  int64
    maxFileCount      int
    maxCompressionRatio float64
}

func (a *ArchiveValidator) ValidateArchive(filePath string) error {
    file, err := os.Open(filePath)
    if err != nil {
        return err
    }
    defer file.Close()

    fileInfo, _ := file.Stat()
    compressedSize := fileInfo.Size()

    reader, err := zip.OpenReader(filePath)
    if err != nil {
        return fmt.Errorf("invalid archive: %w", err)
    }
    defer reader.Close()

    var (
        totalUncompressedSize int64
        fileCount             int
        maxDepth              int
    )

    for _, f := range reader.File {
        fileCount++
        totalUncompressedSize += int64(f.UncompressedSize64)

        // Check file count limit
        if fileCount > a.maxFileCount {
            return fmt.Errorf("archive exceeds file count limit: %d > %d", fileCount, a.maxFileCount)
        }

        // Check total extracted size
        if totalUncompressedSize > a.maxExtractedSize {
            return fmt.Errorf("archive exceeds extraction size limit: %d > %d",
                totalUncompressedSize, a.maxExtractedSize)
        }

        // Check nesting depth
        depth := strings.Count(f.Name, "/")
        if depth > maxDepth {
            maxDepth = depth
        }
        if maxDepth > a.maxNestingDepth {
            return fmt.Errorf("archive exceeds nesting depth: %d > %d", maxDepth, a.maxNestingDepth)
        }

        // Check for nested archives (recursive bomb)
        ext := strings.ToLower(filepath.Ext(f.Name))
        if ext == ".zip" || ext == ".tar" || ext == ".gz" || ext == ".7z" {
            log.Warn().
                Str("archive", filePath).
                Str("nested_file", f.Name).
                Msg("Nested archive detected - possible zip bomb")
            return fmt.Errorf("nested archives not allowed")
        }
    }

    // Check compression ratio
    compressionRatio := float64(totalUncompressedSize) / float64(compressedSize)
    if compressionRatio > a.maxCompressionRatio {
        log.Warn().
            Str("archive", filePath).
            Float64("ratio", compressionRatio).
            Int64("compressed", compressedSize).
            Int64("uncompressed", totalUncompressedSize).
            Msg("Suspicious compression ratio - possible zip bomb")
        return fmt.Errorf("compression ratio too high: %.2f > %.2f",
            compressionRatio, a.maxCompressionRatio)
    }

    return nil
}
```

---

## Layer 3: Processing & Sandbox Security

### 3.1 FFmpeg Sandboxing

**Threat**: Malicious video files exploit FFmpeg vulnerabilities

**Solution**: Containerized FFmpeg with strict resource limits

```yaml
# docker-compose.yml for FFmpeg worker
services:
  ffmpeg-worker:
    image: athena-ffmpeg:latest
    security_opt:
      - no-new-privileges:true
      - seccomp=./seccomp-profiles/ffmpeg-strict.json
      - apparmor=athena-ffmpeg-profile
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETUID
      - SETGID
    read_only: true
    tmpfs:
      - /tmp:noexec,nosuid,size=2g
    volumes:
      - ./uploads:/uploads:ro  # Read-only input
      - ./processed:/processed:rw  # Write output
    environment:
      - FFMPEG_TIMEOUT=3600  # 1 hour max
      - FFMPEG_MAX_MEMORY=4g
      - FFMPEG_MAX_CPU=2.0
    ulimits:
      nproc: 100
      nofile: 1024
      fsize: 53687091200  # 50GB max output file
    networks:
      - processing-isolated  # No internet access
```

**Seccomp Profile** (`seccomp-profiles/ffmpeg-strict.json`):

```json
{
  "defaultAction": "SCMP_ACT_ERRNO",
  "architectures": ["SCMP_ARCH_X86_64"],
  "syscalls": [
    {
      "names": [
        "read", "write", "open", "close", "stat", "fstat", "lstat",
        "poll", "lseek", "mmap", "mprotect", "munmap", "brk",
        "rt_sigaction", "rt_sigprocmask", "ioctl", "access",
        "execve", "exit", "exit_group", "getpid", "getuid", "getgid",
        "clone", "futex", "getcwd", "readlink", "getdents64",
        "fadvise64", "madvise", "sched_yield", "sysinfo"
      ],
      "action": "SCMP_ACT_ALLOW"
    }
  ]
}
```

**AppArmor Profile** (`apparmor.d/athena-ffmpeg-profile`):

```
#include <tunables/global>

profile athena-ffmpeg-profile flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Allow reading input files
  /uploads/** r,

  # Allow writing output files
  /processed/** rw,

  # Allow temporary files
  /tmp/** rw,

  # Allow FFmpeg binary
  /usr/bin/ffmpeg ix,
  /usr/lib/** rm,

  # Deny everything else
  /** deny,

  # Deny network access
  deny network,

  # Deny capability escalation
  deny capability sys_admin,
  deny capability sys_module,
  deny capability sys_rawio,
}
```

### 3.2 Process Monitoring & Anomaly Detection

**Threat**: Malware executes during processing, establishes persistence

**Solution**: Runtime monitoring with automatic termination

```go
type ProcessMonitor struct {
    maxCPUPercent    float64
    maxMemoryMB      int64
    maxDuration      time.Duration
    allowedSyscalls  map[string]bool
}

func (p *ProcessMonitor) MonitorFFmpegJob(ctx context.Context, jobID string, cmd *exec.Cmd) error {
    // Start process
    if err := cmd.Start(); err != nil {
        return fmt.Errorf("failed to start FFmpeg: %w", err)
    }

    pid := cmd.Process.Pid
    log.Info().Int("pid", pid).Str("job_id", jobID).Msg("FFmpeg process started")

    // Monitor process in background
    done := make(chan error, 1)
    go func() {
        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()

        startTime := time.Now()

        for {
            select {
            case <-ctx.Done():
                cmd.Process.Kill()
                done <- ctx.Err()
                return
            case <-ticker.C:
                // Check if process still running
                proc, err := os.FindProcess(pid)
                if err != nil {
                    done <- nil  // Process completed
                    return
                }

                // Check resource usage
                stats, err := p.getProcessStats(pid)
                if err != nil {
                    log.Warn().Err(err).Int("pid", pid).Msg("Failed to get process stats")
                    continue
                }

                // CPU limit check
                if stats.CPUPercent > p.maxCPUPercent {
                    log.Error().
                        Int("pid", pid).
                        Float64("cpu", stats.CPUPercent).
                        Msg("SECURITY: FFmpeg process exceeding CPU limit - terminating")
                    proc.Kill()
                    done <- fmt.Errorf("CPU limit exceeded: %.2f%% > %.2f%%",
                        stats.CPUPercent, p.maxCPUPercent)
                    return
                }

                // Memory limit check
                if stats.MemoryMB > p.maxMemoryMB {
                    log.Error().
                        Int("pid", pid).
                        Int64("memory_mb", stats.MemoryMB).
                        Msg("SECURITY: FFmpeg process exceeding memory limit - terminating")
                    proc.Kill()
                    done <- fmt.Errorf("memory limit exceeded: %dMB > %dMB",
                        stats.MemoryMB, p.maxMemoryMB)
                    return
                }

                // Duration limit check
                if time.Since(startTime) > p.maxDuration {
                    log.Error().
                        Int("pid", pid).
                        Dur("duration", time.Since(startTime)).
                        Msg("SECURITY: FFmpeg process exceeding time limit - terminating")
                    proc.Kill()
                    done <- fmt.Errorf("duration limit exceeded")
                    return
                }

                // Syscall monitoring (if strace enabled)
                if p.allowedSyscalls != nil {
                    if suspicious := p.detectSuspiciousSyscalls(pid); suspicious {
                        log.Error().
                            Int("pid", pid).
                            Msg("SECURITY: Suspicious syscalls detected - terminating")
                        proc.Kill()
                        done <- fmt.Errorf("suspicious syscall pattern detected")
                        return
                    }
                }
            }
        }
    }()

    // Wait for process to complete
    err := <-done
    return err
}

func (p *ProcessMonitor) detectSuspiciousSyscalls(pid int) bool {
    // Check for suspicious system calls that might indicate malware
    // - Network connections: socket, connect, bind, listen
    // - Process manipulation: ptrace, fork, vfork
    // - Privilege escalation: setuid, setgid, capset
    // - File system tampering: mount, umount, chroot

    // This would integrate with eBPF or strace for real-time monitoring
    return false
}
```

---

## Layer 4: Storage & Distribution Security

### 4.1 IPFS Content Validation

**Threat**: Malicious content pinned to IPFS spreads globally

**Solution**: Pre-pin validation and content scanning

```go
type IPFSSecurityGateway struct {
    ipfsClient    *ipfs.Client
    virusScanner  *VirusScanner
    contentFilter *ContentFilter
}

func (i *IPFSSecurityGateway) PinContentSecurely(ctx context.Context, filePath, cid string) error {
    // Step 1: Verify CID matches file content
    calculatedCID, err := i.calculateCID(filePath)
    if err != nil {
        return fmt.Errorf("failed to calculate CID: %w", err)
    }

    if calculatedCID != cid {
        log.Error().
            Str("expected_cid", cid).
            Str("calculated_cid", calculatedCID).
            Str("file", filePath).
            Msg("SECURITY: CID mismatch - possible tampering")
        return fmt.Errorf("CID mismatch: expected %s, got %s", cid, calculatedCID)
    }

    // Step 2: Final virus scan before pinning
    scanResult, err := i.virusScanner.ScanFile(ctx, filePath)
    if err != nil || scanResult.Status != ScanStatusClean {
        log.Error().
            Str("cid", cid).
            Str("file", filePath).
            Msg("SECURITY: Refusing to pin infected content to IPFS")
        return fmt.Errorf("content failed security scan before IPFS pin")
    }

    // Step 3: Content policy check (no illegal content)
    if violations := i.contentFilter.ScanFile(filePath); len(violations) > 0 {
        log.Warn().
            Str("cid", cid).
            Strs("violations", violations).
            Msg("SECURITY: Content policy violations detected")
        return fmt.Errorf("content violates policy: %v", violations)
    }

    // Step 4: Pin to IPFS
    if err := i.ipfsClient.Pin(ctx, cid); err != nil {
        return fmt.Errorf("failed to pin content: %w", err)
    }

    // Step 5: Log pinning event for audit trail
    i.logIPFSPin(cid, filePath, scanResult.StreamHash)

    return nil
}

func (i *IPFSSecurityGateway) UnpinInfectedContent(ctx context.Context, cid, reason string) error {
    // Unpin from local node
    if err := i.ipfsClient.Unpin(ctx, cid); err != nil {
        log.Error().Err(err).Str("cid", cid).Msg("Failed to unpin infected content")
        return err
    }

    // Remove from cluster (if using IPFS Cluster)
    if err := i.ipfsClient.ClusterUnpin(ctx, cid); err != nil {
        log.Error().Err(err).Str("cid", cid).Msg("Failed to cluster unpin infected content")
        return err
    }

    // Add to blocklist to prevent re-pinning
    if err := i.addToBlocklist(cid, reason); err != nil {
        log.Error().Err(err).Str("cid", cid).Msg("Failed to blocklist infected CID")
    }

    log.Warn().
        Str("cid", cid).
        Str("reason", reason).
        Msg("Unpinned infected content from IPFS")

    return nil
}
```

### 4.2 Content Blocklist Management

```sql
-- migrations/058_add_ipfs_blocklist.sql
CREATE TABLE IF NOT EXISTS ipfs_blocklist (
    cid TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    blocked_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    blocked_by UUID REFERENCES users(id),
    metadata JSONB,
    auto_detected BOOLEAN DEFAULT FALSE
);

CREATE INDEX idx_ipfs_blocklist_blocked_at ON ipfs_blocklist(blocked_at);
CREATE INDEX idx_ipfs_blocklist_auto_detected ON ipfs_blocklist(auto_detected) WHERE auto_detected = TRUE;

COMMENT ON TABLE ipfs_blocklist IS 'CIDs blocked from pinning due to security/policy violations';
```

```go
func (i *IPFSSecurityGateway) addToBlocklist(cid, reason string) error {
    query := `
        INSERT INTO ipfs_blocklist (cid, reason, auto_detected, metadata)
        VALUES ($1, $2, TRUE, $3)
        ON CONFLICT (cid) DO UPDATE
        SET reason = EXCLUDED.reason, blocked_at = NOW()
    `

    metadata := map[string]interface{}{
        "detection_method": "virus_scanner",
        "timestamp":        time.Now(),
    }
    metadataJSON, _ := json.Marshal(metadata)

    _, err := i.db.Exec(query, cid, reason, metadataJSON)
    return err
}

func (i *IPFSSecurityGateway) isBlocklisted(cid string) (bool, string, error) {
    var reason string
    query := `SELECT reason FROM ipfs_blocklist WHERE cid = $1`
    err := i.db.QueryRow(query, cid).Scan(&reason)

    if err == sql.ErrNoRows {
        return false, "", nil
    }
    if err != nil {
        return false, "", err
    }

    return true, reason, nil
}
```

---

## Layer 5: Monitoring, Detection & Response

### 5.1 Security Event Correlation

**Threat**: Sophisticated attacks span multiple systems

**Solution**: Centralized security event processing with SIEM integration

```go
type SecurityEventProcessor struct {
    eventBus   *EventBus
    ruleEngine *RuleEngine
    alerter    *Alerter
}

type SecurityEvent struct {
    Timestamp   time.Time         `json:"timestamp"`
    EventType   string            `json:"event_type"`
    Severity    string            `json:"severity"`
    UserID      string            `json:"user_id,omitempty"`
    IP          string            `json:"ip,omitempty"`
    SessionID   string            `json:"session_id,omitempty"`
    Resource    string            `json:"resource,omitempty"`
    Action      string            `json:"action"`
    Result      string            `json:"result"`
    Details     map[string]interface{} `json:"details"`
}

// Correlation Rules
var SecurityRules = []SecurityRule{
    {
        Name:        "Multiple Malware Uploads",
        Description: "User uploaded multiple infected files",
        Condition: func(events []SecurityEvent) bool {
            malwareEvents := filterEvents(events, "malware_detected")
            return len(malwareEvents) >= 3
        },
        Action: func(events []SecurityEvent) {
            userID := events[0].UserID
            // Ban user, alert security team, audit all content
            BanUser(userID, "multiple_malware_uploads")
            AlertSecurityTeam(SEVERITY_CRITICAL, "User %s uploaded %d malware files", userID, len(events))
            AuditUserContent(userID)
        },
    },
    {
        Name:        "Scan Bypass Attempt",
        Description: "Pattern indicates deliberate scan bypass attempt",
        Condition: func(events []SecurityEvent) bool {
            failures := filterEvents(events, "scan_failure")
            uploads := filterEvents(events, "upload_attempt")

            // Pattern: Multiple scan failures followed by successful upload
            // This could indicate retry-based bypass attempts
            return len(failures) >= 5 && len(uploads) > 0 &&
                time.Since(failures[0].Timestamp) < 10*time.Minute
        },
        Action: func(events []SecurityEvent) {
            userID := events[0].UserID
            AlertSecurityTeam(SEVERITY_HIGH, "Possible scan bypass attempt by user %s", userID)
            QuarantineRecentUploads(userID, 1*time.Hour)
            RequireManualReview(userID)
        },
    },
    {
        Name:        "Distributed Attack",
        Description: "Multiple IPs uploading similar malware",
        Condition: func(events []SecurityEvent) bool {
            malwareEvents := filterEvents(events, "malware_detected")

            // Group by virus signature
            signatures := make(map[string][]SecurityEvent)
            for _, evt := range malwareEvents {
                sig := evt.Details["virus_name"].(string)
                signatures[sig] = append(signatures[sig], evt)
            }

            // Check if same signature from multiple IPs
            for _, events := range signatures {
                ips := make(map[string]bool)
                for _, evt := range events {
                    ips[evt.IP] = true
                }
                if len(ips) >= 5 {
                    return true  // Same malware from 5+ IPs
                }
            }

            return false
        },
        Action: func(events []SecurityEvent) {
            AlertSecurityTeam(SEVERITY_CRITICAL, "Distributed malware campaign detected")
            EnableEnhancedScanning()
            NotifyInfraTeam("possible_botnet_activity")
        },
    },
}
```

### 5.2 Threat Intelligence Integration

```go
type ThreatIntelligence struct {
    feeds []ThreatFeed
    cache *redis.Client
}

type ThreatFeed interface {
    GetMalwareHashes() ([]string, error)
    GetMaliciousIPs() ([]string, error)
    GetC2Domains() ([]string, error)
}

func (t *ThreatIntelligence) CheckFileHash(hash string) (bool, ThreatInfo, error) {
    // Check cache first
    cacheKey := fmt.Sprintf("threat:hash:%s", hash)
    if cached, err := t.cache.Get(context.Background(), cacheKey).Result(); err == nil {
        var info ThreatInfo
        json.Unmarshal([]byte(cached), &info)
        return true, info, nil
    }

    // Query threat feeds
    for _, feed := range t.feeds {
        if info, found := feed.QueryHash(hash); found {
            // Cache result for 24 hours
            infoJSON, _ := json.Marshal(info)
            t.cache.Set(context.Background(), cacheKey, infoJSON, 24*time.Hour)

            return true, info, nil
        }
    }

    return false, ThreatInfo{}, nil
}

// VirusTotal integration example
type VirusTotalFeed struct {
    apiKey string
    client *http.Client
}

func (v *VirusTotalFeed) QueryHash(hash string) (ThreatInfo, bool) {
    url := fmt.Sprintf("https://www.virustotal.com/api/v3/files/%s", hash)

    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("x-apikey", v.apiKey)

    resp, err := v.client.Do(req)
    if err != nil {
        return ThreatInfo{}, false
    }
    defer resp.Body.Close()

    if resp.StatusCode == 404 {
        return ThreatInfo{}, false  // Hash unknown
    }

    var result struct {
        Data struct {
            Attributes struct {
                LastAnalysisStats struct {
                    Malicious int `json:"malicious"`
                    Suspicious int `json:"suspicious"`
                } `json:"last_analysis_stats"`
            } `json:"attributes"`
        } `json:"data"`
    }

    json.NewDecoder(resp.Body).Decode(&result)

    if result.Data.Attributes.LastAnalysisStats.Malicious > 0 {
        return ThreatInfo{
            ThreatLevel: "high",
            Detections:  result.Data.Attributes.LastAnalysisStats.Malicious,
            Source:      "virustotal",
        }, true
    }

    return ThreatInfo{}, false
}
```

---

## Configuration Security

### Secure Defaults

```go
// Default configuration MUST be secure
var SecureDefaults = &Config{
    // Virus Scanner
    ClamAV: ClamAVConfig{
        FallbackMode:     FallbackModeStrict,  // Reject if scanner unavailable
        MaxRetries:       3,
        Timeout:          5 * time.Minute,
        QuarantineDir:    "/var/quarantine",
        AutoQuarantine:   true,
    },

    // Upload Limits
    Upload: UploadConfig{
        MaxFileSize:      10 * GB,
        MaxConcurrent:    5,  // Per user
        AllowedExtensions: []string{".mp4", ".mov", ".avi", ".jpg", ".png", ".pdf"},
        BlockedExtensions: []string{".exe", ".dll", ".sh", ".bat", ".cmd"},
        RequireEmailVerification: true,
    },

    // Rate Limiting
    RateLimit: RateLimitConfig{
        UploadsPerMinutePerIP:   10,
        UploadsPerHourPerUser:   100,
        ScanFailuresBeforeBan:   5,
        BanDuration:             1 * time.Hour,
    },

    // Processing
    Processing: ProcessingConfig{
        MaxCPUPercent:    80,
        MaxMemoryMB:      4096,
        MaxDuration:      2 * time.Hour,
        SandboxEnabled:   true,
        NetworkIsolated:  true,
    },

    // Security
    Security: SecurityConfig{
        TLSMinVersion:        "1.3",
        HSTSMaxAge:           63072000,  // 2 years
        RequireHTTPS:         true,
        CSPEnabled:           true,
        AuditLoggingEnabled:  true,
        ThreatIntelEnabled:   true,
    },
}
```

---

## Summary & Roadmap

### Immediate Actions (Week 1)

- ✅ Fix virus scanner stream retry vulnerability
- ✅ Enable strict fallback mode
- ✅ Implement rate limiting on uploads
- ✅ Add MIME validation
- ✅ Deploy monitoring alerts

### Short-term (Month 1)

- ⏳ FFmpeg sandboxing with seccomp/AppArmor
- ⏳ Archive bomb protection
- ⏳ IPFS content validation
- ⏳ Security event correlation
- ⏳ Threat intelligence integration

### Medium-term (Quarter 1)

- ⏳ Multi-engine virus scanning
- ⏳ Behavioral analysis sandbox
- ⏳ Content sanitization pipeline
- ⏳ ML-based anomaly detection
- ⏳ SIEM integration

### Long-term (Year 1)

- ⏳ Zero-trust architecture
- ⏳ Hardware security modules for key management
- ⏳ Automated incident response
- ⏳ Bug bounty program
- ⏳ SOC 2 Type II certification

---

**Remember**: Security is a continuous process, not a destination. Regular audits, penetration testing, and staying informed about emerging threats are essential to maintaining a robust security posture.
