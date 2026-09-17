package saprfc

import (
	"context"
	"fmt"
	"strings"

	"github.com/oisee/open-rfc-go/rfc"
)

// AbapGitSerializeFM is abapGit's RFC-enabled serializer. It ships with abapGit
// itself (function group ZABAPGIT_PARALLEL), so its presence is a property of the
// system, not of vsp.
const AbapGitSerializeFM = "Z_ABAPGIT_SERIALIZE_PACKAGE"

// ExportOptions tunes what the serializer produces.
type ExportOptions struct {
	// FolderLogic is abapGit's folder logic: "FULL" (the package hierarchy) or
	// "PREFIX" (names stripped of the package prefix). Empty leaves the default.
	FolderLogic string
	// MainLanguageOnly skips translations, which is usually what a diff wants.
	MainLanguageOnly bool
}

// ExportPackage serializes an ABAP package into an abapGit ZIP over RFC.
//
// This is one call where the ADT route needs the whole vsp export → APC WebSocket
// → ZCL_VSP_GIT_SERVICE → cl_abap_zip chain, and it works on any system that has
// abapGit installed, with no vsp helper deployed at all.
func ExportPackage(ctx context.Context, c *rfc.Client, pkg string, opts ExportOptions) ([]byte, error) {
	params := rfc.Params{"IV_PACKAGE": strings.ToUpper(strings.TrimSpace(pkg))}
	if opts.FolderLogic != "" {
		params["IV_FOLDER_LOGIC"] = strings.ToUpper(opts.FolderLogic)
	}
	if opts.MainLanguageOnly {
		params["IV_MAIN_LANG_ONLY"] = "X"
	}

	res, err := c.Call(ctx, AbapGitSerializeFM, params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", AbapGitSerializeFM, err)
	}
	zip, ok := res.Get("EV_XSTRING").([]byte)
	if !ok || len(zip) == 0 {
		return nil, fmt.Errorf("%s returned no ZIP data for package %q", AbapGitSerializeFM, pkg)
	}
	// A ZIP always starts with "PK"; anything else means we were handed something
	// other than an archive and the caller should not write it out as one.
	if len(zip) < 2 || zip[0] != 'P' || zip[1] != 'K' {
		return nil, fmt.Errorf("%s returned %d bytes that are not a ZIP archive", AbapGitSerializeFM, len(zip))
	}
	return zip, nil
}
