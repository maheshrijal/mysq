package main

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

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
