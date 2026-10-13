package adt

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

// The package on a message class PUT.
//
// With a transport request on the URI, SAP's UPDATE_MESSAGE/DELETE run a CTS
// check per message that uses the package taken from the PUT body, and drop
// the write without an error when it fails. The body must therefore repeat the
// package, read from the GET that already happens inside the lock window.

// msagGetXMLInPackage is msagGetXML with the <adtcore:packageRef> a real GET
// carries: a direct child of the root, before the messages (checked against a
// real response, see TestMessageClass_ReadsThePackageRefOfARealResponse).
func msagGetXMLInPackage(pkg, description string, messages map[string]string) string {
	plain := msagGetXML(description, messages)
	root := strings.Index(plain, "<mc:messageClass")
	i := root + strings.Index(plain[root:], ">")
	return plain[:i+1] + `<adtcore:packageRef adtcore:name="` + pkg + `"/>` + plain[i+1:]
}

func TestMessageClassWriteBody_PackageRefPrecedesTheMessages(t *testing.T) {
	mc := newMessageClassWriteBody("ZDEMO_MC", "Demo", "ES", "ZDEMO_PKG")
	mc.Messages = []messageClassWriteMessage{{Number: "001", Text: "Hola"}}
	b, err := xml.Marshal(mc)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	ref := strings.Index(body, `<adtcore:packageRef adtcore:name="ZDEMO_PKG"`)
	msgs := strings.Index(body, `<mc:messages`)
	if ref < 0 || msgs < 0 || ref > msgs {
		t.Errorf("packageRef must be present and come before mc:messages:\n%s", body)
	}
}

func TestMessageClassWriteBody_NoPackageNoElement(t *testing.T) {
	mc := newMessageClassWriteBody("ZDEMO_MC", "Demo", "ES", "")
	b, err := xml.Marshal(mc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "packageRef") {
		t.Errorf("an empty package must not produce an element:\n%s", b)
	}
}

func TestMessageClass_ReadsThePackageRef(t *testing.T) {
	var mc MessageClass
	if err := xml.Unmarshal([]byte(msagGetXMLInPackage("ZDEMO_PKG", "Desc", map[string]string{"001": "Hola"})), &mc); err != nil {
		t.Fatal(err)
	}
	if mc.PackageRef.Name != "ZDEMO_PKG" || mc.Description != "Desc" || len(mc.Messages) != 1 {
		t.Errorf("unexpected parse: package %q, description %q, %d messages", mc.PackageRef.Name, mc.Description, len(mc.Messages))
	}
}

func TestWriteMessageClassTexts_TransportableClassSendsItsPackage(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXMLInPackage("ZDEMO_PKG", "Desc ES", map[string]string{"001": "Hola"}), "", 0),
		WithAllowTransportableEdits())

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES",
		[]MessageClassMessage{{Number: "001", Text: "Hola"}}, nil, "HANDLE", "TR-EXAMPLE"); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `<adtcore:packageRef adtcore:name="ZDEMO_PKG"`) {
		t.Errorf("PUT should carry the class's package, got %v", put.bodies)
	}
	if put.corrNrs[0] != "TR-EXAMPLE" {
		t.Errorf("PUT corrNr = %q, want TR-EXAMPLE", put.corrNrs[0])
	}
}

// The package comes from the GET that was already there: the lock window keeps
// the same shape and stays stateful (issue #91).
func TestWriteMessageClassTextsAutoLock_PackageAddsNoRequestInsideTheWindow(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXMLInPackage("ZDEMO_PKG", "Desc ES", map[string]string{"001": "Hola"}), "", 0),
		WithAllowTransportableEdits())

	if err := client.WriteMessageClassTextsAutoLock(context.Background(), "ZDEMO_MC", "ES",
		[]MessageClassMessage{{Number: "001", Text: "Hola"}}, nil, "TR-EXAMPLE"); err != nil {
		t.Fatalf("WriteMessageClassTextsAutoLock: %v", err)
	}
	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	unlockAt := indexOfCall(calls, isUnlock)
	if lockAt < 0 || unlockAt < lockAt {
		t.Fatalf("expected LOCK then UNLOCK; trace:\n%v", calls)
	}
	assertWindowStateful(t, calls, lockAt, unlockAt)
	for _, c := range calls[lockAt:unlockAt] {
		if strings.Contains(c.path, "informationsystem/search") || strings.Contains(c.path, "cts/") {
			t.Errorf("a lookup was added inside the lock window: %s %s", c.method, c.path)
		}
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `adtcore:name="ZDEMO_PKG"`) {
		t.Errorf("PUT should carry ZDEMO_PKG, got %v", put.bodies)
	}
}

// Local class: the package read ($TMP) is sent too, and there is still no corrNr.
func TestWriteMessageClassTexts_LocalClassSendsTmpAndNoCorrNr(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXMLInPackage("$TMP", "Desc ES", map[string]string{"001": "Hola"}), "", 0))

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES",
		[]MessageClassMessage{{Number: "001", Text: "Hola"}}, nil, "HANDLE", ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `adtcore:name="$TMP"`) {
		t.Errorf("PUT should carry $TMP, got %v", put.bodies)
	}
	if put.corrNrs[0] != "" {
		t.Errorf("a local class must not send corrNr, got %q", put.corrNrs[0])
	}
}

// Language with no row yet (404), then the retry answers: the package is taken
// from the retry — it does not depend on the language.
func TestWriteMessageClassTexts_PackageTakenFromTheFallbackGet(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXMLInPackage("ZDEMO_PKG", "Desc ES", map[string]string{"001": "Hola"}), "", http.StatusNotFound),
		WithAllowTransportableEdits())

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "Hello"}}, nil, "HANDLE", "TR-EXAMPLE"); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `adtcore:name="ZDEMO_PKG"`) {
		t.Errorf("PUT should carry the package from the fallback GET, got %v", put.bodies)
	}
}

// With a request and no package to send, the PUT would be dropped silently by
// SAP: refuse before sending, and give the lock back.
func TestWriteMessageClassTextsAutoLock_TransportWithoutPackageSendsNoPut(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/messageclass/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	err := client.WriteMessageClassTextsAutoLock(context.Background(), "ZDEMO_MC", "ES",
		[]MessageClassMessage{{Number: "001", Text: "Hola"}}, nil, "TR-EXAMPLE")
	if err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("expected a refusal before the PUT, got %v", err)
	}
	put.mu.Lock()
	n := len(put.bodies)
	put.mu.Unlock()
	if n != 0 {
		t.Errorf("a PUT was sent without a package: %d", n)
	}
	if indexOfCall(rec.snapshot(), isUnlock) < 0 {
		t.Error("the lock was not released after the refusal")
	}
}

// Without a request the package is not required: a failed GET still writes, as before.
func TestWriteMessageClassTexts_NoTransportDoesNotRequireThePackage(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/messageclass/") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	})
	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES",
		[]MessageClassMessage{{Number: "001", Text: "Hola"}}, nil, "HANDLE", ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || strings.Contains(put.bodies[0], "packageRef") {
		t.Errorf("expected one PUT without packageRef, got %v", put.bodies)
	}
}

func TestWriteMessageClassTexts_TransportableDeleteSendsItsPackage(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXMLInPackage("ZDEMO_PKG", "Desc ES", map[string]string{"001": "Hola"}), "", 0),
		WithAllowTransportableEdits())

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES",
		nil, []string{"009"}, "HANDLE", "TR-EXAMPLE"); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 ||
		!strings.Contains(put.bodies[0], `adtcore:name="ZDEMO_PKG"`) ||
		!strings.Contains(put.bodies[0], `<mc:deletedmessages mc:msgno="009"`) {
		t.Errorf("the delete PUT should carry the package and the plural element, got %v", put.bodies)
	}
}

func TestCreateMessageClass_InitialMessagesPutCarriesThePackage(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	err := client.CreateMessageClass(context.Background(), CreateMessageClassOptions{
		Name:      "ZDEMO_MC",
		Package:   "ZDEMO_PKG",
		Language:  "ES",
		Transport: "TR-EXAMPLE",
		Messages:  []MessageClassMessage{{Number: "001", Text: "Hola"}},
	})
	if err != nil {
		t.Fatalf("CreateMessageClass: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `<adtcore:packageRef adtcore:name="ZDEMO_PKG"`) {
		t.Errorf("initial-messages PUT should carry the package, got %v", put.bodies)
	}
}

// The error names the CTS check only when a request was in play.
func TestVerifyMessageClassWrite_ExplainsTheCtsCauseOnlyWithATransport(t *testing.T) {
	stale := func() (*putBodyRecorder, *Client) {
		put := &putBodyRecorder{}
		rec := &adtRecorder{}
		return put, newStubbedClient(t, rec, languageAwareMessageClass(put,
			msagGetXMLInPackage("ZDEMO_PKG", "Desc ES", map[string]string{"001": "old"}), "", 0),
			WithAllowTransportableEdits())
	}
	texts := []MessageClassMessage{{Number: "001", Text: "new"}}

	_, withTR := stale()
	err := withTR.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES", texts, nil, "HANDLE", "TR-EXAMPLE")
	if err == nil || !strings.Contains(err.Error(), "LIMU MESS") || !strings.Contains(err.Error(), "TR-EXAMPLE") ||
		!strings.Contains(err.Error(), "ZDEMO_PKG") {
		t.Errorf("with a transport the error should explain the CTS cause, got %v", err)
	}

	_, without := stale()
	err = without.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES", texts, nil, "HANDLE", "")
	if err == nil || strings.Contains(err.Error(), "LIMU MESS") {
		t.Errorf("without a transport the error must stay as it was, got %v", err)
	}
}

// The shape captured from a real GET (ADT, S/4HANA on-premise), sanitised: the
// package is a direct child of the root, before the messages, with uri/type
// next to the name.
func TestMessageClass_ReadsThePackageRefOfARealResponse(t *testing.T) {
	const real = `<?xml version="1.0" encoding="utf-8"?><mc:messageClass adtcore:masterLanguage="ES" adtcore:name="ZDEMO_MC" adtcore:type="MSAG/N" adtcore:version="active" adtcore:description="Demo" adtcore:language="ES" xmlns:mc="http://www.sap.com/adt/MessageClass" xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:packageRef adtcore:uri="/sap/bc/adt/packages/zdemo_pkg" adtcore:type="DEVC/K" adtcore:name="ZDEMO_PKG"/><mc:messages mc:msgno="001" mc:msgtext="Hola" mc:selfexplainatory="true" mc:documented="false" adtcore:name=""><atom:link href="/sap/bc/adt/messageclass/zdemo_mc/messages/001" rel="http://www.sap.com/adt/relations/messageclasses/messages" xmlns:atom="http://www.w3.org/2005/Atom"/></mc:messages></mc:messageClass>`
	var mc MessageClass
	if err := xml.Unmarshal([]byte(real), &mc); err != nil {
		t.Fatal(err)
	}
	if mc.PackageRef.Name != "ZDEMO_PKG" || mc.Description != "Demo" || len(mc.Messages) != 1 || mc.Messages[0].Text != "Hola" {
		t.Errorf("unexpected parse: %+v", mc)
	}
}
