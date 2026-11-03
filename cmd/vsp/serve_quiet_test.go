package main

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestServeMCPSilencesUsage(t *testing.T) {
	cmd := &cobra.Command{}
	boom := errors.New("server failed")
	if err := serveMCP(cmd, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if !cmd.SilenceUsage {
		t.Fatal("usage must be silenced once the server runs")
	}
}
