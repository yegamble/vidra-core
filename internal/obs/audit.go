package obs

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// EntityAuditView is the interface for entities that can be audited.
// ToLogKeys returns a flat map of key-value pairs safe to log.
type EntityAuditView interface {
	ToLogKeys() map[string]interface{}
}

// MapAuditView is a simple EntityAuditView backed by a map (used in tests and config auditing).
type MapAuditView map[string]interface{}

// ToLogKeys implements EntityAuditView.
func (m MapAuditView) ToLogKeys() map[string]interface{} {
	return map[string]interface{}(m)
}

const (
	auditActionCreate = "create"
	auditActionUpdate = "update"
	auditActionDelete = "delete"
	auditChannelSize  = 1000
)

// auditEntry is the internal representation of an audit log record.
type auditEntry struct {
	Timestamp string                 `json:"timestamp"`
	User      string                 `json:"user"`
	Domain    string                 `json:"domain"`
	Action    string                 `json:"action"`
	Fields    map[string]interface{} `json:"-"` // merged into top-level JSON
}

// AuditLogger writes audit entries to a dedicated log file asynchronously.
// Entries are queued to a buffered channel and drained by a background goroutine
// to prevent latency spikes during file rotation.
type AuditLogger struct {
	ch     chan auditEntry
	writer io.WriteCloser
	wg     sync.WaitGroup
	once   sync.Once
	logger *slog.Logger // for internal warnings (e.g. channel full)
}

// NewAuditLogger creates an AuditLogger writing JSONL to the given file path.
// The file is created if it does not exist.
func NewAuditLogger(filePath string) *AuditLogger {
	lj := &lumberjack.Logger{
		Filename:   filePath,
		MaxSize:    12, // MB
		MaxBackups: 5,
	}
	return newAuditLoggerWithWriter(lj)
}

// newAuditLoggerWithWriter creates an AuditLogger writing to the given io.WriteCloser.
// Used in tests with temp files.
func newAuditLoggerWithWriter(w io.WriteCloser) *AuditLogger {
	al := &AuditLogger{
		ch:     make(chan auditEntry, auditChannelSize),
		writer: w,
		logger: slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
	al.wg.Add(1)
	go al.drain()
	return al
}

// drain reads from the channel and writes JSON lines to the file.
func (al *AuditLogger) drain() {
	defer al.wg.Done()
	for entry := range al.ch {
		if err := al.writeEntry(entry); err != nil {
			al.logger.Warn("audit log write failed", "error", err)
		}
	}
}

// writeEntry serializes an auditEntry to a single JSON line.
func (al *AuditLogger) writeEntry(entry auditEntry) error {
	// Build the final JSON map: fixed fields + entity fields at top level
	out := map[string]interface{}{
		"timestamp": entry.Timestamp,
		"user":      entry.User,
		"domain":    entry.Domain,
		"action":    entry.Action,
	}
	for k, v := range entry.Fields {
		out[k] = v
	}

	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = al.writer.Write(b)
	return err
}

// enqueue adds an audit entry to the channel, dropping it if the channel is full.
func (al *AuditLogger) enqueue(e auditEntry) {
	select {
	case al.ch <- e:
	default:
		al.logger.Warn("audit log channel full — entry dropped",
			"domain", e.Domain, "action", e.Action, "user", e.User)
	}
}

// Create logs a create action for the given entity.
func (al *AuditLogger) Create(domain, user string, entity EntityAuditView) {
	al.enqueue(auditEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		User:      user,
		Domain:    domain,
		Action:    auditActionCreate,
		Fields:    entity.ToLogKeys(),
	})
}

// Update logs an update action, computing a diff between oldEntity and newEntity.
// Unchanged fields appear as-is; changed fields also appear with a "new-" prefixed copy.
func (al *AuditLogger) Update(domain, user string, newEntity, oldEntity EntityAuditView) {
	oldKeys := oldEntity.ToLogKeys()
	newKeys := newEntity.ToLogKeys()

	fields := make(map[string]interface{})
	// Include all old fields
	for k, v := range oldKeys {
		fields[k] = v
	}
	// Add new- prefixed entries for changed values
	for k, newVal := range newKeys {
		oldVal, existed := oldKeys[k]
		if !existed || fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			fields["new-"+k] = newVal
		}
	}

	al.enqueue(auditEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		User:      user,
		Domain:    domain,
		Action:    auditActionUpdate,
		Fields:    fields,
	})
}

// Delete logs a delete action for the given entity.
func (al *AuditLogger) Delete(domain, user string, entity EntityAuditView) {
	al.enqueue(auditEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		User:      user,
		Domain:    domain,
		Action:    auditActionDelete,
		Fields:    entity.ToLogKeys(),
	})
}

// Close drains remaining entries and closes the underlying file writer.
// Safe to call multiple times.
func (al *AuditLogger) Close() error {
	var err error
	al.once.Do(func() {
		close(al.ch)
		al.wg.Wait()
		err = al.writer.Close()
	})
	return err
}

// DomainAuditLogger is a convenience wrapper that binds a domain name to an AuditLogger.
type DomainAuditLogger struct {
	domain string
	al     *AuditLogger
}

// AuditLoggerFactory creates a DomainAuditLogger for the given domain.
// Matches PeerTube's auditLoggerFactory pattern.
func AuditLoggerFactory(domain string, al *AuditLogger) *DomainAuditLogger {
	return &DomainAuditLogger{domain: domain, al: al}
}

// Create logs a create action.
func (d *DomainAuditLogger) Create(user string, entity EntityAuditView) {
	d.al.Create(d.domain, user, entity)
}

// Update logs an update action.
func (d *DomainAuditLogger) Update(user string, newEntity, oldEntity EntityAuditView) {
	d.al.Update(d.domain, user, newEntity, oldEntity)
}

// Delete logs a delete action.
func (d *DomainAuditLogger) Delete(user string, entity EntityAuditView) {
	d.al.Delete(d.domain, user, entity)
}
