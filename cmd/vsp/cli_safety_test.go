package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestGetClientHonoursDeclaredSafety verifies that a systemParams with
// ReadOnly set actually reaches the built client's safety config — the bug
// this file exists to pin: --read-only (and the equivalent per-system
// .vsp.json / SAP_READ_ONLY setting) used to be silently dropped for every
// command built via resolveSystemParams/getClient.
func TestGetClientHonoursDeclaredSafety(t *testing.T) {
	params := &systemParams{
		URL:      "https://sap.example:44300",
		User:     "TESTER",
		Password: "secret",
		Client:   "001",
		Language: "EN",
		ReadOnly: true,
	}

	client, err := getClient(params)
	if err != nil {
		t.Fatalf("getClient: %v", err)
	}

	if !client.Safety().ReadOnly {
		t.Fatal("ReadOnly did not reach the client's safety config")
	}
}

func TestGetClientCarriesAllowedPackages(t *testing.T) {
	params := &systemParams{
		URL:             "https://sap.example:44300",
		User:            "TESTER",
		Password:        "secret",
		Client:          "001",
		Language:        "EN",
		AllowedPackages: []string{"Z*", "$TMP"},
	}

	client, err := getClient(params)
	if err != nil {
		t.Fatalf("getClient: %v", err)
	}

	got := strings.Join(client.Safety().AllowedPackages, ",")
	if got != "Z*,$TMP" {
		t.Fatalf("AllowedPackages did not reach the client: got %q", got)
	}
}

func TestGetClientUnrestrictedByDefault(t *testing.T) {
	params := &systemParams{
		URL:      "https://sap.example:44300",
		User:     "TESTER",
		Password: "secret",
		Client:   "001",
		Language: "EN",
	}

	client, err := getClient(params)
	if err != nil {
		t.Fatalf("getClient: %v", err)
	}

	safety := client.Safety()
	if safety.ReadOnly {
		t.Fatal("expected ReadOnly=false by default")
	}
	if len(safety.AllowedPackages) != 0 {
		t.Fatalf("expected no AllowedPackages by default, got %v", safety.AllowedPackages)
	}
}

func TestSplitListIgnoresBlanks(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"Z*", "Z*"},
		{"Z*, $TMP", "Z*,$TMP"},
		{"Z*,, $TMP,", "Z*,$TMP"},
		{" Z* ,  $TMP ", "Z*,$TMP"},
	}
	for _, tt := range tests {
		got := strings.Join(splitList(tt.in), ",")
		if got != tt.want {
			t.Errorf("splitList(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestGetClientCarriesTransportSafety covers the 5 transport-safety fields
// added alongside ReadOnly/AllowedPackages — upstream's own port added no
// test coverage for these, so this table is new to this fork.
func TestGetClientCarriesTransportSafety(t *testing.T) {
	base := func() systemParams {
		return systemParams{
			URL:      "https://sap.example:44300",
			User:     "TESTER",
			Password: "secret",
			Client:   "001",
			Language: "EN",
		}
	}

	t.Run("EnableTransports", func(t *testing.T) {
		p := base()
		p.EnableTransports = true
		client, err := getClient(&p)
		if err != nil {
			t.Fatalf("getClient: %v", err)
		}
		if !client.Safety().EnableTransports {
			t.Fatal("EnableTransports did not reach the client")
		}
	})

	t.Run("TransportReadOnly", func(t *testing.T) {
		p := base()
		p.TransportReadOnly = true
		client, err := getClient(&p)
		if err != nil {
			t.Fatalf("getClient: %v", err)
		}
		if !client.Safety().TransportReadOnly {
			t.Fatal("TransportReadOnly did not reach the client")
		}
	})

	t.Run("AllowedTransports", func(t *testing.T) {
		p := base()
		p.AllowedTransports = []string{"A4HK*"}
		client, err := getClient(&p)
		if err != nil {
			t.Fatalf("getClient: %v", err)
		}
		if got := strings.Join(client.Safety().AllowedTransports, ","); got != "A4HK*" {
			t.Fatalf("AllowedTransports did not reach the client: got %q", got)
		}
	})

	t.Run("AllowTransportableEdits", func(t *testing.T) {
		p := base()
		p.AllowTransportableEdits = true
		client, err := getClient(&p)
		if err != nil {
			t.Fatalf("getClient: %v", err)
		}
		if !client.Safety().AllowTransportableEdits {
			t.Fatal("AllowTransportableEdits did not reach the client")
		}
	})

	t.Run("BlockFreeSQL", func(t *testing.T) {
		p := base()
		p.BlockFreeSQL = true
		client, err := getClient(&p)
		if err != nil {
			t.Fatalf("getClient: %v", err)
		}
		if !client.Safety().BlockFreeSQL {
			t.Fatal("BlockFreeSQL did not reach the client")
		}
	})
}

// SAP_READ_ONLY=1 (or yes/on) used to count only in the named-system branch of
// resolveSystemParams; the env-only branch, which is how the MCP server is
// deployed, compared against the literal "true".
func TestResolveSystemParams_EnvOnlyReadOnlyAcceptsTheUsualSpellings(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "1", "yes", "on"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("SAP_URL", "https://sap.example:44300")
			t.Setenv("SAP_USER", "TESTER")
			t.Setenv("SAP_PASSWORD", "secret")
			t.Setenv("SAP_READ_ONLY", v)
			prev := systemName
			systemName = ""
			t.Cleanup(func() { systemName = prev })

			params, err := resolveSystemParams(&cobra.Command{})
			if err != nil {
				t.Fatalf("resolveSystemParams: %v", err)
			}
			if !params.ReadOnly {
				t.Errorf("SAP_READ_ONLY=%q should switch read-only on", v)
			}
		})
	}
}
