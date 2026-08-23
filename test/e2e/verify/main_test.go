package main

import "testing"

func TestVerifyFocusedDataRejectsNullExceptForReplication(t *testing.T) {
	for _, section := range []string{"queries", "tables", "indexes", "processes", "transactions", "locks", "metadata-locks", "waits", "io", "errors", "memory", "engine", "coverage", "variables"} {
		if err := verifyFocusedData(section, []byte("null")); err == nil {
			t.Fatalf("%s unexpectedly accepted null", section)
		}
	}
	if err := verifyFocusedData("replication", []byte("null")); err != nil {
		t.Fatalf("replication null should describe a non-replica: %v", err)
	}
}

func TestVerifyFocusedDataRequiresFixtureMetadataLock(t *testing.T) {
	if err := verifyFocusedData("metadata-locks", []byte("[]")); err == nil {
		t.Fatal("empty metadata-lock output unexpectedly passed")
	}
	data := []byte(`[{"thread_id":1,"process_id":2,"object_type":"TABLE","schema":"app","object":"accounts","lock_type":"SHARED_READ","duration":"TRANSACTION","status":"GRANTED"}]`)
	if err := verifyFocusedData("metadata-locks", data); err != nil {
		t.Fatalf("typed fixture metadata lock was rejected: %v", err)
	}
}

func TestVerifyFocusedDataRejectsSectionMisroutes(t *testing.T) {
	for _, test := range []struct {
		name, section, data string
	}{
		{name: "tables as metadata locks", section: "metadata-locks", data: `[{"schema":"app","name":"accounts","engine":"InnoDB"}]`},
		{name: "memory as waits", section: "waits", data: `[{"name":"memory/sql","current_bytes":1,"high_bytes":2,"allocations":1}]`},
		{name: "waits as file io", section: "io", data: `[{"name":"wait/io/file","class":"io/file","count":1,"sample_count":1}]`},
		{name: "tables as indexes", section: "indexes", data: `[{"schema":"app","name":"accounts","engine":"InnoDB"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := verifyFocusedData(test.section, []byte(test.data)); err == nil {
				t.Fatalf("%s unexpectedly accepted misrouted output: %s", test.section, test.data)
			}
		})
	}
}

func TestVerifyFocusedDataRequiresIOSampleEvidence(t *testing.T) {
	if err := verifyFocusedData("io", []byte(`[{"name":"file","class":"io/file"}]`)); err == nil {
		t.Fatal("cumulative-only file I/O unexpectedly passed")
	}
	if err := verifyFocusedData("io", []byte(`[{"name":"file","class":"io/file","writes_per_second":1}]`)); err != nil {
		t.Fatalf("sampled file I/O was rejected: %v", err)
	}
	if err := verifyFocusedData("io", []byte(`[{"name":"file","class":"io/file"},{"writes_per_second":1}]`)); err == nil {
		t.Fatal("split file I/O identity and sample evidence unexpectedly passed")
	}
}

func TestVerifyFocusedDataRequiresErrorSampleEvidence(t *testing.T) {
	if err := verifyFocusedData("errors", []byte(`[{"number":1062,"name":"ER_DUP_ENTRY","sql_state":"23000"}]`)); err == nil {
		t.Fatal("cumulative-only server error unexpectedly passed")
	}
	if err := verifyFocusedData("errors", []byte(`[{"number":1062,"name":"ER_DUP_ENTRY","sql_state":"23000","sample_raised":1}]`)); err != nil {
		t.Fatalf("sampled server error was rejected: %v", err)
	}
	if err := verifyFocusedData("errors", []byte(`[{"number":1062,"name":"ER_DUP_ENTRY","sql_state":"23000"},{"sample_raised":1}]`)); err == nil {
		t.Fatal("split server error identity and sample evidence unexpectedly passed")
	}
}
