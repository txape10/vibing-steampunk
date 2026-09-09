package adt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Ensure imports are used (will be removed when all code is migrated)
var _ = filepath.Join
var _ = time.Now

// --- Unified Tools (Focused Mode) ---

// GetSourceOptions configures GetSource behavior
type GetSourceOptions struct {
	Parent  string // Function group name (required for FUNC type)
	Include string // Class include type: definitions, implementations, macros, testclasses (optional for CLAS type)
	Method  string // Method name for method-level source extraction (optional for CLAS type)
	// NoCache skips the source cache lookup and forces a fresh read from SAP.
	// Set by callers asking for include_hash: a hash computed from a stale
	// cached read would not actually be the version currently on SAP, which
	// defeats the point of a caller establishing a version baseline before a
	// guarded write (PR #191 port). The cache is still refreshed afterwards,
	// so this does not disable caching for later plain reads.
	NoCache bool
}

// GetSource is a unified tool for reading ABAP source code across different object types.
// Replaces GetProgram, GetClass, GetInterface, GetFunction, GetInclude, GetFunctionGroup, GetClassInclude.
//
// Supported types:
//   - PROG: Programs (name = program name)
//   - CLAS: Classes (name = class name, include = definitions|implementations|macros|testclasses, method = method name)
//   - INTF: Interfaces (name = interface name)
//   - FUNC: Function modules (name = function module name, parent = function group name)
//   - FUGR: Function groups (name = function group name)
//   - INCL: Includes (name = include name)
//   - DDLS: CDS DDL sources (name = DDL source name)
//   - VIEW: DDIC database views (name = view name) - classic SE11 views
//   - BDEF: Behavior Definitions (name = BDEF name) - RAP behavior implementation
//   - SRVD: Service Definitions (name = SRVD name) - RAP service exposure
//   - SRVB: Service Bindings (name = SRVB name) - RAP protocol binding (returns JSON metadata)
//   - MSAG: Message classes (name = message class name) - returns JSON with all messages
func (c *Client) GetSource(ctx context.Context, objectType, name string, opts *GetSourceOptions) (string, error) {
	// Safety check for read operations
	if err := c.checkSafety(OpRead, "GetSource"); err != nil {
		return "", err
	}

	if opts == nil {
		opts = &GetSourceOptions{}
	}

	objectType = strings.ToUpper(objectType)
	name = strings.ToUpper(name)

	// Cache lookup
	method := ""
	include := ""
	parent := ""
	if opts != nil {
		method = opts.Method
		include = opts.Include
		parent = opts.Parent
	}
	if !opts.NoCache {
		if cached, ok := c.sourceCache.get(objectType, name, method, include, parent); ok {
			return cached, nil
		}
	}

	source, err := c.getSourceUncached(ctx, objectType, name, opts)
	if err != nil {
		return "", err
	}
	c.sourceCache.set(objectType, name, method, include, parent, source)
	return source, nil
}

func (c *Client) getSourceUncached(ctx context.Context, objectType, name string, opts *GetSourceOptions) (string, error) {
	switch objectType {
	case "PROG":
		return c.GetProgram(ctx, name)

	case "CLAS":
		// Method-level source extraction
		if opts.Method != "" {
			return c.GetClassMethodSource(ctx, name, opts.Method)
		}
		// Include-level source extraction
		if opts.Include != "" {
			return c.GetClassInclude(ctx, name, ClassIncludeType(opts.Include))
		}
		return c.GetClassSource(ctx, name)

	case "INTF":
		return c.GetInterface(ctx, name)

	case "FUNC":
		if opts.Parent == "" {
			return "", fmt.Errorf("parent (function group name) is required for FUNC type")
		}
		return c.GetFunction(ctx, name, opts.Parent)

	case "FUGR":
		// GetFunctionGroup returns JSON metadata (function module list), not source
		fg, err := c.GetFunctionGroup(ctx, name)
		if err != nil {
			return "", err
		}
		// Serialize to JSON for display
		data, err := json.Marshal(fg)
		if err != nil {
			return "", fmt.Errorf("failed to serialize function group: %w", err)
		}
		return string(data), nil

	case "INCL":
		return c.GetInclude(ctx, name)

	case "DDLS":
		return c.GetDDLS(ctx, name)

	case "VIEW":
		return c.GetView(ctx, name)

	case "BDEF":
		return c.GetBDEF(ctx, name)

	case "SRVD":
		return c.GetSRVD(ctx, name)

	case "SRVB":
		// GetSRVB returns metadata structure, serialize to JSON
		sb, err := c.GetSRVB(ctx, name)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(sb)
		if err != nil {
			return "", fmt.Errorf("failed to serialize service binding: %w", err)
		}
		return string(data), nil

	case "MSAG":
		// GetMessageClass returns JSON metadata (message list), not source
		mc, err := c.GetMessageClass(ctx, name)
		if err != nil {
			return "", err
		}
		// Serialize to JSON for display
		data, err := json.Marshal(mc)
		if err != nil {
			return "", fmt.Errorf("failed to serialize message class: %w", err)
		}
		return string(data), nil

	case "DOMA":
		return c.GetXMLMetadataObject(ctx, fmt.Sprintf("/sap/bc/adt/ddic/domains/%s", strings.ToLower(name)))

	case "DTEL":
		return c.GetXMLMetadataObject(ctx, fmt.Sprintf("/sap/bc/adt/ddic/dataelements/%s", strings.ToLower(name)))

	case "TTYP":
		return c.GetXMLMetadataObject(ctx, fmt.Sprintf("/sap/bc/adt/ddic/tabletypes/%s", strings.ToLower(name)))

	case "ENQU":
		return c.GetXMLMetadataObject(ctx, fmt.Sprintf("/sap/bc/adt/ddic/lockobjects/sources/%s", strings.ToLower(name)))

	default:
		return "", fmt.Errorf("unsupported object type: %s (supported: PROG, CLAS, INTF, FUNC, FUGR, INCL, DDLS, VIEW, BDEF, SRVD, SRVB, MSAG, DOMA, DTEL, TTYP, ENQU)", objectType)
	}
}

// WriteSourceMode specifies how WriteSource behaves
type WriteSourceMode string

const (
	WriteModeUpdate WriteSourceMode = "update" // Update existing object only
	WriteModeCreate WriteSourceMode = "create" // Create new object only
	WriteModeUpsert WriteSourceMode = "upsert" // Create if not exists, update if exists (default)
)

// WriteSourceOptions configures WriteSource behavior
type WriteSourceOptions struct {
	Mode        WriteSourceMode // update, create, upsert (default: upsert)
	Description string          // Object description (for create)
	Package     string          // Package name (for create)
	TestSource  string          // Test source for CLAS (auto-creates test include)
	Transport   string          // Transport request number
	Method      string          // For CLAS only: update only this method (source must be METHOD...ENDMETHOD block)
	Parent      string          // For FUNC: function group name (required)
	// ExpectedSourceHash is the SourceHash returned by GetSource. When supplied
	// for an update, VSP re-reads the source after taking the write lock and
	// refuses to overwrite a version changed since that read.
	ExpectedSourceHash string
}

// WriteSourceResult represents the result of WriteSource operation
type WriteSourceResult struct {
	Success            bool                `json:"success"`
	ObjectType         string              `json:"objectType"`
	ObjectName         string              `json:"objectName"`
	ObjectURL          string              `json:"objectUrl"`
	Mode               string              `json:"mode"`             // "created" or "updated"
	Method             string              `json:"method,omitempty"` // Method name if method-level update
	SyntaxErrors       []SyntaxCheckResult `json:"syntaxErrors,omitempty"`
	Activation         *ActivationResult   `json:"activation,omitempty"`
	TestResults        *UnitTestResult     `json:"testResults,omitempty"` // For CLAS with TestSource
	ExpectedSourceHash string              `json:"expectedSourceHash,omitempty"`
	TargetSourceHash   string              `json:"targetSourceHash,omitempty"`
	VerifiedSourceHash string              `json:"verifiedSourceHash,omitempty"`
	Message            string              `json:"message,omitempty"`
}

// writeSourceObjectURL resolves the ADT object URL for a WriteSource target
// from its short type code, reusing the same URL shapes GetObjectURL builds
// for create. Used by the top-level mutation gate to check AllowedPackages
// on updates without requiring the caller to also pass an explicit package.
// Returns "" for unsupported types (SRVB has no dedicated WriteSource path).
func writeSourceObjectURL(objectType, name, parent string) string {
	switch objectType {
	case "PROG":
		return GetObjectURL(ObjectTypeProgram, name, "")
	case "CLAS":
		return GetObjectURL(ObjectTypeClass, name, "")
	case "INTF":
		return GetObjectURL(ObjectTypeInterface, name, "")
	case "INCL":
		return GetObjectURL(ObjectTypeInclude, name, "")
	case "DDLS":
		return GetObjectURL(ObjectTypeDDLS, name, "")
	case "BDEF":
		return GetObjectURL(ObjectTypeBDEF, name, "")
	case "SRVD":
		return GetObjectURL(ObjectTypeSRVD, name, "")
	case "SRVB":
		return GetObjectURL(ObjectTypeSRVB, name, "")
	case "FUNC":
		return GetObjectURL(ObjectTypeFunctionMod, name, parent)
	default:
		return ""
	}
}

// WriteSource is a unified tool for writing ABAP source code across different object types.
// Replaces WriteProgram, WriteClass, CreateAndActivateProgram, CreateClassWithTests.
//
// Supported types:
//   - PROG: Programs
//   - CLAS: Classes (optionally with test source)
//   - INTF: Interfaces
//   - FUNC: Function modules (opts.Parent = function group name, required)
//   - INCL: Includes
//   - DDLS / BDEF / SRVD / SRVB: RAP and CDS objects
//
// Mode:
//   - upsert (default): Auto-detect if object exists, create or update accordingly
//   - create: Create new object only (fails if exists)
//   - update: Update existing object only (fails if not exists)
func (c *Client) WriteSource(ctx context.Context, objectType, name, source string, opts *WriteSourceOptions) (*WriteSourceResult, error) {
	// Debug logging
	if os.Getenv("VSP_DEBUG") == "true" {
		fmt.Fprintf(os.Stderr, "[DEBUG] WriteSource: BaseURL=%s, objectType=%s, name=%s\n", c.config.BaseURL, objectType, name)
	}

	if opts == nil {
		opts = &WriteSourceOptions{Mode: WriteModeUpsert}
	}
	if opts.Mode == "" {
		opts.Mode = WriteModeUpsert
	}

	objectType = strings.ToUpper(objectType)
	name = strings.ToUpper(name)

	// Top-level mutation gate. For create (opts.Package set), the explicit
	// package is checked directly. For update, opts.Package is empty, so we
	// resolve the object's URL here and let the gate look up its package via
	// SearchObject — otherwise AllowedPackages fails closed on every update
	// even though the package is perfectly resolvable (issue #143).
	gateObjectURL := ""
	if opts.Package == "" {
		gateObjectURL = writeSourceObjectURL(objectType, name, opts.Parent)
	}
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpWorkflow,
		OpName:    "WriteSource",
		ObjectURL: gateObjectURL,
		Package:   opts.Package, // empty for update path, present for create
		Transport: opts.Transport,
	}); err != nil {
		return nil, err
	}

	result := &WriteSourceResult{
		ObjectType: objectType,
		ObjectName: name,
	}

	// Validate object type
	switch objectType {
	case "PROG", "CLAS", "INTF", "INCL", "DDLS", "BDEF", "SRVD", "SRVB", "FUNC":
		// Supported types
	default:
		result.Message = fmt.Sprintf("Unsupported object type: %s (supported: PROG, CLAS, INTF, INCL, DDLS, BDEF, SRVD, SRVB, FUNC)", objectType)
		return result, nil
	}

	// FUNC requires parent (function group)
	if objectType == "FUNC" && opts.Parent == "" {
		result.Message = "Parent (function group name) is required for FUNC type"
		return result, nil
	}

	// This fork has no dedicated writeSourceFunctionModule dispatch (FUNC is
	// handled inline in writeSourceCreate/writeSourceUpdate below), so the
	// guard upstream places at the entry to that function is placed here
	// instead, at the same point FUNC's other preconditions are checked.
	if objectType == "FUNC" && opts.ExpectedSourceHash != "" {
		result.Message = "expected_source_hash is not supported for function-module WriteSource; read and update the complete module through its dedicated workflow"
		return result, nil
	}

	// Determine if the object exists. Needed regardless of mode: Upsert uses it
	// to pick create-vs-update, and explicit Update/Create use it to validate
	// the caller's requested mode against reality (issue: explicit Update always
	// failed with "does not exist" because this check used to be Upsert-only).
	objectExists := false
	switch objectType {
	case "PROG":
		_, err := c.GetProgram(ctx, name)
		objectExists = (err == nil)
	case "CLAS":
		_, err := c.GetClass(ctx, name)
		objectExists = (err == nil)
	case "INTF":
		_, err := c.GetInterface(ctx, name)
		objectExists = (err == nil)
	case "INCL":
		_, err := c.GetInclude(ctx, name)
		objectExists = (err == nil)
	case "DDLS":
		_, err := c.GetDDLS(ctx, name)
		objectExists = (err == nil)
	case "BDEF":
		_, err := c.GetBDEF(ctx, name)
		objectExists = (err == nil)
	case "SRVD":
		_, err := c.GetSRVD(ctx, name)
		objectExists = (err == nil)
	case "SRVB":
		_, err := c.GetSRVB(ctx, name)
		objectExists = (err == nil)
	case "FUNC":
		_, err := c.GetFunction(ctx, name, opts.Parent)
		objectExists = (err == nil)
	}

	// Determine actual operation mode
	var actualMode WriteSourceMode
	if opts.Mode == WriteModeUpsert {
		if objectExists {
			actualMode = WriteModeUpdate
		} else {
			actualMode = WriteModeCreate
		}
	} else {
		actualMode = opts.Mode
	}

	// Validate mode vs existence
	if actualMode == WriteModeCreate && objectExists {
		result.Message = fmt.Sprintf("Object %s already exists (use mode=update or mode=upsert)", name)
		return result, nil
	}
	// Decision A (PR #191 port): upstream bundles an unrelated change into this
	// same check (`opts.Mode == WriteModeUpsert && !objectExists`), letting an
	// explicit update through even when the object does not exist yet. This
	// fork already fixed the underlying objectExists-computation bug that
	// motivated that upstream change (see CLAUDE.md 2f) via a different,
	// narrower fix, so the check below stays exactly as it already was —
	// only the hash-guard validation is new here.
	if opts.ExpectedSourceHash != "" {
		if actualMode != WriteModeUpdate {
			result.Message = "expected_source_hash is only valid when updating an existing object"
			return result, nil
		}
		if opts.Method != "" {
			result.Message = "expected_source_hash is not supported for method-level WriteSource; use a full-class read and update"
			return result, nil
		}
		ctx = withExpectedSourceHash(ctx, opts.ExpectedSourceHash)
	}
	if actualMode == WriteModeUpdate && !objectExists {
		result.Message = fmt.Sprintf("Object %s does not exist (use mode=create or mode=upsert)", name)
		return result, nil
	}

	// Execute create or update workflow
	var writeResult *WriteSourceResult
	var writeErr error
	if actualMode == WriteModeCreate {
		writeResult, writeErr = c.writeSourceCreate(ctx, objectType, name, source, opts)
	} else {
		writeResult, writeErr = c.writeSourceUpdate(ctx, objectType, name, source, opts)
		// Decision F (PR #191 port): verify inline here, inside the update
		// branch, rather than upstream's wrapper around the update call — this
		// falls through to the cache-invalidation tail below, which upstream
		// does not have.
		if writeErr == nil && opts.ExpectedSourceHash != "" && writeResult != nil && writeResult.Success {
			writeResult, writeErr = c.verifyWriteSourceResult(ctx, writeResult, source, opts)
		}
	}
	if writeResult != nil && writeResult.Success {
		c.sourceCache.InvalidateByName(name)
	}
	return writeResult, writeErr
}

// verifyWriteSourceResult performs the post-activation half of an explicit
// versioned update. A successful PUT is not enough: SAP can materialise source
// differently or activation can leave a different active version behind.
func (c *Client) verifyWriteSourceResult(
	ctx context.Context,
	result *WriteSourceResult,
	targetSource string,
	opts *WriteSourceOptions,
) (*WriteSourceResult, error) {
	result.ExpectedSourceHash = opts.ExpectedSourceHash
	result.TargetSourceHash = SourceHash(targetSource)
	if result.ObjectURL == "" {
		result.Success = false
		result.Message = "Source was written and activated, but post-write verification has no object URL. Do not retry blindly."
		return result, nil
	}
	resp, err := c.transport.Request(ctx, result.ObjectURL+"/source/main", &RequestOptions{
		Method: "GET", Accept: "text/plain",
	})
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Source was written and activated, but post-write verification could not read it: %v. Do not retry blindly.", err)
		return result, nil
	}
	result.VerifiedSourceHash = SourceHash(string(resp.Body))
	if result.VerifiedSourceHash != result.TargetSourceHash {
		result.Success = false
		result.Message = fmt.Sprintf("Source was written and activated, but post-write verification differs (target %s, actual %s). Do not retry blindly.", result.TargetSourceHash, result.VerifiedSourceHash)
		return result, nil
	}
	return result, nil
}

// writeSourceCreate handles creation workflow
func (c *Client) writeSourceCreate(ctx context.Context, objectType, name, source string, opts *WriteSourceOptions) (*WriteSourceResult, error) {
	result := &WriteSourceResult{
		ObjectType: objectType,
		ObjectName: name,
		Mode:       "created",
	}

	// Validate required fields for create
	if opts.Package == "" {
		result.Message = "Package is required for creating new objects"
		return result, nil
	}
	if opts.Description == "" {
		result.Message = "Description is required for creating new objects"
		return result, nil
	}

	// Use existing Create*AndActivate* workflows
	switch objectType {
	case "PROG":
		progResult, err := c.CreateAndActivateProgram(ctx, name, opts.Description, opts.Package, source, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to create program: %v", err)
			return result, nil
		}
		result.Success = progResult.Success
		result.ObjectURL = progResult.ObjectURL
		result.SyntaxErrors = progResult.SyntaxErrors
		result.Activation = progResult.Activation
		result.Message = progResult.Message
		return result, nil

	case "CLAS":
		if opts.TestSource != "" {
			classResult, err := c.CreateClassWithTests(ctx, name, opts.Description, opts.Package, source, opts.TestSource, opts.Transport)
			if err != nil {
				result.Message = fmt.Sprintf("Failed to create class with tests: %v", err)
				return result, nil
			}
			result.Success = classResult.Success
			result.ObjectURL = classResult.ObjectURL
			result.Activation = classResult.Activation
			result.TestResults = classResult.UnitTestResult
			result.Message = classResult.Message
			return result, nil
		} else {
			// Create class without tests - use CreateObject + WriteClass workflow
			objectURL := fmt.Sprintf("/sap/bc/adt/oo/classes/%s", url.PathEscape(name))
			result.ObjectURL = objectURL

			// Create object
			err := c.CreateObject(ctx, CreateObjectOptions{
				ObjectType:  ObjectTypeClass,
				Name:        name,
				Description: opts.Description,
				PackageName: opts.Package,
				Transport:   opts.Transport,
			})
			if err != nil {
				result.Message = fmt.Sprintf("Failed to create class: %v", err)
				return result, nil
			}

			// Write source
			writeResult, err := c.WriteClass(ctx, name, source, opts.Transport)
			if err != nil {
				result.Message = fmt.Sprintf("Class created but failed to write source: %v", err)
				return result, nil
			}

			result.Success = writeResult.Success
			result.SyntaxErrors = writeResult.SyntaxErrors
			result.Activation = writeResult.Activation
			result.Message = writeResult.Message
			return result, nil
		}

	case "INTF":
		objectURL := fmt.Sprintf("/sap/bc/adt/oo/interfaces/%s", url.PathEscape(name))
		result.ObjectURL = objectURL

		// Run the unified gate up front (with the explicit Package the caller
		// supplied). CreateObject below checks it again before asking SAP to
		// create the interface there.
		if err := c.checkMutation(ctx, MutationContext{
			Op:        OpCreate,
			OpName:    "WriteSource(INTF,create)",
			Package:   opts.Package,
			Transport: opts.Transport,
		}); err != nil {
			result.Message = fmt.Sprintf("Failed mutation gate: %v", err)
			return result, nil
		}

		// Create object
		err := c.CreateObject(ctx, CreateObjectOptions{
			ObjectType:  ObjectTypeInterface,
			Name:        name,
			Description: opts.Description,
			PackageName: opts.Package,
			Transport:   opts.Transport,
		})
		if err != nil {
			result.Message = fmt.Sprintf("Failed to create interface: %v", err)
			return result, nil
		}

		// The gate above accepted opts.Package, and CreateObject checked it a
		// second time before creating the interface there — so UpdateSource
		// below need not resolve the package again from inside the lock
		// (issue #91).
		ctx = withMutationPackageChecked(ctx, objectURL)

		// Write source (using WriteProgram logic for interface)
		sourceURL := objectURL + "/source/main"

		// Syntax check
		syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
		if err != nil {
			result.Message = fmt.Sprintf("Syntax check failed: %v", err)
			return result, nil
		}

		// Check for syntax errors
		for _, se := range syntaxErrors {
			if se.Severity == "E" || se.Severity == "A" || se.Severity == "X" {
				result.SyntaxErrors = syntaxErrors
				result.Message = "Source has syntax errors - not saved"
				return result, nil
			}
		}
		result.SyntaxErrors = syntaxErrors

		// Lock
		lock, err := c.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			result.Message = fmt.Sprintf("Failed to lock object: %v", err)
			return result, nil
		}

		// Tracked explicitly rather than keyed off result.Success — activation
		// can still fail after a successful unlock below, and result.Success
		// only flips to true at the very end.
		unlocked := false
		defer func() {
			if !unlocked {
				if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
					result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
				}
			}
		}()

		// Update source
		err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update source: %v", err)
			return result, nil
		}

		// Unlock
		err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
		unlocked = true
		if err != nil {
			result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
			return result, nil
		}

		// Activate
		activation, err := c.Activate(ctx, objectURL, name)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to activate: %v", err)
			result.Activation = activation
			return result, nil
		}

		result.Activation = activation
		if activation.Success {
			result.Success = true
			result.Message = "Interface created and activated successfully"
		} else {
			result.Message = "Activation failed - check activation messages"
		}

		return result, nil

	case "INCL":
		if err := c.CreateObject(ctx, CreateObjectOptions{
			ObjectType:  ObjectTypeInclude,
			Name:        name,
			Description: opts.Description,
			PackageName: opts.Package,
			Transport:   opts.Transport,
		}); err != nil {
			result.Message = fmt.Sprintf("Failed to create include: %v", err)
			return result, nil
		}
		inclResult, err := c.WriteInclude(ctx, name, source, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to write include source: %v", err)
			return result, nil
		}
		result.Success = inclResult.Success
		result.ObjectURL = inclResult.ObjectURL
		result.SyntaxErrors = inclResult.SyntaxErrors
		result.Activation = inclResult.Activation
		result.Message = inclResult.Message
		return result, nil

	case "DDLS", "BDEF", "SRVD":
		// Get object type and URL
		var objType CreatableObjectType
		var objectURL string
		switch objectType {
		case "DDLS":
			objType = ObjectTypeDDLS
			objectURL = GetObjectURL(ObjectTypeDDLS, name, "")
		case "BDEF":
			objType = ObjectTypeBDEF
			objectURL = GetObjectURL(ObjectTypeBDEF, name, "")
		case "SRVD":
			objType = ObjectTypeSRVD
			objectURL = GetObjectURL(ObjectTypeSRVD, name, "")
		}
		result.ObjectURL = objectURL
		sourceURL := objectURL + "/source/main"

		// Run the unified gate up front. CreateObject below checks
		// opts.Package again before asking SAP to create the object there.
		if err := c.checkMutation(ctx, MutationContext{
			Op:        OpCreate,
			OpName:    fmt.Sprintf("WriteSource(%s,create)", objectType),
			Package:   opts.Package,
			Transport: opts.Transport,
		}); err != nil {
			result.Message = fmt.Sprintf("Failed mutation gate: %v", err)
			return result, nil
		}

		// Create object first
		// For BDEF, include source in creation (ADT API requirement)
		createOpts := CreateObjectOptions{
			ObjectType:  objType,
			Name:        name,
			Description: opts.Description,
			PackageName: opts.Package,
			Transport:   opts.Transport,
		}
		if objectType == "BDEF" {
			createOpts.Source = source // BDEF requires source embedded in creation request
		}
		err := c.CreateObject(ctx, createOpts)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to create %s: %v", objectType, err)
			return result, nil
		}

		// The gate above accepted opts.Package, and CreateObject checked it a
		// second time before creating the object there — so UpdateSource
		// below need not resolve the package again from inside the lock
		// (issue #91).
		ctx = withMutationPackageChecked(ctx, objectURL)

		// For BDEF, creation creates empty shell, then update source
		if objectType == "BDEF" {
			sourceURL := objectURL + "/source/main"

			// Lock
			lock, err := c.LockObject(ctx, objectURL, "MODIFY")
			if err != nil {
				result.Message = fmt.Sprintf("Failed to lock BDEF: %v", err)
				return result, nil
			}

			// Update source
			err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, opts.Transport)
			if err != nil {
				// Unlock on failure, detached from ctx's cancellation (issue #91).
				if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
					result.Message = fmt.Sprintf("Failed to update BDEF source: %v — %s", err, strandedLockAdvice(objectURL, unlockErr))
				} else {
					result.Message = fmt.Sprintf("Failed to update BDEF source: %v", err)
				}
				return result, nil
			}

			// Unlock
			err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
			if err != nil {
				result.Message = fmt.Sprintf("Failed to unlock BDEF: %v", err)
				return result, nil
			}

			// Activate
			activation, err := c.Activate(ctx, objectURL, name)
			if err != nil {
				result.Message = fmt.Sprintf("Failed to activate: %v", err)
				result.Activation = activation
				return result, nil
			}
			result.Activation = activation
			if activation.Success {
				result.Success = true
				result.Message = fmt.Sprintf("%s created and activated successfully", objectType)
			} else {
				result.Message = "Activation failed - check activation messages"
			}
			return result, nil
		}

		// Syntax check (for DDLS, SRVD)
		syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
		if err != nil {
			result.Message = fmt.Sprintf("Syntax check failed: %v", err)
			return result, nil
		}

		// Check for syntax errors
		for _, se := range syntaxErrors {
			if se.Severity == "E" || se.Severity == "A" || se.Severity == "X" {
				result.SyntaxErrors = syntaxErrors
				result.Message = "Source has syntax errors - not saved"
				return result, nil
			}
		}
		result.SyntaxErrors = syntaxErrors

		// Lock
		lock, err := c.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			result.Message = fmt.Sprintf("Failed to lock object: %v", err)
			return result, nil
		}

		// Tracked explicitly rather than keyed off result.Success — activation
		// can still fail after a successful unlock below, and result.Success
		// only flips to true at the very end.
		unlocked := false
		defer func() {
			if !unlocked {
				if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
					result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
				}
			}
		}()

		// Update source
		err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update source: %v", err)
			return result, nil
		}

		// Unlock
		err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
		unlocked = true
		if err != nil {
			result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
			return result, nil
		}

		// Activate
		activation, err := c.Activate(ctx, objectURL, name)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to activate: %v", err)
			result.Activation = activation
			return result, nil
		}

		result.Activation = activation
		if activation.Success {
			result.Success = true
			result.Message = fmt.Sprintf("%s created and activated successfully", objectType)
		} else {
			result.Message = "Activation failed - check activation messages"
		}

		return result, nil

	case "SRVB":
		// SRVB (Service Binding) - source is JSON configuration
		// Parse JSON to get binding parameters
		var srvbConfig struct {
			ServiceDefName  string `json:"serviceDefName"`
			BindingType     string `json:"bindingType"`     // ODATA
			BindingVersion  string `json:"bindingVersion"`  // V2 or V4
			BindingCategory string `json:"bindingCategory"` // 0=WebAPI, 1=UI
		}
		if err := json.Unmarshal([]byte(source), &srvbConfig); err != nil {
			result.Message = fmt.Sprintf("Invalid SRVB JSON config: %v (expected: {\"serviceDefName\":\"...\",\"bindingType\":\"ODATA\",\"bindingVersion\":\"V4\"})", err)
			return result, nil
		}

		// Validate required fields
		if srvbConfig.ServiceDefName == "" {
			result.Message = "serviceDefName is required in SRVB config"
			return result, nil
		}

		// Set defaults
		if srvbConfig.BindingType == "" {
			srvbConfig.BindingType = "ODATA"
		}
		if srvbConfig.BindingVersion == "" {
			srvbConfig.BindingVersion = "V4"
		}
		if srvbConfig.BindingCategory == "" {
			srvbConfig.BindingCategory = "0" // Web API
		}

		objectURL := fmt.Sprintf("/sap/bc/adt/businessservices/bindings/%s", url.PathEscape(strings.ToLower(name)))
		result.ObjectURL = objectURL

		// Create SRVB
		err := c.CreateObject(ctx, CreateObjectOptions{
			ObjectType:        ObjectTypeSRVB,
			Name:              name,
			Description:       opts.Description,
			PackageName:       opts.Package,
			ServiceDefinition: srvbConfig.ServiceDefName,
			BindingType:       srvbConfig.BindingType,
			BindingVersion:    srvbConfig.BindingVersion,
			BindingCategory:   srvbConfig.BindingCategory,
			Transport:         opts.Transport,
		})
		if err != nil {
			result.Message = fmt.Sprintf("Failed to create SRVB: %v", err)
			return result, nil
		}

		// Activate
		activation, err := c.Activate(ctx, objectURL, name)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to activate: %v", err)
			result.Activation = activation
			return result, nil
		}
		result.Activation = activation
		if activation.Success {
			result.Success = true
			result.Message = "SRVB created and activated successfully"
		} else {
			result.Message = "Activation failed - check activation messages"
		}
		return result, nil

	case "FUNC":
		objectURL := GetObjectURL(ObjectTypeFunctionMod, name, opts.Parent)
		result.ObjectURL = objectURL

		if err := c.checkMutation(ctx, MutationContext{
			Op:        OpCreate,
			OpName:    "WriteSource(FUNC,create)",
			Package:   opts.Package,
			Transport: opts.Transport,
		}); err != nil {
			result.Message = fmt.Sprintf("Failed mutation gate: %v", err)
			return result, nil
		}

		// Create FM shell inside the function group
		err := c.CreateObject(ctx, CreateObjectOptions{
			ObjectType:  ObjectTypeFunctionMod,
			Name:        name,
			Description: opts.Description,
			PackageName: opts.Package,
			Transport:   opts.Transport,
			ParentName:  opts.Parent,
		})
		if err != nil {
			result.Message = fmt.Sprintf("Failed to create function module: %v", err)
			return result, nil
		}

		// The gate above accepted opts.Package, and CreateObject checked it a
		// second time before creating the FM there — so UpdateSource below
		// need not resolve the package again from inside the lock (issue #91).
		ctx = withMutationPackageChecked(ctx, objectURL)

		// Write source into the newly created FM
		sourceURL := objectURL + "/source/main"
		lock, err := c.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			result.Message = fmt.Sprintf("FM created but failed to lock for source write: %v", err)
			return result, nil
		}

		err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, opts.Transport)
		if err != nil {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				result.Message = fmt.Sprintf("FM created but failed to write source: %v — %s", err, strandedLockAdvice(objectURL, unlockErr))
			} else {
				result.Message = fmt.Sprintf("FM created but failed to write source: %v", err)
			}
			return result, nil
		}

		err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to unlock function module: %v", err)
			return result, nil
		}

		activation, err := c.Activate(ctx, objectURL, name)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to activate function module: %v", err)
			result.Activation = activation
			return result, nil
		}
		result.Activation = activation
		if activation.Success {
			result.Success = true
			result.Message = fmt.Sprintf("Function module %s created and activated successfully", name)
		} else {
			result.Message = "Activation failed - check activation messages"
		}
		return result, nil

	default:
		result.Message = fmt.Sprintf("Unsupported object type for creation: %s", objectType)
		return result, nil
	}
}

// writeSourceUpdate handles update workflow
func (c *Client) writeSourceUpdate(ctx context.Context, objectType, name, source string, opts *WriteSourceOptions) (*WriteSourceResult, error) {
	result := &WriteSourceResult{
		ObjectType: objectType,
		ObjectName: name,
		Mode:       "updated",
	}

	// Use existing Write* workflows
	switch objectType {
	case "PROG":
		progResult, err := c.WriteProgram(ctx, name, source, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update program: %v", err)
			return result, nil
		}
		result.Success = progResult.Success
		result.ObjectURL = progResult.ObjectURL
		result.SyntaxErrors = progResult.SyntaxErrors
		result.Activation = progResult.Activation
		result.Message = progResult.Message
		return result, nil

	case "INCL":
		inclResult, err := c.WriteInclude(ctx, name, source, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update include: %v", err)
			return result, nil
		}
		result.Success = inclResult.Success
		result.ObjectURL = inclResult.ObjectURL
		result.SyntaxErrors = inclResult.SyntaxErrors
		result.Activation = inclResult.Activation
		result.Message = inclResult.Message
		return result, nil

	case "CLAS":
		// Method-level update: replace only the specified method
		if opts.Method != "" {
			methodResult, err := c.writeClassMethodUpdate(ctx, name, opts.Method, source, opts.Transport)
			if err != nil {
				result.Message = fmt.Sprintf("Failed to update method: %v", err)
				return result, nil
			}
			result.Success = methodResult.Success
			result.ObjectURL = methodResult.ObjectURL
			result.Method = methodResult.Method
			result.SyntaxErrors = methodResult.SyntaxErrors
			result.Activation = methodResult.Activation
			result.Message = methodResult.Message
			return result, nil
		}

		classResult, err := c.WriteClass(ctx, name, source, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update class: %v", err)
			return result, nil
		}
		result.Success = classResult.Success
		result.ObjectURL = classResult.ObjectURL
		result.SyntaxErrors = classResult.SyntaxErrors
		result.Activation = classResult.Activation
		result.Message = classResult.Message

		// If test source provided, update test include
		if opts.TestSource != "" {
			objectURL := fmt.Sprintf("/sap/bc/adt/oo/classes/%s", url.PathEscape(name))

			// Lock for test update
			lock, err := c.LockObject(ctx, objectURL, "MODIFY")
			if err != nil {
				result.Message += fmt.Sprintf(" (Warning: Failed to lock for test update: %v)", err)
				return result, nil
			}

			// Update test include - try update first, create if it doesn't exist
			err = c.UpdateClassInclude(ctx, name, "testclasses", opts.TestSource, lock.LockHandle, opts.Transport)
			if err != nil {
				// Try to create the test include first (it may not exist)
				createErr := c.CreateTestInclude(ctx, name, lock.LockHandle, opts.Transport)
				if createErr == nil {
					// Retry update after creating
					err = c.UpdateClassInclude(ctx, name, "testclasses", opts.TestSource, lock.LockHandle, opts.Transport)
				}
			}
			unlockErr := c.UnlockObject(ctx, objectURL, lock.LockHandle)
			if err != nil {
				result.Message += fmt.Sprintf(" (Warning: Failed to update test include: %v)", err)
				return result, nil
			}
			if unlockErr != nil {
				result.Message += fmt.Sprintf(" (Warning: Failed to unlock after test update: %v)", unlockErr)
			}

			// Activate the test include
			testIncludeURL := objectURL + "/includes/testclasses"
			_, activateErr := c.Activate(ctx, testIncludeURL, name)
			if activateErr != nil {
				result.Message += fmt.Sprintf(" (Warning: Failed to activate test include: %v)", activateErr)
			}

			// Run tests
			testResult, err := c.RunUnitTests(ctx, objectURL, nil)
			if err == nil {
				result.TestResults = testResult
			}
		}

		return result, nil

	case "INTF":
		// Similar to WriteProgram workflow
		objectURL := fmt.Sprintf("/sap/bc/adt/oo/interfaces/%s", url.PathEscape(name))
		sourceURL := objectURL + "/source/main"
		result.ObjectURL = objectURL

		// Resolve and check the package up front. The mark on the returned
		// context stops UpdateSource resolving it again from inside the
		// lock window, where the lookup's stateless hop would retire SAP's
		// stateful session (ICMENOSESSION) and invalidate the lock handle
		// (issue #91).
		ctx, err := c.gateAndMark(ctx, MutationContext{
			Op:        OpUpdate,
			OpName:    "WriteSource(INTF)",
			ObjectURL: objectURL,
			Transport: opts.Transport,
		})
		if err != nil {
			result.Message = fmt.Sprintf("Failed mutation gate: %v", err)
			return result, nil
		}

		// Syntax check
		syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
		if err != nil {
			result.Message = fmt.Sprintf("Syntax check failed: %v", err)
			return result, nil
		}

		for _, se := range syntaxErrors {
			if se.Severity == "E" || se.Severity == "A" || se.Severity == "X" {
				result.SyntaxErrors = syntaxErrors
				result.Message = "Source has syntax errors - not saved"
				return result, nil
			}
		}
		result.SyntaxErrors = syntaxErrors

		// Lock
		lock, err := c.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			result.Message = fmt.Sprintf("Failed to lock object: %v", err)
			return result, nil
		}

		// Tracked explicitly rather than keyed off result.Success — activation
		// can still fail after a successful unlock below, and result.Success
		// only flips to true at the very end.
		unlocked := false
		defer func() {
			if !unlocked {
				if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
					result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
				}
			}
		}()

		// Update
		err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update source: %v", err)
			return result, nil
		}

		// Unlock
		err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
		unlocked = true
		if err != nil {
			result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
			return result, nil
		}

		// Activate
		activation, err := c.Activate(ctx, objectURL, name)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to activate: %v", err)
			result.Activation = activation
			return result, nil
		}

		result.Activation = activation
		if activation.Success {
			result.Success = true
			result.Message = "Interface updated and activated successfully"
		} else {
			result.Message = "Activation failed - check activation messages"
		}

		return result, nil

	case "DDLS", "BDEF", "SRVD":
		// Get object URL
		var objectURL string
		switch objectType {
		case "DDLS":
			objectURL = GetObjectURL(ObjectTypeDDLS, name, "")
		case "BDEF":
			objectURL = GetObjectURL(ObjectTypeBDEF, name, "")
		case "SRVD":
			objectURL = GetObjectURL(ObjectTypeSRVD, name, "")
		}
		result.ObjectURL = objectURL
		sourceURL := objectURL + "/source/main"

		// Resolve and check the package up front. The mark on the returned
		// context stops UpdateSource resolving it again from inside the
		// lock window (issue #91).
		ctx, err := c.gateAndMark(ctx, MutationContext{
			Op:        OpUpdate,
			OpName:    fmt.Sprintf("WriteSource(%s)", objectType),
			ObjectURL: objectURL,
			Transport: opts.Transport,
		})
		if err != nil {
			result.Message = fmt.Sprintf("Failed mutation gate: %v", err)
			return result, nil
		}

		// Syntax check
		syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
		if err != nil {
			result.Message = fmt.Sprintf("Syntax check failed: %v", err)
			return result, nil
		}

		for _, se := range syntaxErrors {
			if se.Severity == "E" || se.Severity == "A" || se.Severity == "X" {
				result.SyntaxErrors = syntaxErrors
				result.Message = "Source has syntax errors - not saved"
				return result, nil
			}
		}
		result.SyntaxErrors = syntaxErrors

		// Lock
		lock, err := c.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			result.Message = fmt.Sprintf("Failed to lock object: %v", err)
			return result, nil
		}

		// Tracked explicitly rather than keyed off result.Success — activation
		// can still fail after a successful unlock below, and result.Success
		// only flips to true at the very end.
		unlocked := false
		defer func() {
			if !unlocked {
				if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
					result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
				}
			}
		}()

		// Update
		err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update source: %v", err)
			return result, nil
		}

		// Unlock
		err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
		unlocked = true
		if err != nil {
			result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
			return result, nil
		}

		// Activate
		activation, err := c.Activate(ctx, objectURL, name)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to activate: %v", err)
			result.Activation = activation
			return result, nil
		}

		result.Activation = activation
		if activation.Success {
			result.Success = true
			result.Message = fmt.Sprintf("%s updated and activated successfully", objectType)
		} else {
			result.Message = "Activation failed - check activation messages"
		}

		return result, nil

	case "FUNC":
		objectURL := GetObjectURL(ObjectTypeFunctionMod, name, opts.Parent)
		sourceURL := objectURL + "/source/main"
		result.ObjectURL = objectURL

		// The mark on the returned context stops UpdateSource resolving the
		// package again from inside the lock window (issue #91).
		ctx, err := c.gateAndMark(ctx, MutationContext{
			Op:        OpUpdate,
			OpName:    "WriteSource(FUNC)",
			ObjectURL: objectURL,
			Transport: opts.Transport,
		})
		if err != nil {
			result.Message = fmt.Sprintf("Failed mutation gate: %v", err)
			return result, nil
		}

		lock, err := c.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			result.Message = fmt.Sprintf("Failed to lock function module: %v", err)
			return result, nil
		}

		// Tracked explicitly rather than keyed off result.Success — activation
		// can still fail after a successful unlock below, and result.Success
		// only flips to true at the very end.
		unlocked := false
		defer func() {
			if !unlocked {
				if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
					result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
				}
			}
		}()

		err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, opts.Transport)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to update function module source: %v", err)
			return result, nil
		}

		err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
		unlocked = true
		if err != nil {
			result.Message = fmt.Sprintf("Failed to unlock function module: %v", err)
			return result, nil
		}

		activation, err := c.Activate(ctx, objectURL, name)
		if err != nil {
			result.Message = fmt.Sprintf("Failed to activate function module: %v", err)
			result.Activation = activation
			return result, nil
		}
		result.Activation = activation
		if activation.Success {
			result.Success = true
			result.Message = fmt.Sprintf("Function module %s updated and activated successfully", name)
		} else {
			result.Message = "Activation failed - check activation messages"
		}
		return result, nil

	default:
		result.Message = fmt.Sprintf("Unsupported object type for update: %s", objectType)
		return result, nil
	}
}

// writeClassMethodUpdate updates a single method in a class.
// The source should be the METHOD...ENDMETHOD block.
func (c *Client) writeClassMethodUpdate(ctx context.Context, className, methodName, methodSource, transport string) (*WriteSourceResult, error) {
	result := &WriteSourceResult{
		ObjectType: "CLAS",
		ObjectName: className,
		Method:     strings.ToUpper(methodName),
		Mode:       "updated",
	}

	className = strings.ToUpper(className)
	methodName = strings.ToUpper(methodName)
	objectURL := fmt.Sprintf("/sap/bc/adt/oo/classes/%s", url.PathEscape(strings.ToLower(className)))
	result.ObjectURL = objectURL

	// Resolve and check the package up front. The mark on the returned
	// context stops UpdateSource resolving it again from inside the lock
	// window (issue #91).
	ctx, err := c.gateAndMark(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "WriteClassMethod",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed mutation gate: %v", err)
		return result, nil
	}

	// Get method boundaries
	methods, err := c.GetClassMethods(ctx, className)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get class methods: %v", err)
		return result, nil
	}

	// Find the specified method
	var foundMethod *MethodInfo
	for i := range methods {
		if methods[i].Name == methodName {
			foundMethod = &methods[i]
			break
		}
	}
	if foundMethod == nil {
		result.Message = fmt.Sprintf("Method %s not found in class %s", methodName, className)
		return result, nil
	}

	if foundMethod.ImplementationStart == 0 || foundMethod.ImplementationEnd == 0 {
		result.Message = fmt.Sprintf("Method %s has no implementation lines (abstract method?)", methodName)
		return result, nil
	}

	// Get current class source
	currentSource, err := c.GetClassSource(ctx, className)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get current class source: %v", err)
		return result, nil
	}

	// Split into lines
	sourceLines := strings.Split(currentSource, "\n")
	if foundMethod.ImplementationEnd > len(sourceLines) {
		result.Message = fmt.Sprintf("Method line range (%d-%d) exceeds source lines (%d)",
			foundMethod.ImplementationStart, foundMethod.ImplementationEnd, len(sourceLines))
		return result, nil
	}

	// Reconstruct source with new method implementation
	var newSourceLines []string
	newSourceLines = append(newSourceLines, sourceLines[:foundMethod.ImplementationStart-1]...)
	newSourceLines = append(newSourceLines, strings.Split(methodSource, "\n")...)
	newSourceLines = append(newSourceLines, sourceLines[foundMethod.ImplementationEnd:]...)
	newSource := strings.Join(newSourceLines, "\n")

	// Syntax check
	syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, newSource)
	if err != nil {
		result.Message = fmt.Sprintf("Syntax check failed: %v", err)
		return result, nil
	}

	for _, se := range syntaxErrors {
		if se.Severity == "E" || se.Severity == "A" || se.Severity == "X" {
			result.SyntaxErrors = syntaxErrors
			result.Message = fmt.Sprintf("Method %s has syntax errors - not saved", methodName)
			return result, nil
		}
	}
	result.SyntaxErrors = syntaxErrors

	// Lock
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		result.Message = fmt.Sprintf("Failed to lock class: %v", err)
		return result, nil
	}

	writeLockHandle := lock.LockHandle
	writeTransport, err := c.resolveWriteTransport(transport, lock.CorrNr, "WriteClassMethodUpdate")
	if err != nil {
		if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
			result.Message = fmt.Sprintf("Transportable-edit check failed: %v — %s", err, strandedLockAdvice(objectURL, unlockErr))
		} else {
			result.Message = fmt.Sprintf("Transportable-edit check failed: %v", err)
		}
		return result, nil
	}

	// Tracked explicitly rather than keyed off result.Success — activation
	// can still fail after a successful unlock below, and result.Success
	// only flips to true at the very end.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	sourceURL := objectURL + "/source/main"
	err = c.UpdateSource(ctx, sourceURL, newSource, writeLockHandle, writeTransport)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update class source: %v", err)
		return result, nil
	}

	// Unlock
	err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
	unlocked = true
	if err != nil {
		result.Message = fmt.Sprintf("Failed to unlock class: %v", err)
		return result, nil
	}

	// Activate
	activation, err := c.Activate(ctx, objectURL, className)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to activate class: %v", err)
		result.Activation = activation
		return result, nil
	}

	result.Activation = activation
	if activation.Success {
		result.Success = true
		result.Message = fmt.Sprintf("Method %s updated and class %s activated successfully", methodName, className)
	} else {
		result.Message = fmt.Sprintf("Method %s updated but activation failed - check activation messages", methodName)
	}

	return result, nil
}

// --- Compare Source Tool ---

// SourceDiff represents a diff between two sources.
type SourceDiff struct {
	Object1      string `json:"object1"`
	Object2      string `json:"object2"`
	Identical    bool   `json:"identical"`
	AddedLines   int    `json:"addedLines"`
	RemovedLines int    `json:"removedLines"`
	Diff         string `json:"diff"`
}

// CompareSource compares source code of two objects and returns a unified diff.
// Supports comparing any two objects that can be read via GetSource.
func (c *Client) CompareSource(ctx context.Context, type1, name1, type2, name2 string, opts1, opts2 *GetSourceOptions) (*SourceDiff, error) {
	// Get source of first object
	source1, err := c.GetSource(ctx, type1, name1, opts1)
	if err != nil {
		return nil, fmt.Errorf("getting source for %s %s: %w", type1, name1, err)
	}

	// Get source of second object
	source2, err := c.GetSource(ctx, type2, name2, opts2)
	if err != nil {
		return nil, fmt.Errorf("getting source for %s %s: %w", type2, name2, err)
	}

	result := &SourceDiff{
		Object1:   fmt.Sprintf("%s:%s", type1, name1),
		Object2:   fmt.Sprintf("%s:%s", type2, name2),
		Identical: source1 == source2,
	}

	if result.Identical {
		result.Diff = "Sources are identical"
		return result, nil
	}

	// Generate unified diff
	lines1 := strings.Split(source1, "\n")
	lines2 := strings.Split(source2, "\n")

	diff := generateUnifiedDiff(result.Object1, result.Object2, lines1, lines2)
	result.Diff = diff

	// Count added/removed lines
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			result.AddedLines++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			result.RemovedLines++
		}
	}

	return result, nil
}

// generateUnifiedDiff creates a unified diff between two sets of lines.
func generateUnifiedDiff(name1, name2 string, lines1, lines2 []string) string {
	var diff strings.Builder

	diff.WriteString(fmt.Sprintf("--- %s\n", name1))
	diff.WriteString(fmt.Sprintf("+++ %s\n", name2))

	// Simple LCS-based diff algorithm
	m, n := len(lines1), len(lines2)

	// Build LCS table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if lines1[i-1] == lines2[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] > lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}

	// Backtrack to generate diff
	type diffLine struct {
		op   byte // ' ', '+', '-'
		text string
	}
	var diffLines []diffLine

	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && lines1[i-1] == lines2[j-1] {
			diffLines = append([]diffLine{{' ', lines1[i-1]}}, diffLines...)
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			diffLines = append([]diffLine{{'+', lines2[j-1]}}, diffLines...)
			j--
		} else {
			diffLines = append([]diffLine{{'-', lines1[i-1]}}, diffLines...)
			i--
		}
	}

	// Output hunks with context
	const contextLines = 3
	inHunk := false
	hunkStart1, hunkStart2 := 0, 0
	hunkLen1, hunkLen2 := 0, 0
	var hunkContent strings.Builder
	contextBefore := make([]diffLine, 0, contextLines)
	pendingContext := 0

	flushHunk := func() {
		if hunkLen1 > 0 || hunkLen2 > 0 {
			diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", hunkStart1, hunkLen1, hunkStart2, hunkLen2))
			diff.WriteString(hunkContent.String())
		}
		hunkContent.Reset()
		inHunk = false
		hunkLen1, hunkLen2 = 0, 0
	}

	line1, line2 := 1, 1
	for _, dl := range diffLines {
		if dl.op == ' ' {
			if inHunk {
				pendingContext++
				hunkContent.WriteString(fmt.Sprintf(" %s\n", dl.text))
				hunkLen1++
				hunkLen2++
				if pendingContext >= contextLines*2 {
					// Too much context, close hunk
					flushHunk()
					contextBefore = contextBefore[:0]
				}
			} else {
				// Accumulate context before a hunk
				if len(contextBefore) >= contextLines {
					contextBefore = contextBefore[1:]
				}
				contextBefore = append(contextBefore, dl)
			}
			line1++
			line2++
		} else {
			pendingContext = 0
			if !inHunk {
				// Start new hunk
				inHunk = true
				hunkStart1 = line1 - len(contextBefore)
				hunkStart2 = line2 - len(contextBefore)
				if hunkStart1 < 1 {
					hunkStart1 = 1
				}
				if hunkStart2 < 1 {
					hunkStart2 = 1
				}
				// Add context before
				for _, ctx := range contextBefore {
					hunkContent.WriteString(fmt.Sprintf(" %s\n", ctx.text))
					hunkLen1++
					hunkLen2++
				}
				contextBefore = contextBefore[:0]
			}
			hunkContent.WriteString(fmt.Sprintf("%c%s\n", dl.op, dl.text))
			if dl.op == '-' {
				hunkLen1++
				line1++
			} else {
				hunkLen2++
				line2++
			}
		}
	}
	flushHunk()

	return diff.String()
}

// --- Clone Object Tool ---

// CloneObjectResult represents the result of cloning an object.
type CloneObjectResult struct {
	Success    bool   `json:"success"`
	SourceName string `json:"sourceName"`
	TargetName string `json:"targetName"`
	ObjectType string `json:"objectType"`
	Package    string `json:"package"`
	Message    string `json:"message"`
}

// CloneObject copies an ABAP object to a new name.
// Supported types: PROG, CLAS, INTF
func (c *Client) CloneObject(ctx context.Context, objectType, sourceName, targetName, targetPackage string) (*CloneObjectResult, error) {
	// Safety check
	if err := c.checkSafety(OpCreate, "CloneObject"); err != nil {
		return nil, err
	}

	result := &CloneObjectResult{
		SourceName: sourceName,
		TargetName: targetName,
		ObjectType: objectType,
		Package:    targetPackage,
	}

	// Get source of original object
	source, err := c.GetSource(ctx, objectType, sourceName, nil)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to get source: %v", err)
		return result, nil
	}

	// Replace object name in source
	objectType = strings.ToUpper(objectType)
	sourceName = strings.ToUpper(sourceName)
	targetName = strings.ToUpper(targetName)

	// Replace the object name in the source code
	var newSource string
	switch objectType {
	case "PROG":
		// Replace REPORT <old> with REPORT <new>
		re := regexp.MustCompile(`(?i)(REPORT\s+)` + regexp.QuoteMeta(sourceName))
		newSource = re.ReplaceAllString(source, "${1}"+targetName)
	case "CLAS":
		// Replace CLASS <old> with CLASS <new>
		re := regexp.MustCompile(`(?i)(CLASS\s+)` + regexp.QuoteMeta(sourceName))
		newSource = re.ReplaceAllString(source, "${1}"+targetName)
	case "INTF":
		// Replace INTERFACE <old> with INTERFACE <new>
		re := regexp.MustCompile(`(?i)(INTERFACE\s+)` + regexp.QuoteMeta(sourceName))
		newSource = re.ReplaceAllString(source, "${1}"+targetName)
	default:
		result.Message = fmt.Sprintf("Unsupported object type for cloning: %s", objectType)
		return result, nil
	}

	// Write as new object
	description := fmt.Sprintf("Copy of %s", sourceName)
	writeResult, err := c.WriteSource(ctx, objectType, targetName, newSource, &WriteSourceOptions{
		Package:     targetPackage,
		Description: description,
		Mode:        "create",
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create clone: %v", err)
		return result, nil
	}

	if !writeResult.Success {
		result.Message = writeResult.Message
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Successfully cloned %s to %s", sourceName, targetName)
	return result, nil
}

// --- GetClassInfo Tool ---

// ClassInfo contains metadata about an ABAP class.
type ClassInfo struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Package      string   `json:"package,omitempty"`
	Category     string   `json:"category,omitempty"`   // Regular, Abstract, Final
	Visibility   string   `json:"visibility,omitempty"` // Public, Protected, Private
	Superclass   string   `json:"superclass,omitempty"`
	Interfaces   []string `json:"interfaces,omitempty"`
	Methods      []string `json:"methods,omitempty"`
	Attributes   []string `json:"attributes,omitempty"`
	HasTestClass bool     `json:"hasTestClass"`
	IsAbstract   bool     `json:"isAbstract"`
	IsFinal      bool     `json:"isFinal"`
}

// GetClassInfo retrieves class metadata without full source code.
// Uses GetObjectStructure for quick metadata extraction.
func (c *Client) GetClassInfo(ctx context.Context, className string) (*ClassInfo, error) {
	// Safety check
	if err := c.checkSafety(OpRead, "GetClassInfo"); err != nil {
		return nil, err
	}

	className = strings.ToUpper(className)

	// Get object structure
	structure, err := c.GetObjectStructureCAI(ctx, className, 100)
	if err != nil {
		return nil, fmt.Errorf("getting class structure: %w", err)
	}

	info := &ClassInfo{
		Name:       className,
		Methods:    make([]string, 0),
		Attributes: make([]string, 0),
		Interfaces: make([]string, 0),
	}

	// Parse root node
	if structure != nil {
		info.Description = structure.Description

		// Recursive function to extract info from tree
		var extractInfo func(node *ObjectExplorerNode)
		extractInfo = func(node *ObjectExplorerNode) {
			nodeType := strings.ToUpper(node.Type)
			nodeName := node.Name

			switch {
			case strings.Contains(nodeType, "METHOD"):
				info.Methods = append(info.Methods, nodeName)
			case strings.Contains(nodeType, "ATTR"):
				info.Attributes = append(info.Attributes, nodeName)
			case strings.Contains(nodeType, "INTF"):
				info.Interfaces = append(info.Interfaces, nodeName)
			case strings.Contains(nodeType, "TEST"):
				info.HasTestClass = true
			}

			// Check for superclass in description
			if strings.Contains(strings.ToUpper(node.Description), "INHERITING") {
				parts := strings.Fields(node.Description)
				for i, p := range parts {
					if strings.ToUpper(p) == "FROM" && i+1 < len(parts) {
						info.Superclass = parts[i+1]
					}
				}
			}

			// Recurse into children
			for i := range node.Children {
				extractInfo(&node.Children[i])
			}
		}

		extractInfo(structure)
	}

	// Check for abstract/final in main source (quick scan)
	source, err := c.GetClassSource(ctx, className)
	if err == nil {
		sourceUpper := strings.ToUpper(source)
		if strings.Contains(sourceUpper, "CLASS "+className+" DEFINITION ABSTRACT") ||
			strings.Contains(sourceUpper, "ABSTRACT DEFINITION") {
			info.IsAbstract = true
			info.Category = "Abstract"
		}
		if strings.Contains(sourceUpper, "CLASS "+className+" DEFINITION FINAL") ||
			strings.Contains(sourceUpper, "FINAL DEFINITION") {
			info.IsFinal = true
			info.Category = "Final"
		}
		if info.Category == "" {
			info.Category = "Regular"
		}

		// Extract package from source header if present
		lines := strings.Split(source, "\n")
		for _, line := range lines[:min(20, len(lines))] {
			if strings.Contains(strings.ToUpper(line), "DEVC") {
				// Try to extract package
				re := regexp.MustCompile(`DEVC\s+(\$?\w+)`)
				if matches := re.FindStringSubmatch(line); len(matches) > 1 {
					info.Package = matches[1]
				}
			}
		}
	}

	return info, nil
}
