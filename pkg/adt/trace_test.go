package adt

import (
	"context"
	"testing"
)

func TestNormalizeTraceID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "bare GUID unchanged",
			id:   "022F8D0E956911F1807502000A14930A",
			want: "022F8D0E956911F1807502000A14930A",
		},
		{
			name: "full URI as returned by ListTraces is stripped to the GUID",
			id:   "/sap/bc/adt/runtime/traces/abaptraces/022F8D0E956911F1807502000A14930A",
			want: "022F8D0E956911F1807502000A14930A",
		},
		{
			name: "unrelated path is left as-is",
			id:   "/some/other/path/022F8D0E956911F1807502000A14930A",
			want: "/some/other/path/022F8D0E956911F1807502000A14930A",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeTraceID(tc.id); got != tc.want {
				t.Errorf("normalizeTraceID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

// realHitlistFixture is a trimmed (2-entry) capture of a real ADT response for
// GET .../abaptraces/{id}/hitlist, taken from a live S/4HANA system on
// 2026-08-11. Field names/shape must match this exactly, not the schema
// previously assumed by parseTraceAnalysis (which used flat attributes that
// don't exist in the real response and produced empty entries).
const realHitlistFixture = `<?xml version="1.0" encoding="utf-8"?><trc:hitlist xmlns:trc="http://www.sap.com/adt/runtime/traces/abaptraces"><atom:link rel="parent" href="/sap/bc/adt/runtime/traces/abaptraces/022F8D0E956911F1807502000A14930A" xmlns:atom="http://www.w3.org/2005/Atom"/><trc:entry topDownIndex="1" index="3731" hitCount="52" recursionDepth="0" description="DB: Exec  ZTCA_AUTH_STAD" dbAccessAnchor="74"><trc:callingProgram adtcore:context="ZCL_AUTHORITY=================CP" byteCodeOffset="418" adtcore:uri="/sap/bc/adt/oo/classes/zcl_authority/source/main#start=295" adtcore:type="CLAS/OC" adtcore:name="ZCL_AUTHORITY" xmlns:adtcore="http://www.sap.com/adt/core"/><trc:calledProgram adtcore:context="" xmlns:adtcore="http://www.sap.com/adt/core"/><trc:grossTime time="211752569" percentage="98.469"/><trc:traceEventNetTime time="211752569" percentage="98.469"/><trc:proceduralNetTime time="211752569" percentage="98.469"/></trc:entry><trc:entry topDownIndex="2" index="3756" hitCount="14" recursionDepth="0" description="DB: Fetch  ZTSU_VALES" dbAccessAnchor="77"><trc:callingProgram adtcore:context="ZRSU_AFE_TRIANGULACION" byteCodeOffset="2301" objectReferenceQuery="/sap/bc/adt/runtime/traces/abaptraces/objectReferences?context=ZRSU_AFE_TRIANGULACION&amp;byteCodeOffset=2301" xmlns:adtcore="http://www.sap.com/adt/core"/><trc:calledProgram adtcore:context="" xmlns:adtcore="http://www.sap.com/adt/core"/><trc:grossTime time="2253650" percentage="1.048"/><trc:traceEventNetTime time="2253650" percentage="1.048"/><trc:proceduralNetTime time="2253650" percentage="1.048"/></trc:entry></trc:hitlist>`

func TestParseTraceAnalysis_Hitlist(t *testing.T) {
	analysis, err := parseTraceAnalysis([]byte(realHitlistFixture), "022F8D0E956911F1807502000A14930A", "hitlist")
	if err != nil {
		t.Fatalf("parseTraceAnalysis returned error: %v", err)
	}
	if len(analysis.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(analysis.Entries))
	}

	got := analysis.Entries[0]
	want := TraceEntry{
		Program:    "ZCL_AUTHORITY=================CP",
		Event:      "DB: Exec  ZTCA_AUTH_STAD",
		GrossTime:  211752569,
		NetTime:    211752569,
		Calls:      52,
		Percentage: 98.469,
	}
	if got != want {
		t.Errorf("entry[0] = %+v, want %+v", got, want)
	}

	if analysis.TotalCalls != 66 { // 52 + 14
		t.Errorf("TotalCalls = %d, want 66", analysis.TotalCalls)
	}
}

func TestParseTraceAnalysis_UnverifiedToolTypesReturnEmpty(t *testing.T) {
	// "statements" and "dbAccesses" schemas have not been verified against a
	// live system, so parseTraceAnalysis must not guess at their shape.
	for _, toolType := range []string{"statements", "dbAccesses"} {
		analysis, err := parseTraceAnalysis([]byte(realHitlistFixture), "GUID", toolType)
		if err != nil {
			t.Fatalf("toolType=%s: unexpected error: %v", toolType, err)
		}
		if len(analysis.Entries) != 0 {
			t.Errorf("toolType=%s: got %d entries, want 0 (unimplemented)", toolType, len(analysis.Entries))
		}
	}
}

// realSQLTraceStateFixture is a real capture of GET .../st05/trace/state
// (Accept: application/vnd.sap.adt.perf.trace.state.v1+xml), taken from a
// live S/4HANA system on 2026-08-12 right after the SQL trace had been
// switched off again. Field names/shape must match this exactly, not the
// flat <traceState active="..."> schema previously assumed by
// parseSQLTraceState (which doesn't exist in the real response).
const realSQLTraceStateFixture = `<?xml version="1.0" encoding="utf-8"?><ts:traceStateInstanceTable xmlns:ts="http://www.sap.com/adt/tools/performance/tracestate"><ts:traceStateInstance><ts:instance>srvdevsaps4d_S4D_00</ts:instance><ts:host>srvdevsaps4d</ts:host><ts:isLocal>true</ts:isLocal><ts:isSelected>false</ts:isSelected><ts:modificationUser>ZRCHAPADO</ts:modificationUser><ts:modificationDateTime>2026-08-12T06:53:42Z</ts:modificationDateTime><ts:traceTypes><ts:sqlOn>false</ts:sqlOn><ts:bufOn>false</ts:bufOn><ts:enqOn>false</ts:enqOn><ts:rfcOn>false</ts:rfcOn><ts:httpOn>false</ts:httpOn><ts:apcOn>false</ts:apcOn><ts:amcOn>false</ts:amcOn><ts:authOn>false</ts:authOn></ts:traceTypes><ts:traceProperties><ts:includeMissingTableNameOn>false</ts:includeMissingTableNameOn><ts:authErrorsOnly>false</ts:authErrorsOnly><ts:stackTraceOn>false</ts:stackTraceOn><ts:includedTables/><ts:excludedTables/></ts:traceProperties><ts:traceFilter><ts:traceUser/><ts:transactionCode/><ts:program/><ts:rfcFunction/><ts:url/><ts:wpId/></ts:traceFilter></ts:traceStateInstance></ts:traceStateInstanceTable>`

func TestParseSQLTraceState(t *testing.T) {
	state, err := parseSQLTraceState([]byte(realSQLTraceStateFixture))
	if err != nil {
		t.Fatalf("parseSQLTraceState returned error: %v", err)
	}
	if len(state.Instances) != 1 {
		t.Fatalf("got %d instances, want 1", len(state.Instances))
	}

	got := state.Instances[0]
	if got.Instance != "srvdevsaps4d_S4D_00" {
		t.Errorf("Instance = %q, want %q", got.Instance, "srvdevsaps4d_S4D_00")
	}
	if got.Host != "srvdevsaps4d" {
		t.Errorf("Host = %q, want %q", got.Host, "srvdevsaps4d")
	}
	if !got.IsLocal {
		t.Errorf("IsLocal = false, want true")
	}
	if got.ModificationUser != "ZRCHAPADO" {
		t.Errorf("ModificationUser = %q, want %q", got.ModificationUser, "ZRCHAPADO")
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
