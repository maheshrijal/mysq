package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	var dsn string
	var duration time.Duration
	var workers int
	flag.StringVar(&dsn, "dsn", "loadgen:mysq-load-test@tcp(127.0.0.1:33306)/app?parseTime=true", "test MySQL DSN")
	flag.DurationVar(&duration, "duration", 45*time.Second, "load duration")
	flag.IntVar(&workers, "workers", 8, "concurrent OLTP workers")
	flag.Parse()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(workers + 8)
	db.SetMaxIdleConns(workers + 8)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	if err := db.PingContext(setupCtx); err != nil {
		log.Fatal(err)
	}
	seed(setupCtx, db)
	setupCancel()

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var operations atomic.Uint64
	var wg sync.WaitGroup
	lockReady := make(chan error, 1)
	wg.Add(1)
	go func() { defer wg.Done(); holdRowLock(ctx, db, 35*time.Second, lockReady) }()
	if err := <-lockReady; err != nil {
		log.Fatalf("establish row-lock fixture: %v", err)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			work(ctx, db, worker, &operations)
		}(i)
	}
	wg.Add(3)
	statementReady := make(chan error, 1)
	errorReady := make(chan error, 1)
	go func() { defer wg.Done(); waitOnRowLock(ctx, db) }()
	go func() { defer wg.Done(); runLongStatement(ctx, db, 35, statementReady) }()
	go func() { defer wg.Done(); generateServerErrors(ctx, db, errorReady) }()
	if err := <-statementReady; err != nil {
		log.Fatalf("establish long-statement metadata-lock fixture: %v", err)
	}
	if err := <-errorReady; err != nil {
		log.Fatalf("establish server-error fixture: %v", err)
	}
	fmt.Println("load ready")
	wg.Wait()
	fmt.Printf("load complete: %d operations\n", operations.Load())
}

func seed(ctx context.Context, db *sql.DB) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `INSERT IGNORE INTO accounts(id,email,balance) VALUES(?,?,?)`)
	if err != nil {
		log.Fatal(err)
	}
	for i := 1; i <= 500; i++ {
		if _, err := statement.ExecContext(ctx, i, fmt.Sprintf("account-%d@example.test", i), 1000); err != nil {
			log.Fatal(err)
		}
	}
	_ = statement.Close()
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	for i := 0; i < 2500; i++ {
		_, err := db.ExecContext(ctx, `INSERT INTO orders(account_id,status,amount,payload) VALUES(?,?,?,JSON_OBJECT('source','e2e','sequence',?))`,
			1+rand.IntN(500), []string{"pending", "paid", "shipped", "cancelled"}[rand.IntN(4)], 1+rand.IntN(10000), i)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func work(ctx context.Context, db *sql.DB, worker int, operations *atomic.Uint64) {
	statuses := []string{"pending", "paid", "shipped", "cancelled"}
	for ctx.Err() == nil {
		account := 1 + rand.IntN(500)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		_, err = tx.ExecContext(ctx, `UPDATE accounts SET balance=balance+? WHERE id=?`, rand.IntN(20)-10, account)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO orders(account_id,status,amount,payload) VALUES(?,?,?,JSON_OBJECT('worker',?,'token',UUID()))`,
				account, statuses[rand.IntN(len(statuses))], 1+rand.IntN(10000), worker)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(account_id,event_type,message) VALUES(?, 'order', CONCAT('worker-', ?))`, account, worker)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		var sum sql.NullFloat64
		_ = db.QueryRowContext(ctx, `SELECT SUM(amount) FROM orders WHERE status=? AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.source'))='missing'`, statuses[rand.IntN(len(statuses))]).Scan(&sum)
		if operations.Add(1)%100 == 0 {
			rows, queryErr := db.QueryContext(ctx, `SELECT status, COUNT(*), SUM(amount) FROM orders GROUP BY status ORDER BY SUM(amount) DESC`)
			if queryErr == nil {
				for rows.Next() {
					var status string
					var count int
					var amount float64
					_ = rows.Scan(&status, &count, &amount)
				}
				_ = rows.Close()
			}
		}
	}
}

func holdRowLock(ctx context.Context, db *sql.DB, duration time.Duration, ready chan<- error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		ready <- err
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET balance=balance+1 WHERE id=1`); err != nil {
		_ = tx.Rollback()
		ready <- err
		return
	}
	ready <- nil
	timer := time.NewTimer(duration)
	select {
	case <-ctx.Done():
		timer.Stop()
		_ = tx.Rollback()
	case <-timer.C:
		_ = tx.Commit()
	}
}

func waitOnRowLock(ctx context.Context, db *sql.DB) {
	timer := time.NewTimer(800 * time.Millisecond)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}
	_, _ = db.ExecContext(ctx, `UPDATE accounts SET balance=balance-1 WHERE id=1`)
}

func runLongStatement(ctx context.Context, db *sql.DB, seconds int, ready chan<- error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		ready <- err
		return
	}
	defer tx.Rollback()

	// Keep a granted TABLE metadata lock for the lifetime of the long statement.
	// Signaling only after the table read makes the focused metadata-lock E2E
	// evidence deterministic instead of depending on goroutine scheduling.
	var rows int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&rows); err != nil {
		ready <- err
		return
	}
	ready <- nil

	var ignored int
	if err := tx.QueryRowContext(ctx, `SELECT SLEEP(?)`, seconds).Scan(&ignored); err != nil {
		return
	}
	_ = tx.Commit()
}

func generateServerErrors(ctx context.Context, db *sql.DB, ready chan<- error) {
	trigger := func() error {
		// Account 1 is deliberately row-locked by a separate fixture; use account
		// 2 so error generation cannot block readiness behind that transaction.
		_, err := db.ExecContext(ctx, `INSERT INTO accounts(id,email,balance) VALUES(2,'duplicate@example.test',0)`)
		if err == nil {
			return fmt.Errorf("duplicate-key statement unexpectedly succeeded")
		}
		return nil
	}
	if err := trigger(); err != nil {
		ready <- err
		return
	}
	ready <- nil
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = trigger()
		}
	}
}
