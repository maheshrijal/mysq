package export

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestWriteCreatesAgentBundleAndArchive(t *testing.T) {
	ctx := &model.Context{
		SchemaVersion: model.SchemaVersion, ToolVersion: "test", CollectedAt: time.Unix(1, 0).UTC(), Fingerprint: "snapshot",
		Server: model.Server{Host: "127.0.0.1", Port: 3306, Database: "app", Flavor: "MySQL", Version: "8.4.0"},
		Health: model.Health{Score: 100}, Variables: map[string]string{"max_connections": "151"}, GlobalStatus: map[string]string{"Uptime": "10"},
		Queries:          []model.Query{{Digest: "ABC", Statement: "SELECT * FROM users WHERE id = ?"}},
		StatementSamples: []model.StatementSample{{Digest: "ABC", Statement: "SELECT * FROM users WHERE id = ?", Calls: 2, DatabaseTimeMillis: 5, DatabaseTimeSharePercent: 100}},
	}
	output := filepath.Join(t.TempDir(), "bundle")
	result, err := Write(ctx, output, Options{Zip: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "context.json", "summary.md", "queries.csv", "statement-samples.csv", "transactions.csv", "metadata-locks.csv", "wait-events.csv", "file-io.csv", "server-errors.csv", "memory-consumers.csv", "raw/instrumentation.json", "schema/context-1.5.0.json", "variables.cnf", "README.md"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.SecretFree || len(m.Files) < 10 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	archive, err := zip.OpenReader(result.Archive)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != len(result.Files) {
		t.Fatalf("zip has %d files, want %d", len(archive.File), len(result.Files))
	}
}

func TestWriteRefusesToOverwrite(t *testing.T) {
	output := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Write(&model.Context{CollectedAt: time.Now()}, output, Options{})
	if err == nil {
		t.Fatal("expected overwrite error")
	}
}
