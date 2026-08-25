package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHarnessUsesIsolatedComposeProjectAndPort(t *testing.T) {
	data, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, expected := range []string{
		`requested_port="${MYSQ_MYSQL_PORT:-0}"`,
		`--project-name "$project"`,
		`port mysql 3306`,
		`MYSQ_MYSQL_PORT="$requested_port" "${compose[@]}" down`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("E2E harness lost isolation contract %q", expected)
		}
	}
	if strings.Contains(script, `MYSQ_MYSQL_PORT:-33306`) {
		t.Fatal("E2E harness restored a shared default host port")
	}
}

func TestHarnessOnlyPausesRecurringHealthcheckForIdleProbe(t *testing.T) {
	runData, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	composeData, err := os.ReadFile("../../docker-compose.e2e.yml")
	if err != nil {
		t.Fatal(err)
	}
	runScript := string(runData)
	compose := string(composeData)
	for _, expected := range []string{
		`docker exec "$mysql_container" touch /tmp/mysq-health-paused`,
		`docker exec "$mysql_container" rm -f /tmp/mysq-health-paused`,
	} {
		if !strings.Contains(runScript, expected) {
			t.Fatalf("E2E harness lost bounded healthcheck pause %q", expected)
		}
	}
	if !strings.Contains(compose, `test -f /tmp/mysq-health-paused || mysqladmin ping`) {
		t.Fatal("MySQL fixture healthcheck is not recurring outside the bounded idle probe")
	}
	if strings.Contains(compose, "mysq-health-ready") {
		t.Fatal("MySQL fixture restored a permanently green readiness sentinel")
	}
}
