package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contract "github.com/slidebolt/sb-contract"
	"github.com/slidebolt/sb-manager/internal/discovery"
)

func writeManagedBinary(t *testing.T, dir, name, manifestJSON, startScript string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = hello ]; then\n" +
		"cat <<'EOF'\n" + manifestJSON + "\nEOF\n" +
		"exit 0\n" +
		"fi\n" +
		startScript + "\n"
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStartAllSendsDependencyPayloads(t *testing.T) {
	dir := t.TempDir()
	depFile := filepath.Join(dir, "plugin.stdin")

	writeManagedBinary(t, dir, "messenger",
		`{"id":"messenger","kind":"service","contractVersion":1}`,
		"printf '{\"type\":\"ready\",\"payload\":{\"nats_url\":\"127.0.0.1\",\"nats_port\":4222}}\\n'\n"+
			"while IFS= read -r line; do\n"+
			"  if printf '%s' \"$line\" | grep -q 'shutdown'; then\n"+
			"    exit 0\n"+
			"  fi\n"+
			"done")

	writeManagedBinary(t, dir, "plugin",
		`{"id":"plugin","kind":"plugin","contractVersion":1,"dependsOn":["messenger"]}`,
		"while IFS= read -r line; do\n"+
			"  printf '%s\\n' \"$line\" >> '"+depFile+"'\n"+
			"  if printf '%s' \"$line\" | grep -q '\"id\":\"messenger\"'; then\n"+
			"    printf '{\"type\":\"ready\"}\\n'\n"+
			"  fi\n"+
			"  if printf '%s' \"$line\" | grep -q 'shutdown'; then\n"+
			"    exit 0\n"+
			"  fi\n"+
			"done")

	m := New(dir, "")
	m.binaries["messenger"] = discovery.Binary{
		Path:     filepath.Join(dir, "messenger"),
		Manifest: mustManifest(t, `{"id":"messenger","kind":"service","contractVersion":1}`),
	}
	m.binaries["plugin"] = discovery.Binary{
		Path:     filepath.Join(dir, "plugin"),
		Manifest: mustManifest(t, `{"id":"plugin","kind":"plugin","contractVersion":1,"dependsOn":["messenger"]}`),
	}

	if err := m.startAll(); err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(depFile)
		if err == nil && strings.Contains(string(data), `"id":"messenger"`) && strings.Contains(string(data), `"nats_port":4222`) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("dependency payload was not delivered to plugin, file=%s", depFile)
}

func TestTopoSortRejectsMissingDependency(t *testing.T) {
	m := New(t.TempDir(), "")
	m.binaries["plugin"] = discovery.Binary{
		Path:     "/tmp/plugin",
		Manifest: mustManifest(t, `{"id":"plugin","kind":"plugin","contractVersion":1,"dependsOn":["messenger"]}`),
	}

	_, err := m.topoSort()
	if err == nil {
		t.Fatal("expected error for missing dependency")
	}
}

func mustManifest(t *testing.T, raw string) contract.HelloResponse {
	t.Helper()
	var m contract.HelloResponse
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
