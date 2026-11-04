package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/vidra/vidra-core/internal/blobverify"
)

func openFindings(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func writeFinding(w io.Writer, finding blobverify.Finding) error {
	body, err := json.Marshal(finding)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	n, err := w.Write(body)
	if err == nil && n != len(body) {
		return io.ErrShortWrite
	}
	return err
}

func finishFindings(f interface {
	Sync() error
	Close() error
}) error {
	// Close is attempted even after Sync fails. Neither failure may become PASS.
	syncErr := f.Sync()
	return errors.Join(syncErr, f.Close())
}
