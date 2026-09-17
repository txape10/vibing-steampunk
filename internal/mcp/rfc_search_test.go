package mcp

import "testing"

func TestFuncnameLikePredicate_RejectsInjection(t *testing.T) {
	cases := []string{
		"BAPI_USER' OR FUNCNAME LIKE 'A",
		"'; DELETE FROM TFDIR WHERE ''='",
		"X' AND FMODE = 'Z",
	}
	for _, pattern := range cases {
		if _, err := funcnameLikePredicate(pattern); err == nil {
			t.Errorf("funcnameLikePredicate(%q) = nil error, want rejection", pattern)
		}
	}
}

func TestFuncnameLikePredicate_AcceptsValidPatterns(t *testing.T) {
	cases := map[string]string{
		"":              "%",
		"BAPI_USER*":    "BAPI_USER%",
		"*USER*":        "%USER%",
		"RFC_PING":      "%RFC_PING%",
		"/NAMESPACE/FM": "%/NAMESPACE/FM%",
	}
	for pattern, want := range cases {
		got, err := funcnameLikePredicate(pattern)
		if err != nil {
			t.Errorf("funcnameLikePredicate(%q) unexpected error: %v", pattern, err)
			continue
		}
		if got != want {
			t.Errorf("funcnameLikePredicate(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestSanitizePackageForFilename_RejectsTraversal(t *testing.T) {
	cases := map[string]string{
		"$TMP":              "TMP",
		"../../etc/passwd":  "etcpasswd",
		"ZABAP01":           "ZABAP01",
		"Z/../../../secret": "Zsecret",
		"":                  "package",
		"...":               "package",
	}
	for pkg, want := range cases {
		got := sanitizePackageForFilename(pkg)
		if got != want {
			t.Errorf("sanitizePackageForFilename(%q) = %q, want %q", pkg, got, want)
		}
		if got == ".." || got == "." {
			t.Errorf("sanitizePackageForFilename(%q) = %q is a path traversal component", pkg, got)
		}
	}
}
