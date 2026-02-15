package backup

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
)

type FTPBackend struct {
	Host     string
	Port     int
	User     string
	password string
	Path     string
	conn     *ftp.ServerConn
	once     sync.Once
	initErr  error
}

func NewFTPBackend(host string, port int, user, password, path string) *FTPBackend {
	log.Println("WARNING: FTP backup target sends credentials in plaintext. Consider using SFTP or S3 for secure transport.")
	return &FTPBackend{
		Host:     host,
		Port:     port,
		User:     user,
		password: password,
		Path:     path,
	}
}

func (f *FTPBackend) initClient(ctx context.Context) error {
	f.once.Do(func() {
		f.initErr = f.doInitClient(ctx)
	})
	return f.initErr
}

func (f *FTPBackend) doInitClient(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", f.Host, f.Port)

	conn, err := ftp.Dial(addr, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return fmt.Errorf("connecting to FTP server: %w", err)
	}

	if err := conn.Login(f.User, f.password); err != nil {
		_ = conn.Quit()
		return fmt.Errorf("FTP login failed: %w", err)
	}

	f.conn = conn
	return nil
}

func (f *FTPBackend) buildPath(filename string) string {
	basePath := f.Path
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}

	return filepath.Join(basePath, filename)
}

func (f *FTPBackend) Upload(ctx context.Context, reader io.Reader, path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if err := f.initClient(ctx); err != nil {
		return err
	}

	remotePath := f.buildPath(path)

	dir := filepath.Dir(remotePath)
	_ = f.conn.MakeDir(dir)

	if err := f.conn.Stor(remotePath, reader); err != nil {
		return fmt.Errorf("uploading file via FTP: %w", err)
	}

	return nil
}

func (f *FTPBackend) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := f.initClient(ctx); err != nil {
		return nil, err
	}

	remotePath := f.buildPath(path)

	resp, err := f.conn.Retr(remotePath)
	if err != nil {
		return nil, fmt.Errorf("downloading file via FTP: %w", err)
	}

	return resp, nil
}

func (f *FTPBackend) List(ctx context.Context, prefix string) ([]BackupEntry, error) {
	if err := f.initClient(ctx); err != nil {
		return nil, err
	}

	searchPath := f.Path
	if prefix != "" {
		searchPath = filepath.Join(f.Path, prefix)
	}

	entries, err := f.conn.List(searchPath)
	if err != nil {
		return nil, fmt.Errorf("listing FTP directory: %w", err)
	}

	var backupEntries []BackupEntry
	for _, entry := range entries {
		if entry.Type == ftp.EntryTypeFile {
			relPath := entry.Name
			if f.Path != "" {
				relPath = strings.TrimPrefix(relPath, f.Path)
				relPath = strings.TrimPrefix(relPath, "/")
			}

			backupEntries = append(backupEntries, BackupEntry{
				Path:    relPath,
				Size:    int64(entry.Size),
				ModTime: entry.Time,
			})
		}
	}

	return backupEntries, nil
}

func (f *FTPBackend) Delete(ctx context.Context, path string) error {
	if err := f.initClient(ctx); err != nil {
		return err
	}

	remotePath := f.buildPath(path)

	if err := f.conn.Delete(remotePath); err != nil {
		return fmt.Errorf("deleting file via FTP: %w", err)
	}

	return nil
}

func (f *FTPBackend) Close() error {
	if f.conn != nil {
		return f.conn.Quit()
	}
	return nil
}
