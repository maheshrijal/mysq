package collect

import (
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func TestFullConnectionStrings(t *testing.T) {
	t.Setenv("DBOPS_MYSQL_USER", "shell_user")
	t.Setenv("DBOPS_MYSQL_PWD", "shell_password")
	for _, raw := range []string{
		"mysql://explicit:p%40ss%3A%2F%3F%23@db.example:3307/app%20db?tls=true&timeout=3s&readTimeout=4s&writeTimeout=5s&charset=utf8mb4&loc=Asia%2FKolkata",
		"explicit:p@ss:/?#@tcp(db.example:3307)/app%20db?tls=true&timeout=3s&readTimeout=4s&writeTimeout=5s&charset=utf8mb4&loc=Asia%2FKolkata",
	} {
		target, err := ResolveConnection(raw)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := mysqlDriver.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.User != "explicit" || cfg.Passwd != "p@ss:/?#" || cfg.Addr != "db.example:3307" || cfg.DBName != "app db" ||
			cfg.TLSConfig != "true" || cfg.Timeout != 3*time.Second || cfg.ReadTimeout != 4*time.Second ||
			cfg.WriteTimeout != 5*time.Second || cfg.Loc.String() != "Asia/Kolkata" || !strings.Contains(target.DSN, "charset=utf8mb4") {
			t.Fatal("full connection string lost credentials or driver options")
		}
	}
	for _, raw := range []string{"explicit:own@unix(/var/run/mysqld/mysqld.sock)/app", "unix(/var/run/mysqld/mysqld.sock)/app"} {
		target, err := ResolveConnection(raw)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := mysqlDriver.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		wantUser, wantPassword := "shell_user", "shell_password"
		if strings.HasPrefix(raw, "explicit:") {
			wantUser, wantPassword = "explicit", "own"
		}
		if cfg.Net != "unix" || cfg.Addr != "/var/run/mysqld/mysqld.sock" || cfg.DBName != "app" || cfg.User != wantUser || cfg.Passwd != wantPassword {
			t.Fatal("Unix socket connection lost its address or credentials")
		}
	}
}

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
