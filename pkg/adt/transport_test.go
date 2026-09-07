package adt

import (
	"testing"
)

func TestParseUserTransports(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"
         xmlns:atom="http://www.w3.org/2005/Atom">
  <tm:workbench>
    <tm:target tm:name="PROD">
      <tm:modifiable>
        <tm:request tm:number="DEVK900001" tm:owner="DEVELOPER" tm:desc="Test Transport" tm:status="D">
          <tm:task tm:number="DEVK900002" tm:owner="DEVELOPER" tm:desc="Task 1" tm:status="D">
            <tm:abap_object tm:pgmid="R3TR" tm:type="CLAS" tm:name="ZCL_TEST" tm:obj_info="Class ZCL_TEST"/>
          </tm:task>
        </tm:request>
      </tm:modifiable>
      <tm:released>
        <tm:request tm:number="DEVK900010" tm:owner="DEVELOPER" tm:desc="Released Transport" tm:status="R"/>
      </tm:released>
    </tm:target>
  </tm:workbench>
  <tm:customizing>
    <tm:target tm:name="QA">
      <tm:modifiable>
        <tm:request tm:number="CSTK900001" tm:owner="CUSTADMIN" tm:desc="Customizing Request" tm:status="D"/>
      </tm:modifiable>
      <tm:released/>
    </tm:target>
  </tm:customizing>
</tm:root>`

	result, err := parseUserTransports([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseUserTransports failed: %v", err)
	}

	// Check workbench requests
	if len(result.Workbench) != 2 {
		t.Fatalf("expected 2 workbench requests, got %d", len(result.Workbench))
	}

	// Check first workbench request (modifiable)
	wb1 := result.Workbench[0]
	if wb1.Number != "DEVK900001" {
		t.Errorf("expected number 'DEVK900001', got '%s'", wb1.Number)
	}
	if wb1.Owner != "DEVELOPER" {
		t.Errorf("expected owner 'DEVELOPER', got '%s'", wb1.Owner)
	}
	if wb1.Description != "Test Transport" {
		t.Errorf("expected description 'Test Transport', got '%s'", wb1.Description)
	}
	if wb1.Status != "D" {
		t.Errorf("expected status 'D', got '%s'", wb1.Status)
	}
	if wb1.Target != "PROD" {
		t.Errorf("expected target 'PROD', got '%s'", wb1.Target)
	}
	if wb1.Type != "workbench" {
		t.Errorf("expected type 'workbench', got '%s'", wb1.Type)
	}

	// Check tasks
	if len(wb1.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(wb1.Tasks))
	}
	task := wb1.Tasks[0]
	if task.Number != "DEVK900002" {
		t.Errorf("expected task number 'DEVK900002', got '%s'", task.Number)
	}

	// Check task objects
	if len(task.Objects) != 1 {
		t.Fatalf("expected 1 object in task, got %d", len(task.Objects))
	}
	obj := task.Objects[0]
	if obj.PGMID != "R3TR" {
		t.Errorf("expected PGMID 'R3TR', got '%s'", obj.PGMID)
	}
	if obj.Type != "CLAS" {
		t.Errorf("expected type 'CLAS', got '%s'", obj.Type)
	}
	if obj.Name != "ZCL_TEST" {
		t.Errorf("expected name 'ZCL_TEST', got '%s'", obj.Name)
	}

	// Check second workbench request (released)
	wb2 := result.Workbench[1]
	if wb2.Number != "DEVK900010" {
		t.Errorf("expected number 'DEVK900010', got '%s'", wb2.Number)
	}
	if wb2.Status != "R" {
		t.Errorf("expected status 'R', got '%s'", wb2.Status)
	}

	// Check customizing requests
	if len(result.Customizing) != 1 {
		t.Fatalf("expected 1 customizing request, got %d", len(result.Customizing))
	}

	cust := result.Customizing[0]
	if cust.Number != "CSTK900001" {
		t.Errorf("expected number 'CSTK900001', got '%s'", cust.Number)
	}
	if cust.Type != "customizing" {
		t.Errorf("expected type 'customizing', got '%s'", cust.Type)
	}
}

func TestParseUserTransportsEmpty(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:workbench/>
  <tm:customizing/>
</tm:root>`

	result, err := parseUserTransports([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseUserTransports failed: %v", err)
	}

	if len(result.Workbench) != 0 {
		t.Errorf("expected 0 workbench requests, got %d", len(result.Workbench))
	}
	if len(result.Customizing) != 0 {
		t.Errorf("expected 0 customizing requests, got %d", len(result.Customizing))
	}
}

func TestParseTransportInfo(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0">
  <asx:values>
    <DATA>
      <PGMID>R3TR</PGMID>
      <OBJECT>CLAS</OBJECT>
      <OBJECTNAME>ZCL_TEST</OBJECTNAME>
      <OPERATION>I</OPERATION>
      <DEVCLASS>$TMP</DEVCLASS>
      <RECORDING>X</RECORDING>
    </DATA>
  </asx:values>
</asx:abap>`

	result, err := parseTransportInfo([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseTransportInfo failed: %v", err)
	}

	if result.PGMID != "R3TR" {
		t.Errorf("expected PGMID 'R3TR', got '%s'", result.PGMID)
	}
	if result.Object != "CLAS" {
		t.Errorf("expected Object 'CLAS', got '%s'", result.Object)
	}
	if result.ObjectName != "ZCL_TEST" {
		t.Errorf("expected ObjectName 'ZCL_TEST', got '%s'", result.ObjectName)
	}
	if result.Operation != "I" {
		t.Errorf("expected Operation 'I', got '%s'", result.Operation)
	}
	if result.DevClass != "$TMP" {
		t.Errorf("expected DevClass '$TMP', got '%s'", result.DevClass)
	}
	if result.Recording != "X" {
		t.Errorf("expected Recording 'X', got '%s'", result.Recording)
	}
}

func TestParseReleaseResult(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"
         xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <tm:releasereports>
    <chkrun:checkReport chkrun:reporter="CTS" chkrun:status="released">
      <chkrun:checkMessageList>
        <chkrun:checkMessage chkrun:type="I" chkrun:shortText="Transport released successfully"/>
        <chkrun:checkMessage chkrun:type="W" chkrun:shortText="Some objects were ignored"/>
      </chkrun:checkMessageList>
    </chkrun:checkReport>
    <chkrun:checkReport chkrun:reporter="SYNTAX" chkrun:status="passed"/>
  </tm:releasereports>
</tm:root>`

	messages, err := parseReleaseResult([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseReleaseResult failed: %v", err)
	}

	// Should have 4 messages: 2 report statuses + 2 check messages
	if len(messages) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(messages), messages)
	}

	// Check first report status
	if messages[0] != "[CTS] Status: released" {
		t.Errorf("expected '[CTS] Status: released', got '%s'", messages[0])
	}

	// Check first message
	if messages[1] != "  [I] Transport released successfully" {
		t.Errorf("expected '  [I] Transport released successfully', got '%s'", messages[1])
	}

	// Check second message
	if messages[2] != "  [W] Some objects were ignored" {
		t.Errorf("expected '  [W] Some objects were ignored', got '%s'", messages[2])
	}

	// Check second report status
	if messages[3] != "[SYNTAX] Status: passed" {
		t.Errorf("expected '[SYNTAX] Status: passed', got '%s'", messages[3])
	}
}

func TestParseReleaseResultEmpty(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
</tm:root>`

	messages, err := parseReleaseResult([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseReleaseResult failed: %v", err)
	}

	if len(messages) != 0 {
		t.Errorf("expected 0 messages for empty result, got %d", len(messages))
	}
}

func TestTransportTypes(t *testing.T) {
	// Test TransportRequest
	tr := TransportRequest{
		Number:      "DEVK900001",
		Owner:       "DEVELOPER",
		Description: "Test Transport",
		Status:      "D",
		Target:      "PROD",
		Type:        "workbench",
	}

	if tr.Number != "DEVK900001" {
		t.Errorf("TransportRequest.Number mismatch")
	}

	// Test TransportTask
	task := TransportTask{
		Number:      "DEVK900002",
		Owner:       "DEVELOPER",
		Description: "Task 1",
		Status:      "D",
	}

	if task.Number != "DEVK900002" {
		t.Errorf("TransportTask.Number mismatch")
	}

	// Test TransportObject
	obj := TransportObject{
		PGMID:   "R3TR",
		Type:    "CLAS",
		Name:    "ZCL_TEST",
		ObjInfo: "Class ZCL_TEST",
	}

	if obj.PGMID != "R3TR" {
		t.Errorf("TransportObject.PGMID mismatch")
	}

	// Test TransportInfo
	info := TransportInfo{
		PGMID:        "R3TR",
		Object:       "CLAS",
		ObjectName:   "ZCL_TEST",
		Operation:    "I",
		DevClass:     "$TMP",
		Recording:    "X",
		LockedByUser: "DEVELOPER",
		LockedInTask: "DEVK900002",
	}

	if info.LockedByUser != "DEVELOPER" {
		t.Errorf("TransportInfo.LockedByUser mismatch")
	}
}
