package collect

import (
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"github.com/maheshrijal/mysqldot/internal/model"
)

func TestResolveConnectionURLPreservesDriverOptions(t *testing.T) {
	target, err := ResolveConnection("mysql://observer:p%40ss@db.example:3307/app%20db?tls=true&timeout=3s&charset=utf8mb4")
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "db.example" || target.Port != 3307 || target.Database != "app db" {
		t.Fatalf("unexpected target: %+v", target)
	}
	cfg, err := mysqlDriver.ParseDSN(target.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Passwd != "p@ss" || cfg.TLSConfig != "true" || cfg.Timeout != 3*time.Second || !strings.Contains(target.DSN, "charset=utf8mb4") {
		t.Fatalf("unexpected DSN config: %+v", cfg)
	}
}

func TestResolveConnectionRejectsInvalidPort(t *testing.T) {
	if _, err := ResolveConnection("mysql://observer@localhost:70000/app"); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestDeriveMetricsRemovesSamplingQuery(t *testing.T) {
	first := map[string]string{"Questions": "100", "Innodb_rows_read": "10", "Innodb_data_reads": "50", "Innodb_os_log_written": "1000"}
	second := map[string]string{
		"Questions": "101", "Innodb_rows_read": "30", "Innodb_data_reads": "54", "Innodb_os_log_written": "2024",
		"Innodb_redo_log_current_lsn": "5000", "Innodb_redo_log_checkpoint_lsn": "4000",
		"Innodb_redo_log_capacity_resized": "10000", "Innodb_data_pending_reads": "2",
	}
	metrics := deriveMetrics(first, second, map[string]string{}, time.Second)
	if metrics.QueriesPerSecond != 0 || metrics.RowsReadPerSecond != 20 || metrics.DataReadsPerSecond != 4 ||
		metrics.RedoBytesPerSecond != 1024 || metrics.RedoCheckpointAgeBytes != 1000 || metrics.RedoCheckpointAgePct != 10 || metrics.PendingReads != 2 {
		t.Fatalf("unexpected rates: %+v", metrics)
	}
}

func TestAttributeActiveUsersToDigestWithoutGuessing(t *testing.T) {
	queries := []model.Query{{Digest: "A"}, {Digest: "B"}}
	processes := []model.Process{
		{Digest: "A", User: "worker", Command: "Query"},
		{Digest: "A", User: "api", Command: "Query"},
		{Digest: "A", User: "api", Command: "Query"},
		{Digest: "B", User: "reporting", Command: "Sleep"},
	}
	attributeActiveUsers(queries, processes)
	if got := strings.Join(queries[0].ActiveUsers, ","); got != "api,worker" {
		t.Fatalf("active users = %q, want api,worker", got)
	}
	if len(queries[1].ActiveUsers) != 0 {
		t.Fatalf("sleeping user was attributed to digest: %+v", queries[1].ActiveUsers)
	}
}

func TestSummarizeProcessesByUserHostAndPair(t *testing.T) {
	groups := summarizeProcesses([]model.Process{
		{User: "app", Host: "10.0.0.1:5000", Command: "Query", Statement: "SELECT ?"},
		{User: "app", Host: "10.0.0.1:5001", Command: "Sleep"},
		{User: "worker", Host: "10.0.0.2:6000", Command: "Connect"},
	})
	want := map[string]model.ConnectionGroup{
		"user:app":                  {Kind: "user", Key: "app", Total: 2, Active: 1, Sleeping: 1},
		"host:10.0.0.1":             {Kind: "host", Key: "10.0.0.1", Total: 2, Active: 1, Sleeping: 1},
		"user_host:worker@10.0.0.2": {Kind: "user_host", Key: "worker@10.0.0.2", Total: 1, Other: 1},
	}
	for _, group := range groups {
		key := group.Kind + ":" + group.Key
		if expected, ok := want[key]; ok {
			if group != expected {
				t.Fatalf("group %s = %+v, want %+v", key, group, expected)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing groups: %+v", want)
	}
}
