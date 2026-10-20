package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const (
	partURL    = groupURL + "/includes/lzdemotop"
	partSource = partURL + "/source/main"
	batchOKXML = `<chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"/>`
)

// ownPartsStub refuses a single-object activation without a word (the S/4HANA
// answer when parts are pending), accepts a batch of two or more, serves the
// inactive list, and answers the inactive-version GET of a part.
func ownPartsStub(list, inactiveBody string, inactiveStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sap/bc/adt/activation":
			body, _ := io.ReadAll(r.Body)
			if strings.Count(string(body), "<adtcore:objectReference ") > 1 {
				_, _ = io.WriteString(w, batchOKXML)
				return
			}
			_, _ = io.WriteString(w, silentRefusalXML)
		case r.URL.Path == "/sap/bc/adt/activation/inactiveobjects":
			_, _ = io.WriteString(w, list)
		case r.Method == http.MethodGet && r.URL.Query().Get("version") == "inactive":
			if inactiveStatus != 0 {
				w.WriteHeader(inactiveStatus)
				return
			}
			_, _ = io.WriteString(w, inactiveBody)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

func onePartList(user string) string {
	return inactiveListXML(inactiveEntryXML(partURL, "LZDEMOTOP", user))
}

func activationPosts(rec *adtRecorder) int {
	return countCalls(rec.snapshot(), http.MethodPost, "/sap/bc/adt/activation")
}

func TestActivate_PartsWrittenByThisProcessGoLiveWithTheParent(t *testing.T) {
	const src = "FUNCTION-POOL zdemo.\n\nTYPES ty TYPE i.\n"
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, ownPartsStub(onePartList("TESTUSER"), src, 0))
	client.noteSourceWritten(partSource, src)

	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("parts written here and unchanged go live with the group: %+v", res)
	}
	if n := activationPosts(rec); n != 2 {
		t.Errorf("the refusal plus one batch expected, got %d POSTs", n)
	}
	if !strings.Contains(strings.Join(activationMessages(res), " "), "LZDEMOTOP") {
		t.Errorf("the result must say what was activated with it: %+v", res.Messages)
	}
	if _, ok := client.writtenPartFor(partURL); ok {
		t.Error("an activated part must leave the registry")
	}
}

func TestActivate_ReadsTheInactiveVersionFromTheURLItWasWrittenTo(t *testing.T) {
	const src = "x = 1.\n"
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, ownPartsStub(onePartList("TESTUSER"), src, 0))
	client.noteSourceWritten(partSource, src)
	if _, err := client.Activate(context.Background(), groupURL, "ZDEMO"); err != nil {
		t.Fatal(err)
	}
	var got bool
	for _, c := range rec.snapshot() {
		if c.method == http.MethodGet && c.query.Get("version") == "inactive" && c.path == partSource {
			got = true
		}
	}
	if !got {
		t.Error("the part's inactive version must be read from the URL it was written to")
	}
}

func TestActivate_OwnPartsRefusedWhenAnythingIsInDoubt(t *testing.T) {
	const src = "x = 1.\n"
	cases := []struct {
		name           string
		user           string
		recorded       bool
		inactiveBody   string
		inactiveStatus int
		extraForeign   bool
	}{
		{"never written by this process", "TESTUSER", false, src, 0, false},
		{"held by another user", "OTHERUSER", true, src, 0, false},
		{"changed since it was written", "TESTUSER", true, "x = 2.\n", 0, false},
		{"inactive version unreadable", "TESTUSER", true, src, http.StatusInternalServerError, false},
		{"another pending part is foreign", "TESTUSER", true, src, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := []string{inactiveEntryXML(partURL, "LZDEMOTOP", tc.user)}
			if tc.extraForeign {
				entries = append(entries, inactiveEntryXML(groupURL+"/includes/lzdemof01", "LZDEMOF01", "OTHERUSER"))
			}
			rec := &adtRecorder{}
			client := newStubbedClient(t, rec, ownPartsStub(inactiveListXML(entries...), tc.inactiveBody, tc.inactiveStatus))
			if tc.recorded {
				client.noteSourceWritten(partSource, src)
			}
			res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
			if err != nil {
				t.Fatal(err)
			}
			if res.Success {
				t.Fatalf("a part that is not provably ours must not be activated: %+v", res)
			}
			if n := activationPosts(rec); n != 1 {
				t.Errorf("only the refused request may go out, got %d POSTs", n)
			}
			if len(res.Inactive) == 0 {
				t.Error("the pending parts must still be named")
			}
		})
	}
}

func TestActivate_OwnPartsSwitchedOff(t *testing.T) {
	t.Setenv("SAP_ACTIVATE_OWN_PARTS", "false")
	const src = "x = 1.\n"
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, ownPartsStub(onePartList("TESTUSER"), src, 0))
	client.noteSourceWritten(partSource, src)
	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || activationPosts(rec) != 1 {
		t.Fatalf("with SAP_ACTIVATE_OWN_PARTS=false nothing is activated: %+v, %d POSTs", res, activationPosts(rec))
	}
}

func TestActivate_AfterActivatingOwnPartsNothingIsRememberedForNextTime(t *testing.T) {
	const src = "x = 1.\n"
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, ownPartsStub(onePartList("TESTUSER"), src, 0))
	client.noteSourceWritten(partSource, src)
	if res, _ := client.Activate(context.Background(), groupURL, "ZDEMO"); res == nil || !res.Success {
		t.Fatalf("first activation: %+v", res)
	}
	// Someone leaves the same include pending again: this process no longer vouches for it.
	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if res.Success {
		t.Fatalf("the part left the registry with the first activation: %+v", res)
	}
}

func TestUpdateSource_RecordsOnlyWhatSAPAccepted(t *testing.T) {
	status := http.StatusOK
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	status = http.StatusInternalServerError
	if err := client.UpdateSource(context.Background(), partSource, "boom", "H", ""); err == nil {
		t.Fatal("a 500 must fail the write")
	}
	if _, ok := client.writtenPartFor(partURL); ok {
		t.Fatal("a failed PUT must leave nothing in the registry")
	}
	status = http.StatusOK
	if err := client.UpdateSource(context.Background(), partSource, "ok", "H", ""); err != nil {
		t.Fatal(err)
	}
	got, ok := client.writtenPartFor(strings.ToUpper(partURL))
	if !ok || got.hash != SourceHash("ok") || got.sourceURL != partSource {
		t.Fatalf("the accepted write must be recorded: %+v %v", got, ok)
	}
}

func TestNormalizeActivationURI(t *testing.T) {
	for in, want := range map[string]string{
		"/SAP/bc/adt/functions/groups/ZDEMO/source/main":       "/sap/bc/adt/functions/groups/zdemo",
		"/sap/bc/adt/programs/includes/zi?version=inactive":    "/sap/bc/adt/programs/includes/zi",
		"/sap/bc/adt/functions/groups/%2fns%2fgrp/source/main": "/sap/bc/adt/functions/groups//ns/grp",
		"  /sap/bc/adt/oo/classes/zcl_x  ":                     "/sap/bc/adt/oo/classes/zcl_x",
	} {
		if got := normalizeActivationURI(in); got != want {
			t.Errorf("normalizeActivationURI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWrittenParts_ConcurrentUse(t *testing.T) {
	client := newStubbedClient(t, &adtRecorder{}, func(w http.ResponseWriter, r *http.Request) {})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.noteSourceWritten(partSource, "a")
			client.writtenPartFor(partURL)
			client.forgetWritten(partURL)
		}()
	}
	wg.Wait()
}

func TestActivate_ThePendingParentMustBeOursToo(t *testing.T) {
	const src = "x = 1.\n"
	list := inactiveListXML(
		inactiveEntryXML(groupURL, "ZDEMO", "TESTUSER"), // the group itself is pending
		inactiveEntryXML(partURL, "LZDEMOTOP", "TESTUSER"),
	)

	t.Run("parent not written by this process", func(t *testing.T) {
		rec := &adtRecorder{}
		client := newStubbedClient(t, rec, ownPartsStub(list, src, 0))
		client.noteSourceWritten(partSource, src) // the part is ours, the group is not
		res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
		if err != nil {
			t.Fatal(err)
		}
		if res.Success || activationPosts(rec) != 1 {
			t.Fatalf("someone else's pending group must not go live with our include: %+v, %d POSTs", res, activationPosts(rec))
		}
	})

	t.Run("parent and part both ours", func(t *testing.T) {
		rec := &adtRecorder{}
		client := newStubbedClient(t, rec, ownPartsStub(list, src, 0))
		client.noteSourceWritten(partSource, src)
		client.noteSourceWritten(groupURL+"/source/main", src)
		res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Success || activationPosts(rec) != 2 {
			t.Fatalf("both are ours: %+v, %d POSTs", res, activationPosts(rec))
		}
	})
}

func TestActivate_OnePartRecordedAndAnotherOfTheSameUserNot(t *testing.T) {
	const src = "x = 1.\n"
	list := inactiveListXML(
		inactiveEntryXML(partURL, "LZDEMOTOP", "TESTUSER"),
		inactiveEntryXML(groupURL+"/includes/lzdemof01", "LZDEMOF01", "TESTUSER"), // same user, never written here
	)
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, ownPartsStub(list, src, 0))
	client.noteSourceWritten(partSource, src)
	res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || activationPosts(rec) != 1 || len(res.Inactive) != 2 {
		t.Fatalf("all or nothing: %+v, %d POSTs", res, activationPosts(rec))
	}
}

func TestActivate_ParentPendingOnlyBecauseOfThePartGoesLive(t *testing.T) {
	const src = "x = 1.\n"
	const parentMain = "INCLUDE lzdemotop.\n"
	list := inactiveListXML(
		inactiveEntryXML(groupURL, "ZDEMO", "TESTUSER"),
		inactiveEntryXML(partURL, "LZDEMOTOP", "TESTUSER"),
	)
	base := ownPartsStub(list, src, 0)
	serve := func(inactiveMain, activeMain string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == groupURL+"/source/main" {
				if r.URL.Query().Get("version") == "inactive" {
					_, _ = io.WriteString(w, inactiveMain)
				} else {
					_, _ = io.WriteString(w, activeMain)
				}
				return
			}
			base(w, r)
		}
	}

	t.Run("inactive equals active", func(t *testing.T) {
		rec := &adtRecorder{}
		client := newStubbedClient(t, rec, serve(parentMain, parentMain))
		client.noteSourceWritten(partSource, src)
		res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Success || activationPosts(rec) != 2 {
			t.Fatalf("a parent with no change of its own goes live with our part: %+v, %d POSTs", res, activationPosts(rec))
		}
	})

	t.Run("parent has its own pending change", func(t *testing.T) {
		rec := &adtRecorder{}
		client := newStubbedClient(t, rec, serve(parentMain+"* pending edit\n", parentMain))
		client.noteSourceWritten(partSource, src)
		res, err := client.Activate(context.Background(), groupURL, "ZDEMO")
		if err != nil {
			t.Fatal(err)
		}
		if res.Success || activationPosts(rec) != 1 {
			t.Fatalf("someone's pending edit of the parent must not go live: %+v, %d POSTs", res, activationPosts(rec))
		}
	})
}
