package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	var contextPath, bundle, focusedDirectory string
	flag.StringVar(&contextPath, "context", "", "context JSON to verify")
	flag.StringVar(&bundle, "bundle", "", "bundle directory to verify")
	flag.StringVar(&focusedDirectory, "focused-dir", "", "directory containing focused command JSON")
	flag.Parse()
	if contextPath != "" {
		verifyContext(contextPath)
	}
	if bundle != "" {
		verifyBundle(bundle)
	}
	if focusedDirectory != "" {
		verifyFocused(focusedDirectory)
	}
	if contextPath == "" && bundle == "" && focusedDirectory == "" {
		log.Fatal("pass --context, --bundle, or --focused-dir")
	}
}

func verifyFocused(directory string) {
	sections := []string{"queries", "tables", "indexes", "processes", "transactions", "locks", "metadata-locks", "waits", "io", "errors", "memory", "engine", "coverage", "variables", "replication"}
	for _, section := range sections {
		data, err := os.ReadFile(filepath.Join(directory, section+".json"))
		if err != nil {
			log.Fatal(err)
		}
		if err := verifyFocusedData(section, data); err != nil {
			log.Fatalf("invalid focused %s output: %v", section, err)
		}
	}
	fmt.Printf("verified focused commands: %d section-specific JSON outputs\n", len(sections))
}

func verifyFocusedData(section string, data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("empty output")
	}
	if bytes.Equal(trimmed, []byte("null")) && section != "replication" {
		return errors.New("unexpected null output")
	}
	switch section {
	case "queries":
		var items []model.Query
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) == 0 || items[0].Digest == "" {
			return fmt.Errorf("missing query evidence: items=%d err=%v", len(items), err)
		}
	case "tables":
		var items []model.Table
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) < 3 || items[0].Name == "" {
			return fmt.Errorf("missing table evidence: items=%d err=%v", len(items), err)
		}
	case "indexes":
		var items []model.Index
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) == 0 || items[0].Name == "" {
			return fmt.Errorf("missing index evidence: items=%d err=%v", len(items), err)
		}
	case "processes":
		var items []model.Process
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) == 0 || items[0].ID == 0 {
			return fmt.Errorf("missing process evidence: items=%d err=%v", len(items), err)
		}
	case "transactions":
		var items []model.Transaction
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) == 0 || items[0].ID == "" {
			return fmt.Errorf("missing transaction evidence: items=%d err=%v", len(items), err)
		}
	case "locks":
		var items []model.LockWait
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) == 0 || items[0].WaitingTransaction == "" {
			return fmt.Errorf("missing lock evidence: items=%d err=%v", len(items), err)
		}
	case "metadata-locks":
		var items []model.MetadataLock
		if err := decodeStrictJSON(trimmed, &items); err != nil {
			return fmt.Errorf("invalid metadata-lock evidence: %w", err)
		}
		foundFixtureLock := false
		for _, item := range items {
			if item.ObjectType == "TABLE" && item.Schema == "app" && item.Object == "accounts" && item.LockType != "" && item.Status != "" {
				foundFixtureLock = true
				break
			}
		}
		if !foundFixtureLock {
			return fmt.Errorf("missing typed app.accounts metadata-lock evidence: items=%d", len(items))
		}
	case "waits":
		var items []model.WaitEvent
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) == 0 || items[0].Name == "" {
			return fmt.Errorf("missing wait evidence: items=%d err=%v", len(items), err)
		}
	case "io":
		var items []model.FileIO
		err := decodeStrictJSON(trimmed, &items)
		if err != nil || len(items) == 0 {
			return fmt.Errorf("missing file I/O evidence: items=%d err=%v", len(items), err)
		}
		hasSample := false
		for _, item := range items {
			if item.Name == "" || item.Class == "" {
				return fmt.Errorf("file I/O evidence is missing name or class: items=%d", len(items))
			}
			if item.ReadsPerSecond > 0 || item.WritesPerSecond > 0 || item.ReadBytesPerSecond > 0 || item.WriteBytesPerSecond > 0 || item.WaitMillisPerSecond > 0 {
				hasSample = true
			}
		}
		if !hasSample {
			return fmt.Errorf("missing file I/O evidence: items=%d err=%v", len(items), err)
		}
	case "errors":
		var items []model.ServerError
		err := decodeStrictJSON(trimmed, &items)
		if err != nil || len(items) == 0 {
			return fmt.Errorf("missing server error evidence: items=%d err=%v", len(items), err)
		}
		hasSample := false
		for _, item := range items {
			if item.Number == 0 || item.Name == "" || item.SQLState == "" {
				return fmt.Errorf("server error evidence is missing number, name, or SQL state: items=%d", len(items))
			}
			if item.SampleRaised > 0 || item.RaisedPerSecond > 0 {
				hasSample = true
			}
		}
		if !hasSample {
			return fmt.Errorf("missing server error evidence: items=%d err=%v", len(items), err)
		}
	case "memory":
		var items []model.MemoryConsumer
		if err := decodeStrictJSON(trimmed, &items); err != nil || len(items) == 0 || items[0].Name == "" {
			return fmt.Errorf("missing memory evidence: items=%d err=%v", len(items), err)
		}
	case "engine":
		var metrics model.Metrics
		if err := decodeStrictJSON(trimmed, &metrics); err != nil || metrics.ConnectionsMax == 0 || metrics.RedoCapacityBytes == 0 {
			return fmt.Errorf("missing engine evidence: max_connections=%d redo=%d err=%v", metrics.ConnectionsMax, metrics.RedoCapacityBytes, err)
		}
	case "coverage":
		var coverage model.Instrumentation
		if err := decodeStrictJSON(trimmed, &coverage); err != nil || coverage.DigestCapacity == 0 {
			return fmt.Errorf("missing instrumentation evidence: capacity=%d err=%v", coverage.DigestCapacity, err)
		}
	case "variables":
		var variables map[string]string
		if err := decodeStrictJSON(trimmed, &variables); err != nil || !strings.EqualFold(variables["performance_schema"], "ON") {
			return fmt.Errorf("missing variables evidence: performance_schema=%q err=%v", variables["performance_schema"], err)
		}
	case "replication":
		if bytes.Equal(trimmed, []byte("null")) {
			return nil
		}
		var replication model.Replication
		if err := decodeStrictJSON(trimmed, &replication); err != nil || replication.SourceHost == "" {
			return fmt.Errorf("invalid replication evidence: source=%q err=%v", replication.SourceHost, err)
		}
	default:
		return fmt.Errorf("unknown focused section %q", section)
	}
	return nil
}

func decodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
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
	if !m.SecretFree || len(m.Files) != 24 {
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
