package collect

import (
	"database/sql"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// OpenDatabase marks every connection opened by diagnostics, telemetry, or
// control. ResolveConnection remains neutral so parsing a DSN never labels an
// application's connection as mysq.
func OpenDatabase(target Target) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(target.DSN)
	if err != nil {
		return nil, err
	}
	attrs := []string{"program_name:mysq"}
	for _, attr := range strings.Split(cfg.ConnectionAttributes, ",") {
		if attr != "" && !strings.HasPrefix(attr, "program_name:") {
			attrs = append(attrs, attr)
		}
	}
	cfg.ConnectionAttributes = strings.Join(attrs, ",")
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

const excludeMysqSessions = `NOT EXISTS (SELECT 1 FROM performance_schema.session_connect_attrs ca
 WHERE ca.PROCESSLIST_ID=t.PROCESSLIST_ID AND ca.ATTR_NAME='program_name' AND ca.ATTR_VALUE='mysq')`
