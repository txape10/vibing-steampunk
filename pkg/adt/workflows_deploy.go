package adt

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// --- File-Based Deployment Workflows ---

// DeployResult contains the result of a file deployment operation.
type DeployResult struct {
	// Transport is the request the write went under, and TransportNote
	// says how it was chosen when the caller named none.
	Transport          string   `json:"transport,omitempty"`
	TransportNote      string   `json:"transportNote,omitempty"`
	ObjectURL          string   `json:"objectUrl"`
	ObjectName         string   `json:"objectName"`
	ObjectType         string   `json:"objectType"`
	FilePath           string   `json:"filePath"`
	Success            bool     `json:"success"`
	Created            bool     `json:"created"` // true if created, false if updated
	SyntaxErrors       []string `json:"syntaxErrors,omitempty"`
	Errors             []string `json:"errors,omitempty"`
	ExpectedSourceHash string   `json:"expectedSourceHash,omitempty"`
	TargetSourceHash   string   `json:"targetSourceHash,omitempty"`
	VerifiedSourceHash string   `json:"verifiedSourceHash,omitempty"`
	Message            string   `json:"message,omitempty"`
}

// DeployFromFileOptions configures an optional optimistic-concurrency guard
// for an existing object deployment.
type DeployFromFileOptions struct {
	ExpectedSourceHash string
}

// CreateFromFile creates a new ABAP object from a file and activates it.
//
// Workflow: Parse → Create → Lock → SyntaxCheck → Write → Unlock → Activate
//
// The function automatically detects the object type and name from the file extension
// and content. Supported file extensions: .clas.abap, .prog.abap, .intf.abap
//
// Example:
//   result, err := client.CreateFromFile(ctx, "/path/to/zcl_test.clas.abap", "$TMP", "")
// Named returns (result, err) so the deferred unlock below — which can fire
// well after any of the early `return &DeployResult{...}, nil` statements —
// has somewhere to attach strandedLockAdvice when the compensating unlock
// itself fails (issue #91).
func (c *Client) CreateFromFile(ctx context.Context, filePath, packageName, transport string) (result *DeployResult, err error) {
	// Unified mutation policy gate (op type + package + transport). Run with
	// the explicit Package the caller supplied; CreateObject below gates it
	// a second time before asking SAP to create the object there.
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpCreate,
		OpName:    "CreateFromFile",
		Package:   strings.ToUpper(packageName),
		Transport: transport,
	}); err != nil {
		return nil, err
	}

	// 1. Parse file to detect type and name
	info, err := ParseABAPFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("parsing file: %w", err)
	}

	// 2. Read source code
	sourceBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	source := string(sourceBytes)

	// 3. Create object
	var chosen TransportChoice
	err = c.CreateObject(ctx, CreateObjectOptions{
		ObjectType:  info.ObjectType,
		Name:        info.ObjectName,
		ParentName:  info.ParentName, // For function modules: function group name
		Description: info.Description,
		PackageName: packageName,
		Transport:   transport,
		Chosen:      &chosen,
	})
	if err != nil {
		return &DeployResult{
			FilePath:   filePath,
			ObjectName: info.ObjectName,
			ObjectType: string(info.ObjectType),
			Success:    false,
			Errors:     []string{fmt.Sprintf("create failed: %v", err)},
			Message:    fmt.Sprintf("Failed to create %s %s", info.ObjectType, info.ObjectName),
		}, nil
	}
	if chosen.Transport != "" {
		transport = chosen.Transport
	}
	trNote := chosen.Reason

	// 4. Build object URL
	objectURL, err := c.buildObjectURLWithParent(info.ObjectType, info.ObjectName, info.ParentName)
	if err != nil {
		return nil, err
	}

	// CreateObject accepted packageName against the whitelist and SAP put
	// the object there, so UpdateSource below need not resolve the same
	// package again — which it would do from inside the lock, ending the
	// session the lock handle belongs to (issue #91).
	ctx = withMutationPackageChecked(ctx, objectURL)

	// 5. Syntax check BEFORE lock — SyntaxCheck runs stateless and would
	// break stateful session affinity if placed between Lock and UpdateSource,
	// causing ICMENOSESSION / CSRF errors.
	syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
	if err != nil {
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			Errors:        []string{fmt.Sprintf("syntax check failed: %v", err)},
			Message:       fmt.Sprintf("Object created but syntax check failed: %v", err),
		}, nil
	}

	if len(syntaxErrors) > 0 {
		// Convert syntax errors to strings
		errorMsgs := make([]string, len(syntaxErrors))
		for i, e := range syntaxErrors {
			errorMsgs[i] = fmt.Sprintf("Line %d: %s", e.Line, e.Text)
		}
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			SyntaxErrors:  errorMsgs,
			Message:       fmt.Sprintf("Object created but has %d syntax errors", len(syntaxErrors)),
		}, nil
	}

	// 6. Lock object — from here on, ALL requests must be stateful to
	// maintain session affinity for the lock handle (issue #88).
	lockResult, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			Errors:        []string{fmt.Sprintf("lock failed: %v", err)},
			Message:       fmt.Sprintf("Object created but failed to lock: %v", err),
		}, nil
	}

	// Ensure unlock on any error. Detached from ctx's cancellation and given
	// its own deadline (issue #91) — a failure that cancelled ctx would
	// otherwise never send the compensating UNLOCK at all.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lockResult.LockHandle); unlockErr != nil && result != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	// 7. Write source (need source URL, not object URL)
	sourceURL, err := c.buildSourceURL(info.ObjectType, info.ObjectName)
	if err != nil {
		return nil, err
	}
	err = c.UpdateSource(ctx, sourceURL, source, lockResult.LockHandle, transport)
	if err != nil {
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			Errors:        []string{fmt.Sprintf("write source failed: %v", err)},
			Message:       fmt.Sprintf("Object created but failed to write source: %v", err),
		}, nil
	}

	// 8. Unlock
	err = c.UnlockObject(ctx, objectURL, lockResult.LockHandle)
	unlocked = true
	if err != nil {
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			Errors:        []string{fmt.Sprintf("unlock failed: %v", err)},
			Message:       fmt.Sprintf("Source written but failed to unlock: %v", err),
		}, nil
	}

	// 9. Activate
	_, err = c.Activate(ctx, objectURL, info.ObjectName)
	if err != nil {
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			Errors:        []string{fmt.Sprintf("activation failed: %v", err)},
			Message:       fmt.Sprintf("Source written but activation failed: %v", err),
		}, nil
	}

	return &DeployResult{
		FilePath:      filePath,
		ObjectURL:     objectURL,
		ObjectName:    info.ObjectName,
		ObjectType:    string(info.ObjectType),
		Transport:     transport,
		TransportNote: trNote,
		Success:       true,
		Created:       true,
		Message:       fmt.Sprintf("Successfully created and activated %s %s from %s", info.ObjectType, info.ObjectName, filePath),
	}, nil
}

// UpdateFromFile updates an existing ABAP object from a file.
//
// Workflow: Parse → Lock → SyntaxCheck → Write → Unlock → Activate
//
// Example:
//   result, err := client.UpdateFromFile(ctx, "/path/to/zcl_test.clas.abap", "")
func (c *Client) UpdateFromFile(ctx context.Context, filePath, transport string) (*DeployResult, error) {
	return c.UpdateFromFileWithOptions(ctx, filePath, transport, nil)
}

// UpdateFromFileWithOptions is UpdateFromFile with an optional source version
// precondition. Its check happens after the object lock is acquired.
// Named returns for the same reason as CreateFromFile above.
func (c *Client) UpdateFromFileWithOptions(ctx context.Context, filePath, transport string, opts *DeployFromFileOptions) (result *DeployResult, err error) {
	if opts != nil {
		ctx = withExpectedSourceHash(ctx, opts.ExpectedSourceHash)
	}
	// 1. Parse file to detect type and name
	info, err := ParseABAPFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("parsing file: %w", err)
	}

	// Check if this is a class include (testclasses, locals_def, etc.)
	isClassInclude := info.ObjectType == ObjectTypeClass &&
		info.ClassIncludeType != "" &&
		info.ClassIncludeType != ClassIncludeMain

	// 2. Read source code
	sourceBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	source := string(sourceBytes)

	// 3. Build object URL (for class includes, this is the parent class URL)
	objectURL, err := c.buildObjectURL(info.ObjectType, info.ObjectName)
	if err != nil {
		return nil, err
	}

	// Unified mutation policy gate (op type + package + transport). The
	// returned context carries the mark that stops UpdateSource /
	// UpdateClassInclude / CreateTestInclude resolving the same package
	// again from inside the lock window (issue #91).
	ctx, err = c.gateAndMark(ctx, MutationContext{
		Op:        OpUpdate,
		OpName:    "UpdateFromFile",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	// 4. Syntax check BEFORE lock (skip for class includes - will check after update).
	// SyntaxCheck runs stateless and would break stateful session affinity if
	// placed between Lock and UpdateSource, causing ICMENOSESSION / CSRF errors.
	if !isClassInclude {
		syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
		if err != nil {
			return &DeployResult{
				FilePath:   filePath,
				ObjectURL:  objectURL,
				ObjectName: info.ObjectName,
				ObjectType: string(info.ObjectType),
				Success:    false,
				Errors:     []string{fmt.Sprintf("syntax check failed: %v", err)},
				Message:    fmt.Sprintf("Syntax check failed: %v", err),
			}, nil
		}

		if len(syntaxErrors) > 0 {
			// Convert syntax errors to strings
			errorMsgs := make([]string, len(syntaxErrors))
			for i, e := range syntaxErrors {
				errorMsgs[i] = fmt.Sprintf("Line %d: %s", e.Line, e.Text)
			}
			return &DeployResult{
				FilePath:     filePath,
				ObjectURL:    objectURL,
				ObjectName:   info.ObjectName,
				ObjectType:   string(info.ObjectType),
				Success:      false,
				SyntaxErrors: errorMsgs,
				Message:      fmt.Sprintf("Source has %d syntax errors", len(syntaxErrors)),
			}, nil
		}
	}

	// A transportable object with no request named: pick one the way the
	// editor would (PR #203). Runs before the lock — stateless, must not
	// sit between LOCK and PUT (issue #91).
	trPlan := c.planTransport(ctx, transport, objectURL, "")

	// 5. Lock object — from here on, ALL requests must be stateful to
	// maintain session affinity for the lock handle (issue #88).
	lockResult, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		return &DeployResult{
			FilePath:   filePath,
			ObjectURL:  objectURL,
			ObjectName: info.ObjectName,
			ObjectType: string(info.ObjectType),
			Success:    false,
			Errors:     []string{fmt.Sprintf("lock failed: %v", err)},
			Message:    fmt.Sprintf("Failed to lock object: %v", err),
		}, nil
	}

	// Ensure unlock on any error. Detached from ctx's cancellation and given
	// its own deadline (issue #91) — a failure that cancelled ctx would
	// otherwise never send the compensating UNLOCK at all.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lockResult.LockHandle); unlockErr != nil && result != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	// Reuse the request the object is already bound to when the caller
	// supplied no transport (issue #144); otherwise the plan above
	// (issue #91's #203 follow-on).
	effectiveTransport, trNote, err := c.resolveWriteTransportFor(trPlan, transport, lockResult.CorrNr, "UpdateFromFile")
	if err != nil {
		return &DeployResult{
			FilePath:   filePath,
			ObjectURL:  objectURL,
			ObjectName: info.ObjectName,
			ObjectType: string(info.ObjectType),
			Success:    false,
			Errors:     []string{fmt.Sprintf("transportable-edit check failed: %v", err)},
			Message:    fmt.Sprintf("Transportable-edit check failed: %v", err),
		}, nil
	}
	transport = effectiveTransport

	// 6. Write source
	if isClassInclude {
		// For class includes, use UpdateClassInclude
		// First try to update - if fails with 404, create the include first
		err = c.UpdateClassInclude(ctx, info.ObjectName, info.ClassIncludeType, source, lockResult.LockHandle, transport)
		if err != nil {
			// Try to create the include first (for testclasses)
			if info.ClassIncludeType == ClassIncludeTestClasses {
				createErr := c.CreateTestInclude(ctx, info.ObjectName, lockResult.LockHandle, transport)
				if createErr == nil {
					// Retry update
					err = c.UpdateClassInclude(ctx, info.ObjectName, info.ClassIncludeType, source, lockResult.LockHandle, transport)
				}
			}
		}
		if err != nil {
			return &DeployResult{
				FilePath:      filePath,
				ObjectURL:     objectURL,
				ObjectName:    info.ObjectName,
				ObjectType:    fmt.Sprintf("%s.%s", info.ObjectType, info.ClassIncludeType),
				Transport:     transport,
				TransportNote: trNote,
				Success:       false,
				Errors:        []string{fmt.Sprintf("write class include failed: %v", err)},
				Message:       fmt.Sprintf("Failed to write class include: %v", err),
			}, nil
		}
	} else {
		// Regular source update
		sourceURL, err := c.buildSourceURL(info.ObjectType, info.ObjectName)
		if err != nil {
			return nil, err
		}
		err = c.UpdateSource(ctx, sourceURL, source, lockResult.LockHandle, transport)
		if err != nil {
			return &DeployResult{
				FilePath:      filePath,
				ObjectURL:     objectURL,
				ObjectName:    info.ObjectName,
				ObjectType:    string(info.ObjectType),
				Transport:     transport,
				TransportNote: trNote,
				Success:       false,
				Errors:        []string{fmt.Sprintf("write source failed: %v", err)},
				Message:       fmt.Sprintf("Failed to write source: %v", err),
			}, nil
		}
	}

	// 7. Unlock
	err = c.UnlockObject(ctx, objectURL, lockResult.LockHandle)
	unlocked = true
	if err != nil {
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			Errors:        []string{fmt.Sprintf("unlock failed: %v", err)},
			Message:       fmt.Sprintf("Source written but failed to unlock: %v", err),
		}, nil
	}

	// 8. Activate
	_, err = c.Activate(ctx, objectURL, info.ObjectName)
	if err != nil {
		return &DeployResult{
			FilePath:      filePath,
			ObjectURL:     objectURL,
			ObjectName:    info.ObjectName,
			ObjectType:    string(info.ObjectType),
			Transport:     transport,
			TransportNote: trNote,
			Success:       false,
			Errors:        []string{fmt.Sprintf("activation failed: %v", err)},
			Message:       fmt.Sprintf("Source written but activation failed: %v", err),
		}, nil
	}

	// Build result message
	objTypeStr := string(info.ObjectType)
	if isClassInclude {
		objTypeStr = fmt.Sprintf("%s.%s", info.ObjectType, info.ClassIncludeType)
	}

	result = &DeployResult{
		FilePath:      filePath,
		ObjectURL:     objectURL,
		ObjectName:    info.ObjectName,
		ObjectType:    objTypeStr,
		Transport:     transport,
		TransportNote: trNote,
		Success:       true,
		Created:       false,
		Message:       fmt.Sprintf("Successfully updated and activated %s %s from %s", objTypeStr, info.ObjectName, filePath),
	}
	if opts == nil || opts.ExpectedSourceHash == "" {
		return result, nil
	}
	result.ExpectedSourceHash = opts.ExpectedSourceHash
	result.TargetSourceHash = SourceHash(source)
	// Decision C (PR #191 port): this fork's buildSourceURL takes only
	// (objType, name) — it has no 3-arg FUNC-parent variant to extend, and
	// FUNC-file deploys already can't resolve a source URL through this same
	// helper earlier in this function (a pre-existing gap, left untouched).
	verificationURL, verifyURLErr := c.buildSourceURL(info.ObjectType, info.ObjectName)
	if isClassInclude {
		verificationURL = GetClassIncludeSourceURL(info.ObjectName, info.ClassIncludeType)
		verifyURLErr = nil
	}
	if verifyURLErr != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Source was written and activated, but post-write verification could not resolve its source URL: %v. Do not retry blindly.", verifyURLErr)
		return result, nil
	}
	resp, verifyErr := c.transport.Request(ctx, verificationURL, &RequestOptions{Method: "GET", Accept: "text/plain"})
	if verifyErr != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Source was written and activated, but post-write verification could not read it: %v. Do not retry blindly.", verifyErr)
		return result, nil
	}
	result.VerifiedSourceHash = SourceHash(string(resp.Body))
	if result.VerifiedSourceHash != result.TargetSourceHash {
		result.Success = false
		result.Message = fmt.Sprintf("Source was written and activated, but post-write verification differs (target %s, actual %s). Do not retry blindly.", result.TargetSourceHash, result.VerifiedSourceHash)
	}
	return result, nil
}

// DeployFromFile intelligently creates or updates an object from a file.
//
// Workflow: Parse → CheckExists → CreateFromFile OR UpdateFromFile
//
// This is the recommended method for deploying ABAP objects from files as it
// automatically determines whether to create a new object or update an existing one.
//
// Supports class includes (.clas.testclasses.abap, .clas.locals_def.abap, etc.)
// For class includes, the parent class must already exist.
//
// Example:
//   result, err := client.DeployFromFile(ctx, "/path/to/zcl_test.clas.abap", "$TMP", "")
//   result, err := client.DeployFromFile(ctx, "/path/to/zcl_test.clas.testclasses.abap", "$TMP", "")
func (c *Client) DeployFromFile(ctx context.Context, filePath, packageName, transport string) (*DeployResult, error) {
	return c.DeployFromFileWithOptions(ctx, filePath, packageName, transport, nil)
}

// DeployFromFileWithOptions preserves DeployFromFile's create-or-update
// behaviour while allowing an existing-object update to be version guarded.
func (c *Client) DeployFromFileWithOptions(ctx context.Context, filePath, packageName, transport string, opts *DeployFromFileOptions) (*DeployResult, error) {
	// 1. Parse file
	info, err := ParseABAPFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("parsing file: %w", err)
	}

	// Check if this is a class include
	isClassInclude := info.ObjectType == ObjectTypeClass &&
		info.ClassIncludeType != "" &&
		info.ClassIncludeType != ClassIncludeMain

	// Check if this is a function module (requires parent function group)
	isFunctionModule := info.ObjectType == ObjectTypeFunctionMod

	// 2. Check if object exists
	objectURL, err := c.buildObjectURLWithParent(info.ObjectType, info.ObjectName, info.ParentName)
	if err != nil {
		if isFunctionModule && info.ParentName == "" {
			return nil, fmt.Errorf("function module file must follow pattern: {fugr_name}.fugr.{func_name}.func.abap")
		}
		return nil, err
	}

	// Try to get object (if 404, doesn't exist)
	_, err = c.transport.Request(ctx, objectURL, &RequestOptions{
		Method: "GET",
		Accept: "text/plain",
	})

	if err != nil {
		// Check if it's specifically a 404 Not Found error
		if IsNotFoundError(err) {
			// Object doesn't exist
			if isClassInclude {
				// For class includes, the parent class must exist
				return &DeployResult{
					FilePath:   filePath,
					ObjectURL:  objectURL,
					ObjectName: info.ObjectName,
					ObjectType: fmt.Sprintf("%s.%s", info.ObjectType, info.ClassIncludeType),
					Success:    false,
					Errors:     []string{"parent class does not exist"},
					Message:    fmt.Sprintf("Cannot deploy class include: parent class %s does not exist. Create the class first.", info.ObjectName),
				}, nil
			}
			// Regular object - create it
			if opts != nil && opts.ExpectedSourceHash != "" {
				return nil, fmt.Errorf("expected_source_hash is only valid when updating an existing object")
			}
			return c.CreateFromFile(ctx, filePath, packageName, transport)
		}
		// For other errors (session timeout, network issues, etc.), proceed with update
		// The parent class might still exist - let UpdateFromFile handle it
	}

	// Object exists - update it (handles both regular objects and class includes)
	return c.UpdateFromFileWithOptions(ctx, filePath, transport, opts)
}

// buildObjectURL constructs the ADT URL for an object type and name
func (c *Client) buildObjectURL(objType CreatableObjectType, name string) (string, error) {
	return c.buildObjectURLWithParent(objType, name, "")
}

// buildObjectURLWithParent constructs the ADT URL for an object type with optional parent
func (c *Client) buildObjectURLWithParent(objType CreatableObjectType, name, parentName string) (string, error) {
	name = strings.ToLower(name)
	// URL encode to handle namespaced objects like /DMO/...
	encodedName := url.PathEscape(name)
	switch objType {
	case ObjectTypeClass:
		return fmt.Sprintf("/sap/bc/adt/oo/classes/%s", encodedName), nil
	case ObjectTypeProgram:
		return fmt.Sprintf("/sap/bc/adt/programs/programs/%s", encodedName), nil
	case ObjectTypeInterface:
		return fmt.Sprintf("/sap/bc/adt/oo/interfaces/%s", encodedName), nil
	case ObjectTypeFunctionGroup:
		return fmt.Sprintf("/sap/bc/adt/functions/groups/%s", encodedName), nil
	case ObjectTypeFunctionMod:
		if parentName == "" {
			return "", fmt.Errorf("function module requires parent function group name")
		}
		parentName = strings.ToLower(parentName)
		encodedParent := url.PathEscape(parentName)
		return fmt.Sprintf("/sap/bc/adt/functions/groups/%s/fmodules/%s", encodedParent, encodedName), nil
	case ObjectTypeInclude:
		return fmt.Sprintf("/sap/bc/adt/programs/includes/%s", encodedName), nil
	// RAP object types
	case ObjectTypeDDLS:
		return fmt.Sprintf("/sap/bc/adt/ddic/ddl/sources/%s", encodedName), nil
	case ObjectTypeBDEF:
		return fmt.Sprintf("/sap/bc/adt/bo/behaviordefinitions/%s", encodedName), nil
	case ObjectTypeSRVD:
		return fmt.Sprintf("/sap/bc/adt/ddic/srvd/sources/%s", encodedName), nil
	default:
		return "", fmt.Errorf("unsupported object type for URL building: %s", objType)
	}
}

// buildSourceURL constructs the source URL for an object (object URL + /source/main)
func (c *Client) buildSourceURL(objType CreatableObjectType, name string) (string, error) {
	objectURL, err := c.buildObjectURL(objType, name)
	if err != nil {
		return "", err
	}
	return objectURL + "/source/main", nil
}
