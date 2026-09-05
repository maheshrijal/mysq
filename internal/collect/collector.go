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

	"github.com/maheshrijal/mysq/internal/debuglog"
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

// ConnectionInput chooses an endpoint; credentials alone never choose a server.
func ConnectionInput(argument string) string {
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
	return raw
}

func ResolveConnection(argument string) (Target, error) {
	raw := ConnectionInput(argument)
	if raw == "" {
		return Target{}, errors.New("no MySQL endpoint supplied: use mysq inspect host[:port]/database, set MYSQ_DATABASE_URL, or run mysq tui for a connection prompt")
	}

	if strings.HasPrefix(raw, "mysql://") {
		return targetFromURL(raw)
	}
	// A bare host/database uses shell credentials. Preserve native driver DSNs,
	// including credential-free tcp(...) and unix(...) forms.
	if !strings.ContainsAny(raw, "@()") && !strings.HasPrefix(raw, "/") && !strings.Contains(raw, "://") {
		return targetFromURL("mysql://" + raw)
	}
	cfg, err := mysqlDriver.ParseDSN(raw)
	if err != nil {
		return Target{}, fmt.Errorf("parse MySQL DSN: %w", err)
	}
	applyEnvironmentCredentials(cfg)
	prepareConfig(cfg)
	host, port := splitAddress(cfg.Addr)
	return Target{DSN: cfg.FormatDSN(), Host: host, Port: port, Database: cfg.DBName}, nil
}

func applyEnvironmentCredentials(cfg *mysqlDriver.Config) {
	// Explicit credentials are a pair: never attach a shell password to a
	// different explicit username or replace its intentionally empty password.
	if cfg.User == "" && os.Getenv("DBOPS_MYSQL_USER") != "" {
		cfg.User = os.Getenv("DBOPS_MYSQL_USER")
		cfg.Passwd = os.Getenv("DBOPS_MYSQL_PWD")
	}
}

func targetFromURL(raw string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("parse MySQL URL: %w", err)
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
	cfg := mysqlDriver.NewConfig()
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	applyEnvironmentCredentials(cfg)
	if cfg.User == "" {
		return Target{}, errors.New("MySQL URL needs a username; export DBOPS_MYSQL_USER and DBOPS_MYSQL_PWD or include credentials in the URL")
	}
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
	defer debuglog.Start(ctx, "collect.Inspect")()
	db, conn, err := c.openConnection(ctx, target)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	defer conn.Close()

	result := newContext(c.ToolVersion, target)

	if err := c.collectServer(ctx, conn, &result.Server); err != nil {
		return nil, fmt.Errorf("collect server identity: %w", err)
	}
	result.Variables, err = queryNameValue(ctx, conn, "SHOW GLOBAL VARIABLES")
	c.recordCapability(result, "global variables", err)
	redactVariables(result.Variables)
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
		return probeErr
	})
	c.probe(ctx, result, "active query users", func() error {
		return c.collectActiveQueryUsers(ctx, conn, result.Queries)
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
	c.probe(ctx, result, "statement latency histogram", func() error {
		var probeErr error
		result.StatementLatency, probeErr = c.collectStatementLatency(ctx, conn)
		return probeErr
	})
	c.probe(ctx, result, "instrumentation coverage", func() error {
		var probeErr error
		result.Instrumentation, probeErr = c.collectInstrumentation(ctx, conn, result.Variables)
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

	// Counter endpoints are collected sequentially, so each family keeps its own
	// elapsed window. Sharing the status timer would divide earlier/later counter
	// deltas by a window that did not match their actual endpoints.
	firstWaits, firstWaitErr := c.collectWaitCounters(ctx, conn)
	waitsStarted := time.Now()
	firstFileIO, firstFileErr := c.collectFileIOCounters(ctx, conn)
	fileIOStarted := time.Now()
	firstErrors, firstErrorErr := c.collectErrorCounters(ctx, conn)
	errorsStarted := time.Now()
	firstDigests, firstDigestErr := c.collectStatementDigestCounters(ctx, conn)
	digestsStarted := time.Now()
	firstStatements, firstStatementErr := c.collectStatementCounters(ctx, conn)
	statementsStarted := time.Now()
	first, err := queryNameValue(ctx, conn, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, fmt.Errorf("collect initial global status: %w", err)
	}
	statusStarted := time.Now()

	interval := c.Interval
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	doneSample := debuglog.Start(ctx, "sampling.wait")
	timer := time.NewTimer(interval)
	select {
	case <-ctx.Done():
		timer.Stop()
		doneSample()
		return nil, ctx.Err()
	case <-timer.C:
	}
	doneSample()
	second, err := queryNameValue(ctx, conn, "SHOW GLOBAL STATUS")
	if err != nil {
		return nil, fmt.Errorf("collect final global status: %w", err)
	}
	statusElapsed := time.Since(statusStarted)
	secondStatements, secondStatementErr := c.collectStatementCounters(ctx, conn)
	statementsElapsed := time.Since(statementsStarted)
	secondDigests, secondDigestErr := c.collectStatementDigestCounters(ctx, conn)
	digestsElapsed := time.Since(digestsStarted)
	secondWaits, secondWaitErr := c.collectWaitCounters(ctx, conn)
	waitsElapsed := time.Since(waitsStarted)
	secondFileIO, secondFileErr := c.collectFileIOCounters(ctx, conn)
	fileIOElapsed := time.Since(fileIOStarted)
	secondErrors, secondErrorErr := c.collectErrorCounters(ctx, conn)
	errorsElapsed := time.Since(errorsStarted)
	if err := sampledContextError(ctx); err != nil {
		return nil, err
	}
	if c.sampleProbe(result, "wait events", firstWaitErr, secondWaitErr) {
		result.WaitEvents = deriveWaitEvents(firstWaits, secondWaits, waitsElapsed)
	}
	if c.sampleProbe(result, "file I/O", firstFileErr, secondFileErr) {
		result.FileIO = deriveFileIO(firstFileIO, secondFileIO, fileIOElapsed)
	}
	serverErrorSampleErr := sampledServerError(firstErrorErr, secondErrorErr,
		firstDigestErr, firstStatementErr, secondStatementErr, secondDigestErr, secondWaitErr, secondFileErr)
	if c.sampleProbe(result, "server errors", serverErrorSampleErr) {
		result.ServerErrors = deriveServerErrors(firstErrors, secondErrors, errorsElapsed)
	}
	if c.sampleProbe(result, "statement database time", firstDigestErr, secondDigestErr) {
		result.StatementSamples = deriveStatementSamples(firstDigests, secondDigests, digestsElapsed, c.QueryLimit)
	}
	statementSampleAvailable := c.sampleProbe(result, "statement counters", firstStatementErr, secondStatementErr)
	recordFullSampleIntervals(result, statusElapsed, waitsElapsed, fileIOElapsed, errorsElapsed, digestsElapsed, statementsElapsed)
	result.GlobalStatus = second
	result.Server.UptimeSeconds = unsigned(second["Uptime"])
	historyListLength := result.Metrics.HistoryListLength
	result.Metrics = deriveMetrics(first, second, result.Variables, statusElapsed)
	if statementSampleAvailable {
		applyStatementRates(&result.Metrics, firstStatements, secondStatements, statementsElapsed)
	}
	result.Metrics.HistoryListLength = historyListLength
	applyInstrumentationStatus(&result.Instrumentation, second)
	result.Fingerprint = fingerprint(result.Server)
	normalizeRequiredCollections(result)
	return result, nil
}

func recordFullSampleIntervals(result *model.Context, status, waits, fileIO, serverErrors, statementDigests, statementCounters time.Duration) {
	result.IntervalMillis = status.Milliseconds()
	result.SampleIntervals = model.SampleIntervals{
		GlobalStatus:      status.Milliseconds(),
		WaitEvents:        waits.Milliseconds(),
		FileIO:            fileIO.Milliseconds(),
		ServerErrors:      serverErrors.Milliseconds(),
		StatementDigests:  statementDigests.Milliseconds(),
		StatementCounters: statementCounters.Milliseconds(),
	}
}

func (c *Collector) openConnection(ctx context.Context, target Target) (*sql.DB, *sql.Conn, error) {
	defer debuglog.Start(ctx, "collect.openConnection")()
	db, err := sql.Open("mysql", target.DSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open MySQL connection: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(2 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("connect to %s:%d: %w", target.Host, target.Port, err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("reserve MySQL connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SET SESSION MAX_EXECUTION_TIME=10000"); err != nil {
		conn.Close()
		db.Close()
		return nil, nil, fmt.Errorf("pin session statement timeout: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SET SESSION transaction_read_only=ON"); err != nil {
		conn.Close()
		db.Close()
		return nil, nil, fmt.Errorf("pin session read-only: %w", err)
	}
	return db, conn, nil
}

func newContext(version string, target Target) *model.Context {
	result := &model.Context{
		SchemaVersion: model.SchemaVersion,
		ToolVersion:   version,
		CollectedAt:   time.Now().UTC(),
		Server: model.Server{
			Host:     target.Host,
			Port:     target.Port,
			Database: target.Database,
		},
	}
	normalizeRequiredCollections(result)
	return result
}

func normalizeRequiredCollections(result *model.Context) {
	if result.Findings == nil {
		result.Findings = []model.Finding{}
	}
	if result.Queries == nil {
		result.Queries = []model.Query{}
	}
	if result.Tables == nil {
		result.Tables = []model.Table{}
	}
	if result.Indexes == nil {
		result.Indexes = []model.Index{}
	}
	if result.Processes == nil {
		result.Processes = []model.Process{}
	}
	if result.ConnectionGroups == nil {
		result.ConnectionGroups = []model.ConnectionGroup{}
	}
	if result.Locks == nil {
		result.Locks = []model.LockWait{}
	}
	if result.Transactions == nil {
		result.Transactions = []model.Transaction{}
	}
	if result.MetadataLocks == nil {
		result.MetadataLocks = []model.MetadataLock{}
	}
	if result.WaitEvents == nil {
		result.WaitEvents = []model.WaitEvent{}
	}
	if result.FileIO == nil {
		result.FileIO = []model.FileIO{}
	}
	if result.ServerErrors == nil {
		result.ServerErrors = []model.ServerError{}
	}
	if result.MemoryConsumers == nil {
		result.MemoryConsumers = []model.MemoryConsumer{}
	}
	if result.StatementSamples == nil {
		result.StatementSamples = []model.StatementSample{}
	}
	if result.Capabilities == nil {
		result.Capabilities = []model.Capability{}
	}
	if result.Variables == nil {
		result.Variables = map[string]string{}
	}
	if result.GlobalStatus == nil {
		result.GlobalStatus = map[string]string{}
	}
}

func redactVariables(variables map[string]string) {
	for key, value := range variables {
		if sanitize.SensitiveName(key) && value != "" {
			variables[key] = "[redacted]"
		}
	}
}

// InspectSection collects only the probes needed to render one focused command.
// Sampling commands retain the configured interval; cumulative and point-in-time
// commands return immediately instead of paying for an unrelated full snapshot.
func (c *Collector) InspectSection(ctx context.Context, target Target, section string) (*model.Context, error) {
	defer debuglog.Start(ctx, "collect.InspectSection")()
	db, conn, err := c.openConnection(ctx, target)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	defer conn.Close()

	result := newContext(c.ToolVersion, target)
	switch section {
	case "blockers":
		if err := c.collectServer(ctx, conn, &result.Server); err != nil {
			return nil, fmt.Errorf("collect server identity: %w", err)
		}
		result.Fingerprint = fingerprint(result.Server)
		if err := c.focusedProbe(result, "row lock waits", func() error {
			var err error
			result.Locks, err = c.collectLocks(ctx, conn)
			return err
		}); err != nil {
			return nil, err
		}
		if err := c.focusedOptionalProbe(ctx, result, "active transactions", func() error {
			var err error
			result.Transactions, err = c.collectTransactions(ctx, conn)
			return err
		}); err != nil {
			return nil, err
		}
		if err := c.focusedOptionalProbe(ctx, result, "metadata locks", func() error {
			var err error
			result.MetadataLocks, err = c.collectMetadataLocks(ctx, conn)
			return err
		}); err != nil {
			return nil, err
		}
	case "queries":
		if err := c.focusedProbe(result, "statement digests", func() error {
			var probeErr error
			result.Queries, probeErr = c.collectQueries(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
		if err := c.focusedOptionalProbe(ctx, result, "process list", func() error {
			var probeErr error
			result.Processes, probeErr = c.collectProcesses(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
		if err := c.focusedOptionalProbe(ctx, result, "active query users", func() error {
			return c.collectActiveQueryUsers(ctx, conn, result.Queries)
		}); err != nil {
			return nil, err
		}
	case "tables":
		if err := c.focusedProbe(result, "table statistics", func() error {
			var probeErr error
			result.Tables, probeErr = c.collectTables(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	case "indexes":
		if err := c.focusedProbe(result, "index statistics", func() error {
			var probeErr error
			result.Indexes, probeErr = c.collectIndexes(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	case "processes":
		if err := c.focusedProbe(result, "process list", func() error {
			var probeErr error
			result.Processes, probeErr = c.collectProcesses(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	case "transactions":
		if err := c.focusedProbe(result, "active transactions", func() error {
			var probeErr error
			result.Transactions, probeErr = c.collectTransactions(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	case "locks":
		if err := c.focusedProbe(result, "row lock waits", func() error {
			var probeErr error
			result.Locks, probeErr = c.collectLocks(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	case "metadata-locks":
		if err := c.focusedProbe(result, "metadata locks", func() error {
			var probeErr error
			result.MetadataLocks, probeErr = c.collectMetadataLocks(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	case "waits":
		first, firstErr := c.collectWaitCounters(ctx, conn)
		started, err := c.waitForFocusedSample(ctx, result, "wait events", firstErr)
		if err != nil {
			return nil, err
		}
		second, secondErr := c.collectWaitCounters(ctx, conn)
		elapsed := time.Since(started)
		if err := c.focusedSampleProbe(result, "wait events", firstErr, secondErr); err != nil {
			return nil, err
		}
		result.WaitEvents = deriveWaitEvents(first, second, elapsed)
		result.IntervalMillis = elapsed.Milliseconds()
		result.SampleIntervals.WaitEvents = elapsed.Milliseconds()
	case "io":
		first, firstErr := c.collectFileIOCounters(ctx, conn)
		started, err := c.waitForFocusedSample(ctx, result, "file I/O", firstErr)
		if err != nil {
			return nil, err
		}
		second, secondErr := c.collectFileIOCounters(ctx, conn)
		elapsed := time.Since(started)
		if err := c.focusedSampleProbe(result, "file I/O", firstErr, secondErr); err != nil {
			return nil, err
		}
		result.FileIO = deriveFileIO(first, second, elapsed)
		result.IntervalMillis = elapsed.Milliseconds()
		result.SampleIntervals.FileIO = elapsed.Milliseconds()
	case "errors":
		first, firstErr := c.collectErrorCounters(ctx, conn)
		started, err := c.waitForFocusedSample(ctx, result, "server errors", firstErr)
		if err != nil {
			return nil, err
		}
		second, secondErr := c.collectErrorCounters(ctx, conn)
		elapsed := time.Since(started)
		if err := c.focusedSampleProbe(result, "server errors", firstErr, secondErr); err != nil {
			return nil, err
		}
		result.ServerErrors = deriveServerErrors(first, second, elapsed)
		result.IntervalMillis = elapsed.Milliseconds()
		result.SampleIntervals.ServerErrors = elapsed.Milliseconds()
	case "memory":
		if err := c.focusedProbe(result, "memory consumers", func() error {
			var probeErr error
			result.MemoryConsumers, probeErr = c.collectMemoryConsumers(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	case "engine":
		if err := c.collectEngineSection(ctx, conn, result); err != nil {
			return nil, err
		}
	case "coverage":
		result.Variables, err = queryNameValue(ctx, conn, "SHOW GLOBAL VARIABLES")
		if err != nil {
			return nil, fmt.Errorf("collect global variables: %w", err)
		}
		redactVariables(result.Variables)
		if err := c.focusedProbe(result, "instrumentation coverage", func() error {
			var probeErr error
			result.Instrumentation, probeErr = c.collectInstrumentation(ctx, conn, result.Variables)
			return probeErr
		}); err != nil {
			return nil, err
		}
		status, statusErr := queryNameValue(ctx, conn, "SHOW GLOBAL STATUS")
		if statusErr != nil {
			return nil, fmt.Errorf("collect global status: %w", statusErr)
		}
		applyInstrumentationStatus(&result.Instrumentation, status)
	case "variables":
		result.Variables, err = queryNameValue(ctx, conn, "SHOW GLOBAL VARIABLES")
		if err != nil {
			return nil, fmt.Errorf("collect global variables: %w", err)
		}
		redactVariables(result.Variables)
	case "replication":
		if err := c.focusedProbe(result, "replication", func() error {
			var probeErr error
			result.Replication, probeErr = c.collectReplication(ctx, conn)
			return probeErr
		}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown diagnostic section %q", section)
	}
	normalizeRequiredCollections(result)
	return result, nil
}

func (c *Collector) focusedProbe(result *model.Context, name string, fn func() error) error {
	err := fn()
	c.recordCapability(result, name, err)
	if err != nil {
		return fmt.Errorf("collect %s: %w", name, err)
	}
	return nil
}

func (c *Collector) focusedOptionalProbe(ctx context.Context, result *model.Context, name string, fn func() error) error {
	err := fn()
	c.recordCapability(result, name, err)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("collect %s: %w", name, ctxErr)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("collect %s: %w", name, err)
	}
	return nil
}

func (c *Collector) focusedSampleProbe(result *model.Context, name string, errs ...error) error {
	if c.sampleProbe(result, name, errs...) {
		return nil
	}
	for _, err := range errs {
		if err != nil {
			return fmt.Errorf("collect %s: %w", name, err)
		}
	}
	return nil
}

func (c *Collector) collectEngineSection(ctx context.Context, conn *sql.Conn, result *model.Context) error {
	defer debuglog.Start(ctx, "collect.collectEngineSection")()
	var err error
	result.Variables, err = queryNameValue(ctx, conn, "SHOW GLOBAL VARIABLES")
	if err != nil {
		result.Warnings = append(result.Warnings, "global variables unavailable: "+err.Error())
	}
	redactVariables(result.Variables)
	historyListLength := c.historyListLength(ctx, conn, result)
	firstStatements, firstStatementErr := c.collectStatementCounters(ctx, conn)
	statementStarted := time.Now()
	first, err := queryNameValue(ctx, conn, "SHOW GLOBAL STATUS")
	if err != nil {
		return fmt.Errorf("collect initial global status: %w", err)
	}
	started, err := c.waitForSample(ctx)
	if err != nil {
		return err
	}
	second, err := queryNameValue(ctx, conn, "SHOW GLOBAL STATUS")
	if err != nil {
		return fmt.Errorf("collect final global status: %w", err)
	}
	statusElapsed := time.Since(started)
	secondStatements, secondStatementErr := c.collectStatementCounters(ctx, conn)
	statementElapsed := time.Since(statementStarted)
	if err := sampledContextError(ctx); err != nil {
		return err
	}
	result.IntervalMillis = statusElapsed.Milliseconds()
	result.SampleIntervals.GlobalStatus = statusElapsed.Milliseconds()
	result.SampleIntervals.StatementCounters = statementElapsed.Milliseconds()
	result.GlobalStatus = second
	statementAvailable := c.sampleProbe(result, "statement counters", firstStatementErr, secondStatementErr)
	result.Metrics = deriveEngineMetrics(first, second, result.Variables, firstStatements, secondStatements,
		statusElapsed, statementElapsed, statementAvailable)
	result.Metrics.HistoryListLength = historyListLength
	return nil
}

func sampledContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("collect sampled counters: %w", err)
	}
	return nil
}

func sampledServerError(first, second error, enclosed ...error) error {
	if first != nil {
		return first
	}
	if second != nil {
		return second
	}
	for _, err := range enclosed {
		if err != nil {
			return fmt.Errorf("sample window contaminated by collector probe failure: %w", err)
		}
	}
	return nil
}

func deriveEngineMetrics(first, second, variables map[string]string, firstStatements, secondStatements statementCounter,
	statusElapsed, statementElapsed time.Duration, statementAvailable bool) model.Metrics {
	metrics := deriveMetrics(first, second, variables, statusElapsed)
	if statementAvailable {
		// The statement summary has its own endpoints; do not dilute status rates
		// with this query's post-snapshot latency.
		applyStatementRates(&metrics, firstStatements, secondStatements, statementElapsed)
	}
	return metrics
}

func applyStatementRates(metrics *model.Metrics, first, second statementCounter, elapsed time.Duration) {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	metrics.StatementErrorsPerSec = float64(counterDelta(first.Errors, second.Errors)) / seconds
	metrics.StatementWarningsPerSec = float64(counterDelta(first.Warnings, second.Warnings)) / seconds
}

func (c *Collector) waitForSample(ctx context.Context) (time.Time, error) {
	defer debuglog.Start(ctx, "collect.waitForSample")()
	interval := c.Interval
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	started := time.Now()
	doneSample := debuglog.Start(ctx, "sampling.wait")
	timer := time.NewTimer(interval)
	select {
	case <-ctx.Done():
		timer.Stop()
		doneSample()
		return time.Time{}, ctx.Err()
	case <-timer.C:
		doneSample()
		return started, nil
	}
}

func (c *Collector) waitForFocusedSample(ctx context.Context, result *model.Context, name string, firstErr error) (time.Time, error) {
	if firstErr != nil {
		return time.Time{}, c.focusedSampleProbe(result, name, firstErr)
	}
	return c.waitForSample(ctx)
}

func (c *Collector) probe(ctx context.Context, result *model.Context, name string, fn func() error) {
	err := fn()
	debuglog.Result(ctx, "probe."+name, err)
	c.recordCapability(result, name, err)
}

func (c *Collector) sampleProbe(result *model.Context, name string, errors ...error) bool {
	var err error
	for _, candidate := range errors {
		if candidate != nil {
			err = candidate
			break
		}
	}
	c.recordCapability(result, name, err)
	return err == nil
}

func (c *Collector) recordCapability(result *model.Context, name string, err error) {
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
	defer debuglog.Start(ctx, "collect.collectServer")()
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
	defer debuglog.Start(ctx, "collect.queryNameValue")()
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
	defer debuglog.Start(ctx, "collect.queryMaps")()
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
		QueriesPerSecond:        queries / seconds,
		TransactionsPerSecond:   (commits + rollbacks) / seconds,
		RowsReadPerSecond:       delta(first, second, "Innodb_rows_read") / seconds,
		RowsWrittenPerSecond:    (delta(first, second, "Innodb_rows_inserted") + delta(first, second, "Innodb_rows_updated") + delta(first, second, "Innodb_rows_deleted")) / seconds,
		SlowQueriesPerSecond:    perSecond("Slow_queries"),
		AbortedConnectsPerSec:   perSecond("Aborted_connects"),
		RowLockWaitsPerSecond:   perSecond("Innodb_row_lock_waits"),
		RedoWaitsPerSecond:      perSecond("Innodb_log_waits"),
		ConnectionsCurrent:      connections,
		ConnectionsMax:          maxConnections,
		ThreadsRunning:          unsigned(second["Threads_running"]),
		DataReadsPerSecond:      perSecond("Innodb_data_reads"),
		DataWritesPerSecond:     perSecond("Innodb_data_writes"),
		DataFsyncsPerSecond:     perSecond("Innodb_data_fsyncs"),
		RedoBytesPerSecond:      perSecond("Innodb_os_log_written"),
		RedoWritesPerSecond:     perSecond("Innodb_log_writes"),
		RedoFsyncsPerSecond:     perSecond("Innodb_os_log_fsyncs"),
		NetworkInBytesPerSec:    perSecond("Bytes_received"),
		NetworkOutBytesPerSec:   perSecond("Bytes_sent"),
		FullScansPerSecond:      perSecond("Select_scan"),
		SortMergePassesPerSec:   perSecond("Sort_merge_passes"),
		BufferPoolWaitsPerSec:   perSecond("Innodb_buffer_pool_wait_free"),
		PendingReads:            unsigned(second["Innodb_data_pending_reads"]),
		PendingWrites:           unsigned(second["Innodb_data_pending_writes"]),
		PendingFsyncs:           unsigned(second["Innodb_data_pending_fsyncs"]),
		BufferPoolDataBytes:     unsigned(second["Innodb_buffer_pool_bytes_data"]),
		BufferPoolDirtyBytes:    unsigned(second["Innodb_buffer_pool_bytes_dirty"]),
		RedoCurrentLSN:          unsigned(second["Innodb_redo_log_current_lsn"]),
		RedoFlushedLSN:          unsigned(second["Innodb_redo_log_flushed_to_disk_lsn"]),
		RedoCheckpointLSN:       unsigned(second["Innodb_redo_log_checkpoint_lsn"]),
		RedoCapacityBytes:       unsigned(second["Innodb_redo_log_capacity_resized"]),
		DeadlocksPerSecond:      perSecond("Innodb_deadlocks"),
		LockTimeoutsPerSecond:   perSecond("Innodb_row_lock_timeouts"),
		ThreadsCreatedPerSecond: perSecond("Threads_created"),
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
	defer debuglog.Start(ctx, "collect.historyListLength")()
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
	defer debuglog.Start(ctx, "collect.collectQueries")()
	statement := `SELECT COALESCE(DIGEST,''), COALESCE(SCHEMA_NAME,''), COALESCE(DIGEST_TEXT,''),
		COUNT_STAR, SUM_TIMER_WAIT / 1000000000, AVG_TIMER_WAIT / 1000000000,
		CASE WHEN MAX_TIMER_WAIT <= 9223372036854775807 THEN MAX_TIMER_WAIT ELSE 0 END / 1000000000,
		QUANTILE_95 / 1000000000,
		QUANTILE_99 / 1000000000, QUANTILE_999 / 1000000000,
		SUM_ROWS_EXAMINED, SUM_ROWS_SENT, SUM_ROWS_AFFECTED, SUM_ERRORS, SUM_WARNINGS,
		SUM_NO_INDEX_USED, SUM_SELECT_SCAN, SUM_CREATED_TMP_TABLES,
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
			&item.TotalLatencyMillis, &item.MeanLatencyMillis, &item.MaxLatencyMillis,
			&item.P95LatencyMillis, &item.P99LatencyMillis, &item.P999LatencyMillis,
			&item.RowsExamined, &item.RowsSent, &item.RowsAffected, &item.Errors, &item.Warnings,
			&item.NoIndexUsed, &item.FullScans, &item.TmpTables, &item.TmpDiskTables,
			&item.FirstSeen, &item.LastSeen); err != nil {
			return nil, err
		}
		item.Statement = sanitize.SQL(item.Statement)
		queries = append(queries, item)
	}
	return queries, rows.Err()
}

func (c *Collector) collectTables(ctx context.Context, conn *sql.Conn) ([]model.Table, error) {
	defer debuglog.Start(ctx, "collect.collectTables")()
	statement := `SELECT t.TABLE_SCHEMA, t.TABLE_NAME, COALESCE(t.ENGINE,''),
        COALESCE(t.TABLE_ROWS,0), COALESCE(t.DATA_LENGTH,0), COALESCE(t.INDEX_LENGTH,0),
        COALESCE(t.DATA_LENGTH + t.INDEX_LENGTH,0), COALESCE(io.COUNT_READ,0),
		COALESCE(io.COUNT_WRITE,0), COALESCE(io.COUNT_FETCH,0), COALESCE(io.COUNT_INSERT,0),
		COALESCE(io.COUNT_UPDATE,0), COALESCE(io.COUNT_DELETE,0),
		COALESCE(io.SUM_TIMER_READ,0) / 1000000000,
		COALESCE(io.SUM_TIMER_WRITE,0) / 1000000000,
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
			&item.Fetches, &item.Inserts, &item.Updates, &item.Deletes,
			&item.ReadLatencyMillis, &item.WriteLatencyMillis, &item.HasPrimaryKey,
			&item.AutoIncrement); err != nil {
			return nil, err
		}
		tables = append(tables, item)
	}
	return tables, rows.Err()
}

func (c *Collector) collectIndexes(ctx context.Context, conn *sql.Conn) ([]model.Index, error) {
	defer debuglog.Start(ctx, "collect.collectIndexes")()
	statement := `SELECT s.TABLE_SCHEMA, s.TABLE_NAME, s.INDEX_NAME,
        GROUP_CONCAT(s.COLUMN_NAME ORDER BY s.SEQ_IN_INDEX SEPARATOR ', '),
        MIN(s.NON_UNIQUE)=0, MIN(COALESCE(s.IS_VISIBLE,'YES'))='YES',
		COALESCE(MAX(s.CARDINALITY),0), COALESCE(MAX(io.COUNT_READ),0), COALESCE(MAX(io.COUNT_WRITE),0),
		COALESCE(MAX(io.SUM_TIMER_READ),0) / 1000000000,
		COALESCE(MAX(io.SUM_TIMER_WRITE),0) / 1000000000
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
			&item.Unique, &item.Visible, &item.Cardinality, &item.Reads, &item.Writes,
			&item.ReadLatencyMillis, &item.WriteLatencyMillis); err != nil {
			return nil, err
		}
		indexes = append(indexes, item)
	}
	return indexes, rows.Err()
}

func (c *Collector) collectProcesses(ctx context.Context, conn *sql.Conn) ([]model.Process, error) {
	defer debuglog.Start(ctx, "collect.collectProcesses")()
	statement := `SELECT t.PROCESSLIST_ID, t.THREAD_ID, COALESCE(t.PROCESSLIST_USER,''),
		COALESCE(t.PROCESSLIST_HOST,''), COALESCE(t.PROCESSLIST_DB,''),
		COALESCE(t.PROCESSLIST_COMMAND,''), COALESCE(t.PROCESSLIST_TIME,0),
		COALESCE(t.PROCESSLIST_STATE,''), COALESCE(es.DIGEST,''),
		COALESCE(ew.EVENT_NAME,''), COALESCE(es.TIMER_WAIT,0) / 1000000000,
		COALESCE(es.DIGEST_TEXT, t.PROCESSLIST_INFO, '')
	  FROM performance_schema.threads t
	  LEFT JOIN performance_schema.events_statements_current es ON es.THREAD_ID=t.THREAD_ID AND es.END_EVENT_ID IS NULL
	  LEFT JOIN performance_schema.events_waits_current ew ON ew.THREAD_ID=t.THREAD_ID AND ew.END_EVENT_ID IS NULL
	  WHERE t.TYPE='FOREGROUND' AND t.PROCESSLIST_ID IS NOT NULL
		AND t.PROCESSLIST_USER IS NOT NULL
		AND t.PROCESSLIST_ID <> CONNECTION_ID()
	  ORDER BY (t.PROCESSLIST_COMMAND IN ('Query','Execute')) DESC,
		t.PROCESSLIST_TIME DESC, t.PROCESSLIST_ID LIMIT 100`
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

// collectActiveQueryUsers reads current execution identities independently of the
// display's 100-session limit. Filter to the returned schema/digest pairs and
// deduplicate on the server so busy pools do not inflate the result set.
func (c *Collector) collectActiveQueryUsers(ctx context.Context, conn *sql.Conn, queries []model.Query) error {
	defer debuglog.Start(ctx, "collect.collectActiveQueryUsers")()
	var filters []string
	var args []any
	for _, query := range queries {
		if query.Digest == "" {
			continue
		}
		filters = append(filters, "(COALESCE(es.CURRENT_SCHEMA,'')=? AND es.DIGEST=?)")
		args = append(args, query.Schema, query.Digest)
	}
	if len(filters) == 0 {
		return nil
	}
	statement := `SELECT DISTINCT COALESCE(es.CURRENT_SCHEMA,''), es.DIGEST, t.PROCESSLIST_USER
	  FROM performance_schema.threads t
	  JOIN performance_schema.events_statements_current es ON es.THREAD_ID=t.THREAD_ID
	  WHERE t.TYPE='FOREGROUND' AND t.PROCESSLIST_ID IS NOT NULL
		AND t.PROCESSLIST_ID <> CONNECTION_ID()
		AND t.PROCESSLIST_COMMAND IN ('Query','Execute')
		AND t.PROCESSLIST_USER IS NOT NULL AND es.END_EVENT_ID IS NULL
		AND (` + strings.Join(filters, " OR ") + `)`
	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var executions []model.Process
	for rows.Next() {
		process := model.Process{Command: "Query"}
		if err := rows.Scan(&process.Database, &process.Digest, &process.User); err != nil {
			return err
		}
		executions = append(executions, process)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	attributeActiveUsers(queries, executions)
	return nil
}

func attributeActiveUsers(queries []model.Query, processes []model.Process) {
	users := make(map[string]map[string]bool)
	for _, process := range processes {
		if process.Digest == "" || process.User == "" || strings.EqualFold(process.Command, "Sleep") {
			continue
		}
		identity := statementDigestIdentity(process.Database, process.Digest)
		if users[identity] == nil {
			users[identity] = make(map[string]bool)
		}
		users[identity][process.User] = true
	}
	for index := range queries {
		for user := range users[statementDigestIdentity(queries[index].Schema, queries[index].Digest)] {
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
	defer debuglog.Start(ctx, "collect.collectLocks")()
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
	defer debuglog.Start(ctx, "collect.collectTransactions")()
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
	defer debuglog.Start(ctx, "collect.collectMetadataLocks")()
	statement := `SELECT ml.OWNER_THREAD_ID, COALESCE(t.PROCESSLIST_ID,0),
		COALESCE(t.PROCESSLIST_USER,''), COALESCE(t.PROCESSLIST_HOST,''),
		COALESCE(ml.OBJECT_TYPE,''), COALESCE(ml.OBJECT_SCHEMA,''),
		COALESCE(ml.OBJECT_NAME,''), COALESCE(ml.LOCK_TYPE,''),
		COALESCE(ml.LOCK_DURATION,''), COALESCE(ml.LOCK_STATUS,'')
	  FROM performance_schema.metadata_locks ml
	  LEFT JOIN performance_schema.threads t ON t.THREAD_ID=ml.OWNER_THREAD_ID
	  WHERE t.PROCESSLIST_ID IS NOT NULL AND t.PROCESSLIST_ID <> CONNECTION_ID()
		AND (ml.LOCK_STATUS='PENDING' OR COALESCE(t.PROCESSLIST_COMMAND,'') <> 'Sleep' OR EXISTS (
            SELECT 1 FROM performance_schema.metadata_locks pending
            WHERE pending.LOCK_STATUS='PENDING' AND pending.OBJECT_TYPE=ml.OBJECT_TYPE
              AND pending.OBJECT_SCHEMA <=> ml.OBJECT_SCHEMA AND pending.OBJECT_NAME <=> ml.OBJECT_NAME))
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

type waitCounter struct {
	Name, Class             string
	Count                   uint64
	TotalMillis, MeanMicros float64
	MaxMillis               float64
}

type fileIOCounter struct {
	Name, Class                            string
	Reads, Writes, BytesRead, BytesWritten uint64
	ReadMillis, WriteMillis                float64
}

type errorCounter struct {
	Number, Raised, Handled uint64
	Name, SQLState          string
	FirstSeen, LastSeen     string
}

type statementCounter struct {
	Count, Errors, Warnings uint64
}

type statementDigestCounter struct {
	Digest, Schema, Statement string
	Count                     uint64
	TotalMillis               float64
}

func (c *Collector) collectStatementDigestCounters(ctx context.Context, conn *sql.Conn) (map[string]statementDigestCounter, error) {
	defer debuglog.Start(ctx, "collect.collectStatementDigestCounters")()
	rows, err := conn.QueryContext(ctx, `SELECT COALESCE(DIGEST,''), COALESCE(SCHEMA_NAME,''),
		COALESCE(DIGEST_TEXT,''), COUNT_STAR, SUM_TIMER_WAIT / 1000000000
	  FROM performance_schema.events_statements_summary_by_digest
	  WHERE DIGEST IS NOT NULL AND DIGEST_TEXT IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]statementDigestCounter)
	for rows.Next() {
		var item statementDigestCounter
		if err := rows.Scan(&item.Digest, &item.Schema, &item.Statement, &item.Count, &item.TotalMillis); err != nil {
			return nil, err
		}
		item.Statement = sanitize.SQL(item.Statement)
		result[statementDigestIdentity(item.Schema, item.Digest)] = item
	}
	return result, rows.Err()
}

func deriveStatementSamples(first, second map[string]statementDigestCounter, elapsed time.Duration, limit int) []model.StatementSample {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	result := make([]model.StatementSample, 0)
	totalMillis := 0.0
	for identity, current := range second {
		if internalStatementSample(current.Statement) {
			continue
		}
		previous := first[identity]
		calls := counterDelta(previous.Count, current.Count)
		databaseTime := floatDelta(previous.TotalMillis, current.TotalMillis)
		if calls == 0 && databaseTime == 0 {
			continue
		}
		result = append(result, model.StatementSample{
			Digest: current.Digest, Schema: current.Schema, Statement: current.Statement, Calls: calls,
			CallsPerSecond: float64(calls) / seconds, DatabaseTimeMillis: databaseTime,
			DatabaseTimeMillisPerSecond: databaseTime / seconds,
		})
		totalMillis += databaseTime
	}
	for index := range result {
		if totalMillis > 0 {
			result[index].DatabaseTimeSharePercent = result[index].DatabaseTimeMillis * 100 / totalMillis
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DatabaseTimeMillis != result[j].DatabaseTimeMillis {
			return result[i].DatabaseTimeMillis > result[j].DatabaseTimeMillis
		}
		if result[i].Digest != result[j].Digest {
			return result[i].Digest < result[j].Digest
		}
		return result[i].Schema < result[j].Schema
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func statementDigestIdentity(schema, digest string) string {
	return schema + "\x00" + digest
}

func internalStatementSample(statement string) bool {
	canonical := strings.NewReplacer("`", "", " ", "").Replace(strings.ToUpper(statement))
	if canonical == "SHOWGLOBALSTATUS" {
		return true
	}
	if strings.Contains(canonical, "PERFORMANCE_SCHEMA.EVENTS_STATEMENTS_SUMMARY_GLOBAL_BY_EVENT_NAME") &&
		strings.Contains(canonical, "SUM_ERRORS") && strings.Contains(canonical, "SUM_WARNINGS") {
		return true
	}
	return strings.Contains(canonical, "PERFORMANCE_SCHEMA.EVENTS_STATEMENTS_SUMMARY_BY_DIGEST") &&
		strings.Contains(canonical, "SUM_TIMER_WAIT") && strings.Contains(canonical, "COUNT_STAR")
}

func (c *Collector) collectWaitCounters(ctx context.Context, conn *sql.Conn) (map[string]waitCounter, error) {
	defer debuglog.Start(ctx, "collect.collectWaitCounters")()
	statement := `SELECT EVENT_NAME,
		SUBSTRING_INDEX(SUBSTRING_INDEX(EVENT_NAME,'/',3),'/',-2), COUNT_STAR,
		SUM_TIMER_WAIT / 1000000000, AVG_TIMER_WAIT / 1000000,
		MAX_TIMER_WAIT / 1000000000
	  FROM performance_schema.events_waits_summary_global_by_event_name
	  WHERE COUNT_STAR > 0 AND EVENT_NAME <> 'idle' AND EVENT_NAME NOT LIKE 'idle/%'`
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waits := make(map[string]waitCounter)
	for rows.Next() {
		var item waitCounter
		if err := rows.Scan(&item.Name, &item.Class, &item.Count, &item.TotalMillis,
			&item.MeanMicros, &item.MaxMillis); err != nil {
			return nil, err
		}
		waits[item.Name] = item
	}
	return waits, rows.Err()
}

func deriveWaitEvents(first, second map[string]waitCounter, elapsed time.Duration) []model.WaitEvent {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	waits := make([]model.WaitEvent, 0, len(second))
	var sampleTotal float64
	for name, current := range second {
		previous := first[name]
		sampleLatency := floatDelta(previous.TotalMillis, current.TotalMillis)
		sampleTotal += sampleLatency
		waits = append(waits, model.WaitEvent{
			Name: name, Class: current.Class, Count: current.Count,
			TotalLatencyMillis: current.TotalMillis, MeanLatencyMicros: current.MeanMicros,
			MaxLatencyMillis: current.MaxMillis,
			SampleCount:      counterDelta(previous.Count, current.Count), SampleLatencyMillis: sampleLatency,
			EventsPerSecond:     float64(counterDelta(previous.Count, current.Count)) / seconds,
			WaitMillisPerSecond: sampleLatency / seconds,
		})
	}
	for index := range waits {
		if sampleTotal > 0 {
			waits[index].SampleSharePercent = waits[index].SampleLatencyMillis * 100 / sampleTotal
		}
	}
	sort.Slice(waits, func(i, j int) bool {
		if waits[i].SampleLatencyMillis != waits[j].SampleLatencyMillis {
			return waits[i].SampleLatencyMillis > waits[j].SampleLatencyMillis
		}
		return waits[i].TotalLatencyMillis > waits[j].TotalLatencyMillis
	})
	if len(waits) > 30 {
		waits = waits[:30]
	}
	return waits
}

func (c *Collector) collectFileIOCounters(ctx context.Context, conn *sql.Conn) (map[string]fileIOCounter, error) {
	defer debuglog.Start(ctx, "collect.collectFileIOCounters")()
	rows, err := conn.QueryContext(ctx, `SELECT EVENT_NAME,
		SUBSTRING_INDEX(SUBSTRING_INDEX(EVENT_NAME,'/',5),'/',-1),
		COUNT_READ, COUNT_WRITE, SUM_NUMBER_OF_BYTES_READ, SUM_NUMBER_OF_BYTES_WRITE,
		SUM_TIMER_READ / 1000000000, SUM_TIMER_WRITE / 1000000000
	  FROM performance_schema.file_summary_by_event_name
	  WHERE COUNT_READ > 0 OR COUNT_WRITE > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]fileIOCounter)
	for rows.Next() {
		var item fileIOCounter
		if err := rows.Scan(&item.Name, &item.Class, &item.Reads, &item.Writes, &item.BytesRead,
			&item.BytesWritten, &item.ReadMillis, &item.WriteMillis); err != nil {
			return nil, err
		}
		result[item.Name] = item
	}
	return result, rows.Err()
}

func deriveFileIO(first, second map[string]fileIOCounter, elapsed time.Duration) []model.FileIO {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	result := make([]model.FileIO, 0, len(second))
	for name, current := range second {
		previous := first[name]
		reads := counterDelta(previous.Reads, current.Reads)
		writes := counterDelta(previous.Writes, current.Writes)
		readMillis := floatDelta(previous.ReadMillis, current.ReadMillis)
		writeMillis := floatDelta(previous.WriteMillis, current.WriteMillis)
		item := model.FileIO{
			Name: name, Class: current.Class, Reads: current.Reads, Writes: current.Writes,
			BytesRead: current.BytesRead, BytesWritten: current.BytesWritten,
			TotalReadLatencyMillis: current.ReadMillis, TotalWriteLatencyMillis: current.WriteMillis,
			ReadsPerSecond: float64(reads) / seconds, WritesPerSecond: float64(writes) / seconds,
			ReadBytesPerSecond:  float64(counterDelta(previous.BytesRead, current.BytesRead)) / seconds,
			WriteBytesPerSecond: float64(counterDelta(previous.BytesWritten, current.BytesWritten)) / seconds,
			WaitMillisPerSecond: (readMillis + writeMillis) / seconds,
		}
		if reads > 0 {
			item.MeanReadLatencyMillis = readMillis / float64(reads)
		}
		if writes > 0 {
			item.MeanWriteLatencyMillis = writeMillis / float64(writes)
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].WaitMillisPerSecond != result[j].WaitMillisPerSecond {
			return result[i].WaitMillisPerSecond > result[j].WaitMillisPerSecond
		}
		return result[i].TotalReadLatencyMillis+result[i].TotalWriteLatencyMillis >
			result[j].TotalReadLatencyMillis+result[j].TotalWriteLatencyMillis
	})
	if len(result) > 30 {
		result = result[:30]
	}
	return result
}

func (c *Collector) collectErrorCounters(ctx context.Context, conn *sql.Conn) (map[uint64]errorCounter, error) {
	defer debuglog.Start(ctx, "collect.collectErrorCounters")()
	rows, err := conn.QueryContext(ctx, `SELECT ERROR_NUMBER, COALESCE(ERROR_NAME,''), COALESCE(SQL_STATE,''),
		SUM_ERROR_RAISED, SUM_ERROR_HANDLED, COALESCE(CAST(FIRST_SEEN AS CHAR),''),
		COALESCE(CAST(LAST_SEEN AS CHAR),'')
	  FROM performance_schema.events_errors_summary_global_by_error
	  WHERE SUM_ERROR_RAISED > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uint64]errorCounter)
	for rows.Next() {
		var item errorCounter
		if err := rows.Scan(&item.Number, &item.Name, &item.SQLState, &item.Raised, &item.Handled,
			&item.FirstSeen, &item.LastSeen); err != nil {
			return nil, err
		}
		result[item.Number] = item
	}
	return result, rows.Err()
}

func deriveServerErrors(first, second map[uint64]errorCounter, elapsed time.Duration) []model.ServerError {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	result := make([]model.ServerError, 0, len(second))
	for number, current := range second {
		previous := first[number]
		sample := counterDelta(previous.Raised, current.Raised)
		result = append(result, model.ServerError{
			Number: number, Name: current.Name, SQLState: current.SQLState,
			Raised: current.Raised, Handled: current.Handled, FirstSeen: current.FirstSeen, LastSeen: current.LastSeen,
			SampleRaised: sample, RaisedPerSecond: float64(sample) / seconds,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SampleRaised != result[j].SampleRaised {
			return result[i].SampleRaised > result[j].SampleRaised
		}
		return result[i].Raised > result[j].Raised
	})
	if len(result) > 30 {
		result = result[:30]
	}
	return result
}

func (c *Collector) collectStatementCounters(ctx context.Context, conn *sql.Conn) (statementCounter, error) {
	defer debuglog.Start(ctx, "collect.collectStatementCounters")()
	var result statementCounter
	err := conn.QueryRowContext(ctx, `SELECT COALESCE(SUM(COUNT_STAR),0), COALESCE(SUM(SUM_ERRORS),0),
		COALESCE(SUM(SUM_WARNINGS),0)
	  FROM performance_schema.events_statements_summary_global_by_event_name
	  WHERE EVENT_NAME LIKE 'statement/%'`).Scan(&result.Count, &result.Errors, &result.Warnings)
	return result, err
}

func (c *Collector) collectStatementLatency(ctx context.Context, conn *sql.Conn) (model.StatementLatency, error) {
	defer debuglog.Start(ctx, "collect.collectStatementLatency")()
	var result model.StatementLatency
	err := conn.QueryRowContext(ctx, `SELECT
		COALESCE(MIN(CASE WHEN BUCKET_QUANTILE >= 0.95 AND COUNT_BUCKET_AND_LOWER > 0 THEN BUCKET_TIMER_HIGH END),0) / 1000000000,
		COALESCE(MIN(CASE WHEN BUCKET_QUANTILE >= 0.99 AND COUNT_BUCKET_AND_LOWER > 0 THEN BUCKET_TIMER_HIGH END),0) / 1000000000,
		COALESCE(MIN(CASE WHEN BUCKET_QUANTILE >= 0.999 AND COUNT_BUCKET_AND_LOWER > 0 THEN BUCKET_TIMER_HIGH END),0) / 1000000000,
		(SELECT COALESCE(MAX(CASE WHEN MAX_TIMER_WAIT <= 9223372036854775807 THEN MAX_TIMER_WAIT END),0) / 1000000000
		 FROM performance_schema.events_statements_summary_by_digest WHERE DIGEST IS NOT NULL)
	  FROM performance_schema.events_statements_histogram_global`).Scan(
		&result.P95Millis, &result.P99Millis, &result.P999Millis, &result.MaxMillis)
	return result, err
}

func (c *Collector) collectInstrumentation(ctx context.Context, conn *sql.Conn, variables map[string]string) (model.Instrumentation, error) {
	defer debuglog.Start(ctx, "collect.collectInstrumentation")()
	result := model.Instrumentation{DigestCapacity: unsigned(variables["performance_schema_digests_size"])}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM performance_schema.events_statements_summary_by_digest
		WHERE DIGEST IS NOT NULL`).Scan(&result.DigestRows); err != nil {
		return result, err
	}
	rows, err := conn.QueryContext(ctx, `SELECT NAME FROM performance_schema.setup_consumers
		WHERE ENABLED='NO' AND NAME IN ('global_instrumentation','thread_instrumentation',
		'statements_digest','events_statements_current','events_waits_current','events_transactions_current')
		ORDER BY NAME`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return result, err
		}
		result.DisabledConsumers = append(result.DisabledConsumers, name)
	}
	if result.DigestCapacity > 0 {
		result.DigestUtilizationPercent = float64(result.DigestRows) * 100 / float64(result.DigestCapacity)
	}
	return result, rows.Err()
}

func applyInstrumentationStatus(result *model.Instrumentation, status map[string]string) {
	result.Lost = make(map[string]uint64)
	for name, raw := range status {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "performance_schema_") || !strings.HasSuffix(lower, "_lost") {
			continue
		}
		value := unsigned(raw)
		if value == 0 {
			continue
		}
		result.Lost[name] = value
		result.TotalLost += value
	}
	if len(result.Lost) == 0 {
		result.Lost = nil
	}
}

func counterDelta(first, second uint64) uint64 {
	if second < first {
		return 0
	}
	return second - first
}

func floatDelta(first, second float64) float64 {
	if second < first {
		return 0
	}
	return second - first
}

func (c *Collector) collectMemoryConsumers(ctx context.Context, conn *sql.Conn) ([]model.MemoryConsumer, error) {
	defer debuglog.Start(ctx, "collect.collectMemoryConsumers")()
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
	defer debuglog.Start(ctx, "collect.collectReplication")()
	rows, err := queryMaps(ctx, conn, "SHOW REPLICA STATUS")
	if err != nil && legacyReplicationFallback(err) {
		rows, err = queryMaps(ctx, conn, "SHOW SLAVE STATUS")
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("%d replication channels reported; channel-aware assessment is not supported", len(rows))
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
	appliers, err := queryMaps(ctx, conn, "SELECT * FROM performance_schema.replication_applier_status")
	if err != nil {
		return nil, err
	}
	if len(appliers) > 0 {
		applier := appliers[0]
		replica.ApplierState = applier["SERVICE_STATE"]
		replica.TransactionRetries = unsigned(applier["COUNT_TRANSACTIONS_RETRIES"])
		if delay := applier["REMAINING_DELAY"]; delay != "" && !strings.EqualFold(delay, "NULL") {
			parsed := signed(delay)
			replica.RemainingDelaySeconds = &parsed
		}
	}
	workers, err := queryMaps(ctx, conn, "SELECT * FROM performance_schema.replication_applier_status_by_worker")
	if err != nil {
		return nil, err
	}
	for _, worker := range workers {
		replica.Workers = append(replica.Workers, model.ReplicationWorker{
			Channel: worker["CHANNEL_NAME"], WorkerID: unsigned(worker["WORKER_ID"]),
			ThreadID: unsigned(worker["THREAD_ID"]), ServiceState: worker["SERVICE_STATE"],
			LastErrorNumber:            unsigned(worker["LAST_ERROR_NUMBER"]),
			LastErrorMessage:           sanitize.Text(worker["LAST_ERROR_MESSAGE"]),
			LastErrorTimestamp:         worker["LAST_ERROR_TIMESTAMP"],
			ApplyingTransaction:        worker["APPLYING_TRANSACTION"],
			LastAppliedTransaction:     worker["LAST_APPLIED_TRANSACTION"],
			ApplyingTransactionRetries: unsigned(worker["APPLYING_TRANSACTION_RETRIES_COUNT"]),
		})
	}
	return replica, nil
}

func legacyReplicationFallback(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1064
}

func collectInnoDBStatus(ctx context.Context, conn *sql.Conn) (string, error) {
	defer debuglog.Start(ctx, "collect.collectInnoDBStatus")()
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
