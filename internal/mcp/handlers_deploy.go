// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_deploy.go contains handlers for deploying abapGit-format ZIPs to SAP packages.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/embedded/deps"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// deployTransportFormat is the shape of a workbench request number, checked
// before the first object is created so a typo cannot leave shells behind.
var deployTransportFormat = regexp.MustCompile(`^[A-Z0-9]{3}K[0-9]{6}$`)

// objectTypeMapping maps deployment type strings to CreatableObjectType and URL patterns
var objectTypeMapping = map[string]struct {
	creatableType adt.CreatableObjectType
	urlPattern    string // format string with %s for name
}{
	"PROG": {adt.ObjectTypeProgram, "/sap/bc/adt/programs/programs/%s"},
	"CLAS": {adt.ObjectTypeClass, "/sap/bc/adt/oo/classes/%s"},
	"INTF": {adt.ObjectTypeInterface, "/sap/bc/adt/oo/interfaces/%s"},
	"DDLS": {adt.ObjectTypeDDLS, "/sap/bc/adt/ddic/ddl/sources/%s"},
	"BDEF": {adt.ObjectTypeBDEF, "/sap/bc/adt/bo/behaviordefinitions/%s"},
	"SRVD": {adt.ObjectTypeSRVD, "/sap/bc/adt/ddic/srvd/sources/%s"},
}

// handleDeployZip deploys objects from an embedded abapGit-format ZIP to a SAP package.
// Uses a 3-phase approach for bulk deployment:
//
//	Phase 1: Create all objects (empty shells)
//	Phase 2: Upload source code (Lock → UpdateSource → Unlock, NO syntax check)
//	Phase 3: Mass activate all objects
func (s *Server) handleDeployZip(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse parameters
	source, _ := request.GetArguments()["source"].(string)
	if source == "" {
		return newToolResultError("source is required (e.g., 'abapgit-standalone', 'abapgit-full')"), nil
	}

	packageName, _ := request.GetArguments()["package"].(string)
	if packageName == "" {
		return newToolResultError("package is required (e.g., '$ZGIT')"), nil
	}
	packageName = strings.ToUpper(packageName)

	dryRun := false
	if dr, ok := request.GetArguments()["dry_run"].(bool); ok {
		dryRun = dr
	}

	typeFilter := ""
	if tf, ok := request.GetArguments()["type_filter"].(string); ok {
		typeFilter = strings.ToUpper(tf)
	}

	transportArg, _ := request.GetArguments()["transport"].(string)
	transport := adt.NormalizeTransport(transportArg)

	nameFilter := ""
	if nf, ok := request.GetArguments()["name_filter"].(string); ok {
		nameFilter = strings.ToUpper(nf)
	}

	if !dryRun {
		// A real run writes: refuse before any request (or any work on the ZIP) when
		// the policy forbids it. A dry run changes nothing and stays allowed.
		for _, op := range []adt.OperationType{adt.OpCreate, adt.OpUpdate, adt.OpActivate} {
			if refused := s.refuseUnderSafety(op, "DeployZip"); refused != nil {
				return refused, nil
			}
		}
		// A transportable package needs a request the caller names; SAP must never
		// generate one on its own.
		if strings.HasPrefix(packageName, "$") {
			if transport != "" {
				return newToolResultError("transport is not allowed for a local package ($...)"), nil
			}
		} else if transport == "" {
			return newToolResultError(fmt.Sprintf("transport is required: %s is a transportable package and a request is never chosen or created for you", packageName)), nil
		} else if !deployTransportFormat.MatchString(transport) {
			return newToolResultError(fmt.Sprintf("transport %q is not a request number (expected e.g. S4DK900001)", transport)), nil
		}
	}

	// Get ZIP data
	zipData := deps.GetDependencyZIP(source)
	if zipData == nil {
		available := deps.GetAvailableDependencies()
		var names []string
		for _, d := range available {
			if d.Available {
				names = append(names, d.Name)
			}
		}
		return newToolResultError(fmt.Sprintf("Source '%s' not found. Available: %s", source, strings.Join(names, ", "))), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "DeployZip: %s → %s\n", source, packageName)
	sb.WriteString(strings.Repeat("=", 60) + "\n\n")
	fmt.Fprintf(&sb, "Source: %s (%d bytes)\n", source, len(zipData))
	fmt.Fprintf(&sb, "Target: %s\n\n", packageName)

	// Parse ZIP
	files, err := deps.UnzipInMemory(zipData)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to parse ZIP: %v", err)), nil
	}

	// Create deployment plan
	plan := deps.CreateDeploymentPlan(source, packageName, files)
	fmt.Fprintf(&sb, "Found %d objects in %d files\n\n", plan.TotalObjects, plan.TotalFiles)

	// Filter and classify objects
	var deployable, skipped []deps.DeploymentObject
	for _, obj := range plan.Objects {
		// Type filter
		if typeFilter != "" && obj.Type != typeFilter {
			continue
		}
		// Name filter (simple wildcard)
		if nameFilter != "" {
			pattern := strings.ReplaceAll(nameFilter, "*", "")
			if !strings.Contains(obj.Name, pattern) {
				continue
			}
		}

		if _, ok := objectTypeMapping[obj.Type]; ok {
			deployable = append(deployable, obj)
		} else {
			skipped = append(skipped, obj)
		}
	}

	// Show deployment plan
	fmt.Fprintf(&sb, "Deployment Plan (%d deployable, %d skipped):\n", len(deployable), len(skipped))
	sb.WriteString(strings.Repeat("-", 60) + "\n")

	for _, obj := range deployable {
		includeInfo := ""
		if len(obj.Includes) > 0 {
			var incTypes []string
			for t := range obj.Includes {
				incTypes = append(incTypes, t)
			}
			includeInfo = fmt.Sprintf(" [+%s]", strings.Join(incTypes, ","))
		}
		fmt.Fprintf(&sb, "  ✓ %-6s %-40s%s\n", obj.Type, obj.Name, includeInfo)
	}

	if len(skipped) > 0 {
		sb.WriteString("\n  Skipped (requires ZADT_VSP):\n")
		for _, obj := range skipped {
			fmt.Fprintf(&sb, "  ⊘ %-6s %s\n", obj.Type, obj.Name)
		}
	}

	sb.WriteString(strings.Repeat("-", 60) + "\n\n")

	if dryRun {
		sb.WriteString("Dry run - no changes made.\n")
		return mcp.NewToolResultText(sb.String()), nil
	}

	if len(deployable) == 0 {
		sb.WriteString("Nothing to deploy.\n")
		return mcp.NewToolResultText(sb.String()), nil
	}

	// Ensure package exists
	fmt.Fprintf(&sb, "Checking package %s...\n", packageName)
	pkg, pkgErr := s.adtClient.GetPackage(ctx, packageName)
	if pkgErr != nil || pkg.URI == "" {
		fmt.Fprintf(&sb, "Creating package %s...\n", packageName)
		err = s.adtClient.CreateObject(ctx, adt.CreateObjectOptions{
			ObjectType:  adt.ObjectTypePackage,
			Name:        packageName,
			Description: fmt.Sprintf("Deployed from %s", source),
			Transport:   transport,
		})
		if err != nil {
			// Tolerate "already exists" - GetPackage may fail for $ packages
			if strings.Contains(err.Error(), "AlreadyExists") || strings.Contains(err.Error(), "already exist") {
				fmt.Fprintf(&sb, "  ✓ Package already exists\n")
			} else {
				return newToolResultError(fmt.Sprintf("Failed to create package: %v", err)), nil
			}
		} else {
			fmt.Fprintf(&sb, "  ✓ Package created\n")
		}
	} else {
		fmt.Fprintf(&sb, "  ✓ Package exists\n")
	}
	sb.WriteString("\n")

	// ================================================================
	// PHASE 1: Create all objects (empty shells)
	// ================================================================
	sb.WriteString("Phase 1: Creating objects...\n")
	var createSuccess, createSkipped, createFailed int

	for i, obj := range deployable {
		typeInfo, ok := objectTypeMapping[obj.Type]
		if !ok {
			continue
		}

		desc := obj.Description
		if desc == "" {
			desc = obj.Name
		}

		fmt.Fprintf(&sb, "  [%d/%d] Create %s %s... ", i+1, len(deployable), obj.Type, obj.Name)

		err := s.adtClient.CreateObject(ctx, adt.CreateObjectOptions{
			ObjectType:  typeInfo.creatableType,
			Name:        obj.Name,
			Description: desc,
			PackageName: packageName,
			Transport:   transport,
		})
		if err != nil {
			if strings.Contains(err.Error(), "AlreadyExists") || strings.Contains(err.Error(), "already exist") {
				fmt.Fprintf(&sb, "exists\n")
				createSkipped++
			} else {
				fmt.Fprintf(&sb, "FAIL: %v\n", err)
				createFailed++
			}
		} else {
			fmt.Fprintf(&sb, "ok\n")
			createSuccess++
		}
	}

	fmt.Fprintf(&sb, "\n  Phase 1 summary: %d created, %d existed, %d failed\n\n", createSuccess, createSkipped, createFailed)

	// ================================================================
	// PHASE 2: Upload source (Lock → UpdateSource → Unlock, NO syntax check)
	// ================================================================
	sb.WriteString("Phase 2: Uploading source code...\n")
	var uploadSuccess, uploadFailed int
	var uploadFailures, uploadWarnings []string

	for i, obj := range deployable {
		typeInfo, ok := objectTypeMapping[obj.Type]
		if !ok {
			continue
		}

		encodedName := url.PathEscape(strings.ToLower(obj.Name))
		objectURL := fmt.Sprintf(typeInfo.urlPattern, encodedName)
		sourceURL := objectURL + "/source/main"

		fmt.Fprintf(&sb, "  [%d/%d] Upload %s %s... ", i+1, len(deployable), obj.Type, obj.Name)

		err := s.adtClient.WithObjectLock(ctx, "DeployZip", objectURL, transport,
			func(ctx context.Context, lockHandle, tr string) error {
				// No syntax check on purpose: objects reference each other.
				return s.adtClient.UpdateSource(ctx, sourceURL, obj.MainSource, lockHandle, tr)
			})
		if errors.Is(err, adt.ErrLockReleaseFailed) {
			// The source is in; only the unlock failed.
			fmt.Fprintf(&sb, "uploaded, but: %v\n", err)
			uploadSuccess++
			uploadWarnings = append(uploadWarnings, fmt.Sprintf("%s %s: %v", obj.Type, obj.Name, err))
			continue
		}
		if err != nil {
			fmt.Fprintf(&sb, "UPLOAD FAIL: %v\n", err)
			uploadFailed++
			uploadFailures = append(uploadFailures, fmt.Sprintf("%s %s: %v", obj.Type, obj.Name, err))
			continue
		}

		fmt.Fprintf(&sb, "ok\n")
		uploadSuccess++
	}

	fmt.Fprintf(&sb, "\n  Phase 2 summary: %d uploaded, %d failed\n\n", uploadSuccess, uploadFailed)

	// ================================================================
	// PHASE 3: Iterative activation
	// ================================================================
	sb.WriteString("Phase 3: Iterative activation...\n")
	iterResult, err := s.adtClient.ActivatePackageIterative(ctx, packageName, 5)
	if err != nil {
		fmt.Fprintf(&sb, "  Activation error: %v\n", err)
	} else {
		for i, pass := range iterResult.Passes {
			fmt.Fprintf(&sb, "  Pass %d: %d activated, %d failed\n", i+1, len(pass.Activated), len(pass.Failed))
		}
		fmt.Fprintf(&sb, "  Total: %d activated across %d pass(es)\n", iterResult.TotalActivated, iterResult.Iterations)
		if iterResult.StillInactive == 0 {
			sb.WriteString("  All objects active — deployment verified.\n")
		} else {
			fmt.Fprintf(&sb, "  WARNING: %d object(s) still inactive in %s\n", iterResult.StillInactive, packageName)
		}
	}

	// ================================================================
	// SUMMARY
	// ================================================================
	sb.WriteString("\n" + strings.Repeat("=", 60) + "\n")
	fmt.Fprintf(&sb, "Deployment complete:\n")
	fmt.Fprintf(&sb, "  Phase 1 (Create):   %d ok, %d existed, %d failed\n", createSuccess, createSkipped, createFailed)
	fmt.Fprintf(&sb, "  Phase 2 (Upload):   %d ok, %d failed\n", uploadSuccess, uploadFailed)
	if iterResult != nil {
		fmt.Fprintf(&sb, "  Phase 3 (Activate): %s\n", iterResult.Summary)
	}

	if len(uploadWarnings) > 0 {
		sb.WriteString("\nUploaded, but the lock may still be held (check SM12):\n")
		for _, w := range uploadWarnings {
			fmt.Fprintf(&sb, "  • %s\n", w)
		}
	}

	if len(uploadFailures) > 0 {
		sb.WriteString("\nUpload failures:\n")
		for _, f := range uploadFailures {
			fmt.Fprintf(&sb, "  • %s\n", f)
		}
	}

	if len(skipped) > 0 {
		fmt.Fprintf(&sb, "\n%d objects skipped (not supported via ADT native).\n", len(skipped))
		sb.WriteString("Install ZADT_VSP to enable full object type support.\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}
