package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/go-sql-driver/mysql"
)

var errPermanentWorkload = errors.New("permanent workload failure")
var driverSequence atomic.Uint64

func TestWorkReportsUnexpectedBeginFailure(t *testing.T) {
	db, err := sql.Open("mysql", "loadgen:password@tcp(127.0.0.1:1)/app")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var operations atomic.Uint64
	if err := work(context.Background(), db, 0, &operations); err == nil {
		t.Fatal("closed database BeginTx failure was silently accepted")
	}
}

func TestValidateWorkerCount(t *testing.T) {
	for _, workers := range []int{-1, 0} {
		if err := validateWorkerCount(workers); err == nil {
			t.Fatalf("worker count %d unexpectedly passed", workers)
		}
	}
	if err := validateWorkerCount(1); err != nil {
		t.Fatalf("valid worker count failed: %v", err)
	}
}

func TestWorkReportsPermanentTransactionFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		failCommit bool
	}{
		{name: "statement"},
		{name: "commit", failCommit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			driverName := fmt.Sprintf("workload-failure-%s-%d", test.name, driverSequence.Add(1))
			sql.Register(driverName, failingDriver{failCommit: test.failCommit})
			db, err := sql.Open(driverName, "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			var operations atomic.Uint64
			err = work(context.Background(), db, 0, &operations)
			if !errors.Is(err, errPermanentWorkload) {
				t.Fatalf("permanent %s failure was not reported: %v", test.name, err)
			}
			if operations.Load() != 0 {
				t.Fatalf("failed workload recorded %d successful operations", operations.Load())
			}
		})
	}
}

func TestRetryableWorkloadError(t *testing.T) {
	for _, number := range []uint16{1205, 1213} {
		if !retryableWorkloadError(&mysql.MySQLError{Number: number}) {
			t.Fatalf("MySQL error %d should be retryable", number)
		}
	}
	if retryableWorkloadError(&mysql.MySQLError{Number: 1062}) {
		t.Fatal("duplicate-key error unexpectedly retryable")
	}
}

type failingDriver struct {
	failCommit bool
}

func (d failingDriver) Open(string) (driver.Conn, error) {
	return &failingConn{failCommit: d.failCommit}, nil
}

type failingConn struct {
	failCommit bool
}

func (*failingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}

func (*failingConn) Close() error { return nil }

func (c *failingConn) Begin() (driver.Tx, error) {
	return failingTx{failCommit: c.failCommit}, nil
}

func (c *failingConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if !c.failCommit {
		return nil, errPermanentWorkload
	}
	return driver.RowsAffected(1), nil
}

type failingTx struct {
	failCommit bool
}

func (tx failingTx) Commit() error {
	if tx.failCommit {
		return errPermanentWorkload
	}
	return nil
}

func (failingTx) Rollback() error { return nil }
