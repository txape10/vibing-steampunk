package adt

import (
	"strings"
	"testing"
)

func TestJobStatusText(t *testing.T) {
	cases := map[string]string{
		"P": "scheduled", "S": "released", "R": "running", "F": "finished",
		"A": "cancelled", "Y": "ready", "": "", "z": "unknown", "XX": "unknown",
		" p ": "scheduled", // TBTCO can pad the field
	}
	for in, want := range cases {
		if got := JobStatusText(in); got != want {
			t.Errorf("JobStatusText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidJobName(t *testing.T) {
	for _, n := range []string{"VSP' OR JOBNAME LIKE 'Z", "X'; DELETE FROM TBTCO WHERE ''='", "", strings.Repeat("A", 33), "A*", "A B"} {
		if ValidJobName(n) {
			t.Errorf("ValidJobName(%q) = true, want false", n)
		}
	}
	for _, n := range []string{"VSP_ZTESTRCG1", "job-name_123", "A", "/NS/JOB"} {
		if !ValidJobName(n) {
			t.Errorf("ValidJobName(%q) = false, want true", n)
		}
	}
}

func TestValidJobNamePattern(t *testing.T) {
	for _, n := range []string{"VSP_*", "A+B", "*"} {
		if !ValidJobNamePattern(n) {
			t.Errorf("ValidJobNamePattern(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"", "A' OR 1=1", strings.Repeat("A", 33), "A%"} {
		if ValidJobNamePattern(n) {
			t.Errorf("ValidJobNamePattern(%q) = true, want false", n)
		}
	}
}

func TestValidJobCount(t *testing.T) {
	for _, c := range []string{"12345678' OR '1'='1", "", "123456789", "abcdefgh"} {
		if ValidJobCount(c) {
			t.Errorf("ValidJobCount(%q) = true, want false", c)
		}
	}
	for _, c := range []string{"00000001", "12345678", "1"} {
		if !ValidJobCount(c) {
			t.Errorf("ValidJobCount(%q) = false, want true", c)
		}
	}
}
