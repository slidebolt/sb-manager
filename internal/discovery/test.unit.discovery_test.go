package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanFindsExecutableWithValidManifest(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "svc", "#!/bin/sh\nif [ \"$1\" = hello ]; then\ncat <<'EOF'\n{\"id\":\"svc\",\"kind\":\"service\",\"contractVersion\":1}\nEOF\nfi\n")
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatal(err)
	}

	bins, errs := Scan(context.Background(), dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(bins) != 1 {
		t.Fatalf("bins: got %d want 1", len(bins))
	}
	if bins[0].Manifest.ID != "svc" {
		t.Fatalf("id: got %q want %q", bins[0].Manifest.ID, "svc")
	}
}

func TestScanReportsInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "broken", "#!/bin/sh\nif [ \"$1\" = hello ]; then\ncat <<'EOF'\n{\"id\":\"\",\"kind\":\"service\",\"contractVersion\":1}\nEOF\nfi\n")

	bins, errs := Scan(context.Background(), dir)
	if len(bins) != 0 {
		t.Fatalf("bins: got %d want 0", len(bins))
	}
	if len(errs) != 1 {
		t.Fatalf("errs: got %d want 1", len(errs))
	}
}

func TestScanAcceptsFreePortSetupGenerator(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "api", "#!/bin/sh\nif [ \"$1\" = hello ]; then\ncat <<'EOF'\n{\"id\":\"api\",\"kind\":\"service\",\"contractVersion\":1,\"setup\":{\"mode\":\"required\",\"category\":\"Core\",\"fields\":[{\"key\":\"port\",\"environment\":\"SB_API_PORT\",\"name\":\"API port\",\"control\":\"text\",\"generate\":\"free_port\"}]}}\nEOF\nfi\n")

	bins, errs := Scan(context.Background(), dir)
	if len(errs) != 0 || len(bins) != 1 || bins[0].Manifest.ID != "api" {
		t.Fatalf("Scan() bins=%#v errs=%v", bins, errs)
	}
}
