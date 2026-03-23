// Package discovery scans a directory for binaries and runs their hello
// command to collect manifests.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	contract "github.com/slidebolt/sb-contract"
)

// Binary represents a discovered binary and its manifest.
type Binary struct {
	Path     string
	Manifest contract.HelloResponse
}

// Scan lists executable files in dir and runs `<bin> hello` on each.
// Returns successfully discovered binaries and any errors encountered.
func Scan(ctx context.Context, dir string) ([]Binary, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{fmt.Errorf("discovery: read dir %s: %w", dir, err)}
	}

	var binaries []Binary
	var errs []error

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			errs = append(errs, fmt.Errorf("discovery: stat %s: %w", path, err))
			continue
		}

		// Skip non-executable files.
		if info.Mode()&0111 == 0 {
			continue
		}

		manifest, err := runHello(ctx, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("discovery: %s hello: %w", entry.Name(), err))
			continue
		}

		if err := manifest.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("discovery: %s manifest: %w", entry.Name(), err))
			continue
		}

		binaries = append(binaries, Binary{Path: path, Manifest: *manifest})
	}

	return binaries, errs
}

func runHello(ctx context.Context, binPath string) (*contract.HelloResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "hello")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("exec: %w", err)
	}

	var resp contract.HelloResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	return &resp, nil
}
