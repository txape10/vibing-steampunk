package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/oisee/vibing-steampunk/internal/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// resolveCallTimeout reads --call-timeout, else SAP_CALL_TIMEOUT: seconds
// (the env also takes a Go duration such as 5m). 0, or nothing set, means no
// budget of the server's own. Anything else that is not at least one second
// is an error, not a silent "no budget": a typo would otherwise take the limit
// the operator meant to set away without a word. Capped at MaxCallTimeout.
func resolveCallTimeout(cmd *cobra.Command) (time.Duration, error) {
	var d time.Duration
	var source string
	if cmd.Flags().Changed("call-timeout") {
		secs, err := cmd.Flags().GetInt("call-timeout")
		if err != nil {
			return 0, fmt.Errorf("--call-timeout: %w", err)
		}
		source = fmt.Sprintf("--call-timeout %d", secs)
		if secs < 0 {
			return 0, fmt.Errorf("%s: must be a number of seconds, at least 1 (0 for none)", source)
		}
		if secs > int(mcp.MaxCallTimeout/time.Second) {
			return mcp.MaxCallTimeout, nil
		}
		d = time.Duration(secs) * time.Second
	} else if v := strings.TrimSpace(viper.GetString("CALL_TIMEOUT")); v != "" {
		source = fmt.Sprintf("SAP_CALL_TIMEOUT=%q", v)
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
				return 0, fmt.Errorf("%s: must be a number of seconds, at least 1 (0 for none)", source)
			}
			if f > mcp.MaxCallTimeout.Seconds() {
				return mcp.MaxCallTimeout, nil
			}
			d = time.Duration(f * float64(time.Second))
		} else if pd, err := time.ParseDuration(v); err == nil {
			if pd < 0 {
				return 0, fmt.Errorf("%s: must not be negative", source)
			}
			d = pd
		} else {
			return 0, fmt.Errorf("%s: not a number of seconds or a duration such as 5m", source)
		}
	}
	if d == 0 {
		return 0, nil
	}
	if d < time.Second {
		return 0, fmt.Errorf("%s: below 1s; give at least one second, or 0 for none", source)
	}
	if d > mcp.MaxCallTimeout {
		d = mcp.MaxCallTimeout
	}
	return d, nil
}
