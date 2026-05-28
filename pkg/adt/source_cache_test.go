package adt

import (
	"testing"
	"time"
)

func TestSourceCache_HitMiss(t *testing.T) {
	sc := newSourceCache()

	_, ok := sc.get("CLAS", "ZCL_X", "", "", "")
	if ok {
		t.Fatal("expected cache miss on empty cache")
	}

	sc.set("CLAS", "ZCL_X", "", "", "", "CLASS zcl_x DEFINITION.")
	got, ok := sc.get("CLAS", "ZCL_X", "", "", "")
	if !ok {
		t.Fatal("expected cache hit after set")
	}
	if got != "CLASS zcl_x DEFINITION." {
		t.Fatalf("unexpected source: %q", got)
	}

	// Different method key must miss
	_, ok = sc.get("CLAS", "ZCL_X", "MY_METHOD", "", "")
	if ok {
		t.Fatal("different method key should miss")
	}
}

func TestSourceCache_TTL(t *testing.T) {
	sc := &SourceCache{
		entries: make(map[string]cacheEntry),
		ttl:     50 * time.Millisecond,
	}
	sc.set("PROG", "ZTEST", "", "", "", "REPORT ztest.")

	_, ok := sc.get("PROG", "ZTEST", "", "", "")
	if !ok {
		t.Fatal("expected hit before TTL expiry")
	}

	time.Sleep(60 * time.Millisecond)

	_, ok = sc.get("PROG", "ZTEST", "", "", "")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestSourceCache_InvalidateByName(t *testing.T) {
	sc := newSourceCache()
	sc.set("CLAS", "ZCL_EM", "", "", "", "source1")
	sc.set("CLAS", "ZCL_EM", "MY_METHOD", "", "", "source2")
	sc.set("CLAS", "ZCL_OTHER", "", "", "", "source3")

	sc.InvalidateByName("ZCL_EM")

	if _, ok := sc.get("CLAS", "ZCL_EM", "", "", ""); ok {
		t.Error("ZCL_EM should be evicted")
	}
	if _, ok := sc.get("CLAS", "ZCL_EM", "MY_METHOD", "", ""); ok {
		t.Error("ZCL_EM method variant should be evicted")
	}
	if _, ok := sc.get("CLAS", "ZCL_OTHER", "", "", ""); !ok {
		t.Error("ZCL_OTHER should still be cached")
	}
}

func TestSourceCache_InvalidateByURL(t *testing.T) {
	sc := newSourceCache()
	sc.set("CLAS", "ZCL_X", "", "", "", "source")

	// Class URL with include path: should still extract class name
	sc.InvalidateByURL("/sap/bc/adt/oo/classes/zcl_x/includes/implementations")
	if _, ok := sc.get("CLAS", "ZCL_X", "", "", ""); ok {
		t.Error("ZCL_X should be evicted via include URL")
	}
}

func TestCacheNameFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"/sap/bc/adt/oo/classes/zcl_em", "ZCL_EM"},
		{"/sap/bc/adt/oo/classes/zcl_em/includes/implementations", "ZCL_EM"},
		{"/sap/bc/adt/programs/programs/zreport", "ZREPORT"},
		{"/sap/bc/adt/programs/includes/zinc_top", "ZINC_TOP"},
	}
	for _, tc := range cases {
		got := cacheNameFromURL(tc.url)
		if got != tc.want {
			t.Errorf("cacheNameFromURL(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}
