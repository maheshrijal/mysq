package model

import "time"

const SchemaVersion = "1.0.0"

type Context struct {
	SchemaVersion    string            `json:"schema_version"`
	ToolVersion      string            `json:"tool_version"`
	CollectedAt      time.Time         `json:"collected_at"`
	IntervalMillis   int64             `json:"interval_ms"`
	Fingerprint      string            `json:"fingerprint"`
	Server           Server            `json:"server"`
	Health           Health            `json:"health"`
	Metrics          Metrics           `json:"metrics"`
	Findings         []Finding         `json:"findings"`
	Queries          []Query           `json:"queries"`
	Tables           []Table           `json:"tables"`
	Indexes          []Index           `json:"indexes"`
	Processes        []Process         `json:"processes"`
	ConnectionGroups []ConnectionGroup `json:"connection_groups"`
	Locks            []LockWait        `json:"locks"`
	Replication      *Replication      `json:"replication,omitempty"`
	Variables        map[string]string `json:"variables"`
	GlobalStatus     map[string]string `json:"global_status"`
	InnoDBStatus     string            `json:"innodb_status,omitempty"`
	Capabilities     []Capability      `json:"capabilities"`
	Warnings         []string          `json:"collection_warnings,omitempty"`
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
	Score    int `json:"score"`
	Critical int `json:"critical"`
	Warnings int `json:"warnings"`
	Notes    int `json:"notes"`
	Healthy  int `json:"healthy"`
}

type Metrics struct {
	QueriesPerSecond       float64 `json:"queries_per_second"`
	TransactionsPerSecond  float64 `json:"transactions_per_second"`
	RowsReadPerSecond      float64 `json:"rows_read_per_second"`
	RowsWrittenPerSecond   float64 `json:"rows_written_per_second"`
	SlowQueriesPerSecond   float64 `json:"slow_queries_per_second"`
	AbortedConnectsPerSec  float64 `json:"aborted_connects_per_second"`
	RowLockWaitsPerSecond  float64 `json:"row_lock_waits_per_second"`
	RedoWaitsPerSecond     float64 `json:"redo_waits_per_second"`
	ConnectionsCurrent     uint64  `json:"connections_current"`
	ConnectionsMax         uint64  `json:"connections_max"`
	ConnectionsUsedPercent float64 `json:"connections_used_percent"`
	ThreadsRunning         uint64  `json:"threads_running"`
	BufferPoolHitPercent   float64 `json:"buffer_pool_hit_percent"`
	BufferPoolUsedPercent  float64 `json:"buffer_pool_used_percent"`
	BufferPoolDirtyPercent float64 `json:"buffer_pool_dirty_percent"`
	TempDiskTablePercent   float64 `json:"temp_disk_table_percent"`
	TableCacheHitPercent   float64 `json:"table_cache_hit_percent"`
	OpenFilesUsedPercent   float64 `json:"open_files_used_percent"`
	HistoryListLength      uint64  `json:"history_list_length"`
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
	Digest             string  `json:"digest"`
	Schema             string  `json:"schema"`
	Statement          string  `json:"statement"`
	Calls              uint64  `json:"calls"`
	TotalLatencyMillis float64 `json:"total_latency_ms"`
	MeanLatencyMillis  float64 `json:"mean_latency_ms"`
	RowsExamined       uint64  `json:"rows_examined"`
	RowsSent           uint64  `json:"rows_sent"`
	NoIndexUsed        uint64  `json:"no_index_used"`
	TmpDiskTables      uint64  `json:"tmp_disk_tables"`
}

type Table struct {
	Schema           string `json:"schema"`
	Name             string `json:"name"`
	Engine           string `json:"engine"`
	EstimatedRows    uint64 `json:"estimated_rows"`
	DataBytes        uint64 `json:"data_bytes"`
	IndexBytes       uint64 `json:"index_bytes"`
	TotalBytes       uint64 `json:"total_bytes"`
	Reads            uint64 `json:"reads"`
	Writes           uint64 `json:"writes"`
	Fetches          uint64 `json:"fetches"`
	Inserts          uint64 `json:"inserts"`
	Updates          uint64 `json:"updates"`
	Deletes          uint64 `json:"deletes"`
	HasPrimaryKey    bool   `json:"has_primary_key"`
	AutoIncrement    uint64 `json:"auto_increment,omitempty"`
	AutoIncrementMax uint64 `json:"auto_increment_max,omitempty"`
}

type Index struct {
	Schema      string `json:"schema"`
	Table       string `json:"table"`
	Name        string `json:"name"`
	Columns     string `json:"columns"`
	Unique      bool   `json:"unique"`
	Visible     bool   `json:"visible"`
	Cardinality uint64 `json:"cardinality"`
	Reads       uint64 `json:"reads"`
	Writes      uint64 `json:"writes"`
}

type Process struct {
	ID        uint64 `json:"id"`
	User      string `json:"user"`
	Host      string `json:"host"`
	Database  string `json:"database,omitempty"`
	Command   string `json:"command"`
	Seconds   uint64 `json:"seconds"`
	State     string `json:"state,omitempty"`
	Statement string `json:"statement,omitempty"`
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

type Replication struct {
	Channel          string `json:"channel,omitempty"`
	SourceHost       string `json:"source_host,omitempty"`
	SourcePort       uint64 `json:"source_port,omitempty"`
	IORunning        string `json:"io_running,omitempty"`
	SQLRunning       string `json:"sql_running,omitempty"`
	SecondsBehind    *int64 `json:"seconds_behind,omitempty"`
	LastIOError      string `json:"last_io_error,omitempty"`
	LastSQLError     string `json:"last_sql_error,omitempty"`
	RetrievedGTIDSet string `json:"retrieved_gtid_set,omitempty"`
	ExecutedGTIDSet  string `json:"executed_gtid_set,omitempty"`
}

type Capability struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
