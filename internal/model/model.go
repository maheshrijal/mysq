package model

import "time"

const SchemaVersion = "1.5.0"

type Context struct {
	SchemaVersion    string            `json:"schema_version"`
	ToolVersion      string            `json:"tool_version"`
	CollectedAt      time.Time         `json:"collected_at"`
	IntervalMillis   int64             `json:"interval_ms" jsonschema_description:"Backward-compatible primary sample interval: global_status for full and engine inspections, or the requested family for a focused sampled command."`
	SampleIntervals  SampleIntervals   `json:"sample_intervals_ms" jsonschema_description:"Observed endpoint-to-endpoint duration for each sampled counter family, in milliseconds."`
	Fingerprint      string            `json:"fingerprint"`
	Server           Server            `json:"server"`
	BlockingChains   []BlockingChain   `json:"blocking_chains,omitempty"`
	Health           Health            `json:"health"`
	Metrics          Metrics           `json:"metrics"`
	Findings         []Finding         `json:"findings"`
	Queries          []Query           `json:"queries"`
	Tables           []Table           `json:"tables"`
	Indexes          []Index           `json:"indexes"`
	Processes        []Process         `json:"processes"`
	ConnectionGroups []ConnectionGroup `json:"connection_groups"`
	Locks            []LockWait        `json:"locks"`
	Transactions     []Transaction     `json:"transactions"`
	MetadataLocks    []MetadataLock    `json:"metadata_locks"`
	WaitEvents       []WaitEvent       `json:"wait_events"`
	FileIO           []FileIO          `json:"file_io"`
	ServerErrors     []ServerError     `json:"server_errors"`
	MemoryConsumers  []MemoryConsumer  `json:"memory_consumers"`
	StatementSamples []StatementSample `json:"statement_samples"`
	StatementLatency StatementLatency  `json:"statement_latency"`
	Instrumentation  Instrumentation   `json:"instrumentation"`
	Replication      *Replication      `json:"replication,omitempty"`
	Variables        map[string]string `json:"variables"`
	GlobalStatus     map[string]string `json:"global_status"`
	InnoDBStatus     string            `json:"innodb_status,omitempty"`
	Capabilities     []Capability      `json:"capabilities"`
	Warnings         []string          `json:"collection_warnings,omitempty"`
}

type SampleIntervals struct {
	GlobalStatus      int64 `json:"global_status"`
	WaitEvents        int64 `json:"wait_events"`
	FileIO            int64 `json:"file_io"`
	ServerErrors      int64 `json:"server_errors"`
	StatementDigests  int64 `json:"statement_digests"`
	StatementCounters int64 `json:"statement_counters"`
}

type Server struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Database          string `json:"database"`
	Version           string `json:"version"`
	Flavor            string `json:"flavor"`
	Hostname          string `json:"hostname"`
	UUID              string `json:"uuid"`
	ServerID          uint64 `json:"server_id"`
	ReadOnly          bool   `json:"read_only"`
	SuperReadOnly     bool   `json:"super_read_only"`
	PerformanceSchema bool   `json:"performance_schema"`
	UptimeSeconds     uint64 `json:"uptime_seconds"`
}

type Health struct {
	Score      int               `json:"score"`
	Critical   int               `json:"critical"`
	Warnings   int               `json:"warnings"`
	Notes      int               `json:"notes"`
	Healthy    int               `json:"healthy"`
	Unknown    int               `json:"unknown"`
	Subsystems []SubsystemHealth `json:"subsystems,omitempty"`
}

type Metrics struct {
	QueriesPerSecond        float64 `json:"queries_per_second"`
	TransactionsPerSecond   float64 `json:"transactions_per_second"`
	RowsReadPerSecond       float64 `json:"rows_read_per_second"`
	RowsWrittenPerSecond    float64 `json:"rows_written_per_second"`
	SlowQueriesPerSecond    float64 `json:"slow_queries_per_second"`
	AbortedConnectsPerSec   float64 `json:"aborted_connects_per_second"`
	RowLockWaitsPerSecond   float64 `json:"row_lock_waits_per_second"`
	RedoWaitsPerSecond      float64 `json:"redo_waits_per_second"`
	ConnectionsCurrent      uint64  `json:"connections_current"`
	ConnectionsMax          uint64  `json:"connections_max"`
	ConnectionsUsedPercent  float64 `json:"connections_used_percent"`
	ThreadsRunning          uint64  `json:"threads_running"`
	BufferPoolHitPercent    float64 `json:"buffer_pool_hit_percent"`
	BufferPoolUsedPercent   float64 `json:"buffer_pool_used_percent"`
	BufferPoolDirtyPercent  float64 `json:"buffer_pool_dirty_percent"`
	TempDiskTablePercent    float64 `json:"temp_disk_table_percent"`
	TableCacheHitPercent    float64 `json:"table_cache_hit_percent"`
	OpenFilesUsedPercent    float64 `json:"open_files_used_percent"`
	HistoryListLength       uint64  `json:"history_list_length"`
	DataReadsPerSecond      float64 `json:"data_reads_per_second"`
	DataWritesPerSecond     float64 `json:"data_writes_per_second"`
	DataFsyncsPerSecond     float64 `json:"data_fsyncs_per_second"`
	RedoBytesPerSecond      float64 `json:"redo_bytes_per_second"`
	RedoWritesPerSecond     float64 `json:"redo_writes_per_second"`
	RedoFsyncsPerSecond     float64 `json:"redo_fsyncs_per_second"`
	NetworkInBytesPerSec    float64 `json:"network_in_bytes_per_second"`
	NetworkOutBytesPerSec   float64 `json:"network_out_bytes_per_second"`
	FullScansPerSecond      float64 `json:"full_scans_per_second"`
	SortMergePassesPerSec   float64 `json:"sort_merge_passes_per_second"`
	BufferPoolWaitsPerSec   float64 `json:"buffer_pool_waits_per_second"`
	PendingReads            uint64  `json:"pending_reads"`
	PendingWrites           uint64  `json:"pending_writes"`
	PendingFsyncs           uint64  `json:"pending_fsyncs"`
	BufferPoolDataBytes     uint64  `json:"buffer_pool_data_bytes"`
	BufferPoolDirtyBytes    uint64  `json:"buffer_pool_dirty_bytes"`
	RedoCurrentLSN          uint64  `json:"redo_current_lsn"`
	RedoFlushedLSN          uint64  `json:"redo_flushed_lsn"`
	RedoCheckpointLSN       uint64  `json:"redo_checkpoint_lsn"`
	RedoCheckpointAgeBytes  uint64  `json:"redo_checkpoint_age_bytes"`
	RedoCapacityBytes       uint64  `json:"redo_capacity_bytes"`
	RedoCheckpointAgePct    float64 `json:"redo_checkpoint_age_percent"`
	StatementErrorsPerSec   float64 `json:"statement_errors_per_second"`
	StatementWarningsPerSec float64 `json:"statement_warnings_per_second"`
	DeadlocksPerSecond      float64 `json:"deadlocks_per_second"`
	LockTimeoutsPerSecond   float64 `json:"lock_timeouts_per_second"`
	ThreadsCreatedPerSecond float64 `json:"threads_created_per_second"`
}

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityNote     Severity = "note"
)

type Finding struct {
	ID             string         `json:"id"`
	Severity       Severity       `json:"severity"`
	Subsystem      string         `json:"subsystem"`
	Title          string         `json:"title"`
	Summary        string         `json:"summary"`
	Recommendation string         `json:"recommendation"`
	Objects        []string       `json:"objects,omitempty"`
	Evidence       map[string]any `json:"evidence,omitempty"`
}

type Query struct {
	Digest             string   `json:"digest"`
	Schema             string   `json:"schema"`
	Statement          string   `json:"statement"`
	Calls              uint64   `json:"calls"`
	TotalLatencyMillis float64  `json:"total_latency_ms"`
	MeanLatencyMillis  float64  `json:"mean_latency_ms"`
	MaxLatencyMillis   float64  `json:"max_latency_ms"`
	P95LatencyMillis   float64  `json:"p95_latency_ms"`
	P99LatencyMillis   float64  `json:"p99_latency_ms"`
	P999LatencyMillis  float64  `json:"p999_latency_ms"`
	RowsExamined       uint64   `json:"rows_examined"`
	RowsSent           uint64   `json:"rows_sent"`
	RowsAffected       uint64   `json:"rows_affected"`
	Errors             uint64   `json:"errors"`
	Warnings           uint64   `json:"warnings"`
	FullScans          uint64   `json:"full_scans"`
	NoIndexUsed        uint64   `json:"no_index_used"`
	TmpTables          uint64   `json:"tmp_tables"`
	TmpDiskTables      uint64   `json:"tmp_disk_tables"`
	FirstSeen          string   `json:"first_seen,omitempty"`
	LastSeen           string   `json:"last_seen,omitempty"`
	ActiveUsers        []string `json:"active_users,omitempty"`
}

type Table struct {
	Schema             string  `json:"schema"`
	Name               string  `json:"name"`
	Engine             string  `json:"engine"`
	EstimatedRows      uint64  `json:"estimated_rows"`
	DataBytes          uint64  `json:"data_bytes"`
	IndexBytes         uint64  `json:"index_bytes"`
	TotalBytes         uint64  `json:"total_bytes"`
	Reads              uint64  `json:"reads"`
	Writes             uint64  `json:"writes"`
	ReadLatencyMillis  float64 `json:"read_latency_ms"`
	WriteLatencyMillis float64 `json:"write_latency_ms"`
	Fetches            uint64  `json:"fetches"`
	Inserts            uint64  `json:"inserts"`
	Updates            uint64  `json:"updates"`
	Deletes            uint64  `json:"deletes"`
	HasPrimaryKey      bool    `json:"has_primary_key"`
	AutoIncrement      uint64  `json:"auto_increment,omitempty"`
	AutoIncrementMax   uint64  `json:"auto_increment_max,omitempty"`
}

type Index struct {
	Schema             string  `json:"schema"`
	Table              string  `json:"table"`
	Name               string  `json:"name"`
	Columns            string  `json:"columns"`
	Unique             bool    `json:"unique"`
	Visible            bool    `json:"visible"`
	Cardinality        uint64  `json:"cardinality"`
	Reads              uint64  `json:"reads"`
	Writes             uint64  `json:"writes"`
	ReadLatencyMillis  float64 `json:"read_latency_ms"`
	WriteLatencyMillis float64 `json:"write_latency_ms"`
}

type Process struct {
	ID                     uint64  `json:"id"`
	ThreadID               uint64  `json:"thread_id"`
	User                   string  `json:"user"`
	Host                   string  `json:"host"`
	Database               string  `json:"database,omitempty"`
	Command                string  `json:"command"`
	Seconds                uint64  `json:"seconds"`
	State                  string  `json:"state,omitempty"`
	Digest                 string  `json:"digest,omitempty"`
	WaitEvent              string  `json:"wait_event,omitempty"`
	StatementLatencyMillis float64 `json:"statement_latency_ms"`
	Statement              string  `json:"statement,omitempty"`
}

type ConnectionGroup struct {
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Total    int    `json:"total"`
	Active   int    `json:"active"`
	Sleeping int    `json:"sleeping"`
	Other    int    `json:"other"`
}

type LockWait struct {
	WaitingTransaction  string `json:"waiting_transaction"`
	BlockingTransaction string `json:"blocking_transaction"`
	Schema              string `json:"schema,omitempty"`
	Table               string `json:"table,omitempty"`
	Index               string `json:"index,omitempty"`
	LockType            string `json:"lock_type,omitempty"`
	LockMode            string `json:"lock_mode,omitempty"`
}

type Transaction struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	StartedAt    string `json:"started_at,omitempty"`
	AgeSeconds   uint64 `json:"age_seconds"`
	ProcessID    uint64 `json:"process_id"`
	User         string `json:"user,omitempty"`
	Host         string `json:"host,omitempty"`
	RowsLocked   uint64 `json:"rows_locked"`
	RowsModified uint64 `json:"rows_modified"`
	TablesInUse  uint64 `json:"tables_in_use"`
	TablesLocked uint64 `json:"tables_locked"`
	Statement    string `json:"statement,omitempty"`
}

type MetadataLock struct {
	ThreadID   uint64 `json:"thread_id"`
	ProcessID  uint64 `json:"process_id"`
	User       string `json:"user,omitempty"`
	Host       string `json:"host,omitempty"`
	ObjectType string `json:"object_type"`
	Schema     string `json:"schema,omitempty"`
	Object     string `json:"object,omitempty"`
	LockType   string `json:"lock_type"`
	Duration   string `json:"duration"`
	Status     string `json:"status"`
}

type WaitEvent struct {
	Name                string  `json:"name"`
	Class               string  `json:"class"`
	Count               uint64  `json:"count"`
	TotalLatencyMillis  float64 `json:"total_latency_ms"`
	MeanLatencyMicros   float64 `json:"mean_latency_us"`
	MaxLatencyMillis    float64 `json:"max_latency_ms"`
	SampleCount         uint64  `json:"sample_count"`
	SampleLatencyMillis float64 `json:"sample_latency_ms"`
	EventsPerSecond     float64 `json:"events_per_second"`
	WaitMillisPerSecond float64 `json:"wait_ms_per_second"`
	SampleSharePercent  float64 `json:"sample_share_percent"`
}

type FileIO struct {
	Name                    string  `json:"name"`
	Class                   string  `json:"class"`
	Reads                   uint64  `json:"reads"`
	Writes                  uint64  `json:"writes"`
	BytesRead               uint64  `json:"bytes_read"`
	BytesWritten            uint64  `json:"bytes_written"`
	TotalReadLatencyMillis  float64 `json:"total_read_latency_ms"`
	TotalWriteLatencyMillis float64 `json:"total_write_latency_ms"`
	ReadsPerSecond          float64 `json:"reads_per_second"`
	WritesPerSecond         float64 `json:"writes_per_second"`
	ReadBytesPerSecond      float64 `json:"read_bytes_per_second"`
	WriteBytesPerSecond     float64 `json:"write_bytes_per_second"`
	MeanReadLatencyMillis   float64 `json:"mean_read_latency_ms"`
	MeanWriteLatencyMillis  float64 `json:"mean_write_latency_ms"`
	WaitMillisPerSecond     float64 `json:"wait_ms_per_second"`
}

type ServerError struct {
	Number          uint64  `json:"number"`
	Name            string  `json:"name"`
	SQLState        string  `json:"sql_state"`
	Raised          uint64  `json:"raised"`
	Handled         uint64  `json:"handled"`
	FirstSeen       string  `json:"first_seen,omitempty"`
	LastSeen        string  `json:"last_seen,omitempty"`
	SampleRaised    uint64  `json:"sample_raised"`
	RaisedPerSecond float64 `json:"raised_per_second"`
}

type StatementLatency struct {
	P95Millis  float64 `json:"p95_ms"`
	P99Millis  float64 `json:"p99_ms"`
	P999Millis float64 `json:"p999_ms"`
	MaxMillis  float64 `json:"max_ms"`
}

type StatementSample struct {
	Digest                      string  `json:"digest"`
	Schema                      string  `json:"schema"`
	Statement                   string  `json:"statement"`
	Calls                       uint64  `json:"calls"`
	CallsPerSecond              float64 `json:"calls_per_second"`
	DatabaseTimeMillis          float64 `json:"database_time_ms"`
	DatabaseTimeMillisPerSecond float64 `json:"database_time_ms_per_second"`
	DatabaseTimeSharePercent    float64 `json:"database_time_share_percent"`
}

type Instrumentation struct {
	DigestRows               uint64            `json:"digest_rows"`
	DigestCapacity           uint64            `json:"digest_capacity"`
	DigestUtilizationPercent float64           `json:"digest_utilization_percent"`
	TotalLost                uint64            `json:"total_lost"`
	Lost                     map[string]uint64 `json:"lost,omitempty"`
	DisabledConsumers        []string          `json:"disabled_consumers,omitempty"`
}

type MemoryConsumer struct {
	Name         string `json:"name"`
	CurrentBytes uint64 `json:"current_bytes"`
	HighBytes    uint64 `json:"high_bytes"`
	Allocations  uint64 `json:"allocations"`
}

type Replication struct {
	Channel               string              `json:"channel,omitempty"`
	SourceHost            string              `json:"source_host,omitempty"`
	SourcePort            uint64              `json:"source_port,omitempty"`
	IORunning             string              `json:"io_running,omitempty"`
	SQLRunning            string              `json:"sql_running,omitempty"`
	SecondsBehind         *int64              `json:"seconds_behind,omitempty"`
	LastIOError           string              `json:"last_io_error,omitempty"`
	LastSQLError          string              `json:"last_sql_error,omitempty"`
	RetrievedGTIDSet      string              `json:"retrieved_gtid_set,omitempty"`
	ExecutedGTIDSet       string              `json:"executed_gtid_set,omitempty"`
	ApplierState          string              `json:"applier_state,omitempty"`
	RemainingDelaySeconds *int64              `json:"remaining_delay_seconds,omitempty"`
	TransactionRetries    uint64              `json:"transaction_retries"`
	Workers               []ReplicationWorker `json:"workers,omitempty"`
}

type ReplicationWorker struct {
	Channel                    string `json:"channel,omitempty"`
	WorkerID                   uint64 `json:"worker_id"`
	ThreadID                   uint64 `json:"thread_id"`
	ServiceState               string `json:"service_state"`
	LastErrorNumber            uint64 `json:"last_error_number"`
	LastErrorMessage           string `json:"last_error_message,omitempty"`
	LastErrorTimestamp         string `json:"last_error_timestamp,omitempty"`
	ApplyingTransaction        string `json:"applying_transaction,omitempty"`
	LastAppliedTransaction     string `json:"last_applied_transaction,omitempty"`
	ApplyingTransactionRetries uint64 `json:"applying_transaction_retries"`
}

type Capability struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
