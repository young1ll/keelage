package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The installer against a fake release: archive + checksums served over
// HTTP (KEELAGE_RELEASE_BASE), no cosign (KEELAGE_SKIP_COSIGN). Checks the
// happy path, a tampered archive, and the guard against contradictory
// cosign flags. cosign itself is exercised by the release workflow.
func TestInstallScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const version = "v9.9.9"
	name := fmt.Sprintf("keelage_9.9.9_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archive := tarGz(t, "keelage", []byte("#!/bin/sh\necho keelage-fake\n"))
	sum := sha256.Sum256(archive)
	checksums := hex.EncodeToString(sum[:]) + "  " + name + "\n"

	var mu sync.Mutex
	files := map[string][]byte{"/" + name: archive, "/checksums.txt": []byte(checksums)}
	set := func(path string, b []byte) {
		mu.Lock()
		defer mu.Unlock()
		files[path] = b
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		b, ok := files[r.URL.Path]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	run := func(extra ...string) (string, error) {
		t.Helper()
		dir := t.TempDir()
		cmd := exec.Command("sh", script)
		cmd.Env = append(os.Environ(),
			"KEELAGE_RELEASE_BASE="+srv.URL, "KEELAGE_VERSION="+version, "KEELAGE_INSTALL_DIR="+dir,
			"HTTPS_PROXY=", "https_proxy=", "HTTP_PROXY=", "http_proxy=", "NO_PROXY=*")
		cmd.Env = append(cmd.Env, extra...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			if b, rerr := os.ReadFile(filepath.Join(dir, "keelage")); rerr != nil || !strings.Contains(string(b), "keelage-fake") {
				t.Fatalf("binary not installed: %v %s", rerr, b)
			}
		}
		return string(out), err
	}

	out, err := run("KEELAGE_SKIP_COSIGN=1")
	if err != nil || !strings.Contains(out, "checksum: ok") || !strings.Contains(out, "signature: skipped") {
		t.Fatalf("happy path: %v\n%s", err, out)
	}
	// contradictory flags are refused before any download
	if out, err := run("KEELAGE_SKIP_COSIGN=1", "KEELAGE_REQUIRE_COSIGN=1"); err == nil || !strings.Contains(out, "cannot both") {
		t.Fatalf("skip+require: %v\n%s", err, out)
	}
	// latest cannot be resolved against an override base
	if out, err := run("KEELAGE_SKIP_COSIGN=1", "KEELAGE_VERSION=latest"); err == nil || !strings.Contains(out, "needs KEELAGE_VERSION") {
		t.Fatalf("latest with base: %v\n%s", err, out)
	}
	// a tampered archive fails the checksum and installs nothing
	set("/"+name, append([]byte("junk"), archive...))
	if out, err := run("KEELAGE_SKIP_COSIGN=1"); err == nil || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("tampered: %v\n%s", err, out)
	}
	// an archive missing from checksums.txt is refused
	set("/checksums.txt", []byte("deadbeef  other.tar.gz\n"))
	if out, err := run("KEELAGE_SKIP_COSIGN=1"); err == nil || !strings.Contains(out, "not in checksums.txt") {
		t.Fatalf("missing checksum: %v\n%s", err, out)
	}
}

func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
