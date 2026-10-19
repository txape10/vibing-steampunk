package adt

import (
	"context"
	"testing"
)

// realSQLTraceStateFixture is a real capture of GET .../st05/trace/state
// (Accept: application/vnd.sap.adt.perf.trace.state.v1+xml), taken from a
// live S/4HANA system on 2026-08-12 right after the SQL trace had been
// switched off again. Field names/shape must match this exactly, not the
// flat <traceState active="..."> schema previously assumed by
// parseSQLTraceState (which doesn't exist in the real response).
const realSQLTraceStateFixture = `<?xml version="1.0" encoding="utf-8"?><ts:traceStateInstanceTable xmlns:ts="http://www.sap.com/adt/tools/performance/tracestate"><ts:traceStateInstance><ts:instance>devhost_DEV_00</ts:instance><ts:host>devhost</ts:host><ts:isLocal>true</ts:isLocal><ts:isSelected>false</ts:isSelected><ts:modificationUser>TESTUSER</ts:modificationUser><ts:modificationDateTime>2026-08-12T06:53:42Z</ts:modificationDateTime><ts:traceTypes><ts:sqlOn>false</ts:sqlOn><ts:bufOn>false</ts:bufOn><ts:enqOn>false</ts:enqOn><ts:rfcOn>false</ts:rfcOn><ts:httpOn>false</ts:httpOn><ts:apcOn>false</ts:apcOn><ts:amcOn>false</ts:amcOn><ts:authOn>false</ts:authOn></ts:traceTypes><ts:traceProperties><ts:includeMissingTableNameOn>false</ts:includeMissingTableNameOn><ts:authErrorsOnly>false</ts:authErrorsOnly><ts:stackTraceOn>false</ts:stackTraceOn><ts:includedTables/><ts:excludedTables/></ts:traceProperties><ts:traceFilter><ts:traceUser/><ts:transactionCode/><ts:program/><ts:rfcFunction/><ts:url/><ts:wpId/></ts:traceFilter></ts:traceStateInstance></ts:traceStateInstanceTable>`

func TestParseSQLTraceState(t *testing.T) {
	state, err := parseSQLTraceState([]byte(realSQLTraceStateFixture))
	if err != nil {
		t.Fatalf("parseSQLTraceState returned error: %v", err)
	}
	if len(state.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(state.Instances))
	}

	got := state.Instances[0]
	if got.Instance != "devhost_DEV_00" {
		t.Errorf("Instance = %q, want %q", got.Instance, "devhost_DEV_00")
	}
	if got.Host != "devhost" {
		t.Errorf("Host = %q, want %q", got.Host, "devhost")
	}
	if !got.IsLocal {
		t.Errorf("IsLocal = false, want true")
	}
	if got.ModificationUser != "TESTUSER" {
		t.Errorf("ModificationUser = %q, want %q", got.ModificationUser, "TESTUSER")
	}
	if got.Active {
		t.Errorf("Active = true, want false (all trace types off in fixture)")
	}
	if got.TraceTypes.SQLOn {
		t.Errorf("TraceTypes.SQLOn = true, want false")
	}
}

func TestListSQLTraces_FailsWithoutCallingSAP(t *testing.T) {
	// ADT's ST05 directory endpoint returns a Fiori UI link, not
	// machine-readable trace data (verified live 2026-08-12) — this must
	// fail immediately and never hit the network.
	client := &Client{}
	err := client.ListSQLTraces(context.Background())
	if err == nil {
		t.Fatal("ListSQLTraces returned nil error, want an explanatory error")
	}
}
