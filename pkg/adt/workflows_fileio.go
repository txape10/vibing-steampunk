package adt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// --- Utility Workflows ---

// RenameObjectResult contains the result of renaming an object.
type RenameObjectResult struct {
	OldName    string `json:"oldName"`
	NewName    string `json:"newName"`
	ObjectType string `json:"objectType"`
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	Errors     []string `json:"errors,omitempty"`
	// Transport is the request the new object's source write went under, and
	// TransportNote says how it was chosen when the caller named none.
	Transport     string `json:"transport,omitempty"`
	TransportNote string `json:"transportNote,omitempty"`
}

// RenameObject renames an ABAP object by creating a copy with the new name and deleting the old one.
//
// Workflow: GetSource → CreateNew → WriteSource → ActivateNew → DeleteOld
//
// Only object types with a single editable /source/main document can be renamed
// this way: CLAS/OC, PROG/P, INTF/OI, PROG/I. A function group's source is split
// across its top include, the UXX includes and its function modules, so a
// copy-and-delete cannot reproduce it; DDIC and RAP types are not plain-ABAP
// source. Those are rejected up front, before anything is locked or created.
//
// This is a destructive operation - use with caution!
func (c *Client) RenameObject(ctx context.Context, objType CreatableObjectType, oldName, newName, packageName, transport string) (*RenameObjectResult, error) {
	result := &RenameObjectResult{
		OldName:    oldName,
		NewName:    newName,
		ObjectType: string(objType),
	}

	switch objType {
	case ObjectTypeClass, ObjectTypeProgram, ObjectTypeInterface, ObjectTypeInclude:
		// rename-by-copy works: one editable /source/main document
	default:
		return nil, fmt.Errorf(
			"RenameObject does not support object type %s: only CLAS/OC, PROG/P, "+
				"INTF/OI and PROG/I can be renamed by copy — other types have no "+
				"single source document to reproduce", objType)
	}

	oldURL, err := c.buildObjectURL(objType, oldName)
	if err != nil {
		return nil, err
	}

	// Unified mutation policy gate for the old object being deleted. The mark
	// on the returned context covers exactly the object this gate resolved,
	// so the DeleteObject at the end does not resolve it again while holding
	// the lock it is about to use (issue #91).
	ctx, err = c.gateAndMark(ctx, MutationContext{
		Op:        OpDelete,
		OpName:    "RenameObject",
		ObjectURL: oldURL,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	// If a target package is supplied, gate the create side as well.
	// CreateObject below checks it again, so this is not marked away — the
	// new object's URL is marked separately once it exists (see below).
	if packageName != "" {
		if err := c.checkMutation(ctx, MutationContext{
			Op:        OpCreate,
			OpName:    "RenameObject",
			Package:   packageName,
			Transport: transport,
		}); err != nil {
			return nil, err
		}
	}

	// 1. Get old object source
	resp, err := c.transport.Request(ctx, oldURL+"/source/main", &RequestOptions{
		Method: "GET",
		Accept: "text/plain",
	})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to read old object: %v", err))
		return result, nil
	}
	oldSource := string(resp.Body)

	// 2. Replace old name with new name in source
	newSource := strings.ReplaceAll(oldSource, strings.ToUpper(oldName), strings.ToUpper(newName))
	newSource = strings.ReplaceAll(newSource, strings.ToLower(oldName), strings.ToLower(newName))

	// 3. Create new object. Capture the transport CreateObject picks for a
	// transportable target with none named, so the source PUT below (and the
	// rollback delete of this shell, if it comes to that) go into that same
	// request rather than letting SAP generate one per write (PR #203 / the
	// project's transport rule). The old-object delete resolves its own
	// request separately from the old object's lock.
	var shellChoice TransportChoice
	err = c.CreateObject(ctx, CreateObjectOptions{
		ObjectType:  objType,
		Name:        newName,
		Description: fmt.Sprintf("Renamed from %s", oldName),
		PackageName: packageName,
		Transport:   transport,
		Chosen:      &shellChoice,
	})
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to create new object: %v", err))
		return result, nil
	}

	// 4. Write source to new object
	newURL, _ := c.buildObjectURL(objType, newName)
	// The new object was created in packageName, which the create-side gate
	// above accepted (and CreateObject checked again). Only mark when a
	// package was actually supplied and therefore actually checked: with
	// packageName empty there is no approved package to stand behind, and
	// leaving the mark off makes UpdateSource fall back to the full gate.
	if packageName != "" {
		ctx = withMutationPackageChecked(ctx, newURL)
	}

	lockResult, err := c.LockObject(ctx, newURL, "MODIFY")
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to lock new object: %v", err))
		return result, nil
	}

	// Track the release explicitly. The old form was an unconditional defer
	// plus an inline unlock on the happy path, so every successful rename
	// sent a second UNLOCK for a handle that was already gone.
	newUnlocked := false
	defer func() {
		if !newUnlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, newURL, lockResult.LockHandle); unlockErr != nil {
				result.Errors = append(result.Errors, strandedLockAdvice(newURL, unlockErr))
			}
		}
	}()

	// Adopt the request the shell was created in (or the one the lock
	// reports), re-validated against the transportable-edit policy so a
	// request chosen here cannot bypass the gate that naming it would hit.
	effectiveTransport, trNote, err := c.resolveWriteTransportFor(&shellChoice, transport, lockResult.CorrNr, "RenameObject")
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Transportable-edit check failed: %v", err))
		return result, nil
	}
	result.Transport, result.TransportNote = effectiveTransport, trNote

	// rollbackShell removes the object created in step 3 when a later step
	// fails, so a failed rename does not leave an empty shell that blocks the
	// retry (CreateObject would then report "already exists"). The shell is
	// still MODIFY-locked here, so unlock before deleting; DeleteObjectWithAutoLock
	// self-gates and locks+deletes atomically (CLAUDE.md 2ae/2af).
	rollbackShell := func() {
		if !newUnlocked {
			_ = c.UnlockObject(ctx, newURL, lockResult.LockHandle)
			newUnlocked = true
		}
		if delErr := c.DeleteObjectWithAutoLock(ctx, newURL, effectiveTransport); delErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf(
				"the partially created %s could not be rolled back (%v) — delete it manually", newName, delErr))
		} else {
			result.Errors = append(result.Errors, fmt.Sprintf("rolled back the partially created %s", newName))
		}
		// The write was undone; a transport reported here would name the
		// request it was rolled back out of, which misleads a consumer
		// reading Transport on a failed result.
		result.Transport, result.TransportNote = "", ""
	}

	// The source resource is the object URL + /source/main — UpdateSource PUTs
	// to exactly the URL it is given, and step 1 reads from the same suffix.
	// Without it the plain ABAP body lands on the bare object URL, where SAP
	// expects the metadata XML and answers 400 ExceptionInvalidData.
	err = c.UpdateSource(ctx, newURL+"/source/main", newSource, lockResult.LockHandle, effectiveTransport)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to write source: %v", err))
		rollbackShell()
		return result, nil
	}

	unlockErr := c.UnlockObject(ctx, newURL, lockResult.LockHandle)
	newUnlocked = true
	if unlockErr != nil {
		result.Errors = append(result.Errors, strandedLockAdvice(newURL, unlockErr))
	}

	// 5. Activate new object
	_, err = c.Activate(ctx, newURL, newName)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to activate new object: %v", err))
		rollbackShell()
		return result, nil
	}

	// 6. Delete old object. Gate + mark the context for oldURL *before* its
	// lock: the old object's package was never checked above (only the new
	// object's), so this is a real gate, and it must run here rather than
	// inside DeleteObject, where its SearchObject would be a stateless hop in
	// the lock window (issue #91). No lock is open at this point — the new
	// object was already unlocked and activated.
	ctx, err = c.gateAndMark(ctx, MutationContext{
		Op:        OpDelete,
		OpName:    "RenameObject",
		ObjectURL: oldURL,
		Transport: transport,
	})
	if err != nil {
		result.Message = fmt.Sprintf("New object %s created successfully, but the old object %s cannot be deleted: %v. Please delete manually.", newName, oldName, err)
		result.Success = true
		return result, nil
	}

	oldLockResult, err := c.LockObject(ctx, oldURL, "MODIFY")
	if err != nil {
		result.Message = fmt.Sprintf("New object %s created successfully, but failed to lock old object %s for deletion: %v. Please delete manually.", newName, oldName, err)
		result.Success = true
		return result, nil
	}

	// A successful DELETE consumes the lock with the object; anything else
	// leaves the ENQUEUE on an object the user has just been told to delete
	// by hand — which is precisely what would make the manual delete fail.
	// This branch had no defer at all, so the failure path returned
	// Success=true with the lock still held and nothing said about it.
	oldReleased := false
	defer func() {
		if !oldReleased {
			if unlockErr := c.releaseLockAfterFailure(ctx, oldURL, oldLockResult.LockHandle); unlockErr != nil {
				result.Errors = append(result.Errors, strandedLockAdvice(oldURL, unlockErr))
			}
		}
	}()

	// The old object may sit in its own open request; adopt that (re-validated
	// against policy) rather than passing the caller's transport blindly.
	oldTransport, err := c.resolveWriteTransport(transport, oldLockResult.CorrNr, "RenameObject")
	if err != nil {
		result.Message = fmt.Sprintf("New object %s created successfully, but the transportable-edit check for deleting %s failed: %v. Please delete manually.", newName, oldName, err)
		result.Success = true
		return result, nil
	}

	err = c.DeleteObject(ctx, oldURL, oldLockResult.LockHandle, oldTransport)
	if err != nil {
		result.Message = fmt.Sprintf("New object %s created successfully, but failed to delete old object %s: %v. Please delete manually.", newName, oldName, err)
		result.Success = true
		return result, nil
	}
	oldReleased = true

	result.Success = true
	result.Message = fmt.Sprintf("Successfully renamed %s to %s", oldName, newName)
	return result, nil
}

// SaveToFileResult contains the result of saving an object to a file.
type SaveToFileResult struct {
	ObjectName string `json:"objectName"`
	ObjectType string `json:"objectType"`
	FilePath   string `json:"filePath"`
	LineCount  int    `json:"lineCount"`
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
}

// SaveToFile saves an ABAP object's source code to a local file.
//
// Workflow: GetSource → WriteFile
//
// The file extension is automatically determined based on object type.
func (c *Client) SaveToFile(ctx context.Context, objType CreatableObjectType, objectName, parentName, outputPath string) (*SaveToFileResult, error) {
	result := &SaveToFileResult{
		ObjectName: objectName,
		ObjectType: string(objType),
	}

	// 1. Determine file extension
	var ext string
	switch objType {
	case ObjectTypeClass:
		ext = ".clas.abap"
	case ObjectTypeProgram:
		ext = ".prog.abap"
	case ObjectTypeInterface:
		ext = ".intf.abap"
	case ObjectTypeFunctionGroup:
		ext = ".fugr.abap"
	case ObjectTypeFunctionMod:
		ext = ".func.abap"
	case ObjectTypeInclude:
		ext = ".abap"
	// RAP object types (using ABAPGit-compatible extensions)
	case ObjectTypeDDLS:
		ext = ".ddls.asddls"
	case ObjectTypeBDEF:
		ext = ".bdef.asbdef"
	case ObjectTypeSRVD:
		ext = ".srvd.srvdsrv"
	default:
		ext = ".abap"
	}

	// 2. Build file path
	if outputPath == "" {
		outputPath = "."
	}
	if !strings.HasSuffix(outputPath, ext) {
		// outputPath is a directory
		objectName = strings.ToLower(objectName)
		// Replace namespace slashes with # for filesystem compatibility (abapGit convention)
		safeFileName := strings.ReplaceAll(objectName, "/", "#")
		result.FilePath = filepath.Join(outputPath, safeFileName+ext)
	} else {
		result.FilePath = outputPath
	}

	// 3. Get object source
	objectURL, err := c.buildObjectURLWithParent(objType, objectName, parentName)
	if err != nil {
		return nil, err
	}

	resp, err := c.transport.Request(ctx, objectURL+"/source/main", &RequestOptions{
		Method: "GET",
		Accept: "text/plain",
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to read object: %v", err)
		return result, nil
	}

	source := string(resp.Body)
	result.LineCount = len(strings.Split(source, "\n"))

	// 4. Write to file
	err = os.WriteFile(result.FilePath, []byte(source), 0644)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to write file: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Saved %s %s to %s (%d lines)", objType, objectName, result.FilePath, result.LineCount)
	return result, nil
}

// SaveClassIncludeToFile saves a class include's source code to a local file.
//
// Workflow: GetClassInclude → WriteFile
//
// The file extension is determined by the include type (abapGit-compatible):
//   - testclasses  → .clas.testclasses.abap
//   - definitions  → .clas.locals_def.abap
//   - implementations → .clas.locals_imp.abap
//   - macros       → .clas.macros.abap
//   - main         → .clas.abap
func (c *Client) SaveClassIncludeToFile(ctx context.Context, className string, includeType ClassIncludeType, outputPath string) (*SaveToFileResult, error) {
	result := &SaveToFileResult{
		ObjectName: className,
		ObjectType: fmt.Sprintf("CLAS.%s", includeType),
	}

	// 1. Determine file extension based on include type
	var ext string
	switch includeType {
	case ClassIncludeTestClasses:
		ext = ".clas.testclasses.abap"
	case ClassIncludeDefinitions:
		ext = ".clas.locals_def.abap"
	case ClassIncludeImplementations:
		ext = ".clas.locals_imp.abap"
	case ClassIncludeMacros:
		ext = ".clas.macros.abap"
	case ClassIncludeMain, "":
		ext = ".clas.abap"
	default:
		ext = ".clas.abap"
	}

	// 2. Build file path
	if outputPath == "" {
		outputPath = "."
	}
	if !strings.HasSuffix(outputPath, ext) {
		// outputPath is a directory
		className = strings.ToLower(className)
		// Replace namespace slashes with # for filesystem compatibility (abapGit convention)
		safeFileName := strings.ReplaceAll(className, "/", "#")
		result.FilePath = filepath.Join(outputPath, safeFileName+ext)
	} else {
		result.FilePath = outputPath
	}

	// 3. Get class include source
	source, err := c.GetClassInclude(ctx, className, includeType)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to read class include: %v", err)
		return result, nil
	}

	result.LineCount = len(strings.Split(source, "\n"))

	// 4. Write to file
	err = os.WriteFile(result.FilePath, []byte(source), 0644)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to write file: %v", err)
		return result, nil
	}

	result.Success = true
	result.Message = fmt.Sprintf("Saved %s %s.%s to %s (%d lines)", "CLAS", className, includeType, result.FilePath, result.LineCount)
	return result, nil
}
