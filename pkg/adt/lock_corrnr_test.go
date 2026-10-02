package adt

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// corrNr on the LOCK request, and the language on a message class PUT.
//
// ADT takes the transport on the LOCK itself, and on-premise systems that bind
// the lock to a request expect it there. A message class PUT without
// adtcore:language lands in T100 with an empty SPRSL: written, reported as
// written, and never found by a MESSAGE statement at runtime.

func lockHandler(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK" {
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
		_, _ = io.WriteString(w, testLockXML)
		return true
	}
	return false
}

// TestLockObject_EmitsCorrNr pins both directions: a supplied transport is on
// the LOCK, and without one the request is exactly what it was before.
func TestLockObject_EmitsCorrNr(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport []string
		want      string
	}{
		{"with transport", []string{"TR-EXAMPLE"}, "TR-EXAMPLE"},
		{"empty transport", []string{""}, ""},
		{"no transport argument", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &adtRecorder{}
			client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				if !lockHandler(w, r) {
					w.WriteHeader(http.StatusOK)
				}
			}, WithAllowTransportableEdits())

			if _, err := client.LockObject(context.Background(),
				"/sap/bc/adt/oo/classes/zcl_demo", "MODIFY", tc.transport...); err != nil {
				t.Fatalf("LockObject: %v", err)
			}

			lockAt := indexOfCall(rec.snapshot(), isLock)
			if lockAt < 0 {
				t.Fatal("no LOCK request recorded")
			}
			q := rec.snapshot()[lockAt].query
			if got, present := q.Get("corrNr"), q.Has("corrNr"); got != tc.want || present != (tc.want != "") {
				t.Errorf("LOCK corrNr = %q (present %v), want %q", got, present, tc.want)
			}
		})
	}
}

// The transport goes out on the LOCK, so the transport policy has to be
// checked before it, not only in the write that follows.
func TestLockObject_RefusesADisallowedTransportBeforeTheLock(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{"transportable edits disabled", nil},
		{"transport not in the allowlist", []Option{WithAllowTransportableEdits(), WithAllowedTransports("TR-ALLOWED*")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &adtRecorder{}
			client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
				if !lockHandler(w, r) {
					w.WriteHeader(http.StatusOK)
				}
			}, tc.opts...)

			if _, err := client.LockObject(context.Background(),
				"/sap/bc/adt/oo/classes/zcl_demo", "MODIFY", "TR-EXAMPLE"); err == nil {
				t.Error("LockObject accepted a transport the configuration disallows")
			}
			if calls := rec.snapshot(); len(calls) != 0 {
				t.Error("a LOCK carrying a disallowed transport still reached SAP:")
				dumpCalls(t, calls)
			}
		})
	}
}

func TestTransportChoice_LockCorrNr(t *testing.T) {
	for _, tc := range []struct {
		name     string
		plan     *TransportChoice
		supplied string
		want     string
	}{
		{"supplied wins over the plan", &TransportChoice{Transport: "TR-PLANNED"}, "TR-NAMED", "TR-NAMED"},
		{"no plan, nothing supplied", nil, "", ""},
		{"no plan, supplied", nil, "TR-NAMED", "TR-NAMED"},
		{"plan chose a request", &TransportChoice{Transport: "TR-PLANNED"}, "", "TR-PLANNED"},
		{"plan chose nothing", &TransportChoice{Reason: "left to SAP"}, "", ""},
		{"plan failed to create one", &TransportChoice{Transport: "TR-PLANNED", Err: errTransportCreate}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.plan.lockCorrNr(tc.supplied); got != tc.want {
				t.Errorf("lockCorrNr = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- Message class: the language on the PUT ---

// Without adtcore:language the messages were stored with an empty SPRSL.
func TestMessageClassWriteCarriesTheLanguage(t *testing.T) {
	mc := newMessageClassWriteBody("ZDEMO", "Demo", "DE")
	mc.Messages = []messageClassWriteMessage{{Number: "001", Text: "Text"}}
	b, err := xml.Marshal(mc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `adtcore:language="DE"`) {
		t.Errorf("no language in %s", b)
	}
}

// putBodyRecorder answers like a message class endpoint that accepts anything
// and remembers the body and query of every PUT to it.
type putBodyRecorder struct {
	mu      sync.Mutex
	bodies  []string
	corrNrs []string
}

func (p *putBodyRecorder) route(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/messageclass/") {
		b, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.bodies = append(p.bodies, string(b))
		p.corrNrs = append(p.corrNrs, r.URL.Query().Get("corrNr"))
		p.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return true
	}
	return lockHandler(w, r)
}

// The same through WriteMessageClassTexts: the PUT body carries the language
// asked for (uppercased), not the session default.
func TestWriteMessageClassTexts_PutBodyCarriesTheLanguage(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	})

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "de",
		[]MessageClassMessage{{Number: "001", Text: "Hallo"}}, nil, "HANDLE", ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}

	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 {
		t.Fatalf("expected one PUT of the message class, got %d", len(put.bodies))
	}
	if !strings.Contains(put.bodies[0], `adtcore:language="DE"`) {
		t.Errorf("PUT body has no adtcore:language=\"DE\":\n%s", put.bodies[0])
	}
}

// CreateMessageClass writes its initial messages with a PUT of its own, built
// from the same struct — it needs the language too.
func TestCreateMessageClass_InitialMessagesPutCarriesTheLanguage(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	})

	err := client.CreateMessageClass(context.Background(), CreateMessageClassOptions{
		Name:     "ZDEMO_MC",
		Package:  "$TMP",
		Language: "de",
		Messages: []MessageClassMessage{{Number: "001", Text: "Hallo"}},
	})
	if err != nil {
		t.Fatalf("CreateMessageClass: %v", err)
	}

	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 {
		t.Fatalf("expected one PUT of the initial messages, got %d", len(put.bodies))
	}
	if !strings.Contains(put.bodies[0], `adtcore:language="DE"`) {
		t.Errorf("initial-messages PUT has no adtcore:language=\"DE\":\n%s", put.bodies[0])
	}
}

// --- Message class: corrNr on the LOCK ---

// A transportable class created with initial messages: the LOCK, like the shell
// POST and the PUT, belongs to the request the caller named.
func TestCreateMessageClass_LockCarriesTransport(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	err := client.CreateMessageClass(context.Background(), CreateMessageClassOptions{
		Name:      "ZDEMO_MC",
		Package:   "ZDEMO",
		Language:  "ES",
		Transport: "TR-EXAMPLE",
		Messages:  []MessageClassMessage{{Number: "001", Text: "Hola"}},
	})
	if err != nil {
		t.Fatalf("CreateMessageClass: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	unlockAt := indexOfCall(calls, isUnlock)
	if lockAt < 0 || unlockAt < lockAt {
		t.Fatalf("expected a LOCK followed by an UNLOCK; trace:\n%v", calls)
	}
	if got := calls[lockAt].query.Get("corrNr"); got != "TR-EXAMPLE" {
		t.Errorf("LOCK corrNr = %q, want TR-EXAMPLE", got)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.corrNrs) != 1 || put.corrNrs[0] != "TR-EXAMPLE" {
		t.Errorf("PUT corrNr = %v, want [TR-EXAMPLE]", put.corrNrs)
	}
	assertWindowStateful(t, calls, lockAt, unlockAt)
}

// The auto-lock write names no request: the plan picks one before the lock, and
// the LOCK must carry it — the PUT that follows uses the plan's request, so a
// bare LOCK would bind the lock and the PUT to different ones (or leave SAP to
// generate a request of its own).
func TestWriteMessageClassTextsAutoLock_PlannedTransportGoesOnLock(t *testing.T) {
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_MC",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"})

	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "cts/transportchecks") {
			_, _ = io.WriteString(w, checkXML)
			return
		}
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	err := client.WriteMessageClassTextsAutoLock(context.Background(), "ZDEMO_MC", "ES",
		[]MessageClassMessage{{Number: "001", Text: "Hola"}}, nil, "")
	if err != nil {
		t.Fatalf("WriteMessageClassTextsAutoLock: %v", err)
	}

	calls := rec.snapshot()
	checkAt := indexOfCall(calls, func(c wireCall) bool { return strings.Contains(c.path, "cts/transportchecks") })
	lockAt := indexOfCall(calls, isLock)
	unlockAt := indexOfCall(calls, isUnlock)
	if checkAt < 0 || lockAt < 0 || unlockAt < lockAt {
		t.Fatalf("expected a transport check, then LOCK, then UNLOCK; trace:\n%v", calls)
	}
	if checkAt > lockAt {
		t.Errorf("the transport check ran after the LOCK (index %d > %d) — a stateless hop inside the "+
			"lock window retires the session the lock handle lives in", checkAt, lockAt)
	}
	if got := calls[lockAt].query.Get("corrNr"); got != "TR-A" {
		t.Errorf("LOCK corrNr = %q, want the planned TR-A", got)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.corrNrs) != 1 || put.corrNrs[0] != "TR-A" {
		t.Errorf("PUT corrNr = %v, want [TR-A]", put.corrNrs)
	}
	assertWindowStateful(t, calls, lockAt, unlockAt)
}

// --- Message class: validation before any request ---

func TestValidateMessageClassMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		texts   []MessageClassMessage
		deletes []string
		wantErr bool
	}{
		{"three digits", []MessageClassMessage{{Number: "001", Text: "x"}}, nil, false},
		{"one digit", []MessageClassMessage{{Number: "1", Text: "x"}}, nil, true},
		{"four digits", []MessageClassMessage{{Number: "0001", Text: "x"}}, nil, true},
		{"not digits", []MessageClassMessage{{Number: "00A", Text: "x"}}, nil, true},
		{"73 characters", []MessageClassMessage{{Number: "001", Text: strings.Repeat("a", 73)}}, nil, false},
		{"74 characters", []MessageClassMessage{{Number: "001", Text: strings.Repeat("a", 74)}}, nil, true},
		{"73 multibyte characters count as 73", []MessageClassMessage{{Number: "001", Text: strings.Repeat("ñ", 73)}}, nil, false},
		{"delete with three digits", nil, []string{"009"}, false},
		{"delete with one digit", nil, []string{"9"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMessageClassMessages(tc.texts, tc.deletes)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateMessageClassLanguage(t *testing.T) {
	for lang, wantErr := range map[string]bool{"ES": false, "EN": false, "": true, "E": true, "ESP": true} {
		if err := validateMessageClassLanguage(lang); (err != nil) != wantErr {
			t.Errorf("language %q: err = %v, wantErr %v", lang, err, wantErr)
		}
	}
}

// A malformed initial message must not leave a half-created class behind, so
// nothing may be sent — not even the shell POST.
func TestCreateMessageClass_RejectsInvalidMessagesBeforeAnyRequest(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	err := client.CreateMessageClass(context.Background(), CreateMessageClassOptions{
		Name:     "ZDEMO_MC",
		Package:  "$TMP",
		Language: "ES",
		Messages: []MessageClassMessage{{Number: "1", Text: "x"}},
	})
	if err == nil {
		t.Fatal("CreateMessageClass accepted a message number that is not 3 digits")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Error("a rejected CreateMessageClass still talked to SAP:")
		dumpCalls(t, calls)
	}
}

// A malformed message must not cost a lock.
func TestWriteMessageClassTextsAutoLock_RejectsInvalidMessagesBeforeLock(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if !lockHandler(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	})

	err := client.WriteMessageClassTextsAutoLock(context.Background(), "ZDEMO_MC", "ES",
		[]MessageClassMessage{{Number: "001", Text: strings.Repeat("a", 74)}}, nil, "")
	if err == nil {
		t.Fatal("WriteMessageClassTextsAutoLock accepted a text longer than T100's 73 characters")
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Error("a rejected write still talked to SAP:")
		dumpCalls(t, calls)
	}
}

// --- Phase 3: the write workflows that plan a transport before the lock ---

// WriteProgram names no request: the plan picks one before the lock, and the
// LOCK must carry it — the write that follows takes the plan's request through
// resolveWriteTransportFor.
func TestWriteProgram_PlannedTransportGoesOnLock(t *testing.T) {
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_PROBE",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"})

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		case strings.Contains(r.URL.Path, "/checkruns"):
			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			_, _ = io.WriteString(w, testEmptyCheckXML)
		case lockHandler(w, r):
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	if _, err := client.WriteProgram(context.Background(), "ZDEMO_PROBE", "REPORT zdemo_probe.\n", ""); err != nil {
		t.Fatalf("WriteProgram: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, isSourcePut)
	if lockAt < 0 || putAt < lockAt {
		t.Fatalf("expected a LOCK followed by a source PUT; trace:\n%v", calls)
	}
	if got := calls[lockAt].query.Get("corrNr"); got != "TR-A" {
		t.Errorf("LOCK corrNr = %q, want the planned TR-A", got)
	}
	if got := calls[putAt].query.Get("corrNr"); got != "TR-A" {
		t.Errorf("source PUT corrNr = %q, want TR-A", got)
	}
	assertWindowStateful(t, calls, lockAt, putAt)
}

// A transport the caller names goes on the LOCK as given, not replaced by a plan.
func TestWriteProgram_NamedTransportGoesOnLock(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/checkruns"):
			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			_, _ = io.WriteString(w, testEmptyCheckXML)
		case lockHandler(w, r):
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	if _, err := client.WriteProgram(context.Background(), "ZDEMO_PROBE", "REPORT zdemo_probe.\n", "TR-NAMED"); err != nil {
		t.Fatalf("WriteProgram: %v", err)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	if lockAt < 0 {
		t.Fatalf("expected a LOCK; trace:\n%v", calls)
	}
	if got := calls[lockAt].query.Get("corrNr"); got != "TR-NAMED" {
		t.Errorf("LOCK corrNr = %q, want TR-NAMED", got)
	}
	if indexOfCall(calls, func(c wireCall) bool { return strings.Contains(c.path, "cts/transportchecks") }) >= 0 {
		t.Error("a transport named by the caller must not trigger a transport check")
	}
}

// EditSourceWithOptions plans its transport before the lock like the other
// workflows; its LOCK has to carry the planned request too.
func TestEditSource_PlannedTransportGoesOnLock(t *testing.T) {
	checkXML := transportCheckXML(true, "ZDEMO_PKG", "ZDEMO_EDIT",
		checkCandidate{"TR-A", "TESTUSER", "feature A", "D"})

	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "cts/transportchecks"):
			_, _ = io.WriteString(w, checkXML)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/source/main"):
			_, _ = io.WriteString(w, "REPORT zdemo_edit.\nWRITE 'old'.\n")
		case lockHandler(w, r):
		default:
			w.WriteHeader(http.StatusOK)
		}
	}, WithAllowTransportableEdits())

	result, err := client.EditSourceWithOptions(context.Background(),
		"/sap/bc/adt/programs/programs/ZDEMO_EDIT", "WRITE 'old'.", "WRITE 'new'.",
		&EditSourceOptions{SyntaxCheck: false})
	if err != nil {
		t.Fatalf("EditSourceWithOptions: %v", err)
	}
	if !result.Success {
		t.Fatalf("edit did not succeed: %s", result.Message)
	}

	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	putAt := indexOfCall(calls, isSourcePut)
	if lockAt < 0 || putAt < lockAt {
		t.Fatalf("expected a LOCK followed by a source PUT; trace:\n%v", calls)
	}
	if got := calls[lockAt].query.Get("corrNr"); got != "TR-A" {
		t.Errorf("LOCK corrNr = %q, want the planned TR-A", got)
	}
	if got := calls[putAt].query.Get("corrNr"); got != "TR-A" {
		t.Errorf("source PUT corrNr = %q, want TR-A", got)
	}
}

// --- Message class: the language of the GETs, and the delete element ---

// msagGetXML is a message class GET answer. SAP ignores the sap-language
// header on this resource and picks the language from the `language` query
// parameter only, falling back to the session language without it; the stub
// below does the same, which is what makes these tests meaningful.
func msagGetXML(description string, messages map[string]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><mc:messageClass xmlns:mc="` + msagNS +
		`" xmlns:adtcore="` + adtcoreNS + `" adtcore:name="ZDEMO_MC" adtcore:description="` + description + `">`)
	for _, no := range []string{"001", "002", "009"} {
		if text, ok := messages[no]; ok {
			sb.WriteString(`<mc:messages mc:msgno="` + no + `" mc:msgtext="` + text + `"/>`)
		}
	}
	sb.WriteString(`</mc:messageClass>`)
	return sb.String()
}

// languageAwareMessageClass serves ES unless `language=E` is on the query, and
// records the PUT bodies like putBodyRecorder.
func languageAwareMessageClass(put *putBodyRecorder, getES, getEN string, enGetStatus int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/messageclass/") {
			body := getES
			if r.URL.Query().Get("language") == "E" {
				if enGetStatus != 0 {
					w.WriteHeader(enGetStatus)
					return
				}
				body = getEN
			}
			w.Header().Set("Content-Type", "application/vnd.sap.adt.mc.messageclass+xml")
			_, _ = io.WriteString(w, body)
			return
		}
		if !put.route(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	}
}

// Writing a language other than the one SAP answers in by default must be
// verified in that language: the read-back used to see the session-language
// text and report a write that had landed as one that had not.
func TestVerifyMessageClassWrite_ReadsTheLanguageThatWasWritten(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola"}),
		msagGetXML("Desc EN", map[string]string{"001": "Hello"}), 0))

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "Hello"}}, nil, "HANDLE", ""); err != nil {
		t.Fatalf("a write in EN was reported as failed: %v", err)
	}
}

// A real no-op must still be caught when the language is the right one.
func TestVerifyMessageClassWrite_StillCatchesASilentNoOp(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola"}),
		msagGetXML("Desc EN", map[string]string{"001": "old text"}), 0))

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "Hello"}}, nil, "HANDLE", ""); err == nil {
		t.Error("a PUT that did not change the EN text was accepted")
	}
}

// The description read before the PUT is echoed back on it, so it has to be
// the description of the language being written.
func TestWriteMessageClassTexts_EchoesTheDescriptionOfTheLanguageWritten(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola"}),
		msagGetXML("Desc EN", map[string]string{"001": "Hello"}), 0))

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "Hello"}}, nil, "HANDLE", ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `adtcore:description="Desc EN"`) {
		t.Errorf("PUT should echo the EN description, got %v", put.bodies)
	}
}

// First translation: DO_GET answers 404 when the language has no row yet. The
// write must still go out, echoing the session-language description rather
// than an empty one — and only after having tried the language asked for.
func TestWriteMessageClassTexts_FallsBackWhenTheLanguageHasNoRowYet(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola"}), "", http.StatusNotFound))

	// The verifier's own GET in EN is a 404 as well, which it treats as
	// "cannot verify", not as a failed write.
	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "Hello"}}, nil, "HANDLE", ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `adtcore:description="Desc ES"`) {
		t.Errorf("PUT should fall back to the session-language description, got %v", put.bodies)
	}

	// Order on the wire: the GET in EN first, then the one without `language`.
	var gets []string
	for _, c := range rec.snapshot() {
		if c.method == http.MethodGet && strings.Contains(c.path, "/messageclass/") {
			gets = append(gets, c.query.Get("language"))
		}
	}
	if len(gets) < 2 || gets[0] != "E" || gets[1] != "" {
		t.Errorf("expected a GET with language=E then one without, got language values %q", gets)
	}
}

// Any failure other than "no row in that language" must not turn into the
// session-language description on an EN row.
func TestWriteMessageClassTexts_DoesNotEchoTheWrongLanguageOnOtherErrors(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola"}), "", http.StatusInternalServerError))

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "Hello"}}, nil, "HANDLE", ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || strings.Contains(put.bodies[0], "Desc ES") {
		t.Errorf("the ES description leaked into an EN write after a 5xx: %v", put.bodies)
	}
}

// Everything between the LOCK and the PUT is stateful — the description GET
// included, with or without the retry.
func TestWriteMessageClassTexts_ReadsInsideTheWindowAreStateful(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola"}), "", http.StatusNotFound))

	if err := client.WriteMessageClassTextsAutoLock(context.Background(), "ZDEMO_MC", "EN",
		[]MessageClassMessage{{Number: "001", Text: "Hello"}}, nil, ""); err != nil {
		t.Fatalf("WriteMessageClassTextsAutoLock: %v", err)
	}
	calls := rec.snapshot()
	lockAt := indexOfCall(calls, isLock)
	unlockAt := indexOfCall(calls, isUnlock)
	if lockAt < 0 || unlockAt < 0 {
		t.Fatal("no LOCK/UNLOCK recorded")
	}
	assertWindowStateful(t, calls, lockAt, unlockAt)
}

// delete_numbers goes out as the element SAP's transformation reads.
func TestWriteMessageClassTexts_DeleteSendsThePluralElement(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola"}), "", 0))

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES",
		nil, []string{"009"}, "HANDLE", ""); err != nil {
		t.Fatalf("WriteMessageClassTexts: %v", err)
	}
	put.mu.Lock()
	defer put.mu.Unlock()
	if len(put.bodies) != 1 || !strings.Contains(put.bodies[0], `<mc:deletedmessages mc:msgno="009"`) {
		t.Errorf("PUT should carry <mc:deletedmessages>, got %v", put.bodies)
	}
}

// And a delete that SAP ignored is still reported: 009 stays in the read-back.
func TestWriteMessageClassTexts_DeleteThatDidNotTakeEffectIsReported(t *testing.T) {
	put := &putBodyRecorder{}
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, languageAwareMessageClass(put,
		msagGetXML("Desc ES", map[string]string{"001": "Hola", "009": "Still here"}), "", 0))

	if err := client.WriteMessageClassTexts(context.Background(), "ZDEMO_MC", "ES",
		nil, []string{"009"}, "HANDLE", ""); err == nil {
		t.Error("a delete that left the message in place was reported as done")
	}
}

func TestMessageClassReadQuery(t *testing.T) {
	for lang, want := range map[string]string{"ES": "S", "EN": "E", "es": "S", "S": "S", "": ""} {
		if got := messageClassReadQuery(lang).Get("language"); got != want {
			t.Errorf("messageClassReadQuery(%q) language = %q, want %q", lang, got, want)
		}
	}
}

// An ISO code spras() cannot map would be read back in another language than
// it is written in (ET -> E), so it is refused before any request.
func TestValidateMessageClassLanguage_RefusesUnmappedCodes(t *testing.T) {
	for _, lang := range []string{"ES", "EN", "DE"} {
		if err := validateMessageClassLanguage(lang); err != nil {
			t.Errorf("%s refused: %v", lang, err)
		}
	}
	for _, lang := range []string{"ET", "LV", "XX", "E", "ESP", ""} {
		if err := validateMessageClassLanguage(lang); err == nil {
			t.Errorf("%q accepted", lang)
		}
	}
}

// The read path asks SAP for the language it was given.
func TestGetMessageClassTexts_SendsTheLanguageQuery(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.sap.adt.mc.messageclass+xml")
		_, _ = io.WriteString(w, msagGetXML("Desc FR", map[string]string{"001": "Bonjour"}))
	})
	if _, err := client.GetMessageClassTexts(context.Background(), "ZDEMO_MC", "FR"); err != nil {
		t.Fatalf("GetMessageClassTexts: %v", err)
	}
	for _, c := range rec.snapshot() {
		if strings.Contains(c.path, "/messageclass/") && c.query.Get("language") == "F" {
			return
		}
	}
	t.Error("no message class GET carried language=F")
}
