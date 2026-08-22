package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/maheshrijal/mysq/internal/model"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

type manifest struct {
	SecretFree bool `json:"secret_free"`
	Files      []struct {
		Path, SHA256 string
	} `json:"files"`
}

func main() {
	var contextPath, bundle string
	flag.StringVar(&contextPath, "context", "", "context JSON to verify")
	flag.StringVar(&bundle, "bundle", "", "bundle directory to verify")
	flag.Parse()
	if contextPath != "" {
		verifyContext(contextPath)
	}
	if bundle != "" {
		verifyBundle(bundle)
	}
	if contextPath == "" && bundle == "" {
		log.Fatal("pass --context or --bundle")
	}
}

func verifyContext(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	var ctx model.Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		log.Fatal(err)
	}
	if ctx.SchemaVersion != model.SchemaVersion || ctx.Server.Flavor != "MySQL" || ctx.Server.Host != "127.0.0.1" {
		log.Fatalf("unexpected identity: %+v", ctx.Server)
	}
	if len(ctx.Queries) == 0 || len(ctx.Tables) < 3 || len(ctx.Indexes) == 0 || len(ctx.Processes) == 0 || len(ctx.ConnectionGroups) == 0 || len(ctx.Findings) == 0 {
		log.Fatalf("insufficient coverage: queries=%d tables=%d indexes=%d processes=%d connection_groups=%d findings=%d", len(ctx.Queries), len(ctx.Tables), len(ctx.Indexes), len(ctx.Processes), len(ctx.ConnectionGroups), len(ctx.Findings))
	}
	if len(ctx.WaitEvents) == 0 || len(ctx.MemoryConsumers) == 0 || ctx.Metrics.RedoCapacityBytes == 0 || ctx.Metrics.BufferPoolDataBytes == 0 {
		log.Fatalf("insufficient engine coverage: waits=%d memory=%d redo_capacity=%d buffer_data=%d", len(ctx.WaitEvents), len(ctx.MemoryConsumers), ctx.Metrics.RedoCapacityBytes, ctx.Metrics.BufferPoolDataBytes)
	}
	if len(ctx.FileIO) == 0 || len(ctx.ServerErrors) == 0 || ctx.StatementLatency.P95Millis == 0 || ctx.Instrumentation.DigestCapacity == 0 {
		log.Fatalf("insufficient MySQL investigation data: file_io=%d errors=%d p95=%.2f digest_capacity=%d",
			len(ctx.FileIO), len(ctx.ServerErrors), ctx.StatementLatency.P95Millis, ctx.Instrumentation.DigestCapacity)
	}
	// MySQL occasionally exposes an unsigned timer underflow near UINT64_MAX.
	// It is not a real statement duration and must never reach the product surface.
	if ctx.StatementLatency.MaxMillis > 9223372036.854775 {
		log.Fatalf("statement max latency contains an invalid Performance Schema timer: %.4fms", ctx.StatementLatency.MaxMillis)
	}
	for _, process := range ctx.Processes {
		if strings.Contains(process.Statement, "mysq-load-test") {
			log.Fatal("process statement leaked a literal")
		}
	}
	for name, value := range ctx.Variables {
		if sanitize.SensitiveName(name) && value != "" && value != "[redacted]" {
			log.Fatalf("sensitive variable %s was not redacted", name)
		}
	}
	fmt.Printf("verified context: score=%d findings=%d queries=%d tables=%d locks=%d\n", ctx.Health.Score, len(ctx.Findings), len(ctx.Queries), len(ctx.Tables), len(ctx.Locks))
}

func verifyBundle(directory string) {
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		log.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		log.Fatal(err)
	}
	if !m.SecretFree || len(m.Files) != 23 {
		log.Fatalf("invalid manifest: secret_free=%t files=%d", m.SecretFree, len(m.Files))
	}
	for _, file := range m.Files {
		content, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(file.Path)))
		if err != nil {
			log.Fatal(err)
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != file.SHA256 {
			log.Fatalf("checksum mismatch: %s", file.Path)
		}
	}
	verifyContext(filepath.Join(directory, "context.json"))
	fmt.Printf("verified bundle: %d artifacts and checksums\n", len(m.Files)+1)
}
