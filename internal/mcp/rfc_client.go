// Package mcp provides the MCP server implementation for ABAP ADT tools.
// rfc_client.go bridges Server.config to a classic-RFC client (pkg/saprfc,
// github.com/oisee/open-rfc-go) — a direct socket connection to the SAP
// gateway, independent of the ZADT_VSP WebSocket bridge the rest of the
// "debug" domain still uses.
package mcp

import (
	"context"
	"fmt"

	"github.com/oisee/open-rfc-go/rfc"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// ensureRFCClient lazily opens the classic-RFC client used by CALL_RFC,
// RFC_SEARCH, RFC_METADATA and abapGit package export. The destination
// (gateway host/sysnr/port) derives from Server.config's RFC* fields, falling
// back to the ADT connection's host/credentials when those are empty — see
// saprfc.Resolve. The underlying client pools and re-dials its own
// connections, so opening it once and reusing it is safe across calls.
func (s *Server) ensureRFCClient(ctx context.Context) (*rfc.Client, error) {
	if s.rfcClient != nil {
		return s.rfcClient, nil
	}

	dest, err := saprfc.Resolve(saprfc.Input{
		URL:         s.config.BaseURL,
		User:        s.config.Username,
		Password:    s.config.Password,
		Client:      s.config.Client,
		Language:    s.config.Language,
		RFCHost:     s.config.RFCHost,
		RFCSysnr:    s.config.RFCSysnr,
		RFCPort:     s.config.RFCPort,
		RFCUser:     s.config.RFCUser,
		RFCPassword: s.config.RFCPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("RFC destination: %w", err)
	}

	c, err := saprfc.Open(ctx, dest)
	if err != nil {
		return nil, fmt.Errorf("RFC logon to %s:%d failed: %w", dest.Host, dest.Port, err)
	}
	s.rfcClient = c
	return c, nil
}
