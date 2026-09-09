package adt

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

func TestGetObjectTextsInLanguage(t *testing.T) {
	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newTestResponse("REPORT ztest.\nWRITE 'Bonjour'."),
			"discovery": newTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	content, err := client.GetObjectTextsInLanguage(context.Background(), "/sap/bc/adt/programs/programs/ZTEST/source/main", "FR")
	if err != nil {
		t.Fatalf("GetObjectTextsInLanguage failed: %v", err)
	}

	if !strings.Contains(content, "Bonjour") {
		t.Errorf("Expected content to contain 'Bonjour', got: %s", content)
	}
}

func TestGetDataElementLabels(t *testing.T) {
	xmlResp := `<?xml version="1.0" encoding="UTF-8"?>
<dataElement shortDescription="Court" mediumDescription="Moyen" longDescription="Long texte" heading="En-tête"/>`

	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"/sap/bc/adt/ddic/dataelements/ZTEST_DTEL": newTestResponse(xmlResp),
			"discovery": newTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	labels, err := client.GetDataElementLabels(context.Background(), "ZTEST_DTEL", "FR")
	if err != nil {
		t.Fatalf("GetDataElementLabels failed: %v", err)
	}

	if labels.Short != "Court" {
		t.Errorf("Short = %v, want Court", labels.Short)
	}
	if labels.Medium != "Moyen" {
		t.Errorf("Medium = %v, want Moyen", labels.Medium)
	}
	if labels.Long != "Long texte" {
		t.Errorf("Long = %v, want Long texte", labels.Long)
	}
	if labels.Heading != "En-tête" {
		t.Errorf("Heading = %v, want En-tête", labels.Heading)
	}
}

func TestGetMessageClassTexts(t *testing.T) {
	// Shape live-verified 2026-09-08 against message class "00" (standard on
	// every SAP system) — root element messageClass (camelCase) in namespace
	// http://www.sap.com/adt/MessageClass, adtcore:-namespaced name, and
	// mc:-namespaced msgno/msgtext. Trimmed of the atom:link children and
	// extra message-metadata attributes the real response also carries,
	// which GetMessageClassTexts/MessageClass don't read.
	xmlResp := `<?xml version="1.0" encoding="UTF-8"?>
<mc:messageClass xmlns:mc="http://www.sap.com/adt/MessageClass" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="ZTEST_MC">
  <mc:messages mc:msgno="001" mc:msgtext="Message un"/>
  <mc:messages mc:msgno="002" mc:msgtext="Message deux"/>
</mc:messageClass>`

	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"/sap/bc/adt/messageclass/ztest_mc": newTestResponse(xmlResp),
			"discovery":                         newTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	texts, err := client.GetMessageClassTexts(context.Background(), "ZTEST_MC", "FR")
	if err != nil {
		t.Fatalf("GetMessageClassTexts failed: %v", err)
	}

	if len(texts) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(texts))
	}

	if texts[0].Number != "001" {
		t.Errorf("Number = %v, want 001", texts[0].Number)
	}
	if texts[0].Text != "Message un" {
		t.Errorf("Text = %v, want 'Message un'", texts[0].Text)
	}
}

func TestGetTextPoolInLanguage(t *testing.T) {
	xmlResp := `<?xml version="1.0" encoding="UTF-8"?>
<textPool>
  <entry id="I" key="001" entry="Texte un"/>
  <entry id="I" key="002" entry="Texte deux"/>
</textPool>`

	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST/textelements": newTestResponse(xmlResp),
			"discovery": newTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	entries, err := client.GetTextPoolInLanguage(context.Background(), "ZTEST", "FR")
	if err != nil {
		t.Fatalf("GetTextPoolInLanguage failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(entries))
	}

	if entries[0].Key != "001" {
		t.Errorf("Key = %v, want 001", entries[0].Key)
	}
	if entries[0].Text != "Texte un" {
		t.Errorf("Text = %v, want 'Texte un'", entries[0].Text)
	}
}

func TestCompareObjectLanguages(t *testing.T) {
	// Use a func-based mock that returns different content based on sap-language query param
	mock := &funcMockClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "discovery") {
				return newTestResponse("OK"), nil
			}
			lang := req.URL.Query().Get("sap-language")
			if lang == "FR" {
				return newTestResponse("REPORT ztest.\nWRITE 'Bonjour'."), nil
			}
			return newTestResponse("REPORT ztest.\nWRITE 'Hello'."), nil
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	comparison, err := client.CompareObjectLanguages(context.Background(),
		"/sap/bc/adt/programs/programs/ZTEST/source/main", "EN", "FR")
	if err != nil {
		t.Fatalf("CompareObjectLanguages failed: %v", err)
	}

	if comparison.SourceLang != "EN" {
		t.Errorf("SourceLang = %v, want EN", comparison.SourceLang)
	}
	if comparison.TargetLang != "FR" {
		t.Errorf("TargetLang = %v, want FR", comparison.TargetLang)
	}

	// Line 2 differs: 'Hello' vs 'Bonjour'
	if len(comparison.Entries) == 0 {
		t.Error("Expected at least 1 diff entry for differing content")
	}
}

// funcMockClient is a mock HTTP client that uses a function for responses.
type funcMockClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *funcMockClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func TestOverrideLanguageInRequest(t *testing.T) {
	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"/sap/bc/adt/programs/programs/ZTEST/source/main": newTestResponse("OK"),
			"discovery": newTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	cfg.Language = "EN"
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	_, err := client.GetObjectTextsInLanguage(context.Background(), "/sap/bc/adt/programs/programs/ZTEST/source/main", "FR")
	if err != nil {
		t.Fatalf("GetObjectTextsInLanguage failed: %v", err)
	}

	// Verify that the request URL contains sap-language=FR (override)
	if len(mock.requests) < 1 {
		t.Fatal("Expected at least 1 request")
	}

	lastReq := mock.requests[len(mock.requests)-1]
	sapLang := lastReq.URL.Query().Get("sap-language")
	if sapLang != "FR" {
		t.Errorf("Expected sap-language=FR in URL, got sap-language=%s (full URL: %s)", sapLang, lastReq.URL.String())
	}
}

// TestMessageClassMarshalXML pins the wire format WriteMessageClassTexts
// produces, via messageClassWriteBody — the write-only type. Before this fix
// WriteMessageClassTexts marshalled the read type, MessageClass, which had no
// XMLName at all and produced <MessageClass name="..." description=""> — the
// bare Go type name, no namespace — which SAP's /sap/bc/adt/messageclass
// resource cannot map.
//
// The literal "mc:"/"adtcore:" prefixes asserted here (not just namespace
// membership under some Go-chosen prefix) are the shape ported from
// upstream's own fix for this issue (oisee/vibing-steampunk commit 4a9e01f0,
// 2026-08-20) after this project's own live testing found that the
// namespace-URI tag form (`xml:"http://... local,attr"`) makes Marshal
// auto-pick its own prefix instead — and a PUT built that way returned 200
// OK while silently persisting nothing. The `xml:"mc:local,attr"` form (no
// space before the colon) used below is a different encoding/xml feature:
// it takes "mc:local" as a literal, unresolved name rather than a namespace
// declaration, so the output byte-for-byte matches the mc:/adtcore: prefixes
// a live GET response actually uses (confirmed independently by this
// project's own live GET of message class "00", not just by upstream).
//
// The namespace URI (http://www.sap.com/adt/MessageClass, capital M) and the
// root element (messageClass, camelCase) are LIVE-VERIFIED (2026-09-08,
// against the real SAP system this project connects to) — not the
// http://www.sap.com/adt/mc / lowercase "messageclass" this originally
// guessed from a hand-written GET fixture that was never actually captured
// live. The live PUT with unqualified msgno/msgtext (right value, no
// namespace) separately reproduced a concrete, confirmable failure: HTTP 400
// ExceptionResourceBadRequest, "Falta número de mensaje" (message number
// missing) — proving SAP's parser treats an unqualified attribute as
// *absent*, not a lenient match on local name the way this package's own
// Unmarshal is.
//
// The deletedmessage element name remains an unverified guess (see
// messageClassDeletedMessage's doc comment; upstream has no delete support
// at all yet, issue #161 still open there) — this test only pins what this
// code currently produces, so a live PUT that reveals a different name has
// something concrete to correct.
func TestMessageClassMarshalXML(t *testing.T) {
	mc := newMessageClassWriteBody("ZTEST_MC", "Test messages")
	mc.Messages = []messageClassWriteMessage{
		{Number: "001", Text: "Enter a value"},
	}
	mc.Deleted = []messageClassDeletedMessage{
		{Number: "009"},
	}

	body, err := xml.Marshal(mc)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	out := string(body)

	for _, want := range []string{
		`<mc:messageClass`,
		`xmlns:mc="` + msagNS + `"`,
		`xmlns:adtcore="` + adtcoreNS + `"`,
		`adtcore:name="ZTEST_MC"`,
		`adtcore:description="Test messages"`,
		`<mc:messages mc:msgno="001" mc:msgtext="Enter a value"`,
		`<mc:deletedmessage mc:msgno="009"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected the marshalled body to contain %q, got: %s", want, out)
		}
	}
	if strings.Contains(out, "<MessageClass") {
		t.Errorf("the bare read-model element name leaked into the request: %s", out)
	}

	// Round-trip via the lenient read type: MessageClass has no XMLName and
	// its attr tags carry no namespace, and Go's Unmarshal matches an
	// attribute by local name regardless of the namespace it actually
	// arrived in — confirmed live during this session's manual verification
	// against a real, namespace-qualified SAP response (a throwaway test
	// against message class "00", not checked into this repo), not just here.
	var roundTrip MessageClass
	if err := xml.Unmarshal(body, &roundTrip); err != nil {
		t.Fatalf("round-trip Unmarshal failed: %v", err)
	}
	if roundTrip.Name != mc.Name || len(roundTrip.Messages) != 1 || roundTrip.Messages[0].Number != "001" {
		t.Errorf("round-trip mismatch: %+v", roundTrip)
	}
}

// TestMessageClassMarshalXML_OmitsEmptyDescription confirms Go's actual
// encoding/xml behaviour for `omitempty` on a string attribute — the plan
// this fix came from explicitly flagged this as unverified rather than
// assumed.
func TestMessageClassMarshalXML_OmitsEmptyDescription(t *testing.T) {
	mc := newMessageClassWriteBody("ZTEST_MC", "")

	body, err := xml.Marshal(mc)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	out := string(body)

	if strings.Contains(out, "description=") {
		t.Errorf("expected description attribute to be omitted when empty, got: %s", out)
	}
}

// TestMessageClass_UnmarshalStaysLenient guards the MEDIUM finding from code
// review directly: the read type must keep accepting whatever root
// element/namespace shape a live SAP response actually uses, the way it
// always has, rather than inheriting the write type's strict XMLName.
func TestMessageClass_UnmarshalStaysLenient(t *testing.T) {
	docs := []string{
		`<mc:messageclass xmlns:mc="http://www.sap.com/adt/mc" name="X"><mc:messages msgno="1" msgtext="a"/></mc:messageclass>`,
		`<messageclass name="X"><messages msgno="1" msgtext="a"/></messageclass>`,
		`<foo xmlns="http://www.sap.com/adt/mc" name="X"><messages msgno="1" msgtext="a"/></foo>`,
	}
	for i, doc := range docs {
		var mc MessageClass
		if err := xml.Unmarshal([]byte(doc), &mc); err != nil {
			t.Errorf("case %d: expected lenient Unmarshal to accept this shape, got: %v", i, err)
			continue
		}
		if mc.Name != "X" || len(mc.Messages) != 1 {
			t.Errorf("case %d: expected Name=X and 1 message, got %+v", i, mc)
		}
	}
}

func TestWriteOperationsCheckSafety(t *testing.T) {
	mock := &mockTransportClient{
		responses: map[string]*http.Response{
			"discovery": newTestResponse("OK"),
		},
	}

	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	cfg.Safety.ReadOnly = true // Enable read-only mode
	transport := NewTransportWithClient(cfg, mock)
	client := NewClientWithTransport(cfg, transport)

	// WriteMessageClassTexts should be blocked by safety (OpUpdate)
	err := client.WriteMessageClassTexts(context.Background(), "ZTEST_MC", "FR", nil, nil, "lock123", "")
	if err == nil {
		t.Error("WriteMessageClassTexts should fail in read-only mode")
	}

	// WriteDataElementLabels should be blocked by safety (OpUpdate)
	err = client.WriteDataElementLabels(context.Background(), "ZTEST_DTEL", "FR", &DataElementLabels{}, "lock123", "")
	if err == nil {
		t.Error("WriteDataElementLabels should fail in read-only mode")
	}

	// Read operations should still work (will fail on mock but not on safety)
	_, err = client.GetDataElementLabels(context.Background(), "ZTEST_DTEL", "FR")
	// This will fail because we have no mock response for this path, but the error
	// should NOT be a safety error
	if err != nil && strings.Contains(err.Error(), "read-only") {
		t.Error("GetDataElementLabels should not be blocked by read-only mode")
	}
}
