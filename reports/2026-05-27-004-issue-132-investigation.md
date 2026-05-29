# Issue #132 Investigation — Transport-Owned Objects Lock Failure

**Date:** 2026-05-27  
**Status:** Root cause identified, no fix yet. Workaround documented.  
**Related:** Issue #132 (oisee/vibing-steampunk)

---

## Summary

Editing ABAP objects that are already assigned to an active CTS transport request on on-prem S/4HANA fails with `423 ExceptionResourceInvalidLockHandle`. The proposed simple fix (`&& corrNr == ""` guard in `LockObject`) is correct but not sufficient.

---

## What Was Tested

**System:** on-prem S/4HANA 758  
**Object:** `ZCL_VSP_APC_HANDLER` in transport order TR-EXAMPLE (task: subtask)  
**Reference tool:** `mcp-abap-abap-adt-api` (Node.js, uses `abap-adt-api` library) — works correctly

---

## Root Cause

When an ABAP object is already in a user's active CTS transport, SAP ADT returns a special lock response:
- `modificationSupport = "NoModification"`
- `corrNr = <transport order number>`
- `lockHandle = <some handle>`

This handle is **not a real ENQUEUE lock** — SAP did not create a session-bound lock. The handle is a pseudo/informational value. Any subsequent `PUT .../source/main?lockHandle=<that handle>` is rejected with:

```
423 ExceptionResourceInvalidLockHandle:
Resource CLASS ZCL_VSP_APC_HANDLER is not locked (invalid lock handle: <handle>)
```

Three distinct approaches were tested and all failed:
1. **Use pseudo-handle as-is** → 423 (handle not in SAP ENQUEUE table)
2. **Re-lock with corrNr param** (`POST .../lock?corrNr=<order>`) → returns a new handle, also invalid for PUT
3. **Omit lock handle entirely, only pass corrNr** → 423 "invalid lock handle: (empty)"

---

## Why mcp-abap-abap-adt-api Works

The Node.js `abap-adt-api` library does not check `modificationSupport` and proceeds with the lock handle as returned. Despite receiving the same NoModification response, the write succeeds.

**Hypothesis (unconfirmed):** The Node.js library may be handling HTTP session/cookie affinity differently, or SAP's behavior differs based on subtle differences in the HTTP request (headers, session type). Needs instrumentation to confirm.

**Confirmed working pattern with mcp-abap:**
1. `lock` → get lockHandle (ignoring NoModification)
2. `setObjectSource` with lockHandle + transport = **ORDER number** (not task number)

**Key finding — ORDER vs TASK:**
- SAP internally assigns objects to tasks (sub-orders)
- `corrNr` in the lock response may be the task number, not the order
- The `setObjectSource` `transport` parameter needs the **ORDER** (parent), not the task
- Use `transportInfo` to get the correct ORDER number (`TRKORR`) before writing — do not guess or retry

---

## Current State of Working Tree

The guard fix is in place and correct:

```go
// pkg/adt/crud.go — LockObject
if accessMode == "MODIFY" &&
    strings.EqualFold(result.ModificationSupport, "NoModification") &&
    result.CorrNr == "" {  // <-- only reject if genuinely read-only (no transport)
    return nil, fmt.Errorf("object %s is not modifiable...")
}
```

The re-lock block (second `POST .../lock?corrNr=...`) was added and tested but **does not solve the problem**. It remains in the code but needs to be removed — it adds complexity with no benefit.

Also present (correct, should stay):
- `TestLockObject_AllowsNoModificationWithExistingTransport` test in `crud_reconcile_test.go`
- Transport fallback in `workflows_edit.go` and `workflows_source.go`: `if writeTransport == "" && lockResult.CorrNr != ""` adopts corrNr as transport

---

## What to Try Next

The real fix likely requires understanding why `abap-adt-api` (Node.js) succeeds. Recommended investigation steps:

1. **Intercept HTTP traffic from mcp-abap-abap-adt-api** using a proxy (Fiddler/mitmproxy). Capture:
   - Exact lock request headers (especially `X-sap-adt-sessiontype`, session cookies)
   - Lock response (does it get NoModification? what's the handle?)
   - Write request headers (same session cookie as lock?)
   - Write response

2. **Session affinity hypothesis:** Check if vsp's `Stateful: true` is using the same HTTP session for lock and write. If each stateful request creates a new SAP session instead of reusing the existing one, the ENQUEUE lock from the lock request is invisible to the write request.
   - Look at `pkg/adt/http.go` cookie jar behavior — does it persist cookies from lock response to write request?

3. **Try writing without SAP session header:** Test `PUT .../source/main?lockHandle=X&corrNr=ORDER` with `Stateful: false` on the write — maybe transport-owned objects work in stateless mode.

4. **Compare lock request format** with Node.js library. Check if it sends `corrNr` in the lock URL or any other parameter we don't.

---

## Workaround

Use `mcp-abap-abap-adt-api` for editing objects with active transports.

**Workflow:**
```
1. SAP(action="read")                          ← vsp (low tokens, deps included)
2. mcp-abap: searchObject → get URI
3. mcp-abap: transportInfo → get ORDER number
4. mcp-abap: lock → get lockHandle
5. mcp-abap: setObjectSource (transport=ORDER)
6. mcp-abap: activateByName or activateObjects
7. mcp-abap: unLock
```

---

## Files Modified in This Investigation

| File | Change | Status |
|---|---|---|
| `pkg/adt/crud.go` | Guard `&& corrNr == ""`; re-lock block (should be removed) | Uncommitted |
| `pkg/adt/crud_reconcile_test.go` | New test `TestLockObject_AllowsNoModificationWithExistingTransport` | Uncommitted |
| `pkg/adt/workflows_edit.go` | Transport fallback, removed `isTransportOwned`/`LockDebug` | Uncommitted |
| `pkg/adt/workflows_source.go` | Transport fallback, removed debug logging + `isTransportOwned` | Uncommitted |
| `CLAUDE.md` | Notes on lock behavior (may include session-relevant findings) | Uncommitted |
