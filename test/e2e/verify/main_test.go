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

func TestVerifyFocusedDataAllowsEmptyMetadataLockArray(t *testing.T) {
	if err := verifyFocusedData("metadata-locks", []byte("[]")); err != nil {
		t.Fatal(err)
	}
}
