package adt

import "testing"

func TestMoveObjectParams_TransportOnlyWhenGiven(t *testing.T) {
	local := moveObjectParams("PROG", "ZX", "$TMP", "")
	if _, ok := local["transport"]; ok {
		t.Error("transport must not be sent for a move between local packages")
	}
	if local["object"] != "PROG" || local["obj_name"] != "ZX" || local["new_package"] != "$TMP" {
		t.Errorf("unexpected params: %v", local)
	}
	withTR := moveObjectParams("PROG", "ZX", "ZABAP01", "S4DK000001")
	if withTR["transport"] != "S4DK000001" {
		t.Errorf("transport = %v", withTR["transport"])
	}
}
