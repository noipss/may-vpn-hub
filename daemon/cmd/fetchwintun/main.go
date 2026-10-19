// fetchwintun (build tool): downloads the signed Wintun driver DLL from wintun.net, checks the
// archive's SHA-256 and extracts wintun.dll for one architecture.
//
//	go run ./cmd/fetchwintun <amd64|arm64> <out dir> [cache dir]
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	URL    = "https://www.wintun.net/builds/wintun-0.14.1.zip"
	SHA256 = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
)

func main() {
	if len(os.Args) < 3 {
		fail(fmt.Errorf("usage: fetchwintun <amd64|arm64> <out dir> [cache dir]"))
	}
	arch, out := os.Args[1], os.Args[2]
	cache := filepath.Join(os.TempDir(), "mayvpn-wintun")
	if len(os.Args) > 3 {
		cache = os.Args[3]
	}
	zipPath := filepath.Join(cache, filepath.Base(URL))
	data, err := os.ReadFile(zipPath)
	if err != nil || sum(data) != SHA256 {
		c := &http.Client{Timeout: 60 * time.Second}
		resp, err := c.Get(URL)
		if err != nil {
			fail(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			fail(fmt.Errorf("wintun.net answered %s", resp.Status))
		}
		if data, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20)); err != nil {
			fail(err)
		}
		if got := sum(data); got != SHA256 {
			fail(fmt.Errorf("wintun archive checksum mismatch: %s", got))
		}
		os.MkdirAll(cache, 0o755)
		os.WriteFile(zipPath, data, 0o644)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fail(err)
	}
	want := "wintun/bin/" + arch + "/wintun.dll"
	for _, f := range zr.File {
		if f.Name != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			fail(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			fail(err)
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(filepath.Join(out, "wintun.dll"), b, 0o644); err != nil {
			fail(err)
		}
		return
	}
	fail(fmt.Errorf("%s not in the archive", want))
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func fail(err error) { fmt.Fprintln(os.Stderr, "fetchwintun:", err); os.Exit(1) }
