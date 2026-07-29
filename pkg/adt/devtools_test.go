package adt

import (
	"testing"
)

// --- stripXMLNamespaces ---

func TestStripXMLNamespaces_RemovesDeclarations(t *testing.T) {
	input := `<root xmlns:adtcomp="http://www.sap.com/adt/activation" xmlns:adtcore="http://www.sap.com/adt/core"><adtcomp:msg adtcomp:type="E"/></root>`
	got := string(stripXMLNamespaces([]byte(input)))
	for _, bad := range []string{"xmlns:", "adtcomp:", "adtcore:"} {
		if idx := indexStr(got, bad); idx >= 0 {
			t.Errorf("output still contains %q at pos %d: %s", bad, idx, got)
		}
	}
}

func TestStripXMLNamespaces_PreservesURLsInValues(t *testing.T) {
	// http:// inside attribute values must not be modified
	input := `<root xmlns:ns="http://example.com"><ns:link ns:href="http://example.com/path"/></root>`
	got := string(stripXMLNamespaces([]byte(input)))
	if indexStr(got, "http://example.com/path") < 0 {
		t.Errorf("URL in attribute value was stripped: %s", got)
	}
}

func indexStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// --- parseActivationResult ---

func TestParseActivationResult_EmptyBody_Success(t *testing.T) {
	result, err := parseActivationResult([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("empty body should mean success")
	}
}

func TestParseActivationResult_NamespacedErrorMessage_DetectsFailure(t *testing.T) {
	// SAP ADT response with adtcomp: namespace prefix — previously undetected due to namespace bug
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<adtcomp:activationLog xmlns:adtcomp="http://www.sap.com/adt/activation" xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcomp:inactiveObjects>
    <adtcomp:entry>
      <adtcomp:object>
        <adtcore:ref adtcore:uri="/sap/bc/adt/programs/includes/ztest_f01" adtcore:type="PROG/I" adtcore:name="ZTEST_F01"/>
      </adtcomp:object>
    </adtcomp:entry>
  </adtcomp:inactiveObjects>
  <adtcomp:messages>
    <adtcomp:msg adtcomp:type="E" adtcomp:objDescr="ZTEST_F01" adtcomp:line="5">
      <adtcomp:shortText>
        <adtcomp:txt>Syntax error: "." expected.</adtcomp:txt>
      </adtcomp:shortText>
    </adtcomp:msg>
  </adtcomp:messages>
</adtcomp:activationLog>`

	result, err := parseActivationResult([]byte(xmlData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("should be failure: SAP returned E-type message and inactive object")
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	if result.Messages[0].Type != "E" {
		t.Errorf("expected message type 'E', got %q", result.Messages[0].Type)
	}
	if result.Messages[0].ShortText == "" {
		t.Error("expected non-empty short text")
	}
	if len(result.Inactive) == 0 {
		t.Fatal("expected at least one inactive object")
	}
	if result.Inactive[0].Name != "ZTEST_F01" {
		t.Errorf("expected inactive name 'ZTEST_F01', got %q", result.Inactive[0].Name)
	}
}

func TestParseActivationResult_MessagesOnlyNoInactive_DetectsFailure(t *testing.T) {
	// Some SAP versions return messages without inactiveObjects for include errors
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<adtcomp:activationLog xmlns:adtcomp="http://www.sap.com/adt/activation">
  <adtcomp:messages>
    <adtcomp:msg adtcomp:type="E" adtcomp:line="3">
      <adtcomp:shortText>
        <adtcomp:txt>Syntax error in include.</adtcomp:txt>
      </adtcomp:shortText>
    </adtcomp:msg>
  </adtcomp:messages>
</adtcomp:activationLog>`

	result, err := parseActivationResult([]byte(xmlData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("should be failure: E-type message present")
	}
	if result.Messages[0].Type != "E" {
		t.Errorf("expected type 'E', got %q", result.Messages[0].Type)
	}
}

func TestParseActivationResult_ChklMessages_DetectsFailure(t *testing.T) {
	// Actual SAP S/4HANA on-prem response format: chkl:messages root element.
	// Previously BOTH vsp and mcp-abap-abap-adt-api silently reported success.
	xmlData := `<?xml version="1.0" encoding="utf-8"?><chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><chkl:properties checkExecuted="true" activationExecuted="false" generationExecuted="false"/><msg objDescr="" type="W" line="0" href=""><shortText><txt>Activation was cancelled.</txt><txt>"Tratamiento cancelado" (EU 202)</txt></shortText></msg><msg objDescr="Programa ZPROG_WITH_INCL" type="E" line="1" href="/sap/bc/adt/programs/programs/zprog_with_incl/source/main#start=2,8" forceSupported="true"><shortText><txt>INCLUDE report "ZRCG1_INCL" not found.</txt></shortText></msg></chkl:messages>`

	result, err := parseActivationResult([]byte(xmlData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("should be failure: activationExecuted=false and E-type message")
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages (W + E), got %d", len(result.Messages))
	}
	if result.Messages[0].Type != "W" {
		t.Errorf("expected first message type 'W', got %q", result.Messages[0].Type)
	}
	if result.Messages[1].Type != "E" {
		t.Errorf("expected second message type 'E', got %q", result.Messages[1].Type)
	}
	if result.Messages[1].ShortText == "" {
		t.Error("expected non-empty short text for E message")
	}
	if result.Messages[1].ObjDescr == "" {
		t.Error("expected non-empty objDescr for E message")
	}
	if result.Messages[1].Line != 1 {
		t.Errorf("expected line 1, got %d", result.Messages[1].Line)
	}
}

func TestParseActivationResult_ChklMessages_SuccessWhenActivated(t *testing.T) {
	// chkl:messages with activationExecuted="true" and no error messages = real success.
	xmlData := `<?xml version="1.0" encoding="utf-8"?><chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><chkl:properties checkExecuted="true" activationExecuted="true" generationExecuted="true"/></chkl:messages>`

	result, err := parseActivationResult([]byte(xmlData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("should be success: activationExecuted=true and no error messages")
	}
	if len(result.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result.Messages))
	}
}

func TestParseActivationResult_InactiveObjectsRoot_DetectsFailure(t *testing.T) {
	// SAP can return <ioc:inactiveObjects> directly as the response ROOT (no
	// enclosing <activationLog> and no <chkl:messages> wrapper). Before this fix,
	// neither Format A (activationLog) nor Format B (chkl:messages) branches
	// recognized this shape, so doc.Msgs/doc.Entries stayed empty and activation
	// was wrongly reported as successful.
	xmlData := `<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/abapxml/inactiveCtsObjects" xmlns:adtcore="http://www.sap.com/adt/core"><ioc:entry><ioc:object><ioc:ref adtcore:uri="/sap/bc/adt/programs/programs/ztest" adtcore:type="PROG/P" adtcore:name="ZTEST"/></ioc:object><ioc:transport/></ioc:entry></ioc:inactiveObjects>`

	result, err := parseActivationResult([]byte(xmlData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected Success=false when inactive objects are returned as root")
	}
	if len(result.Inactive) != 1 {
		t.Fatalf("expected 1 inactive object, got %d", len(result.Inactive))
	}
	if result.Inactive[0].Name != "ZTEST" {
		t.Errorf("expected inactive object name ZTEST, got %q", result.Inactive[0].Name)
	}
	if result.Inactive[0].Type != "PROG/P" {
		t.Errorf("expected inactive object type PROG/P, got %q", result.Inactive[0].Type)
	}
}

// --- parseSyntaxCheckResults ---

func TestParseSyntaxCheckResults_NamespacedResponse_ParsesErrors(t *testing.T) {
	// Verify that stripping all namespaces (not just chkrun:) works correctly
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun" xmlns:adtcore="http://www.sap.com/adt/core">
  <chkrun:checkReport chkrun:reporter="abapCheckRun">
    <chkrun:checkMessageList>
      <chkrun:checkMessage chkrun:uri="/sap/bc/adt/programs/includes/ztest_f01/source/main#start=5,1" chkrun:type="E" chkrun:shortText="Syntax error: period expected."/>
      <chkrun:checkMessage chkrun:uri="/sap/bc/adt/programs/includes/ztest_f01/source/main#start=8,3" chkrun:type="W" chkrun:shortText="Unused variable LV_X."/>
    </chkrun:checkMessageList>
  </chkrun:checkReport>
</chkrun:checkRunReports>`

	results, err := parseSyntaxCheckResults([]byte(xmlData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Severity != "E" {
		t.Errorf("expected severity 'E', got %q", results[0].Severity)
	}
	if results[0].Line != 5 {
		t.Errorf("expected line 5, got %d", results[0].Line)
	}
	if results[1].Severity != "W" {
		t.Errorf("expected severity 'W', got %q", results[1].Severity)
	}
}

func TestParseInactiveObjects(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/activation/inactiveobjects"
    xmlns:adtcore="http://www.sap.com/adt/core">
  <ioc:entry>
    <ioc:object ioc:user="DEVELOPER" ioc:deleted="false">
      <ioc:ref adtcore:uri="/sap/bc/adt/oo/classes/ZCL_TEST"
               adtcore:type="CLAS/OC"
               adtcore:name="ZCL_TEST"
               adtcore:parentUri="/sap/bc/adt/packages/$TMP"/>
    </ioc:object>
  </ioc:entry>
  <ioc:entry>
    <ioc:object ioc:user="DEVELOPER" ioc:deleted="true">
      <ioc:ref adtcore:uri="/sap/bc/adt/programs/programs/ZTEST"
               adtcore:type="PROG/P"
               adtcore:name="ZTEST"/>
    </ioc:object>
    <ioc:transport ioc:user="TRANSPORT_USER" ioc:deleted="false">
      <ioc:ref adtcore:uri="/sap/bc/adt/cts/transportrequests/DEVK900001"
               adtcore:type="TASK"
               adtcore:name="DEVK900001"/>
    </ioc:transport>
  </ioc:entry>
</ioc:inactiveObjects>`

	result, err := parseInactiveObjects([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseInactiveObjects failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	// Check first entry (class without transport)
	entry1 := result[0]
	if entry1.Object == nil {
		t.Fatal("expected first entry to have object")
	}
	if entry1.Object.Name != "ZCL_TEST" {
		t.Errorf("expected name 'ZCL_TEST', got '%s'", entry1.Object.Name)
	}
	if entry1.Object.Type != "CLAS/OC" {
		t.Errorf("expected type 'CLAS/OC', got '%s'", entry1.Object.Type)
	}
	if entry1.Object.User != "DEVELOPER" {
		t.Errorf("expected user 'DEVELOPER', got '%s'", entry1.Object.User)
	}
	if entry1.Object.Deleted {
		t.Error("expected first object not to be deleted")
	}
	if entry1.Transport != nil {
		t.Error("expected first entry to have no transport")
	}

	// Check second entry (program with transport, marked deleted)
	entry2 := result[1]
	if entry2.Object == nil {
		t.Fatal("expected second entry to have object")
	}
	if entry2.Object.Name != "ZTEST" {
		t.Errorf("expected name 'ZTEST', got '%s'", entry2.Object.Name)
	}
	if !entry2.Object.Deleted {
		t.Error("expected second object to be deleted")
	}
	if entry2.Transport == nil {
		t.Fatal("expected second entry to have transport")
	}
	if entry2.Transport.Name != "DEVK900001" {
		t.Errorf("expected transport name 'DEVK900001', got '%s'", entry2.Transport.Name)
	}
}

func TestParseInactiveObjectsEmpty(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/activation/inactiveobjects">
</ioc:inactiveObjects>`

	result, err := parseInactiveObjects([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseInactiveObjects failed: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected 0 entries, got %d", len(result))
	}
}

func TestParseInactiveObjectsEmptyResponse(t *testing.T) {
	result, err := parseInactiveObjects([]byte{})
	if err != nil {
		t.Fatalf("parseInactiveObjects failed: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected 0 entries, got %d", len(result))
	}
}
