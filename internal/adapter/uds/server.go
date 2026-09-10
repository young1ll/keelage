// Package uds serves HTTP over a Unix domain socket. It is the transport
// for the daemon's local API (~/.keelage/keelage.sock, mode 0600).
package uds

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ErrAlreadyRunning is returned when another process already answers on the socket.
var ErrAlreadyRunning = errors.New("uds: another daemon is already listening on the socket")

// Serve listens on path (creating the parent directory 0700 and the socket
// 0600) and serves h until ctx is done. A stale socket file left by a
// crashed daemon is removed; a live one makes Serve return ErrAlreadyRunning.
func Serve(ctx context.Context, path string, h http.Handler) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("uds: mkdir: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		if c, derr := net.DialTimeout("unix", path, 200*time.Millisecond); derr == nil {
			_ = c.Close()
			return ErrAlreadyRunning
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("uds: remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("uds: listen: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("uds: chmod: %w", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err = srv.Serve(ln)
	<-done
	_ = os.Remove(path)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
