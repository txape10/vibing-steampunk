package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func searchHitsXML(hits ...[3]string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
	for _, h := range hits {
		sb.WriteString(`<adtcore:objectReference adtcore:uri="/sap/bc/adt/x/` + strings.ToLower(h[1]) +
			`" adtcore:type="` + h[0] + `" adtcore:name="` + h[1] + `" adtcore:packageName="` + h[2] + `"/>`)
	}
	sb.WriteString(`</adtcore:objectReferences>`)
	return sb.String()
}

func searchStub(xml string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "informationsystem/search") {
			_, _ = io.WriteString(w, xml)
		}
	}
}

func TestCheckObjectPackageByName_NoWhitelistSendsNothing(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, searchStub(""))
	if err := client.CheckObjectPackageByName(context.Background(), "PROG", "ZDEMO", "Op"); err != nil {
		t.Fatalf("err = %v, want nil without a whitelist", err)
	}
	if n := len(rec.snapshot()); n != 0 {
		t.Errorf("%d request(s) sent without a whitelist", n)
	}
}

func TestCheckObjectPackageByName(t *testing.T) {
	cases := []struct {
		name    string
		objType string
		xml     string
		wantErr bool
	}{
		{"allowed package", "PROG", searchHitsXML([3]string{"PROG/P", "ZDEMO", "ZPKG"}), false},
		{"forbidden package", "PROG", searchHitsXML([3]string{"PROG/P", "ZDEMO", "SAP_STD"}), true},
		{"not found", "PROG", searchHitsXML(), true},
		{"type mismatch", "PROG", searchHitsXML([3]string{"CLAS/OC", "ZDEMO", "ZPKG"}), true},
		{"subtype counts as its type", "TABL", searchHitsXML([3]string{"TABL/DS", "ZDEMO", "ZPKG"}), false},
		{"include is a program", "INCL", searchHitsXML([3]string{"PROG/I", "ZDEMO", "ZPKG"}), false},
		{"function module is found as its group type", "FUNC", searchHitsXML([3]string{"FUGR/FF", "ZDEMO", "ZPKG"}), false},
		{"function group", "FUGR", searchHitsXML([3]string{"FUGR/F", "ZDEMO", "SAP_STD"}), true},
		{"message class", "MSAG", searchHitsXML([3]string{"MSAG/N", "ZDEMO", "ZPKG"}), false},
		{"one hit of several forbidden", "PROG", searchHitsXML(
			[3]string{"PROG/P", "ZDEMO", "ZPKG"}, [3]string{"PROG/P", "ZDEMO", "SAP_STD"}), true},
		{"unknown package", "PROG", searchHitsXML([3]string{"PROG/P", "ZDEMO", ""}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &adtRecorder{}
			client := newStubbedClient(t, rec, searchStub(tc.xml), WithAllowedPackages("Z*", "$TMP"))
			err := client.CheckObjectPackageByName(context.Background(), tc.objType, "ZDEMO", "Op")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
