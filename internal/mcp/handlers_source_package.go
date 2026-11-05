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
	created   string // what exists after the create, for the MISMATCH text
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

// packageStatus is the outcome of the check after a create.
type packageStatus int

const (
	pkgOK         packageStatus = iota // TADIR has the requested package
	pkgCorrected                       // it was elsewhere and vsp moved it
	pkgUnverified                      // TADIR could not be read / no row
	pkgMismatch                        // it is elsewhere and could not be moved
)

// createdAs is how the MISMATCH text describes what exists in SAP.
func (pc *packageCheck) createdAs() string {
	if pc.created != "" {
		return pc.created
	}
	return "created and activated"
}

// checkPackageAfterCreate runs after a create that reached SAP. It never
// deletes anything. It returns the text for the caller's packageCheck field
// and the status. If the call's context already ran out (a long-call budget)
// TADIR is still read on a detached context but no move is attempted.
func (s *Server) checkPackageAfterCreate(ctx context.Context, pc *packageCheck) (string, packageStatus) {
	expired := ctx.Err() != nil
	rctx := ctx
	if expired {
		var cancel context.CancelFunc
		rctx, cancel = adt.CleanupContext(ctx)
		defer cancel()
	}
	row, err := s.adtClient.TADIRRow(rctx, pc.objType, pc.name)
	if err != nil {
		return fmt.Sprintf("not verified: TADIR could not be read (%v). Check the package of %s %s in SE80.", err, pc.objType, pc.name), pkgUnverified
	}
	if row.Found && strings.EqualFold(row.Package, pc.pkg) {
		return "ok: TADIR has the object in " + pc.pkg, pkgOK
	}
	actual := row.Package
	if !row.Found {
		return fmt.Sprintf("not verified: no TADIR row for %s %s was found after the create.", pc.objType, pc.name), pkgUnverified
	}

	why := ""
	if expired {
		why = "the call's time budget expired, so vsp did not try to move it"
	} else if why = s.moveBackImpossible(&packageCheck{transport: pc.transport, stale: actual}); why == "" {
		why = s.autoMove(ctx, pc)
		if why == "" {
			if after, aerr := s.adtClient.TADIRRow(ctx, pc.objType, pc.name); aerr == nil && strings.EqualFold(after.Package, pc.pkg) {
				return fmt.Sprintf("corrected: SAP created %s %s in %s (stale TADIR row); vsp moved it to %s and recorded it in %s",
					pc.objType, pc.name, actual, pc.pkg, pc.transport), pkgCorrected
			}
			why = "the move answered success but TADIR still does not show the requested package"
		}
	}
	return fmt.Sprintf("MISMATCH: %s %s was %s but is in package %s, not %s, and vsp could not move it: %s. "+
		"Nothing was deleted. Choose: (1) you move it yourself (SE80 / SE03 'change package') or (2) tell vsp to delete the object so you create it manually.",
		pc.objType, pc.name, pc.createdAs(), actual, pc.pkg, why), pkgMismatch
}

// verifyPackageAfterCreate is the WriteSource form: it fills result.PackageCheck
// and returns true when the object is NOT in the requested package.
func (s *Server) verifyPackageAfterCreate(ctx context.Context, pc *packageCheck, result *adt.WriteSourceResult) (mismatch bool) {
	text, st := s.checkPackageAfterCreate(ctx, pc)
	result.PackageCheck = text
	return st == pkgMismatch
}

// reportPackageOnly is for a create that reached SAP but whose workflow then
// failed: it only says where the object is, it never moves anything.
func (s *Server) reportPackageOnly(ctx context.Context, pc *packageCheck) string {
	rctx, cancel := adt.CleanupContext(ctx)
	defer cancel()
	row, err := s.adtClient.TADIRRow(rctx, pc.objType, pc.name)
	switch {
	case err != nil:
		return fmt.Sprintf("not verified: TADIR could not be read (%v). The workflow failed after the object was created; check the package of %s %s in SE80.", err, pc.objType, pc.name)
	case !row.Found:
		return fmt.Sprintf("not verified: no TADIR row for %s %s was found.", pc.objType, pc.name)
	case strings.EqualFold(row.Package, pc.pkg):
		return "TADIR has the object in " + pc.pkg + " (the workflow failed after it was created; nothing was moved)"
	}
	return fmt.Sprintf("WARNING: %s %s exists in package %s, not %s (stale TADIR row), and the workflow failed after it was created, so vsp did not move it. Nothing was deleted.",
		pc.objType, pc.name, row.Package, pc.pkg)
}

// withWarning prefixes a pre-create warning (TADIR unreadable, D4).
func (pc *packageCheck) withWarning(text string) string {
	if pc == nil || pc.warning == "" {
		return text
	}
	return strings.TrimSpace(pc.warning + ". " + text)
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
