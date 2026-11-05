package adt

import "testing"

func TestMoveObjectParams_TransportOnlyWhenGiven(t *testing.T) {
	local := moveObjectParams("PROG", "ZX", "$TMP", "", false)
	if _, ok := local["transport"]; ok {
		t.Error("transport must not be sent for a move between local packages")
	}
	if local["object"] != "PROG" || local["obj_name"] != "ZX" || local["new_package"] != "$TMP" {
		t.Errorf("unexpected params: %v", local)
	}
	withTR := moveObjectParams("PROG", "ZX", "ZABAP01", "S4DK000001", false)
	if withTR["transport"] != "S4DK000001" {
		t.Errorf("transport = %v", withTR["transport"])
	}
}

func TestMoveObjectParams_DryRunTravelsAsAQuotedString(t *testing.T) {
	// ZCL_VSP_RFC_SERVICE=>extract_param reads quoted scalars only
	dry := moveObjectParams("PROG", "ZX", "ZABAP01", "S4DK000001", true)
	if dry["dry_run"] != "true" {
		t.Errorf("dry_run = %#v, want the string \"true\"", dry["dry_run"])
	}
	real := moveObjectParams("PROG", "ZX", "ZABAP01", "S4DK000001", false)
	if _, ok := real["dry_run"]; ok {
		t.Error("dry_run must not be sent when false")
	}
	// A dry run has no meaning without a transport: it is the move-and-record path
	local := moveObjectParams("PROG", "ZX", "$TMP", "", true)
	if _, ok := local["dry_run"]; ok {
		t.Error("dry_run must not be sent for a move between local packages")
	}
}
