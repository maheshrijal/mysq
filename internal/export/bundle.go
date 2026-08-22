package export

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
	"github.com/maheshrijal/mysq/internal/render"
	contract "github.com/maheshrijal/mysq/schema"
)

type Options struct {
	Zip bool
}

type Result struct {
	Directory string
	Archive   string
	Files     []string
}

type manifest struct {
	BundleVersion string         `json:"bundle_version"`
	SchemaVersion string         `json:"schema_version"`
	ToolVersion   string         `json:"tool_version"`
	CreatedAt     time.Time      `json:"created_at"`
	Snapshot      string         `json:"snapshot"`
	SecretFree    bool           `json:"secret_free"`
	Files         []manifestFile `json:"files"`
}

type manifestFile struct {
	Path        string `json:"path"`
	MediaType   string `json:"media_type"`
	SHA256      string `json:"sha256"`
	Description string `json:"description"`
}

type artifact struct {
	name, mediaType, description string
	data                         []byte
}

func Write(ctx *model.Context, output string, options Options) (Result, error) {
	if strings.TrimSpace(output) == "" {
		output = fmt.Sprintf("mysq-export-%s", ctx.CollectedAt.Local().Format("20060102-150405"))
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return Result{}, fmt.Errorf("resolve export path: %w", err)
	}
	if _, err := os.Stat(abs); err == nil {
		return Result{}, fmt.Errorf("export path already exists: %s", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Result{}, fmt.Errorf("create export parent: %w", err)
	}
	temp, err := os.MkdirTemp(parent, ".mysq-export-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary export: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temp)
		}
	}()

	artifacts, err := buildArtifacts(ctx)
	if err != nil {
		return Result{}, err
	}
	m := manifest{
		BundleVersion: "1.0.0", SchemaVersion: ctx.SchemaVersion, ToolVersion: ctx.ToolVersion,
		CreatedAt: time.Now().UTC(), Snapshot: ctx.Fingerprint, SecretFree: true,
	}
	result := Result{Directory: abs}
	for _, item := range artifacts {
		path := filepath.Join(temp, filepath.FromSlash(item.name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(path, item.data, 0o644); err != nil {
			return Result{}, fmt.Errorf("write %s: %w", item.name, err)
		}
		sum := sha256.Sum256(item.data)
		m.Files = append(m.Files, manifestFile{
			Path: item.name, MediaType: item.mediaType, SHA256: hex.EncodeToString(sum[:]), Description: item.description,
		})
		result.Files = append(result.Files, item.name)
	}
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Result{}, err
	}
	manifestJSON = append(manifestJSON, '\n')
	if err := os.WriteFile(filepath.Join(temp, "manifest.json"), manifestJSON, 0o644); err != nil {
		return Result{}, fmt.Errorf("write manifest: %w", err)
	}
	result.Files = append(result.Files, "manifest.json")
	if err := os.Rename(temp, abs); err != nil {
		return Result{}, fmt.Errorf("commit export: %w", err)
	}
	committed = true
	sort.Strings(result.Files)

	if options.Zip {
		result.Archive = abs + ".zip"
		if err := zipDirectory(abs, result.Archive); err != nil {
			return result, err
		}
	}
	return result, nil
}

func buildArtifacts(ctx *model.Context) ([]artifact, error) {
	jsonFile := func(name, description string, value any) (artifact, error) {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return artifact{}, fmt.Errorf("encode %s: %w", name, err)
		}
		return artifact{name: name, mediaType: "application/json", description: description, data: append(data, '\n')}, nil
	}

	items := make([]artifact, 0, 14)
	for _, spec := range []struct {
		name, description string
		value             any
	}{
		{"context.json", "Complete versioned diagnostic context for agents and programs", ctx},
		{"findings.json", "Deterministic findings and supporting evidence", ctx.Findings},
		{"metrics.json", "Derived interval metrics", ctx.Metrics},
		{"raw/instrumentation.json", "Performance Schema coverage and lost-event counters", ctx.Instrumentation},
		{"raw/global-status.json", "SHOW GLOBAL STATUS values at the end of the sample", ctx.GlobalStatus},
		{"raw/capabilities.json", "Probe availability and degraded-coverage reasons", ctx.Capabilities},
	} {
		item, err := jsonFile(spec.name, spec.description, spec.value)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	var markdown bytes.Buffer
	if err := render.Markdown(&markdown, ctx); err != nil {
		return nil, err
	}
	items = append(items, artifact{name: "summary.md", mediaType: "text/markdown", description: "Human and agent-readable findings-first report", data: markdown.Bytes()})
	items = append(items, artifact{name: "README.md", mediaType: "text/markdown", description: "Bundle contract and safe-use notes", data: []byte(bundleReadme(ctx))})
	items = append(items, artifact{name: "schema/context-1.2.0.json", mediaType: "application/schema+json", description: "JSON Schema for context.json", data: contract.ContextLatest()})
	items = append(items, artifact{name: "raw/innodb-status.txt", mediaType: "text/plain", description: "Redacted SHOW ENGINE INNODB STATUS output", data: []byte(ctx.InnoDBStatus)})
	items = append(items, artifact{name: "variables.cnf", mediaType: "text/plain", description: "Sorted server variables in option-file syntax", data: variablesFile(ctx.Variables)})

	csvArtifacts, err := csvFiles(ctx)
	if err != nil {
		return nil, err
	}
	items = append(items, csvArtifacts...)
	return items, nil
}

func csvFiles(ctx *model.Context) ([]artifact, error) {
	type csvSpec struct {
		name, description string
		header            []string
		rows              [][]string
	}
	queries := make([][]string, 0, len(ctx.Queries))
	for _, item := range ctx.Queries {
		queries = append(queries, []string{item.Digest, item.Schema, item.Statement, strconv.FormatUint(item.Calls, 10),
			formatFloat(item.TotalLatencyMillis), formatFloat(item.MeanLatencyMillis), formatFloat(item.MaxLatencyMillis),
			formatFloat(item.P95LatencyMillis), formatFloat(item.P99LatencyMillis), formatFloat(item.P999LatencyMillis),
			strconv.FormatUint(item.RowsExamined, 10), strconv.FormatUint(item.RowsSent, 10), strconv.FormatUint(item.RowsAffected, 10),
			strconv.FormatUint(item.Errors, 10), strconv.FormatUint(item.Warnings, 10), strconv.FormatUint(item.NoIndexUsed, 10),
			strconv.FormatUint(item.FullScans, 10), strconv.FormatUint(item.TmpTables, 10),
			strconv.FormatUint(item.TmpDiskTables, 10), item.FirstSeen, item.LastSeen, strings.Join(item.ActiveUsers, ",")})
	}
	tables := make([][]string, 0, len(ctx.Tables))
	for _, item := range ctx.Tables {
		tables = append(tables, []string{item.Schema, item.Name, item.Engine, strconv.FormatUint(item.EstimatedRows, 10),
			strconv.FormatUint(item.DataBytes, 10), strconv.FormatUint(item.IndexBytes, 10), strconv.FormatUint(item.TotalBytes, 10),
			strconv.FormatUint(item.Reads, 10), strconv.FormatUint(item.Writes, 10), formatFloat(item.ReadLatencyMillis),
			formatFloat(item.WriteLatencyMillis), strconv.FormatBool(item.HasPrimaryKey)})
	}
	indexes := make([][]string, 0, len(ctx.Indexes))
	for _, item := range ctx.Indexes {
		indexes = append(indexes, []string{item.Schema, item.Table, item.Name, item.Columns, strconv.FormatBool(item.Unique),
			strconv.FormatBool(item.Visible), strconv.FormatUint(item.Cardinality, 10), strconv.FormatUint(item.Reads, 10),
			strconv.FormatUint(item.Writes, 10), formatFloat(item.ReadLatencyMillis), formatFloat(item.WriteLatencyMillis)})
	}
	processes := make([][]string, 0, len(ctx.Processes))
	for _, item := range ctx.Processes {
		processes = append(processes, []string{strconv.FormatUint(item.ID, 10), strconv.FormatUint(item.ThreadID, 10), item.User, item.Host, item.Database,
			item.Command, strconv.FormatUint(item.Seconds, 10), item.State, item.Digest, item.WaitEvent,
			formatFloat(item.StatementLatencyMillis), item.Statement})
	}
	connectionGroups := make([][]string, 0, len(ctx.ConnectionGroups))
	for _, item := range ctx.ConnectionGroups {
		connectionGroups = append(connectionGroups, []string{item.Kind, item.Key, strconv.Itoa(item.Total), strconv.Itoa(item.Active), strconv.Itoa(item.Sleeping), strconv.Itoa(item.Other)})
	}
	locks := make([][]string, 0, len(ctx.Locks))
	for _, item := range ctx.Locks {
		locks = append(locks, []string{item.WaitingTransaction, item.BlockingTransaction, item.Schema, item.Table, item.Index, item.LockType, item.LockMode})
	}
	transactions := make([][]string, 0, len(ctx.Transactions))
	for _, item := range ctx.Transactions {
		transactions = append(transactions, []string{item.ID, item.State, item.StartedAt, strconv.FormatUint(item.AgeSeconds, 10),
			strconv.FormatUint(item.ProcessID, 10), item.User, item.Host, strconv.FormatUint(item.RowsLocked, 10),
			strconv.FormatUint(item.RowsModified, 10), strconv.FormatUint(item.TablesInUse, 10), strconv.FormatUint(item.TablesLocked, 10), item.Statement})
	}
	metadataLocks := make([][]string, 0, len(ctx.MetadataLocks))
	for _, item := range ctx.MetadataLocks {
		metadataLocks = append(metadataLocks, []string{strconv.FormatUint(item.ThreadID, 10), strconv.FormatUint(item.ProcessID, 10),
			item.User, item.Host, item.ObjectType, item.Schema, item.Object, item.LockType, item.Duration, item.Status})
	}
	waits := make([][]string, 0, len(ctx.WaitEvents))
	for _, item := range ctx.WaitEvents {
		waits = append(waits, []string{item.Name, item.Class, strconv.FormatUint(item.Count, 10), formatFloat(item.TotalLatencyMillis),
			formatFloat(item.MeanLatencyMicros), formatFloat(item.MaxLatencyMillis), strconv.FormatUint(item.SampleCount, 10),
			formatFloat(item.SampleLatencyMillis), formatFloat(item.EventsPerSecond), formatFloat(item.WaitMillisPerSecond),
			formatFloat(item.SampleSharePercent)})
	}
	fileIO := make([][]string, 0, len(ctx.FileIO))
	for _, item := range ctx.FileIO {
		fileIO = append(fileIO, []string{item.Name, item.Class, strconv.FormatUint(item.Reads, 10), strconv.FormatUint(item.Writes, 10),
			strconv.FormatUint(item.BytesRead, 10), strconv.FormatUint(item.BytesWritten, 10), formatFloat(item.TotalReadLatencyMillis),
			formatFloat(item.TotalWriteLatencyMillis), formatFloat(item.ReadsPerSecond), formatFloat(item.WritesPerSecond),
			formatFloat(item.ReadBytesPerSecond), formatFloat(item.WriteBytesPerSecond), formatFloat(item.MeanReadLatencyMillis),
			formatFloat(item.MeanWriteLatencyMillis), formatFloat(item.WaitMillisPerSecond)})
	}
	errors := make([][]string, 0, len(ctx.ServerErrors))
	for _, item := range ctx.ServerErrors {
		errors = append(errors, []string{strconv.FormatUint(item.Number, 10), item.Name, item.SQLState, strconv.FormatUint(item.Raised, 10),
			strconv.FormatUint(item.Handled, 10), item.FirstSeen, item.LastSeen, strconv.FormatUint(item.SampleRaised, 10), formatFloat(item.RaisedPerSecond)})
	}
	memory := make([][]string, 0, len(ctx.MemoryConsumers))
	for _, item := range ctx.MemoryConsumers {
		memory = append(memory, []string{item.Name, strconv.FormatUint(item.CurrentBytes, 10), strconv.FormatUint(item.HighBytes, 10), strconv.FormatUint(item.Allocations, 10)})
	}

	specs := []csvSpec{
		{"queries.csv", "Normalized statement digest statistics", []string{"digest", "schema", "statement", "calls", "total_latency_ms", "mean_latency_ms", "max_latency_ms", "p95_latency_ms", "p99_latency_ms", "p999_latency_ms", "rows_examined", "rows_sent", "rows_affected", "errors", "warnings", "no_index_used", "full_scans", "tmp_tables", "tmp_disk_tables", "first_seen", "last_seen", "active_users"}, queries},
		{"tables.csv", "Table size, I/O count, and latency statistics", []string{"schema", "table", "engine", "estimated_rows", "data_bytes", "index_bytes", "total_bytes", "reads", "writes", "read_latency_ms", "write_latency_ms", "has_primary_key"}, tables},
		{"indexes.csv", "Index definitions, usage, and latency counters", []string{"schema", "table", "index", "columns", "unique", "visible", "cardinality", "reads", "writes", "read_latency_ms", "write_latency_ms"}, indexes},
		{"processes.csv", "Redacted active-session snapshot", []string{"id", "thread_id", "user", "host", "database", "command", "seconds", "state", "digest", "wait_event", "statement_latency_ms", "statement"}, processes},
		{"connections.csv", "Connection counts grouped by user, host, and user-host pair", []string{"kind", "key", "total", "active", "sleeping", "other"}, connectionGroups},
		{"locks.csv", "Active InnoDB row lock waits", []string{"waiting_transaction", "blocking_transaction", "schema", "table", "index", "lock_type", "lock_mode"}, locks},
		{"transactions.csv", "Active InnoDB transaction snapshot", []string{"transaction", "state", "started_at", "age_seconds", "process_id", "user", "host", "rows_locked", "rows_modified", "tables_in_use", "tables_locked", "statement"}, transactions},
		{"metadata-locks.csv", "Active and pending metadata locks", []string{"thread_id", "process_id", "user", "host", "object_type", "schema", "object", "lock_type", "duration", "status"}, metadataLocks},
		{"wait-events.csv", "Sampled and cumulative Performance Schema wait events", []string{"event", "class", "count", "total_latency_ms", "mean_latency_us", "max_latency_ms", "sample_count", "sample_latency_ms", "events_per_second", "wait_ms_per_second", "sample_share_percent"}, waits},
		{"file-io.csv", "Sampled and cumulative MySQL file I/O", []string{"event", "class", "reads", "writes", "bytes_read", "bytes_written", "total_read_latency_ms", "total_write_latency_ms", "reads_per_second", "writes_per_second", "read_bytes_per_second", "write_bytes_per_second", "mean_read_latency_ms", "mean_write_latency_ms", "wait_ms_per_second"}, fileIO},
		{"server-errors.csv", "Sampled and cumulative MySQL errors and warnings", []string{"number", "name", "sql_state", "raised", "handled", "first_seen", "last_seen", "sample_raised", "raised_per_second"}, errors},
		{"memory-consumers.csv", "Top MySQL memory consumers", []string{"consumer", "current_bytes", "high_bytes", "allocations"}, memory},
	}
	result := make([]artifact, 0, len(specs))
	for _, spec := range specs {
		var buffer bytes.Buffer
		writer := csv.NewWriter(&buffer)
		if err := writer.Write(spec.header); err != nil {
			return nil, err
		}
		if err := writer.WriteAll(spec.rows); err != nil {
			return nil, err
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return nil, err
		}
		result = append(result, artifact{name: spec.name, mediaType: "text/csv", description: spec.description, data: buffer.Bytes()})
	}
	return result, nil
}

func variablesFile(values map[string]string) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteString("# Captured by mysq. This is evidence, not an apply-ready recommendation.\n[mysqld]\n")
	for _, key := range keys {
		fmt.Fprintf(&out, "%s=%s\n", key, strings.ReplaceAll(values[key], "\n", " "))
	}
	return []byte(out.String())
}

func bundleReadme(ctx *model.Context) string {
	return fmt.Sprintf(`# mysq agent bundle

This bundle is an immutable, point-in-time MySQL diagnostic snapshot (`+"`%s`"+`). Start with `+"`summary.md`"+` for the findings, then use `+"`context.json`"+` when exact structured evidence is needed.

The JSON contract is versioned as `+"`%s`"+`. Statement text and SQL found in process/InnoDB output were normalized before the snapshot was created; connection credentials are never stored. `+"`manifest.json`"+` contains a SHA-256 digest for every artifact.

Counter conclusions are scoped to this server's uptime and the sample interval. Zero index reads are review evidence, never proof that an index is safe to drop. Table row counts are InnoDB estimates. Missing probes are explicit in `+"`raw/capabilities.json`"+` and `+"`context.json.collection_warnings`"+`.
`, ctx.Fingerprint, ctx.SchemaVersion)
}

func formatFloat(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

func zipDirectory(directory, archive string) error {
	file, err := os.OpenFile(archive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create zip archive: %w", err)
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(archive)
		}
	}()
	writer := zip.NewWriter(file)
	err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Method = zip.Deflate
		destination, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(destination, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		_ = writer.Close()
		return fmt.Errorf("archive export: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish zip archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close zip archive: %w", err)
	}
	failed = false
	return nil
}
