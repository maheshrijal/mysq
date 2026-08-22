package collect

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	mysqlDriver "github.com/go-sql-driver/mysql"

	"github.com/maheshrijal/mysq/internal/model"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

type Collector struct {
	ToolVersion string
	Interval    time.Duration
	Timeout     time.Duration
	QueryLimit  int
}

type Target struct {
	DSN      string
	Host     string
	Port     int
	Database string
}

func New(version string) *Collector {
	return &Collector{
		ToolVersion: version,
		Interval:    time.Second,
		Timeout:     10 * time.Second,
		QueryLimit:  20,
	}
}

func ResolveConnection(argument string) (Target, error) {
	raw := strings.TrimSpace(argument)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("MYSQ_DATABASE_URL"))
	}
	// MYSQLDOT_DATABASE_URL is the pre-rename variable. Keep reading it so
	// existing shell profiles and automation continue to connect safely.
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("MYSQLDOT_DATABASE_URL"))
	}
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if raw == "" {
		return Target{}, errors.New("no MySQL connection supplied: pass a DSN or set MYSQ_DATABASE_URL")
	}

	if strings.HasPrefix(raw, "mysql://") {
		return targetFromURL(raw)
	}
	cfg, err := mysqlDriver.ParseDSN(raw)
	if err != nil {
		return Target{}, fmt.Errorf("parse MySQL DSN: %w", err)
	}
	prepareConfig(cfg)
	host, port := splitAddress(cfg.Addr)
	return Target{DSN: cfg.FormatDSN(), Host: host, Port: port, Database: cfg.DBName}, nil
}

func targetFromURL(raw string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("parse MySQL URL: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return Target{}, errors.New("MySQL URL must include a username")
	}
	host := u.Hostname()
	if host == "" {
		return Target{}, errors.New("MySQL URL must include a host")
	}
	port := 3306
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return Target{}, fmt.Errorf("invalid MySQL port: %w", err)
		}
		if port < 1 || port > 65535 {
			return Target{}, fmt.Errorf("invalid MySQL port: %d", port)
		}
	}
	password, _ := u.User.Password()
	cfg := mysqlDriver.NewConfig()
	cfg.User = u.User.Username()
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, strconv.Itoa(port))
	cfg.DBName = strings.TrimPrefix(u.EscapedPath(), "/")
	if decoded, decodeErr := url.PathUnescape(cfg.DBName); decodeErr == nil {
		cfg.DBName = decoded
	}
	cfg.Params = map[string]string{}
	for key, values := range u.Query() {
		if len(values) == 0 {
			continue
		}
		value := values[len(values)-1]
		switch key {
		case "tls":
			cfg.TLSConfig = value
		case "collation":
			cfg.Collation = value
		case "loc":
			location, locationErr := time.LoadLocation(value)
			if locationErr != nil {
				return Target{}, fmt.Errorf("invalid MySQL location %q: %w", value, locationErr)
			}
			cfg.Loc = location
		case "timeout":
			cfg.Timeout, err = time.ParseDuration(value)
		case "readTimeout":
			cfg.ReadTimeout, err = time.ParseDuration(value)
		case "writeTimeout":
			cfg.WriteTimeout, err = time.ParseDuration(value)
		case "interpolateParams":
			cfg.InterpolateParams, err = strconv.ParseBool(value)
		case "multiStatements":
			cfg.MultiStatements, err = strconv.ParseBool(value)
		case "rejectReadOnly":
			cfg.RejectReadOnly, err = strconv.ParseBool(value)
		case "parseTime":
			cfg.ParseTime, err = strconv.ParseBool(value)
		default:
			cfg.Params[key] = value
		}
		if err != nil {
			return Target{}, fmt.Errorf("invalid MySQL URL parameter %s=%q: %w", key, value, err)
		}
	}
	prepareConfig(cfg)
	return Target{DSN: cfg.FormatDSN(), Host: host, Port: port, Database: cfg.DBName}, nil
}

func prepareConfig(cfg *mysqlDriver.Config) {
	if cfg.Net == "" {
		cfg.Net = "tcp"
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:3306"
	}
	cfg.ParseTime = true
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 15 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 15 * time.Second
	}
	cfg.AllowNativePasswords = true
}

func splitAddress(addr string) (string, int) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 3306
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		port = 3306
	}
	return host, port
}

func (c *Collector) Inspect(ctx context.Context, target Target) (*model.Context, error) {
	db, err := sql.Open("mysql", target.DSN)
	if err != nil {
		return nil, fmt.Errorf("open MySQL connection: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(2 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("connect to %s:%d: %w", target.Host, target.Port, err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve MySQL connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SET SESSION MAX_EXECUTION_TIME=10000"); err != nil {
		return nil, fmt.Errorf("pin session statement timeout: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SET SESSION transaction_read_only=ON"); err != nil {
		return nil, fmt.Errorf("pin session read-only: %w", err)
	}

	result := &model.Context{
		SchemaVersion: model.SchemaVersion,
		ToolVersion:   c.ToolVersion,
		CollectedAt:   time.Now().UTC(),
		Variables:     map[string]string{},
		GlobalStatus:  map[string]string{},
		Server: model.Server{
			Host:     target.Host,
			Port:     target.Port,
			Database: target.Database,
		},
	}

	if err := c.collectServer(ctx, conn, &result.Server); err != nil {
		return nil, fmt.Errorf("collect server identity: %w", err)
	}
	result.Variables, err = queryNameValue(ctx, conn, "SHOW GLOBAL VARIABLES")
	if err != nil {
		result.Warnings = append(result.Warnings, "global variables unavailable: "+err.Error())
	}
	for key, value := range result.Variables {
		if sanitize.SensitiveName(key) && value != "" {
			result.Variables[key] = "[redacted]"
		}
	}
	result.Server.PerformanceSchema = strings.EqualFold(result.Variables["performance_schema"], "ON")

	c.probe(ctx, result, "statement digests", func() error {
		var probeErr error
		result.Queries, probeErr = c.collectQueries(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "table statistics", func() error {
		var probeErr error
		result.Tables, probeErr = c.collectTables(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "index statistics", func() error {
		var probeErr error
		result.Indexes, probeErr = c.collectIndexes(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "process list", func() error {
		var probeErr error
		result.Processes, probeErr = c.collectProcesses(ctx, conn)
		result.ConnectionGroups = summarizeProcesses(result.Processes)
		attributeActiveUsers(result.Queries, result.Processes)
		return probeErr
	})
	c.probe(ctx, result, "row lock waits", func() error {
		var probeErr error
		result.Locks, probeErr = c.collectLocks(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "active transactions", func() error {
		var probeErr error
		result.Transactions, probeErr = c.collectTransactions(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "metadata locks", func() error {
		var probeErr error
		result.MetadataLocks, probeErr = c.collectMetadataLocks(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "wait events", func() error {
		var probeErr error
		result.WaitEvents, probeErr = c.collectWaitEvents(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "memory consumers", func() error {
		var probeErr error
		result.MemoryConsumers, probeErr = c.collectMemoryConsumers(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "replication", func() error {
		var probeErr error
		result.Replication, probeErr = c.collectReplication(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "InnoDB monitor", func() error {
		var probeErr error
		result.InnoDBStatus, probeErr = collectInnoDBStatus(ctx, conn)
		return probeErr
	})
	result.Metrics.HistoryListLength = c.historyListLength(ctx, conn, result)

	// The rate window starts only after catalog and Performance Schema probes,
	// so the diagnostic does not report its own work as application throughput.
	first, err := queryNameValue(ctx, conn, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, fmt.Errorf("collect initial global status: %w", err)
	}

	interval := c.Interval
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	started := time.Now()
	timer := time.NewTimer(interval)
	select {
	case <-ctx.Done():
		timer.Stop()
		return nil, ctx.Err()
	case <-timer.C:
	}
	second, err := queryNameValue(ctx, conn, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, fmt.Errorf("collect final global status: %w", err)
	}
	elapsed := time.Since(started)
	result.IntervalMillis = elapsed.Milliseconds()
	result.GlobalStatus = second
	result.Server.UptimeSeconds = unsigned(second["Uptime"])
	historyListLength := result.Metrics.HistoryListLength
	result.Metrics = deriveMetrics(first, second, result.Variables, elapsed)
	result.Metrics.HistoryListLength = historyListLength
	result.Fingerprint = fingerprint(result.Server)
	return result, nil
}

func (c *Collector) probe(ctx context.Context, result *model.Context, name string, fn func() error) {
	err := fn()
	capability := model.Capability{Name: name, Available: err == nil}
	if err != nil {
		capability.Reason = compactError(err)
		result.Warnings = append(result.Warnings, name+" unavailable: "+compactError(err))
	}
	result.Capabilities = append(result.Capabilities, capability)
}

func compactError(err error) string {
	value := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(value) > 220 {
		return value[:217] + "..."
	}
	return value
}

func (c *Collector) collectServer(ctx context.Context, conn *sql.Conn, server *model.Server) error {
	row := conn.QueryRowContext(ctx, `SELECT VERSION(), @@hostname, DATABASE(), @@server_uuid,
        @@server_id, @@read_only, @@super_read_only`)
	var database sql.NullString
	var readOnly, superReadOnly uint8
	if err := row.Scan(&server.Version, &server.Hostname, &database, &server.UUID,
		&server.ServerID, &readOnly, &superReadOnly); err != nil {
		return err
	}
	if database.Valid {
		server.Database = database.String
	}
	server.ReadOnly = readOnly != 0
	server.SuperReadOnly = superReadOnly != 0
	versionLower := strings.ToLower(server.Version)
	switch {
	case strings.Contains(versionLower, "mariadb"):
		server.Flavor = "MariaDB"
	case strings.Contains(versionLower, "percona"):
		server.Flavor = "Percona Server"
	default:
		server.Flavor = "MySQL"
	}
	return nil
}

func queryNameValue(ctx context.Context, conn *sql.Conn, statement string) (map[string]string, error) {
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		values[name] = value
	}
	return values, rows.Err()
}

func queryMaps(ctx context.Context, conn *sql.Conn, statement string, args ...any) ([]map[string]string, error) {
	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	results := make([]map[string]string, 0)
	for rows.Next() {
		raw := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		item := make(map[string]string, len(columns))
		for i, column := range columns {
			item[column] = string(raw[i])
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func unsigned(value string) uint64 {
	n, _ := strconv.ParseUint(value, 10, 64)
	return n
}

func signed(value string) int64 {
	n, _ := strconv.ParseInt(value, 10, 64)
	return n
}

func decimal(value string) float64 {
	n, _ := strconv.ParseFloat(value, 64)
	return n
}

func delta(first, second map[string]string, key string) float64 {
	a := decimal(first[key])
	b := decimal(second[key])
	if b < a {
		return 0
	}
	return b - a
}

func deriveMetrics(first, second, variables map[string]string, elapsed time.Duration) model.Metrics {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	perSecond := func(key string) float64 { return delta(first, second, key) / seconds }
	queries := delta(first, second, "Questions")
	// The second SHOW GLOBAL STATUS increments Questions before returning its
	// snapshot. Remove that one known self-query from the sampled workload.
	if queries >= 1 {
		queries--
	}
	commits := delta(first, second, "Com_commit")
	rollbacks := delta(first, second, "Com_rollback")
	readRequests := delta(first, second, "Innodb_buffer_pool_read_requests")
	physicalReads := delta(first, second, "Innodb_buffer_pool_reads")
	tmpTables := delta(first, second, "Created_tmp_tables")
	tmpDisk := delta(first, second, "Created_tmp_disk_tables")
	tableHits := delta(first, second, "Table_open_cache_hits")
	tableMisses := delta(first, second, "Table_open_cache_misses")
	poolPages := decimal(second["Innodb_buffer_pool_pages_total"])
	poolFree := decimal(second["Innodb_buffer_pool_pages_free"])
	poolDirty := decimal(second["Innodb_buffer_pool_pages_dirty"])
	connections := unsigned(second["Threads_connected"])
	maxConnections := unsigned(variables["max_connections"])
	openFiles := unsigned(second["Open_files"])
	openFilesLimit := unsigned(variables["open_files_limit"])

	metrics := model.Metrics{
		QueriesPerSecond:      queries / seconds,
		TransactionsPerSecond: (commits + rollbacks) / seconds,
		RowsReadPerSecond:     delta(first, second, "Innodb_rows_read") / seconds,
		RowsWrittenPerSecond:  (delta(first, second, "Innodb_rows_inserted") + delta(first, second, "Innodb_rows_updated") + delta(first, second, "Innodb_rows_deleted")) / seconds,
		SlowQueriesPerSecond:  perSecond("Slow_queries"),
		AbortedConnectsPerSec: perSecond("Aborted_connects"),
		RowLockWaitsPerSecond: perSecond("Innodb_row_lock_waits"),
		RedoWaitsPerSecond:    perSecond("Innodb_log_waits"),
		ConnectionsCurrent:    connections,
		ConnectionsMax:        maxConnections,
		ThreadsRunning:        unsigned(second["Threads_running"]),
		DataReadsPerSecond:    perSecond("Innodb_data_reads"),
		DataWritesPerSecond:   perSecond("Innodb_data_writes"),
		DataFsyncsPerSecond:   perSecond("Innodb_data_fsyncs"),
		RedoBytesPerSecond:    perSecond("Innodb_os_log_written"),
		RedoWritesPerSecond:   perSecond("Innodb_log_writes"),
		RedoFsyncsPerSecond:   perSecond("Innodb_os_log_fsyncs"),
		NetworkInBytesPerSec:  perSecond("Bytes_received"),
		NetworkOutBytesPerSec: perSecond("Bytes_sent"),
		FullScansPerSecond:    perSecond("Select_scan"),
		SortMergePassesPerSec: perSecond("Sort_merge_passes"),
		BufferPoolWaitsPerSec: perSecond("Innodb_buffer_pool_wait_free"),
		PendingReads:          unsigned(second["Innodb_data_pending_reads"]),
		PendingWrites:         unsigned(second["Innodb_data_pending_writes"]),
		PendingFsyncs:         unsigned(second["Innodb_data_pending_fsyncs"]),
		BufferPoolDataBytes:   unsigned(second["Innodb_buffer_pool_bytes_data"]),
		BufferPoolDirtyBytes:  unsigned(second["Innodb_buffer_pool_bytes_dirty"]),
		RedoCurrentLSN:        unsigned(second["Innodb_redo_log_current_lsn"]),
		RedoFlushedLSN:        unsigned(second["Innodb_redo_log_flushed_to_disk_lsn"]),
		RedoCheckpointLSN:     unsigned(second["Innodb_redo_log_checkpoint_lsn"]),
		RedoCapacityBytes:     unsigned(second["Innodb_redo_log_capacity_resized"]),
	}
	if metrics.RedoCapacityBytes == 0 {
		metrics.RedoCapacityBytes = unsigned(variables["innodb_redo_log_capacity"])
	}
	if metrics.RedoCurrentLSN >= metrics.RedoCheckpointLSN {
		metrics.RedoCheckpointAgeBytes = metrics.RedoCurrentLSN - metrics.RedoCheckpointLSN
	}
	if metrics.RedoCapacityBytes > 0 {
		metrics.RedoCheckpointAgePct = float64(metrics.RedoCheckpointAgeBytes) * 100 / float64(metrics.RedoCapacityBytes)
	}
	if maxConnections > 0 {
		metrics.ConnectionsUsedPercent = float64(connections) * 100 / float64(maxConnections)
	}
	if readRequests > 0 {
		metrics.BufferPoolHitPercent = (1 - physicalReads/readRequests) * 100
	} else {
		metrics.BufferPoolHitPercent = 100
	}
	if poolPages > 0 {
		metrics.BufferPoolUsedPercent = (poolPages - poolFree) * 100 / poolPages
		metrics.BufferPoolDirtyPercent = poolDirty * 100 / poolPages
	}
	if tmpTables > 0 {
		metrics.TempDiskTablePercent = tmpDisk * 100 / tmpTables
	}
	if tableHits+tableMisses > 0 {
		metrics.TableCacheHitPercent = tableHits * 100 / (tableHits + tableMisses)
	} else {
		metrics.TableCacheHitPercent = 100
	}
	if openFilesLimit > 0 {
		metrics.OpenFilesUsedPercent = float64(openFiles) * 100 / float64(openFilesLimit)
	}
	return metrics
}

func (c *Collector) historyListLength(ctx context.Context, conn *sql.Conn, result *model.Context) uint64 {
	var value uint64
	err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(COUNT), 0)
        FROM information_schema.INNODB_METRICS WHERE NAME='trx_rseg_history_len'`).Scan(&value)
	if err != nil {
		result.Warnings = append(result.Warnings, "InnoDB history list unavailable: "+compactError(err))
	}
	return value
}

func fingerprint(server model.Server) string {
	identity := server.UUID + "\x00" + server.Database
	if server.UUID == "" {
		identity = fmt.Sprintf("%s:%d\x00%s", server.Host, server.Port, server.Database)
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:12])
}

func (c *Collector) collectQueries(ctx context.Context, conn *sql.Conn) ([]model.Query, error) {
	statement := `SELECT COALESCE(DIGEST,''), COALESCE(SCHEMA_NAME,''), COALESCE(DIGEST_TEXT,''),
		COUNT_STAR, SUM_TIMER_WAIT / 1000000000, AVG_TIMER_WAIT / 1000000000,
		SUM_ROWS_EXAMINED, SUM_ROWS_SENT, SUM_NO_INDEX_USED, SUM_CREATED_TMP_TABLES,
		SUM_CREATED_TMP_DISK_TABLES, COALESCE(CAST(FIRST_SEEN AS CHAR),''), COALESCE(CAST(LAST_SEEN AS CHAR),'')
	  FROM performance_schema.events_statements_summary_by_digest
      WHERE DIGEST IS NOT NULL AND DIGEST_TEXT IS NOT NULL
      ORDER BY SUM_TIMER_WAIT DESC LIMIT ?`
	rows, err := conn.QueryContext(ctx, statement, c.QueryLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	queries := make([]model.Query, 0, c.QueryLimit)
	for rows.Next() {
		var item model.Query
		if err := rows.Scan(&item.Digest, &item.Schema, &item.Statement, &item.Calls,
			&item.TotalLatencyMillis, &item.MeanLatencyMillis, &item.RowsExamined,
			&item.RowsSent, &item.NoIndexUsed, &item.TmpTables, &item.TmpDiskTables,
			&item.FirstSeen, &item.LastSeen); err != nil {
			return nil, err
		}
		item.Statement = sanitize.SQL(item.Statement)
		queries = append(queries, item)
	}
	return queries, rows.Err()
}

func (c *Collector) collectTables(ctx context.Context, conn *sql.Conn) ([]model.Table, error) {
	statement := `SELECT t.TABLE_SCHEMA, t.TABLE_NAME, COALESCE(t.ENGINE,''),
        COALESCE(t.TABLE_ROWS,0), COALESCE(t.DATA_LENGTH,0), COALESCE(t.INDEX_LENGTH,0),
        COALESCE(t.DATA_LENGTH + t.INDEX_LENGTH,0), COALESCE(io.COUNT_READ,0),
        COALESCE(io.COUNT_WRITE,0), COALESCE(io.COUNT_FETCH,0), COALESCE(io.COUNT_INSERT,0),
        COALESCE(io.COUNT_UPDATE,0), COALESCE(io.COUNT_DELETE,0),
        EXISTS(SELECT 1 FROM information_schema.STATISTICS s
          WHERE s.TABLE_SCHEMA=t.TABLE_SCHEMA AND s.TABLE_NAME=t.TABLE_NAME AND s.INDEX_NAME='PRIMARY'),
        COALESCE(t.AUTO_INCREMENT,0)
      FROM information_schema.TABLES t
      LEFT JOIN performance_schema.table_io_waits_summary_by_table io
        ON io.OBJECT_SCHEMA=t.TABLE_SCHEMA AND io.OBJECT_NAME=t.TABLE_NAME
      WHERE t.TABLE_TYPE='BASE TABLE'
        AND t.TABLE_SCHEMA NOT IN ('mysql','performance_schema','information_schema','sys')
      ORDER BY t.DATA_LENGTH + t.INDEX_LENGTH DESC LIMIT 100`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := make([]model.Table, 0)
	for rows.Next() {
		var item model.Table
		if err := rows.Scan(&item.Schema, &item.Name, &item.Engine, &item.EstimatedRows,
			&item.DataBytes, &item.IndexBytes, &item.TotalBytes, &item.Reads, &item.Writes,
			&item.Fetches, &item.Inserts, &item.Updates, &item.Deletes, &item.HasPrimaryKey,
			&item.AutoIncrement); err != nil {
			return nil, err
		}
		tables = append(tables, item)
	}
	return tables, rows.Err()
}

func (c *Collector) collectIndexes(ctx context.Context, conn *sql.Conn) ([]model.Index, error) {
	statement := `SELECT s.TABLE_SCHEMA, s.TABLE_NAME, s.INDEX_NAME,
        GROUP_CONCAT(s.COLUMN_NAME ORDER BY s.SEQ_IN_INDEX SEPARATOR ', '),
        MIN(s.NON_UNIQUE)=0, MIN(COALESCE(s.IS_VISIBLE,'YES'))='YES',
        COALESCE(MAX(s.CARDINALITY),0), COALESCE(MAX(io.COUNT_READ),0), COALESCE(MAX(io.COUNT_WRITE),0)
      FROM information_schema.STATISTICS s
      LEFT JOIN performance_schema.table_io_waits_summary_by_index_usage io
        ON io.OBJECT_SCHEMA=s.TABLE_SCHEMA AND io.OBJECT_NAME=s.TABLE_NAME
       AND io.INDEX_NAME=s.INDEX_NAME
      WHERE s.TABLE_SCHEMA NOT IN ('mysql','performance_schema','information_schema','sys')
      GROUP BY s.TABLE_SCHEMA, s.TABLE_NAME, s.INDEX_NAME
      ORDER BY s.TABLE_SCHEMA, s.TABLE_NAME, s.INDEX_NAME`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	indexes := make([]model.Index, 0)
	for rows.Next() {
		var item model.Index
		if err := rows.Scan(&item.Schema, &item.Table, &item.Name, &item.Columns,
			&item.Unique, &item.Visible, &item.Cardinality, &item.Reads, &item.Writes); err != nil {
			return nil, err
		}
		indexes = append(indexes, item)
	}
	return indexes, rows.Err()
}

func (c *Collector) collectProcesses(ctx context.Context, conn *sql.Conn) ([]model.Process, error) {
	statement := `SELECT t.PROCESSLIST_ID, t.THREAD_ID, COALESCE(t.PROCESSLIST_USER,''),
		COALESCE(t.PROCESSLIST_HOST,''), COALESCE(t.PROCESSLIST_DB,''),
		COALESCE(t.PROCESSLIST_COMMAND,''), COALESCE(t.PROCESSLIST_TIME,0),
		COALESCE(t.PROCESSLIST_STATE,''), COALESCE(es.DIGEST,''),
		COALESCE(ew.EVENT_NAME,''), COALESCE(es.TIMER_WAIT,0) / 1000000000,
		COALESCE(es.DIGEST_TEXT, t.PROCESSLIST_INFO, '')
	  FROM performance_schema.threads t
	  LEFT JOIN performance_schema.events_statements_current es ON es.THREAD_ID=t.THREAD_ID
	  LEFT JOIN performance_schema.events_waits_current ew ON ew.THREAD_ID=t.THREAD_ID
	  WHERE t.TYPE='FOREGROUND' AND t.PROCESSLIST_ID IS NOT NULL
		AND t.PROCESSLIST_USER IS NOT NULL
		AND t.PROCESSLIST_ID <> CONNECTION_ID()
	  ORDER BY t.PROCESSLIST_TIME DESC LIMIT 100`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	processes := make([]model.Process, 0)
	for rows.Next() {
		var item model.Process
		if err := rows.Scan(&item.ID, &item.ThreadID, &item.User, &item.Host, &item.Database,
			&item.Command, &item.Seconds, &item.State, &item.Digest, &item.WaitEvent,
			&item.StatementLatencyMillis, &item.Statement); err != nil {
			return nil, err
		}
		item.Statement = sanitize.SQL(item.Statement)
		processes = append(processes, item)
	}
	return processes, rows.Err()
}

func attributeActiveUsers(queries []model.Query, processes []model.Process) {
	users := make(map[string]map[string]bool)
	for _, process := range processes {
		if process.Digest == "" || process.User == "" || strings.EqualFold(process.Command, "Sleep") {
			continue
		}
		if users[process.Digest] == nil {
			users[process.Digest] = make(map[string]bool)
		}
		users[process.Digest][process.User] = true
	}
	for index := range queries {
		for user := range users[queries[index].Digest] {
			queries[index].ActiveUsers = append(queries[index].ActiveUsers, user)
		}
		sort.Strings(queries[index].ActiveUsers)
	}
}

func summarizeProcesses(processes []model.Process) []model.ConnectionGroup {
	type counts struct{ total, active, sleeping, other int }
	groups := map[string]map[string]*counts{"user": {}, "host": {}, "user_host": {}}
	for _, process := range processes {
		host := process.Host
		if parsed, _, err := net.SplitHostPort(process.Host); err == nil {
			host = parsed
		} else if index := strings.LastIndex(host, ":"); index > 0 {
			host = host[:index]
		}
		keys := map[string]string{"user": process.User, "host": host, "user_host": process.User + "@" + host}
		for kind, key := range keys {
			group := groups[kind][key]
			if group == nil {
				group = &counts{}
				groups[kind][key] = group
			}
			group.total++
			switch {
			case strings.EqualFold(process.Command, "Sleep"):
				group.sleeping++
			case strings.EqualFold(process.Command, "Query") || process.Statement != "":
				group.active++
			default:
				group.other++
			}
		}
	}
	result := make([]model.ConnectionGroup, 0)
	for _, kind := range []string{"user", "host", "user_host"} {
		for key, group := range groups[kind] {
			result = append(result, model.ConnectionGroup{Kind: kind, Key: key, Total: group.total, Active: group.active, Sleeping: group.sleeping, Other: group.other})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].Total != result[j].Total {
			return result[i].Total > result[j].Total
		}
		return result[i].Key < result[j].Key
	})
	return result
}

func (c *Collector) collectLocks(ctx context.Context, conn *sql.Conn) ([]model.LockWait, error) {
	statement := `SELECT w.REQUESTING_ENGINE_TRANSACTION_ID, w.BLOCKING_ENGINE_TRANSACTION_ID,
        COALESCE(r.OBJECT_SCHEMA,''), COALESCE(r.OBJECT_NAME,''), COALESCE(r.INDEX_NAME,''),
        COALESCE(r.LOCK_TYPE,''), COALESCE(r.LOCK_MODE,'')
      FROM performance_schema.data_lock_waits w
      JOIN performance_schema.data_locks r ON r.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
      ORDER BY r.OBJECT_SCHEMA, r.OBJECT_NAME`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locks := make([]model.LockWait, 0)
	for rows.Next() {
		var item model.LockWait
		if err := rows.Scan(&item.WaitingTransaction, &item.BlockingTransaction, &item.Schema,
			&item.Table, &item.Index, &item.LockType, &item.LockMode); err != nil {
			return nil, err
		}
		locks = append(locks, item)
	}
	return locks, rows.Err()
}

func (c *Collector) collectTransactions(ctx context.Context, conn *sql.Conn) ([]model.Transaction, error) {
	statement := `SELECT COALESCE(trx.TRX_ID,''), COALESCE(trx.TRX_STATE,''),
		COALESCE(CAST(trx.TRX_STARTED AS CHAR),''),
		COALESCE(TIMESTAMPDIFF(SECOND, trx.TRX_STARTED, NOW()),0),
		COALESCE(trx.TRX_MYSQL_THREAD_ID,0), COALESCE(t.PROCESSLIST_USER,''),
		COALESCE(t.PROCESSLIST_HOST,''), COALESCE(trx.TRX_ROWS_LOCKED,0),
		COALESCE(trx.TRX_ROWS_MODIFIED,0), COALESCE(trx.TRX_TABLES_IN_USE,0),
		COALESCE(trx.TRX_TABLES_LOCKED,0), COALESCE(trx.TRX_QUERY,'')
	  FROM information_schema.INNODB_TRX trx
	  LEFT JOIN performance_schema.threads t ON t.PROCESSLIST_ID=trx.TRX_MYSQL_THREAD_ID
	  ORDER BY trx.TRX_STARTED LIMIT 100`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	transactions := make([]model.Transaction, 0)
	for rows.Next() {
		var item model.Transaction
		if err := rows.Scan(&item.ID, &item.State, &item.StartedAt, &item.AgeSeconds,
			&item.ProcessID, &item.User, &item.Host, &item.RowsLocked, &item.RowsModified,
			&item.TablesInUse, &item.TablesLocked, &item.Statement); err != nil {
			return nil, err
		}
		item.Statement = sanitize.SQL(item.Statement)
		transactions = append(transactions, item)
	}
	return transactions, rows.Err()
}

func (c *Collector) collectMetadataLocks(ctx context.Context, conn *sql.Conn) ([]model.MetadataLock, error) {
	statement := `SELECT ml.OWNER_THREAD_ID, COALESCE(t.PROCESSLIST_ID,0),
		COALESCE(t.PROCESSLIST_USER,''), COALESCE(t.PROCESSLIST_HOST,''),
		COALESCE(ml.OBJECT_TYPE,''), COALESCE(ml.OBJECT_SCHEMA,''),
		COALESCE(ml.OBJECT_NAME,''), COALESCE(ml.LOCK_TYPE,''),
		COALESCE(ml.LOCK_DURATION,''), COALESCE(ml.LOCK_STATUS,'')
	  FROM performance_schema.metadata_locks ml
	  LEFT JOIN performance_schema.threads t ON t.THREAD_ID=ml.OWNER_THREAD_ID
	  WHERE t.PROCESSLIST_ID IS NOT NULL AND t.PROCESSLIST_ID <> CONNECTION_ID()
		AND (ml.LOCK_STATUS='PENDING' OR COALESCE(t.PROCESSLIST_COMMAND,'') <> 'Sleep')
	  ORDER BY ml.LOCK_STATUS='PENDING' DESC, t.PROCESSLIST_TIME DESC
	  LIMIT 100`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	locks := make([]model.MetadataLock, 0)
	for rows.Next() {
		var item model.MetadataLock
		if err := rows.Scan(&item.ThreadID, &item.ProcessID, &item.User, &item.Host,
			&item.ObjectType, &item.Schema, &item.Object, &item.LockType, &item.Duration,
			&item.Status); err != nil {
			return nil, err
		}
		locks = append(locks, item)
	}
	return locks, rows.Err()
}

func (c *Collector) collectWaitEvents(ctx context.Context, conn *sql.Conn) ([]model.WaitEvent, error) {
	statement := `SELECT EVENT_NAME,
		SUBSTRING_INDEX(SUBSTRING_INDEX(EVENT_NAME,'/',2),'/',-1), COUNT_STAR,
		SUM_TIMER_WAIT / 1000000000, AVG_TIMER_WAIT / 1000000,
		MAX_TIMER_WAIT / 1000000000
	  FROM performance_schema.events_waits_summary_global_by_event_name
	  WHERE COUNT_STAR > 0 AND EVENT_NAME <> 'idle' AND EVENT_NAME NOT LIKE 'idle/%'
	  ORDER BY SUM_TIMER_WAIT DESC LIMIT 30`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waits := make([]model.WaitEvent, 0)
	for rows.Next() {
		var item model.WaitEvent
		if err := rows.Scan(&item.Name, &item.Class, &item.Count, &item.TotalLatencyMillis,
			&item.MeanLatencyMicros, &item.MaxLatencyMillis); err != nil {
			return nil, err
		}
		waits = append(waits, item)
	}
	return waits, rows.Err()
}

func (c *Collector) collectMemoryConsumers(ctx context.Context, conn *sql.Conn) ([]model.MemoryConsumer, error) {
	statement := `SELECT EVENT_NAME, CURRENT_NUMBER_OF_BYTES_USED,
		HIGH_NUMBER_OF_BYTES_USED, CURRENT_COUNT_USED
	  FROM performance_schema.memory_summary_global_by_event_name
	  WHERE CURRENT_NUMBER_OF_BYTES_USED > 0
	  ORDER BY CURRENT_NUMBER_OF_BYTES_USED DESC LIMIT 30`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	consumers := make([]model.MemoryConsumer, 0)
	for rows.Next() {
		var item model.MemoryConsumer
		if err := rows.Scan(&item.Name, &item.CurrentBytes, &item.HighBytes, &item.Allocations); err != nil {
			return nil, err
		}
		consumers = append(consumers, item)
	}
	return consumers, rows.Err()
}

func (c *Collector) collectReplication(ctx context.Context, conn *sql.Conn) (*model.Replication, error) {
	rows, err := queryMaps(ctx, conn, "SHOW REPLICA STATUS")
	if err != nil {
		rows, err = queryMaps(ctx, conn, "SHOW SLAVE STATUS")
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	value := func(current, legacy string) string {
		if r[current] != "" {
			return r[current]
		}
		return r[legacy]
	}
	replica := &model.Replication{
		Channel:          value("Channel_Name", "Channel_Name"),
		SourceHost:       value("Source_Host", "Master_Host"),
		SourcePort:       unsigned(value("Source_Port", "Master_Port")),
		IORunning:        value("Replica_IO_Running", "Slave_IO_Running"),
		SQLRunning:       value("Replica_SQL_Running", "Slave_SQL_Running"),
		LastIOError:      sanitize.Text(value("Last_IO_Error", "Last_IO_Error")),
		LastSQLError:     sanitize.Text(value("Last_SQL_Error", "Last_SQL_Error")),
		RetrievedGTIDSet: value("Retrieved_Gtid_Set", "Retrieved_Gtid_Set"),
		ExecutedGTIDSet:  value("Executed_Gtid_Set", "Executed_Gtid_Set"),
	}
	lag := value("Seconds_Behind_Source", "Seconds_Behind_Master")
	if lag != "" && !strings.EqualFold(lag, "NULL") {
		parsed := signed(lag)
		replica.SecondsBehind = &parsed
	}
	return replica, nil
}

func collectInnoDBStatus(ctx context.Context, conn *sql.Conn) (string, error) {
	rows, err := conn.QueryContext(ctx, "SHOW ENGINE INNODB STATUS")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", rows.Err()
	}
	var engine, name, status string
	if err := rows.Scan(&engine, &name, &status); err != nil {
		return "", err
	}
	return sanitize.Text(status), nil
}

func SortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
