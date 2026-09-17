// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_git.go contains handlers for Git/abapGit operations via ZADT_VSP.
package mcp

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// routeGitAction routes "system" with git-related types.
func (s *Server) routeGitAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "system" {
		return nil, false, nil
	}
	gitType := getStringParam(params, "type")
	switch gitType {
	case "git_types":
		return s.callHandler(ctx, s.handleGitTypes, params)
	case "git_export":
		return s.callHandler(ctx, s.handleGitExport, params)
	}
	return nil, false, nil
}

// --- Git/abapGit Handlers ---

// sanitizePackageForFilename turns an SAP package name into a safe filename
// component: only letters, digits, underscore and hyphen survive (a leading
// "$" from a local package is dropped, same as before), everything else —
// including "/" or "..", which would otherwise let a caller-supplied package
// name write the export ZIP outside outputDir — is dropped rather than
// interpolated into the path.
func sanitizePackageForFilename(pkg string) string {
	pkg = strings.TrimPrefix(pkg, "$")
	var b strings.Builder
	for _, r := range pkg {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "package"
	}
	return b.String()
}

func (s *Server) handleGitTypes(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if errResult := s.ensureWSConnected(ctx, "GitTypes"); errResult != nil {
		return errResult, nil
	}

	types, err := s.amdpWSClient.GitTypes(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GitTypes failed: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Supported abapGit Object Types: %d\n\n", len(types))
	for i, t := range types {
		sb.WriteString(t)
		if i < len(types)-1 {
			if (i+1)%10 == 0 {
				sb.WriteString("\n")
			} else {
				sb.WriteString(", ")
			}
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (s *Server) handleGitExport(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := adt.GitExportParams{}

	// Parse packages
	if pkgStr, ok := request.GetArguments()["packages"].(string); ok && pkgStr != "" {
		params.Packages = strings.Split(pkgStr, ",")
		for i, p := range params.Packages {
			params.Packages[i] = strings.TrimSpace(p)
		}
	}

	// Parse objects
	if objsStr, ok := request.GetArguments()["objects"].(string); ok && objsStr != "" {
		var objs []adt.GitObjectRef
		if err := json.Unmarshal([]byte(objsStr), &objs); err != nil {
			return newToolResultError(fmt.Sprintf("Invalid objects JSON: %v", err)), nil
		}
		params.Objects = objs
	}

	// Include subpackages
	if inclSub, ok := request.GetArguments()["include_subpackages"].(bool); ok {
		params.IncludeSubpackages = inclSub
	} else {
		params.IncludeSubpackages = true // default
	}

	if len(params.Packages) == 0 && len(params.Objects) == 0 {
		return newToolResultError("Either packages or objects parameter is required"), nil
	}

	// Determine output directory (default: current directory)
	outputDir := "."
	if dir, ok := request.GetArguments()["output_dir"].(string); ok && dir != "" {
		outputDir = dir
	}

	// A single whole package with no loose objects and the default
	// subpackage behavior exports over classic RFC (Z_ABAPGIT_SERIALIZE_PACKAGE,
	// pkg/saprfc) — no ZADT_VSP deployment needed. Anything else (several
	// packages, individual objects, or an explicit include_subpackages=false
	// this fork hasn't confirmed the RFC serializer honors) keeps the
	// WebSocket path unchanged.
	if len(params.Objects) == 0 && len(params.Packages) == 1 && params.IncludeSubpackages {
		return s.handleGitExportRFC(ctx, params.Packages[0], outputDir)
	}

	if errResult := s.ensureWSConnected(ctx, "GitExport"); errResult != nil {
		return errResult, nil
	}

	result, err := s.amdpWSClient.GitExport(ctx, params)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GitExport failed: %v", err)), nil
	}

	// Generate filename with timestamp
	var zipName string
	if len(params.Packages) > 0 {
		// Use first package name (sanitize $ for filename)
		pkgName := sanitizePackageForFilename(params.Packages[0])
		zipName = fmt.Sprintf("%s_%s.zip", pkgName, time.Now().Format("20060102_150405"))
	} else {
		zipName = fmt.Sprintf("abapgit_export_%s.zip", time.Now().Format("20060102_150405"))
	}
	zipPath := filepath.Join(outputDir, zipName)

	// Decode base64 and save ZIP
	zipData, err := base64.StdEncoding.DecodeString(result.ZipBase64)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to decode ZIP: %v", err)), nil
	}

	if err := os.WriteFile(zipPath, zipData, 0644); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to write ZIP file: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Git Export Successful\n\n")
	fmt.Fprintf(&sb, "Objects: %d\n", result.ObjectCount)
	fmt.Fprintf(&sb, "Files: %d\n", result.FileCount)
	fmt.Fprintf(&sb, "ZIP: %s (%d bytes)\n\n", zipPath, len(zipData))

	sb.WriteString("Files in archive:\n")
	for _, f := range result.Files {
		fmt.Fprintf(&sb, "  %s (%d bytes)\n", f.Path, f.Size)
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// handleGitExportRFC serializes one whole ABAP package to an abapGit ZIP over
// classic RFC (Z_ABAPGIT_SERIALIZE_PACKAGE, pkg/saprfc.ExportPackage) — no
// ZADT_VSP WebSocket bridge, no vsp helper deployed on the system at all.
// Unlike GitExportResult from the WebSocket path, the serializer itself
// reports no object/file count, so this lists the ZIP's own directory
// instead of trusting a count from the far side.
func (s *Server) handleGitExportRFC(ctx context.Context, pkg, outputDir string) (*mcp.CallToolResult, error) {
	c, err := s.ensureRFCClient(ctx)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to connect via RFC: %v", err)), nil
	}

	zipData, err := saprfc.ExportPackage(ctx, c, pkg, saprfc.ExportOptions{})
	if err != nil {
		return newToolResultError(fmt.Sprintf("GitExport (RFC) failed: %v", err)), nil
	}

	pkgName := sanitizePackageForFilename(pkg)
	zipName := fmt.Sprintf("%s_%s.zip", pkgName, time.Now().Format("20060102_150405"))
	zipPath := filepath.Join(outputDir, zipName)
	if err := os.WriteFile(zipPath, zipData, 0644); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to write ZIP file: %v", err)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Git Export Successful (classic RFC, Z_ABAPGIT_SERIALIZE_PACKAGE)\n\n")
	fmt.Fprintf(&sb, "Package: %s\n", pkg)
	fmt.Fprintf(&sb, "ZIP: %s (%d bytes)\n\n", zipPath, len(zipData))

	zr, zerr := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if zerr != nil {
		sb.WriteString("(ZIP written, but its directory could not be listed)\n")
		return mcp.NewToolResultText(sb.String()), nil
	}
	fmt.Fprintf(&sb, "Files: %d\n\n", len(zr.File))
	sb.WriteString("Files in archive:\n")
	for _, f := range zr.File {
		fmt.Fprintf(&sb, "  %s (%d bytes)\n", f.Name, f.UncompressedSize64)
	}

	return mcp.NewToolResultText(sb.String()), nil
}
