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

	deadline := time.Now().Add(15 * time.Second)
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

func TestStatusDoesNotInferReadyFromDiscovery(t *testing.T) {
	m := New(t.TempDir(), "")
	m.binaries["plugin-tv"] = discovery.Binary{Manifest: mustManifest(t, `{"id":"plugin-tv","kind":"plugin","contractVersion":1,"dependsOn":["storage"]}`)}
	got := m.Status()
	if len(got) != 1 || got[0].ID != "plugin-tv" || got[0].State != "stopped" {
		t.Fatalf("unexpected status: %#v", got)
	}
}

func TestDiscoverFailsWhenAnExecutableManifestIsInvalid(t *testing.T) {
	dir := t.TempDir()
	writeManagedBinary(t, dir, "valid", `{"id":"valid","kind":"service","contractVersion":1}`, "printf '{\"type\":\"ready\"}\\n'")
	writeManagedBinary(t, dir, "invalid", `{"id":"","kind":"service","contractVersion":1}`, "exit 0")
	m := New(dir, "")
	if err := m.discover(); err == nil {
		t.Fatal("discover() = nil, want invalid component error")
	}
}

func TestComponentEnvironmentIsScopedToTargetProcess(t *testing.T) {
	dir := t.TempDir()
	valueFile := filepath.Join(dir, "value")
	writeManagedBinary(t, dir, "plugin",
		`{"id":"plugin","kind":"service","contractVersion":1}`,
		"printf '%s' \"$PLUGIN_SECRET\" > '"+valueFile+"'\n"+
			"printf '{\"type\":\"ready\"}\\n'\n"+
			"while IFS= read -r line; do case \"$line\" in *shutdown*) exit 0;; esac; done")

	m := NewWithEnvironment(dir, "", map[string]map[string]string{"plugin": {"PLUGIN_SECRET": "scoped-value"}})
	if err := m.discover(); err != nil {
		t.Fatal(err)
	}
	if err := m.startAll(); err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	data, err := os.ReadFile(valueFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "scoped-value" {
		t.Fatalf("component environment = %q", data)
	}
}

func TestMissingRequiredToolBlocksOnlyComponentAndDependents(t *testing.T) {
	dir := t.TempDir()
	writeManagedBinary(t, dir, "blocked", `{"id":"blocked","kind":"service","contractVersion":1,"setup":{"mode":"required","category":"Core","tools":[{"id":"definitely-missing-tool","name":"Missing tool","source":"system","kind":"executable","required":true,"checks":[{"arguments":["-version"]}]}]}}`, "exit 99")
	writeManagedBinary(t, dir, "dependent", `{"id":"dependent","kind":"service","contractVersion":1,"dependsOn":["blocked"]}`, "exit 99")
	writeManagedBinary(t, dir, "healthy", `{"id":"healthy","kind":"service","contractVersion":1}`, "printf '{\"type\":\"ready\"}\\n'\n"+
		"while IFS= read -r line; do case \"$line\" in *shutdown*) exit 0;; esac; done")

	m := New(dir, "")
	if err := m.discover(); err != nil {
		t.Fatal(err)
	}
	if err := m.startAll(); err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	states := map[string]ComponentStatus{}
	for _, status := range m.Status() {
		states[status.ID] = status
	}
	if states["blocked"].State != "blocked" || states["dependent"].State != "blocked" {
		t.Fatalf("blocked states = %#v", states)
	}
	if states["healthy"].State != "ready" {
		t.Fatalf("healthy state = %#v", states["healthy"])
	}
}

func TestResolvedToolPathIsScopedToTargetProcess(t *testing.T) {
	dir := t.TempDir()
	toolDir := t.TempDir()
	tool := filepath.Join(toolDir, "video-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'video-tool ready\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	valueFile := filepath.Join(dir, "tool-path")
	manifest := `{"id":"media","kind":"service","contractVersion":1,"setup":{"mode":"required","category":"Core","tools":[{"id":"video-tool","name":"Video tool","source":"system","kind":"executable","environment":"VIDEO_TOOL_PATH","required":true,"checks":[{"arguments":["-version"],"contains":["ready"]}]}]}}`
	writeManagedBinary(t, dir, "media", manifest, "printf '%s' \"$VIDEO_TOOL_PATH\" > '"+valueFile+"'\n"+
		"printf '{\"type\":\"ready\"}\\n'\n"+
		"while IFS= read -r line; do case \"$line\" in *shutdown*) exit 0;; esac; done")
	m := NewWithEnvironment(dir, "", map[string]map[string]string{"media": {"VIDEO_TOOL_PATH": tool}})
	if err := m.discover(); err != nil {
		t.Fatal(err)
	}
	if err := m.startAll(); err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	raw, err := os.ReadFile(valueFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != tool {
		t.Fatalf("tool path = %q, want %q", raw, tool)
	}
	status := m.Status()[0]
	if len(status.Tools) != 1 || !status.Tools[0].Healthy || status.Tools[0].Path != tool {
		t.Fatalf("tool status = %#v", status.Tools)
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
