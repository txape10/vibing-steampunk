// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_install.go contains handlers for installing ZADT_VSP and abapGit.
package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	embedded "github.com/oisee/vibing-steampunk/embedded/abap"
	"github.com/oisee/vibing-steampunk/embedded/deps"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// routeInstallAction routes "system" with install-related types.
func (s *Server) routeInstallAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "system" {
		return nil, false, nil
	}
	installType := getStringParam(params, "type")
	switch installType {
	case "install_zadt_vsp":
		return s.callHandler(ctx, s.handleInstallZADTVSP, params)
	case "list_dependencies":
		return s.callHandler(ctx, s.handleListDependencies, params)
	case "deploy_zip":
		return s.callHandler(ctx, s.handleDeployZip, params)
	}
	return nil, false, nil
}

// --- Install Handlers ---

func (s *Server) handleInstallZADTVSP(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse parameters
	packageName := "$ZADT_VSP"
	if pkg, ok := request.GetArguments()["package"].(string); ok && pkg != "" {
		packageName = strings.ToUpper(pkg)
	}

	skipGitService := false
	if skip, ok := request.GetArguments()["skip_git_service"].(bool); ok {
		skipGitService = skip
	}

	checkOnly := false
	if check, ok := request.GetArguments()["check_only"].(bool); ok {
		checkOnly = check
	}

	// Validate package name
	if !strings.HasPrefix(packageName, "$") {
		return newToolResultError("Package name must start with $ (local package)"), nil
	}

	var sb strings.Builder
	sb.WriteString("ZADT_VSP Installation\n")
	sb.WriteString("=====================\n\n")

	// Phase 1: Check prerequisites
	sb.WriteString("Checking prerequisites...\n")

	// Check if package exists (verify URI is populated - empty URI means package doesn't really exist)
	packageExists := false
	pkg, err := s.adtClient.GetPackage(ctx, packageName)
	if err == nil && pkg.URI != "" {
		packageExists = true
		fmt.Fprintf(&sb, "  ✓ Package %s exists\n", packageName)
	} else {
		fmt.Fprintf(&sb, "  → Package %s will be created\n", packageName)
	}

	// Check for abapGit (for Git service)
	hasAbapGit := false
	results, err := s.adtClient.SearchObject(ctx, "ZCL_ABAPGIT_OBJECTS", 1)
	if err == nil && len(results) > 0 {
		hasAbapGit = true
		sb.WriteString("  ✓ abapGit detected → Git service will be deployed\n")
	} else {
		sb.WriteString("  ⚠ abapGit not detected → Git service will be skipped\n")
		skipGitService = true
	}

	// Check existing objects
	objects := embedded.GetObjects()
	existingObjects := []string{}
	for _, obj := range objects {
		results, err := s.adtClient.SearchObject(ctx, obj.Name, 1)
		if err == nil && len(results) > 0 {
			existingObjects = append(existingObjects, obj.Name)
		}
	}
	if len(existingObjects) > 0 {
		fmt.Fprintf(&sb, "  ⚠ Existing objects will be updated: %s\n", strings.Join(existingObjects, ", "))
	}

	sb.WriteString("\n")

	// If check_only, stop here
	if checkOnly {
		sb.WriteString("Check complete (--check_only mode, no changes made).\n\n")
		sb.WriteString("Objects to deploy:\n")
		for i, obj := range objects {
			if obj.Optional && skipGitService && obj.Name == "ZCL_VSP_GIT_SERVICE" {
				fmt.Fprintf(&sb, "  [%d/%d] %s - SKIP (no abapGit)\n", i+1, len(objects), obj.Name)
			} else {
				fmt.Fprintf(&sb, "  [%d/%d] %s - %s\n", i+1, len(objects), obj.Name, obj.Description)
			}
		}
		return mcp.NewToolResultText(sb.String()), nil
	}

	// Temporarily allow the install target package to bypass SAP_ALLOWED_PACKAGES restrictions.
	// Install operations are self-contained bootstrap operations that should not be blocked.
	cleanupPkgSafety := s.adtClient.AllowPackageTemporarily(packageName)
	defer cleanupPkgSafety()

	// Phase 2: Create package if needed
	if !packageExists {
		fmt.Fprintf(&sb, "Creating package %s...\n", packageName)
		createOpts := adt.CreateObjectOptions{
			ObjectType:  adt.ObjectTypePackage,
			Name:        packageName,
			Description: "VSP WebSocket Handler",
		}
		err := s.adtClient.CreateObject(ctx, createOpts)
		if err != nil {
			// On older SAP releases (e.g. 7.40), /sap/bc/adt/packages may not exist.
			// Don't abort — the package may have been pre-created via SE21/SE80,
			// and WriteSource will fail with a clear error if it truly doesn't exist.
			fmt.Fprintf(&sb, "  ⚠ Package creation failed: %v\n", err)
			fmt.Fprintf(&sb, "  → Continuing anyway (package may already exist via SE21/SE80)\n\n")
		} else {
			fmt.Fprintf(&sb, "  ✓ Package %s created\n\n", packageName)
		}
	} else {
		fmt.Fprintf(&sb, "Using existing package %s\n\n", packageName)
	}

	// Phase 3: Deploy objects
	sb.WriteString("Deploying ABAP objects...\n")

	deployed := []string{}
	skipped := []string{}
	failed := []string{}

	for i, obj := range objects {
		// Skip Git service if no abapGit
		if obj.Name == "ZCL_VSP_GIT_SERVICE" && skipGitService {
			fmt.Fprintf(&sb, "  [%d/%d] %s ⊘ Skipped (no abapGit)\n", i+1, len(objects), obj.Name)
			skipped = append(skipped, obj.Name)
			continue
		}

		fmt.Fprintf(&sb, "  [%d/%d] %s ", i+1, len(objects), obj.Name)

		// Use WriteSource to create/update
		opts := &adt.WriteSourceOptions{
			Package:     packageName,
			Description: obj.Description,
			Mode:        adt.WriteModeUpsert,
		}
		result, err := s.adtClient.WriteSource(ctx, obj.Type, obj.Name, obj.Source, opts)
		if err != nil {
			fmt.Fprintf(&sb, "✗ Failed: %v\n", err)
			failed = append(failed, obj.Name+": "+err.Error())
		} else if !result.Success {
			fmt.Fprintf(&sb, "✗ Failed: %s\n", result.Message)
			failed = append(failed, obj.Name+": "+result.Message)
		} else {
			sb.WriteString("✓ Deployed\n")
			deployed = append(deployed, obj.Name)
		}
	}

	sb.WriteString("\n")

	// Summary
	sb.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")
	if len(failed) > 0 {
		sb.WriteString("  DEPLOYMENT PARTIALLY FAILED\n")
		sb.WriteString("═══════════════════════════════════════════════════════════════════════════════\n\n")
		sb.WriteString("Failed objects:\n")
		for _, f := range failed {
			fmt.Fprintf(&sb, "  • %s\n", f)
		}
	} else {
		sb.WriteString("  DEPLOYMENT COMPLETE - Manual Steps Required\n")
		sb.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")
	}

	fmt.Fprintf(&sb, "\nDeployed: %d, Skipped: %d, Failed: %d\n\n", len(deployed), len(skipped), len(failed))

	// Post-deployment instructions
	sb.WriteString(embedded.PostDeploymentInstructions())

	// Features unlocked
	sb.WriteString("\nFeatures unlocked:\n")
	sb.WriteString("  ✓ WebSocket debugging (TPDAPI)\n")
	sb.WriteString("  ✓ RFC/BAPI execution\n")
	sb.WriteString("  ✓ AMDP debugging (experimental)\n")
	if hasAbapGit && !skipGitService {
		sb.WriteString("  ✓ abapGit export (158 object types)\n")
	} else {
		sb.WriteString("  ✗ abapGit export (install abapGit first)\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleListDependencies(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var sb strings.Builder
	sb.WriteString("Available Dependencies\n")
	sb.WriteString("======================\n\n")

	dependencies := deps.GetAvailableDependencies()
	for _, dep := range dependencies {
		status := "⚠ Not embedded (placeholder)"
		if dep.Available {
			status = "✓ Available"
		}
		fmt.Fprintf(&sb, "• %s\n", dep.Name)
		fmt.Fprintf(&sb, "  Description: %s\n", dep.Description)
		fmt.Fprintf(&sb, "  Package: %s\n", dep.Package)
		fmt.Fprintf(&sb, "  Status: %s\n", status)
		sb.WriteString("\n")
	}

	sb.WriteString("Usage:\n")
	sb.WriteString("  vsp install abapgit --edition standalone  # Single program ZABAPGIT\n")
	sb.WriteString("  vsp install abapgit --edition dev         # Full $ZGIT_DEV packages\n")
	sb.WriteString("\nNote: To add dependency ZIPs, export from SAP with GitExport and\n")
	sb.WriteString("embed in embedded/deps/\n")

	return mcp.NewToolResultText(sb.String()), nil
}

