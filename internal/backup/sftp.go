package backup

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type SFTPBackend struct {
	Host         string
	Port         int
	User         string
	password     string
	keyPath      string
	Path         string
	HostKey      string
	client       *sftp.Client
	sshClient    *ssh.Client
	knownHostKey string
	once         sync.Once
	initErr      error
}

func NewSFTPBackend(host string, port int, user, password, keyPath, path string) *SFTPBackend {
	return &SFTPBackend{
		Host:     host,
		Port:     port,
		User:     user,
		password: password,
		keyPath:  keyPath,
		Path:     path,
	}
}

func (s *SFTPBackend) initClient(ctx context.Context) error {
	s.once.Do(func() {
		s.initErr = s.doInitClient(ctx)
	})
	return s.initErr
}

func (s *SFTPBackend) doInitClient(ctx context.Context) error {
	var authMethods []ssh.AuthMethod

	if s.password != "" {
		authMethods = append(authMethods, ssh.Password(s.password))
	}

	if s.keyPath != "" {
		keyData, err := os.ReadFile(s.keyPath)
		if err != nil {
			return fmt.Errorf("reading private key file: %w", err)
		}
		key, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			return fmt.Errorf("parsing private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(key))
	}

	if len(authMethods) == 0 {
		return fmt.Errorf("no authentication method provided (password or key required)")
	}

	hostKeyCallback, err := s.buildHostKeyCallback()
	if err != nil {
		return fmt.Errorf("building host key callback: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            s.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("connecting to SSH: %w", err)
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return fmt.Errorf("creating SFTP client: %w", err)
	}

	s.sshClient = sshClient
	s.client = sftpClient

	return nil
}

func (s *SFTPBackend) buildHostKeyCallback() (ssh.HostKeyCallback, error) {
	if s.HostKey != "" {
		hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s.HostKey))
		if err != nil {
			return nil, fmt.Errorf("parsing host key: %w", err)
		}
		return ssh.FixedHostKey(hostKey), nil
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		marshaledKey := string(ssh.MarshalAuthorizedKey(key))
		if s.knownHostKey == "" {
			log.Printf("WARNING: accepting unverified host key for %s on first connection (TOFU)", hostname)
			log.Printf("Host key fingerprint: %s", marshaledKey)
			s.knownHostKey = marshaledKey
			return nil
		}
		if s.knownHostKey != marshaledKey {
			return fmt.Errorf("host key mismatch for %s (possible MITM attack)", hostname)
		}
		return nil
	}, nil
}

func (s *SFTPBackend) buildPath(filename string) string {
	basePath := s.Path
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}

	return filepath.Join(basePath, filename)
}

func (s *SFTPBackend) Upload(ctx context.Context, reader io.Reader, path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if err := s.initClient(ctx); err != nil {
		return err
	}

	remotePath := s.buildPath(path)

	dir := filepath.Dir(remotePath)
	if err := s.client.MkdirAll(dir); err != nil {
		return fmt.Errorf("creating remote directory: %w", err)
	}

	file, err := s.client.Create(remotePath)
	if err != nil {
		return fmt.Errorf("creating remote file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("uploading file: %w", err)
	}

	return nil
}

func (s *SFTPBackend) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := s.initClient(ctx); err != nil {
		return nil, err
	}

	remotePath := s.buildPath(path)

	file, err := s.client.Open(remotePath)
	if err != nil {
		return nil, fmt.Errorf("opening remote file: %w", err)
	}

	return file, nil
}

func (s *SFTPBackend) List(ctx context.Context, prefix string) ([]BackupEntry, error) {
	if err := s.initClient(ctx); err != nil {
		return nil, err
	}

	searchPath := s.Path
	if prefix != "" {
		searchPath = filepath.Join(s.Path, prefix)
	}

	var entries []BackupEntry

	walker := s.client.Walk(searchPath)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			continue
		}

		stat := walker.Stat()
		if stat.IsDir() {
			continue
		}

		relPath, err := filepath.Rel(s.Path, walker.Path())
		if err != nil {
			continue
		}

		entries = append(entries, BackupEntry{
			Path:    relPath,
			Size:    stat.Size(),
			ModTime: stat.ModTime(),
		})
	}

	return entries, nil
}

func (s *SFTPBackend) Delete(ctx context.Context, path string) error {
	if err := s.initClient(ctx); err != nil {
		return err
	}

	remotePath := s.buildPath(path)

	if err := s.client.Remove(remotePath); err != nil {
		return fmt.Errorf("deleting remote file: %w", err)
	}

	return nil
}

func (s *SFTPBackend) Close() error {
	if s.client != nil {
		s.client.Close()
	}
	if s.sshClient != nil {
		return s.sshClient.Close()
	}
	return nil
}
