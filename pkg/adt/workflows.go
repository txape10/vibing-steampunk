package adt

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// --- Workflow Tools ---
// These tools combine multiple operations into atomic workflows for simpler usage.

// WriteProgramResult represents the result of writing a program.
type WriteProgramResult struct {
	// Transport is the request the write went under, and TransportNote
	// says how it was chosen when the caller named none.
	Transport     string              `json:"transport,omitempty"`
	TransportNote string              `json:"transportNote,omitempty"`
	Success       bool                `json:"success"`
	ProgramName   string              `json:"programName"`
	ObjectURL     string              `json:"objectUrl"`
	SyntaxErrors  []SyntaxCheckResult `json:"syntaxErrors,omitempty"`
	Activation    *ActivationResult   `json:"activation,omitempty"`
	Message       string              `json:"message,omitempty"`
}

// WriteProgram performs Lock -> SyntaxCheck -> UpdateSource -> Unlock -> Activate workflow.
// This is a convenience method for updating existing programs.
func (c *Client) WriteProgram(ctx context.Context, programName string, source string, transport string) (*WriteProgramResult, error) {
	programName = strings.ToUpper(programName)
	objectURL := fmt.Sprintf("/sap/bc/adt/programs/programs/%s", url.PathEscape(programName))
	sourceURL := objectURL + "/source/main"

	// Unified mutation policy gate (op type + package + transport). The
	// returned context carries the mark that stops UpdateSource resolving
	// the same package again from inside the lock window (issue #91).
	ctx, err := c.gateAndMark(ctx, MutationContext{
		Op:        OpWorkflow,
		OpName:    "WriteProgram",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	result := &WriteProgramResult{
		ProgramName: programName,
		ObjectURL:   objectURL,
	}

	// Step 1: Syntax check before making changes
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
	result.SyntaxErrors = syntaxErrors // Include warnings if any

	// A transportable object with no request named: pick one the way the
	// editor would (PR #203). Runs before the lock — it is a stateless
	// request, and a stateless hop between LOCK and PUT retires the handle
	// (issue #91).
	trPlan := c.planTransport(ctx, transport, objectURL, "")

	// Step 2: Lock the object
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		result.Message = fmt.Sprintf("Failed to lock object: %v", err)
		return result, nil
	}

	// Ensure we unlock on any error. Tracked explicitly rather than keyed off
	// result.Success: activation can still fail after a successful Step 4
	// unlock below, and result.Success only flips to true at the very end —
	// without this flag the defer would fire a second, spurious UNLOCK on a
	// handle already released.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	// Adopt transport from lock result when caller did not supply one — the
	// object may already be captured in an open request (issue #144).
	// Otherwise the plan above (issue #91's #203 follow-on).
	effectiveTransport, trNote, err := c.resolveWriteTransportFor(trPlan, transport, lock.CorrNr, "WriteProgram")
	if err != nil {
		result.Message = fmt.Sprintf("Transportable-edit check failed: %v", err)
		return result, nil
	}
	result.Transport, result.TransportNote = effectiveTransport, trNote

	// Step 3: Update source
	err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, effectiveTransport)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update source: %v", err)
		return result, nil
	}

	// Step 4: Unlock before activation (SAP requirement)
	err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
	unlocked = true
	if err != nil {
		result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
		return result, nil
	}

	// Step 5: Activate
	activation, err := c.Activate(ctx, objectURL, programName)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to activate: %v", err)
		result.Activation = activation
		return result, nil
	}

	result.Activation = activation
	if activation.Success {
		result.Success = true
		result.Message = "Program updated and activated successfully"
	} else {
		result.Message = "Activation failed - check activation messages"
	}

	return result, nil
}

// WriteIncludeResult represents the result of writing an ABAP include.
type WriteIncludeResult struct {
	// Transport is the request the write went under, and TransportNote
	// says how it was chosen when the caller named none.
	Transport     string              `json:"transport,omitempty"`
	TransportNote string              `json:"transportNote,omitempty"`
	Success       bool                `json:"success"`
	IncludeName   string              `json:"includeName"`
	ObjectURL     string              `json:"objectUrl"`
	SyntaxErrors  []SyntaxCheckResult `json:"syntaxErrors,omitempty"`
	Activation    *ActivationResult   `json:"activation,omitempty"`
	Message       string              `json:"message,omitempty"`
}

// WriteInclude performs SyntaxCheck → Lock → UpdateSource → Unlock → Activate for an ABAP include.
// SyntaxCheck runs before Lock to avoid breaking the stateful SAP session (stateless hop between Lock and PUT).
// Only E/A/X severity blocks the write; warnings are reported but do not prevent saving.
func (c *Client) WriteInclude(ctx context.Context, includeName string, source string, transport string) (*WriteIncludeResult, error) {
	includeName = strings.ToUpper(includeName)
	objectURL := fmt.Sprintf("/sap/bc/adt/programs/includes/%s", url.PathEscape(strings.ToLower(includeName)))
	sourceURL := objectURL + "/source/main"

	// Unified mutation policy gate (op type + package + transport). The
	// returned context carries the mark that stops UpdateSource resolving
	// the same package again from inside the lock window (issue #91).
	ctx, err := c.gateAndMark(ctx, MutationContext{
		Op:        OpWorkflow,
		OpName:    "WriteInclude",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	result := &WriteIncludeResult{
		IncludeName: includeName,
		ObjectURL:   objectURL,
	}

	// Syntax check before Lock — stateless, must not run between Lock and PUT.
	syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
	if err != nil {
		result.Message = fmt.Sprintf("Syntax check failed: %v", err)
		return result, nil
	}
	for _, se := range syntaxErrors {
		if se.Severity == "E" || se.Severity == "A" || se.Severity == "X" {
			result.SyntaxErrors = syntaxErrors
			result.Message = "Source has syntax errors — not saved"
			return result, nil
		}
	}
	result.SyntaxErrors = syntaxErrors

	// A transportable object with no request named: pick one the way the
	// editor would (PR #203). Runs before the lock for the same reason the
	// syntax check does — stateless, must not sit between LOCK and PUT.
	trPlan := c.planTransport(ctx, transport, objectURL, "")

	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		result.Message = fmt.Sprintf("Failed to lock object: %v", err)
		return result, nil
	}
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	effectiveTransport, trNote, err := c.resolveWriteTransportFor(trPlan, transport, lock.CorrNr, "WriteInclude")
	if err != nil {
		result.Message = fmt.Sprintf("Transportable-edit check failed: %v", err)
		return result, nil
	}
	result.Transport, result.TransportNote = effectiveTransport, trNote

	if err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, effectiveTransport); err != nil {
		result.Message = fmt.Sprintf("Failed to update source: %v", err)
		return result, nil
	}

	if err = c.UnlockObject(ctx, objectURL, lock.LockHandle); err != nil {
		result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
		return result, nil
	}
	unlocked = true
	c.sourceCache.InvalidateByURL(objectURL)

	activation, err := c.Activate(ctx, objectURL, includeName)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to activate: %v", err)
		result.Activation = activation
		return result, nil
	}

	result.Activation = activation
	if activation.Success {
		result.Success = true
		result.Message = "Include updated and activated successfully"
	} else {
		result.Message = "Activation failed — check activation messages"
	}
	return result, nil
}

// WriteClassResult represents the result of writing a class.
type WriteClassResult struct {
	// Transport is the request the write went under, and TransportNote
	// says how it was chosen when the caller named none.
	Transport     string              `json:"transport,omitempty"`
	TransportNote string              `json:"transportNote,omitempty"`
	Success       bool                `json:"success"`
	ClassName     string              `json:"className"`
	ObjectURL     string              `json:"objectUrl"`
	SyntaxErrors  []SyntaxCheckResult `json:"syntaxErrors,omitempty"`
	Activation    *ActivationResult   `json:"activation,omitempty"`
	Message       string              `json:"message,omitempty"`
}

// WriteClass performs Lock -> SyntaxCheck -> UpdateSource -> Unlock -> Activate workflow for classes.
func (c *Client) WriteClass(ctx context.Context, className string, source string, transport string) (*WriteClassResult, error) {
	className = strings.ToUpper(className)
	objectURL := fmt.Sprintf("/sap/bc/adt/oo/classes/%s", url.PathEscape(className))
	sourceURL := objectURL + "/source/main"

	// Unified mutation policy gate (op type + package + transport). The
	// returned context carries the mark that stops UpdateSource resolving
	// the same package again from inside the lock window (issue #91).
	ctx, err := c.gateAndMark(ctx, MutationContext{
		Op:        OpWorkflow,
		OpName:    "WriteClass",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	result := &WriteClassResult{
		ClassName: className,
		ObjectURL: objectURL,
	}

	// Step 1: Syntax check
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

	// A transportable object with no request named: pick one the way the
	// editor would (PR #203). Runs before the lock — stateless, must not
	// sit between LOCK and PUT (issue #91).
	trPlan := c.planTransport(ctx, transport, objectURL, "")

	// Step 2: Lock
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		result.Message = fmt.Sprintf("Failed to lock object: %v", err)
		return result, nil
	}

	// Tracked explicitly rather than keyed off result.Success — see the
	// identical comment in WriteProgram above.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	// Adopt transport from lock result when caller did not supply one — the
	// object may already be captured in an open request (issue #144).
	// Otherwise the plan above (issue #91's #203 follow-on).
	effectiveTransport, trNote, err := c.resolveWriteTransportFor(trPlan, transport, lock.CorrNr, "WriteClass")
	if err != nil {
		result.Message = fmt.Sprintf("Transportable-edit check failed: %v", err)
		return result, nil
	}
	result.Transport, result.TransportNote = effectiveTransport, trNote

	// Step 3: Update source
	err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, effectiveTransport)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update source: %v", err)
		return result, nil
	}

	// Step 4: Unlock
	err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
	unlocked = true
	if err != nil {
		result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
		return result, nil
	}

	// Step 5: Activate
	activation, err := c.Activate(ctx, objectURL, className)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to activate: %v", err)
		result.Activation = activation
		return result, nil
	}

	result.Activation = activation
	if activation.Success {
		result.Success = true
		result.Message = "Class updated and activated successfully"
	} else {
		result.Message = "Activation failed - check activation messages"
	}

	return result, nil
}

// CreateProgramResult represents the result of creating a program.
type CreateProgramResult struct {
	// Transport is the request the write went under, and TransportNote
	// says how it was chosen when the caller named none.
	Transport     string              `json:"transport,omitempty"`
	TransportNote string              `json:"transportNote,omitempty"`
	Success       bool                `json:"success"`
	ProgramName   string              `json:"programName"`
	ObjectURL     string              `json:"objectUrl"`
	SyntaxErrors  []SyntaxCheckResult `json:"syntaxErrors,omitempty"`
	Activation    *ActivationResult   `json:"activation,omitempty"`
	Message       string              `json:"message,omitempty"`
}

// CreateAndActivateProgram creates a new program with source code and activates it.
// Workflow: CreateObject -> Lock -> UpdateSource -> Unlock -> Activate
func (c *Client) CreateAndActivateProgram(ctx context.Context, programName string, description string, packageName string, source string, transport string) (*CreateProgramResult, error) {
	programName = strings.ToUpper(programName)
	packageName = strings.ToUpper(packageName)

	// Unified mutation policy gate (op type + package + transport).
	// CreateObject below runs its own gate against packageName again before
	// asking SAP to create the object there — that second check is cheap
	// (no ObjectURL to resolve), so it is left in place rather than marked
	// away.
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpWorkflow,
		OpName:    "CreateAndActivateProgram",
		Package:   packageName,
		Transport: transport,
	}); err != nil {
		return nil, err
	}

	objectURL := fmt.Sprintf("/sap/bc/adt/programs/programs/%s", url.PathEscape(programName))
	sourceURL := objectURL + "/source/main"

	result := &CreateProgramResult{
		ProgramName: programName,
		ObjectURL:   objectURL,
	}

	// Step 1: Create the program
	var chosen TransportChoice
	err := c.CreateObject(ctx, CreateObjectOptions{
		ObjectType:  ObjectTypeProgram,
		Name:        programName,
		Description: description,
		PackageName: packageName,
		Transport:   transport,
		Chosen:      &chosen,
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create program: %v", err)
		return result, nil
	}
	if chosen.Transport != "" {
		transport = chosen.Transport
	}
	result.Transport, result.TransportNote = transport, chosen.Reason

	// The gate above accepted packageName, and CreateObject gated it a
	// second time before asking SAP to put the program there — so the
	// program's package is a package the whitelist allows. Record that for
	// the object, or UpdateSource resolves it again from inside the lock
	// (issue #91).
	ctx = withMutationPackageChecked(ctx, objectURL)

	// Step 2: Lock
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		result.Message = fmt.Sprintf("Failed to lock object: %v", err)
		return result, nil
	}

	// Tracked explicitly rather than keyed off result.Success — see the
	// identical comment in WriteProgram above.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	// Step 3: Update source
	err = c.UpdateSource(ctx, sourceURL, source, lock.LockHandle, transport)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update source: %v", err)
		return result, nil
	}

	// Step 4: Unlock
	err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
	unlocked = true
	if err != nil {
		result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
		return result, nil
	}

	// Step 5: Activate
	activation, err := c.Activate(ctx, objectURL, programName)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to activate: %v", err)
		result.Activation = activation
		return result, nil
	}

	result.Activation = activation
	if activation.Success {
		result.Success = true
		result.Message = "Program created and activated successfully"
	} else {
		result.Message = "Activation failed - check activation messages"
	}

	return result, nil
}

// CreateClassWithTestsResult represents the result of creating a class with unit tests.
type CreateClassWithTestsResult struct {
	Success        bool              `json:"success"`
	ClassName      string            `json:"className"`
	ObjectURL      string            `json:"objectUrl"`
	Activation     *ActivationResult `json:"activation,omitempty"`
	UnitTestResult *UnitTestResult   `json:"unitTestResult,omitempty"`
	Message        string            `json:"message,omitempty"`
}

// CreateClassWithTests creates a new class with unit tests and runs them.
// Workflow: CreateObject -> Lock -> UpdateSource -> CreateTestInclude -> UpdateClassInclude -> Unlock -> Activate -> RunUnitTests
func (c *Client) CreateClassWithTests(ctx context.Context, className string, description string, packageName string, classSource string, testSource string, transport string) (*CreateClassWithTestsResult, error) {
	className = strings.ToUpper(className)
	packageName = strings.ToUpper(packageName)

	// Unified mutation policy gate (op type + package + transport).
	// CreateObject below runs its own gate against packageName again before
	// asking SAP to create the class there.
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpWorkflow,
		OpName:    "CreateClassWithTests",
		Package:   packageName,
		Transport: transport,
	}); err != nil {
		return nil, err
	}

	objectURL := fmt.Sprintf("/sap/bc/adt/oo/classes/%s", url.PathEscape(className))
	sourceURL := objectURL + "/source/main"

	result := &CreateClassWithTestsResult{
		ClassName: className,
		ObjectURL: objectURL,
	}

	// Step 1: Create the class
	err := c.CreateObject(ctx, CreateObjectOptions{
		ObjectType:  ObjectTypeClass,
		Name:        className,
		Description: description,
		PackageName: packageName,
		Transport:   transport,
	})
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create class: %v", err)
		return result, nil
	}

	// Same reasoning as CreateAndActivateProgram: packageName passed the
	// gate twice and the class was created there, so the three mutators
	// that run under the single lock below (UpdateSource, CreateTestInclude,
	// UpdateClassInclude — all of which resolve to this class URL) need not
	// each resolve the package again mid-window (issue #91).
	ctx = withMutationPackageChecked(ctx, objectURL)

	// Step 2: Lock
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		result.Message = fmt.Sprintf("Failed to lock object: %v", err)
		return result, nil
	}

	// Tracked explicitly rather than keyed off result.Success — see the
	// identical comment in WriteProgram above.
	unlocked := false
	defer func() {
		if !unlocked {
			if unlockErr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); unlockErr != nil {
				result.Message = fmt.Sprintf("%s — %s", result.Message, strandedLockAdvice(objectURL, unlockErr))
			}
		}
	}()

	// Step 3: Update main source
	err = c.UpdateSource(ctx, sourceURL, classSource, lock.LockHandle, transport)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update class source: %v", err)
		return result, nil
	}

	// Step 4: Create test include
	err = c.CreateTestInclude(ctx, className, lock.LockHandle, transport)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to create test include: %v", err)
		return result, nil
	}

	// Step 5: Update test include
	err = c.UpdateClassInclude(ctx, className, ClassIncludeTestClasses, testSource, lock.LockHandle, transport)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to update test source: %v", err)
		return result, nil
	}

	// Step 6: Unlock
	err = c.UnlockObject(ctx, objectURL, lock.LockHandle)
	unlocked = true
	if err != nil {
		result.Message = fmt.Sprintf("Failed to unlock object: %v", err)
		return result, nil
	}

	// Step 7: Activate
	activation, err := c.Activate(ctx, objectURL, className)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to activate: %v", err)
		result.Activation = activation
		return result, nil
	}
	result.Activation = activation

	if !activation.Success {
		result.Message = "Activation failed - check activation messages"
		return result, nil
	}

	// Step 8: Run unit tests
	flags := DefaultUnitTestFlags()
	testResult, err := c.RunUnitTests(ctx, objectURL, &flags)
	if err != nil {
		result.Message = fmt.Sprintf("Class activated but unit tests failed to run: %v", err)
		result.Success = true // Class was created successfully
		return result, nil
	}

	result.UnitTestResult = testResult
	result.Success = true
	result.Message = "Class created, activated, and unit tests executed successfully"

	return result, nil
}
