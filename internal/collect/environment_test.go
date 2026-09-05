package collect

import (
	"strings"
	"testing"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func TestDBOPSCredentialsAndConnectionPrecedence(t *testing.T) {
	for _, name := range []string{"MYSQ_DATABASE_URL", "MYSQLDOT_DATABASE_URL", "DATABASE_URL"} {
		t.Setenv(name, "")
	}
	const password = "p@ss:/?#%$'\"\\ +雪\n"
	t.Setenv("DBOPS_MYSQL_USER", "shell_monitor")
	t.Setenv("DBOPS_MYSQL_PWD", password)
	for _, tc := range []struct{ raw, host, db, user, pwd string }{
		{"db.example/app", "db.example", "app", "shell_monitor", password},
		{"db.example:3307/app", "db.example", "app", "shell_monitor", password},
		{"mysql://db.example:3307/app?tls=true", "db.example", "app", "shell_monitor", password},
		{"tcp(db.example:3307)/app", "db.example", "app", "shell_monitor", password},
		{"mysql://explicit:own@db.example/app", "db.example", "app", "explicit", "own"},
		{"explicit:own@tcp(db.example:3307)/app", "db.example", "app", "explicit", "own"},
		{"mysql://explicit@db.example/app", "db.example", "app", "explicit", ""},
		{"explicit:@tcp(db.example:3307)/app", "db.example", "app", "explicit", ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			target, err := ResolveConnection(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := mysqlDriver.ParseDSN(target.DSN)
			if err != nil {
				t.Fatal(err)
			}
			if target.Host != tc.host || target.Database != tc.db || cfg.User != tc.user || cfg.Passwd != tc.pwd {
				t.Fatal("endpoint or credentials changed")
			}
		})
	}
	t.Setenv("MYSQ_DATABASE_URL", "configured:own@tcp(configured-db:3306)/app")
	target, err := ResolveConnection("")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := mysqlDriver.ParseDSN(target.DSN)
	if target.Host != "configured-db" || cfg.User != "configured" || cfg.Passwd != "own" {
		t.Fatal("shell credentials replaced explicit environment connection")
	}
	target, err = ResolveConnection("mysql://argument-db/app")
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "argument-db" {
		t.Fatal("environment URL replaced positional endpoint")
	}
}

func TestEndpointValidationAndTLS(t *testing.T) {
	t.Setenv("DBOPS_MYSQL_USER", "monitor")
	t.Setenv("DBOPS_MYSQL_PWD", "do-not-print-this-password")
	target, err := ResolveConnection("[::1]:3307/app%20db?tls=true")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mysqlDriver.ParseDSN(target.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "[::1]:3307" || cfg.DBName != "app db" || cfg.TLSConfig != "true" {
		t.Fatal("endpoint/TLS options lost")
	}
	for _, raw := range []string{"host:0/app", "host:65536/app", "host:bad/app", "mysql:///app"} {
		if _, err := ResolveConnection(raw); err == nil || strings.Contains(err.Error(), "do-not-print") {
			t.Fatal("invalid endpoint accepted or credentials leaked")
		}
	}
	for _, name := range []string{"MYSQ_DATABASE_URL", "MYSQLDOT_DATABASE_URL", "DATABASE_URL"} {
		t.Setenv(name, "")
	}
	if _, err := ResolveConnection(""); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatal("credentials alone silently selected a server")
	}
	t.Setenv("DBOPS_MYSQL_USER", "")
	if _, err := ResolveConnection("db.example/app"); err == nil || !strings.Contains(err.Error(), "DBOPS_MYSQL_USER") {
		t.Fatal("missing credentials lacked setup guidance")
	}
}
