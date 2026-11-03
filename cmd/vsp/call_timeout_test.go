package main

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func callTimeoutCmd(flag string) *cobra.Command {
	c := &cobra.Command{}
	c.Flags().Int("call-timeout", 0, "")
	if flag != "" {
		_ = c.Flags().Set("call-timeout", flag)
	}
	return c
}

func TestResolveCallTimeout(t *testing.T) {
	cases := []struct {
		flag, env string
		want      time.Duration
		wantErr   bool
	}{
		{"", "", 0, false},
		{"300", "", 300 * time.Second, false},
		{"", "300", 300 * time.Second, false},
		{"", "5m", 5 * time.Minute, false},
		{"99999", "", time.Hour, false},
		{"-1", "", 0, true},
		{"", "abc", 0, true},
		{"", "-5", 0, true},
		{"", "0.5", 0, true},
		{"0", "", 0, false},
		{"", "0s", 0, false},
		{"", "10000h", time.Hour, false},
		{"60", "10", 60 * time.Second, false}, // flag beats env
	}
	for _, c := range cases {
		viper.Set("CALL_TIMEOUT", c.env)
		got, err := resolveCallTimeout(callTimeoutCmd(c.flag))
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("flag=%q env=%q: got %v, %v", c.flag, c.env, got, err)
		}
	}
	viper.Set("CALL_TIMEOUT", "")
}
