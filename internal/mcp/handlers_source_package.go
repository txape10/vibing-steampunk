package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// A create over a stale TADIR row (DELFLAG = 'X', left by a deleted object)
// reuses the old row: SAP answers success but the object stays in the OLD
// package. For a transportable target this is checked before the POST (refuse
// when the object could not be moved back) and after it (move it with the
// explicit request, dry run first, and say so if that fails too).

// packageCheck is the state carried from before the create to after it.
type packageCheck struct {
	objType   string
	name      string
	pkg       string
	transport string
	stale     string // old package of the stale row, "" when none was seen
	warning   string // a TADIR read that failed before the create (D4)
}

// movableTypes are the object types MoveObject can record in a request.
func movableType(t string) bool {
	switch t {
	case "PROG", "CLAS", "INTF":
		return true
	}
	return false
}

// planPackageCheck decides whether the create needs the check and, when a stale
// TADIR row says the move back would be impossible, refuses before anything is
// created. It returns (nil, nil) when the check does not apply.
func (s *Server) planPackageCheck(ctx context.Context, objectType, name, pkg, transport, mode string) (*packageCheck, *mcp.CallToolResult) {
	t := strings.ToUpper(strings.SplitN(strings.TrimSpace(objectType), "/", 2)[0])
	pkg = strings.ToUpper(strings.TrimSpace(pkg))
	if pkg == "" || strings.HasPrefix(pkg, "$") || strings.EqualFold(mode, "update") || !movableType(t) {
		return nil, nil
	}
	pc := &packageCheck{objType: t, name: strings.ToUpper(name), pkg: pkg, transport: adt.NormalizeTransport(transport)}
	row, err := s.adtClient.TADIRRow(ctx, t, name)
	if err != nil {
		pc.warning = fmt.Sprintf("TADIR could not be read before the create (%v): the package was not pre-checked", err)
		return pc, nil
	}
	if !row.Found || !row.Deleted || strings.EqualFold(row.Package, pkg) {
		return pc, nil
	}
	pc.stale = row.Package
	if reason := s.moveBackImpossible(pc); reason != "" {
		return nil, newToolResultError(fmt.Sprintf("Create refused: %s %s has a stale TADIR row in package %s (left by a deleted object), so SAP would create it there instead of in %s, and vsp could not move it afterwards: %s. Nothing was created.",
			t, pc.name, row.Package, pkg, reason))
	}
	return pc, nil
}

// moveBackImpossible returns why the automatic MOVE could not be done, or "".
// Only the request the caller named is used (never one vsp would choose).
func (s *Server) moveBackImpossible(pc *packageCheck) string {
	if pc.transport == "" {
		return "no explicit transport was given (name transport=<workbench request>, vsp never picks one for a move)"
	}
	if !moveTransportPattern.MatchString(pc.transport) {
		return fmt.Sprintf("%q is not a transport request number", pc.transport)
	}
	if err := s.adtClient.Safety().CheckOperation(adt.OpUpdate, "MoveObject"); err != nil {
		return err.Error()
	}
	if err := s.adtClient.Safety().CheckTransportableEdit(pc.transport, "MoveObject"); err != nil {
		return err.Error()
	}
	if pc.stale != "" {
		if err := s.adtClient.Safety().CheckPackage(pc.stale); err != nil {
			return fmt.Sprintf("the package it is in (%s) is not allowed: %v", pc.stale, err)
		}
	}
	return ""
}

// verifyPackageAfterCreate runs after a successful create. It never turns a
// created object into an error silently: it fills result.PackageCheck and
// returns true when the object is NOT in the requested package.
func (s *Server) verifyPackageAfterCreate(ctx context.Context, pc *packageCheck, result *adt.WriteSourceResult) (mismatch bool) {
	row, err := s.adtClient.TADIRRow(ctx, pc.objType, pc.name)
	if err != nil {
		result.PackageCheck = fmt.Sprintf("not verified: TADIR could not be read (%v). Check the package of %s %s in SE80.", err, pc.objType, pc.name)
		return false
	}
	if row.Found && strings.EqualFold(row.Package, pc.pkg) {
		result.PackageCheck = "ok: TADIR has the object in " + pc.pkg
		return false
	}
	actual := row.Package
	if !row.Found {
		result.PackageCheck = fmt.Sprintf("not verified: no TADIR row for %s %s was found after the create.", pc.objType, pc.name)
		return false
	}

	why := s.moveBackImpossible(&packageCheck{transport: pc.transport, stale: actual})
	if why == "" {
		why = s.autoMove(ctx, pc)
		if why == "" {
			if after, aerr := s.adtClient.TADIRRow(ctx, pc.objType, pc.name); aerr == nil && strings.EqualFold(after.Package, pc.pkg) {
				result.PackageCheck = fmt.Sprintf("corrected: SAP created %s %s in %s (stale TADIR row); vsp moved it to %s and recorded it in %s",
					pc.objType, pc.name, actual, pc.pkg, pc.transport)
				return false
			}
			why = "the move answered success but TADIR still does not show the requested package"
		}
	}
	result.PackageCheck = fmt.Sprintf("MISMATCH: %s %s was created and activated but is in package %s, not %s, and vsp could not move it: %s. "+
		"Nothing was deleted. Choose: (1) you move it yourself (SE80 / SE03 'change package') or (2) tell vsp to delete the object so you create it manually.",
		pc.objType, pc.name, actual, pc.pkg, why)
	return true
}

// autoMove runs MOVE with a dry run first. It returns "" on success or why not.
func (s *Server) autoMove(ctx context.Context, pc *packageCheck) string {
	if err := s.adtClient.Safety().CheckPackage(pc.pkg); err != nil {
		return err.Error()
	}
	if s.moveFn == nil {
		if err := s.ensureDebugWSClient(ctx); err != nil {
			return fmt.Sprintf("cannot connect to ZADT_VSP: %v", err)
		}
	}
	dry, err := s.postCreateMove(ctx, pc, true)
	if err != nil {
		return fmt.Sprintf("dry run failed: %v", err)
	}
	if !dry.Success {
		return fmt.Sprintf("dry run refused [%s]: %s", dry.Code, dry.Message)
	}
	if dry.Recording != "dry_run" && dry.Recording != "already_recorded" {
		return fmt.Sprintf("the ZADT_VSP backend did not honour dry_run (answered %q), so the object may already have been moved: check TADIR/E071", dry.Recording)
	}
	res, err := s.postCreateMove(ctx, pc, false)
	if err != nil {
		return fmt.Sprintf("move failed: %v", err)
	}
	if !res.Success {
		return fmt.Sprintf("move refused [%s]: %s", res.Code, res.Message)
	}
	if res.Recording == "" {
		return "the backend moved it without recording it in the request (it predates the transport support): check TADIR/E071"
	}
	return ""
}

// postCreateMove is the single place the automatic correction talks to the
// WebSocket backend; moveFn replaces it in tests.
func (s *Server) postCreateMove(ctx context.Context, pc *packageCheck, dryRun bool) (*adt.MoveObjectResult, error) {
	if s.moveFn != nil {
		return s.moveFn(ctx, pc.objType, pc.name, pc.pkg, pc.transport, dryRun)
	}
	return s.debugWSClient.MoveObject(ctx, pc.objType, pc.name, pc.pkg, pc.transport, dryRun)
}

// incompleteCreateNote is added when a create that may have reached SAP
// failed before the package could be verified: the object can exist in the
// stale package.
func incompleteCreateNote(pc *packageCheck) string {
	if pc == nil || pc.stale == "" {
		return ""
	}
	return fmt.Sprintf(" NOTE: %s %s may have been created in the stale package %s instead of %s before the failure; check TADIR/SE80 before retrying.",
		pc.objType, pc.name, pc.stale, pc.pkg)
}
