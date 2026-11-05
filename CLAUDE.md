# CLAUDE.md

**vsp** — Go-native MCP server and CLI for SAP ABAP Development Tools (ADT).

> **Doc intent:** CLAUDE.md = dev context. README.md = user onboarding. reports/ = research/history. contexts/ = session handoff.

---

## Current Priorities

> **Archived history (2026-11-05):** entries "1." through "2av." (2026-05-27 → 2026-10-16) — all
> closed, verified bugs/features more than three weeks old — were moved verbatim to
> [CLAUDE-HISTORY.md](CLAUDE-HISTORY.md). This section continues with the recent/active work below.

### 2aw. Upstream wave 1 — eleven small ports (2026-10-16)
- Source: `docs/upstream-review-2026-10-priority-list.md` (gitignored). Commit `0d9c2e2`. Each item was adapted by hand (upstream refactored; nothing applies verbatim).
- **#257** `CheckRedirect` (`pkg/adt/config.go`): `keepsSAPCredentials` — Authorization, CSRF and session-type headers are dropped on a redirect to another host or an https→http downgrade.
- **#258** `rememberSession` (`pkg/adt/http.go`) saves CSRF and session id after a retry too; **#362** a 403 on a write keeps SAP's own body in the error.
- **#305** `detectObjectTypeFromContent` ends the `ParseABAPFile`↔`parseFromContent` recursion for generic `.abap` files. **#338** `packageExists` uses `errors.As` instead of matching "404" in the URL.
- **#307** `CreateTable` adds `key client` only when the first key is not already a client field; `client_dependent:false` for client-independent tables; a first key named like a client field with a built-in type is refused with an explanation; the table activation is checked. **Behaviour change**: tables whose first key is a client field no longer get a second one.
- **#264/#227** `ExecuteABAPResult.Failure` when ABAP Unit ran nothing; cleanup runs on `failureCleanupContext` (detached) and `CleanedUp` is true only if the DELETE was confirmed.
- **#164** `all_rows` (MCP) / `--top 0` (CLI) via `pkg/adt/limits.go` (`UnlimitedRows = 90000`). **#334** `InstallAbapGit`/`InstallDummyTest` stubs removed.
- **#208** not applied to K/W: requestStatus/requestType explicit (see 2ax).
- Tests per item (`redirect_credentials_test.go`, `http_retry_session_test.go`, `fileparser_generic_test.go`, `package_exists_test.go`, `workflows_execute_cleanup_test.go`, `table_client_test.go`, `limits_test.go`, `handlers_read_rows_test.go`, `handlers_install_stubs_test.go`).
- **Verified live (2026-10-16)**: #307 with and without `client_dependent:false` (DD03L: `CLIENT` key added / absent), `execute_abap` runs and cleans up, `all_rows` accepted on dev and read-only prod. **Not verified live**: #257 (needs `VSP_HTTP_TRACE=1` in the server's environment), the `Failure` path of #264 (only unit tests), `CreateTable` failing on a bad field list leaves an empty table shell (pre-existing).

### 2ax. Transports of copies and honest release (2026-10-16)
- Commit `a1d0c0f`. The user works daily with transports of copies (E070-TRFUNCTION `T`). `GetUserTransports`/`ListTransports` now send `requestType=KWT` and `requestStatus=D` explicitly and list copies in their own section ("Transports of Copies", `UserTransports.TransportOfCopies`); the SQL fallback reads `K/W/T` and descriptions in the session language (`e07tLanguage`). `E070/E071/E07T` removed from the response-cache `stableTables` (an open request changes under you).
- **Release**: ADT answers HTTP 200 whatever happens; the outcome is `chkrun:status` (`released`, `relwithignlock`…). `parseReleaseResult`/`releaseOutcome` report it; a refused release is an error with SAP's own messages. Copies always need `ignore_locks` (their objects stay locked in the original request) → `relwithignlock` with a `tm:root` document. `ReleaseTransportV2` refuses `IgnoreLocks && SkipATC`.
- **Parked: #247 `copy_to_toc`** — `TR_COPY_COMM` is not RFC-enabled (TFDIR FMODE empty) and `TRINT_COPY_COMM` does `ROLLBACK WORK` (a dump in APC).
- **Verified live**: a copy (status D, target S4Q) appears under "Transports of Copies" together with the older ones. **Not verified**: release of a copy (needs one the user releases anyway); the raw XML of a copy was not captured, the parser routes on `tm:type="T"`.
- Tests: `transport_copies_test.go`, `transport_release_test.go`.

### 2ay. Upstream #183 -- `lock_handle` optional on `UpdateSource`/`CreateTestInclude`/`UpdateClassInclude`; `LockObject`/`UnlockObject` out of focused mode (2026-10-19)
- Commit `f4168a3`. A lock handle cannot cross two MCP tool calls (issue #169: each call is its own server-side session), so the handle-passing flow was a trap. Without `lock_handle` the three tools now lock, write and unlock in one session through the new `Client.WithObjectLock(ctx, opName, lockURL, transport, fn)` (`pkg/adt/lock_scope.go`). A handle the caller does pass is respected as given (decision: follow upstream); `DeleteObject` keeps ignoring it (2ae).
- Order inside `WithObjectLock`, nothing stateless between LOCK and the write that consumes the handle: `gateAndMark` above the LOCK -> `planTransport` (a failed plan is returned, golden rule: SAP never autogenerates a request) -> `LockObject` with the planned corrNr -> deferred release on a detached context (`releaseLockAfterFailure` + `strandedLockAdvice`) -> `resolveWriteTransportFor` -> `fn` -> source cache invalidated -> `UnlockObject`. A failed unlock after a good write wraps the sentinel `ErrLockReleaseFailed`.
- `LockObject`/`UnlockObject` left the focused whitelist (still expert). A reviewer HIGH (no `planTransport` first) was fixed before the commit.
- Not fixed (LOW): the gate op of `CreateTestInclude` is `OpUpdate`, not `OpCreate`; the happy-path unlock uses the caller's context.
- Tests: `pkg/adt/lock_scope_test.go`, `internal/mcp/lock_scope_test.go`.
- **Verified live (2026-10-19)**: `edit UPDATE_SOURCE` on `ZTESTRCG1` with no `lock_handle` wrote the change (read back), reverted with `EDITSOURCE` and activated; `ENQUEUE_READ` shows no enqueue of the dev user afterwards. Not exercised: a transportable object (planTransport + corrNr on the LOCK) and an explicit `lock_handle`.

### 2az. Upstream #288 -- read-only invariant test, `DeployZip` gate/transport/lock scope (2026-10-19)
- Commit `cf74c5e`. New `internal/mcp/readonly_invariant_test.go`: every registered tool must be classified (read or write), and every write tool must be refused under `SAP_READ_ONLY` with "blocked by safety configuration" before any request leaves. **Adding a tool without classifying it now fails the suite.** Building it exposed two real holes, closed here: `InstallZADTVSP` and `RecoverFailedCreate` (now gates `OpDelete`) had no gate.
- `DeployZip` (`handlers_deploy.go`): the safety gate and the transport rules run before the ZIP is read (non-dry-run). **Behaviour change**: a non-`$` package now requires an explicit `transport` (it is refused for a local package); a dry run needs neither. Phase 2 uses `WithObjectLock`; upload warnings are collected (`uploadWarnings`).
- Tests: `handlers_deploy_test.go` (embedded ZIPs are 0 bytes, so the checks sit before parsing to be testable). Mutation-checked.
- **Verified live (2026-10-19)**: `deploy_zip` on dev without `transport` for `ZDEMO_PKG` -> "transport is required"; `$ZGIT` with a `transport` -> "not allowed for a local package"; on the read-only production connection -> "blocked by safety configuration". **Not verifiable**: the deploy phases themselves -- the embedded ZIPs are empty placeholders in this build (`dry_run` fails with "not a valid zip file"), so `WithObjectLock` in phase 2 is covered by unit tests only.

### 2ba. Upstream #251 -- concurrent callers of one client no longer break each other's lock chains (2026-10-19)
- Commit `296848a`. Two things end the stateful ADT context a lock handle is bound to, and both are the ordinary work of another caller sharing the client: a stateless request arriving in that context (the jar puts `sap-contextid` on every request), and a second concurrent request into a busy context (ICM answers 400, the session recovery then drops the cookies and orphans the context together with its enqueue).
- `Transport.do` (`pkg/adt/http_do.go`): while `lockWindow.present()` or a stateful request is in flight, a stateless request is isolated -- it goes without `sap-contextid`, through a jar-less client, and its response cookies are not learned. Requests into the context (stateful or unmarked, e.g. the CSRF probe) go one at a time through `contextGate` (`context_gate.go`, a FIFO readers-writer gate: stateless requests that may end the context hold it shared, so a LOCK cannot open a window in the context they are ending). `jarMu` guards the jar against `clearSAPSessionCookies`. The session-error recovery uses a 30-minute `outstanding()` check, not the 12-hour record, so a lost handle cannot disable recovery for a day. The response-cache wrapper is unchanged (a cache hit never reaches `do`).
- **Behaviour to remember**: a long stateful request (`DebuggerListen`, up to its timeout) holds the gate for as long and every other stateful or unmarked request on the same client queues behind it; before this the ICM answered those with 400.
- Tests: `lock_context_test.go`, `lock_context_extra_test.go`; mutation-checked. Reviewer MEDIUMs (jar race, stale record, Listen serialization) fixed or documented in the `do` comment.
- **Verified live (2026-10-19)** with a throwaway Go program (not committed) against the real system: LOCK on `ZTESTRCG1`, a concurrent `SearchObject` from another goroutine, then the write. Code before the commit: write 423 `ExceptionResourceInvalidLockHandle`. Code with the commit: write and unlock fine, activation fine. `EDITSOURCE` through the deployed MCP (change and revert) fine; `ENQUEUE_READ` shows no enqueue of the dev user afterwards. `ZTESTRCG1` ends identical to how it started.

### 2bb. Upstream #271 (adapted, "option B") -- a wordless activation refusal is no longer a success, and pending parts are named, never activated (2026-10-19)
- Before: S/4HANA answers `<chkl:properties activationExecuted="false"/>` with no messages when the object has nothing to activate *or* when its parts are the inactive ones. `parseActivationResult` read that as `Success=true`, so writing an include of a function group and activating the group "succeeded" while the include stayed inactive.
- Now: that checklist is `silentRefusal` (`Success=false`). `Activate` then runs `explainSilentRefusal`, which only **reads** `GetInactiveObjects`: nothing inactive at or below the object -> success with the info message "Nothing to activate"; inactive parts below it (any user, shown with their user) -> `Success=false`, `Inactive` filled, a W message naming them and pointing at `ACTIVATE_MULTI`; the object itself inactive but refused -> failure; list unreadable -> failure with that reason. `ActivateMultiple` with two or more objects never consults the list (no recursion with `Activate`); it only adds a W message. `ActivatePackage` marks every object `Failed` when SAP refused without naming any. `/sap/bc/adt/activation/` joins `neverCacheablePrefixes`. `activationMessages` names inactive parts when there is no text; `CreateTable` uses it.
- **Deliberately not upstream's behaviour**: upstream (#271) retries activating the group together with its own inactive parts. That would activate includes the user left pending on purpose (golden rule: only activate what was modified in this session), including from `SetDescription`/`EditSource`. Option D (activate only what this process wrote) was done afterwards -- see 2be.
- **Behaviour change**: a checklist without `<properties>` and with only W/I messages is now a success (before, any message made it a failure).
- **Verified live (2026-10-19)** with a throwaway Go program and a scratch function group `ZVSP_TST_ACTP` in `$TMP`: `Activate` on an active program -> success "Nothing to activate"; after `UPDATE_SOURCE` of the group's TOP include (left inactive) `Activate` on the group -> `success=false`, the include named with its user, one activation POST, include still inactive; `ACTIVATE_MULTI` of group + include -> success; `Activate` again -> "Nothing to activate". The inactive list holds only the dev user's objects (56 entries, no other user's) and **lists each object twice** (once per holder), so `inactivePartsOf` de-duplicates by URI. Not verified: URI shape of class test includes / text pools. Callers that discard `Activate`'s result (`RenameObject`, creators in `crud.go`) do not see the verdict.
- Tests: `pkg/adt/activation_parts_test.go` (mutation-checked), `response_cache_test.go`. Code review: APPROVE, 0 CRITICAL/HIGH; the `ActivatePackage` MEDIUM was fixed.

### 2bc. Upstream #267 (adapted) -- ANSI-SQL rewrites and explained 400s in `RunQuery` (2026-10-19)
- The freestyle endpoint takes ABAP SQL only and the model writes ANSI (`alias.col`, `DESC`, trailing `;`). `RunQuery` now runs `normalizeOpenSQL` (`pkg/adt/query_sql.go`) **before** `wrapFreestyleSQL` (2av; `DESC`->`DESCENDING` lengthens lines): `alias.col`->`alias~col` (only qualifiers that are tables/aliases of FROM/JOIN), `DESC`/`ASC` after a sort expression (scan bounded to the ORDER BY clause: stops at `)`, UNION, UP TO, OFFSET; skipped right after BY or a comma, where it is a column), and the closing `.`/`;` dropped. Nothing to rewrite -> the query is returned byte for byte; idempotent. What was rewritten comes back in `TableContentsResult.Notes`.
- Literals (`'..'`, backtick, `|..|`) and comments (`"`, `*` in column 1) are masked per line by `maskFreestyleSQL` (length- and line-preserving). **Deviation from upstream**: its `maskLiterals` only knew `'..'` and an odd quote in a comment desynchronised the rest of the text.
- A 400 becomes `*QueryError` (`Message`, `Hints`, `Notes`; `Unwrap` -> `*APIError`): SAP's message without the XML, a list of the table's real columns plus the closest name for an unknown column, a check against DD02L for a missing table/view (T100 `ADT_DATAPREVIEW_MSG` 022, V1 = name; EN "Cannot find"), and hints for client fields in WHERE, LRAW/LCHR fields and invalid literals. Hints only on `RunQuery`'s error path; `GetTableContents` filters are **not** normalized; no LIMIT translation. A failed hint lookup never hides SAP's message; non-400 errors pass through unchanged.
- **Behaviour change**: the error text is now `SAP refused the query (HTTP 400): <msg>` + `vsp rewrote:`/`hint:` lines instead of `running query: ADT API error: status 400 at <path>: <xml>`.
- **Captured live (ES logon)**: parser messages stay in English; "No existe X" (key 022) and "Solo está permitida una instrucción SELECT" (key 005) are localized -> hints key on the T100 number, not the text.
- Tests: `pkg/adt/query_sql_test.go` (table incl. comments, CRLF, multibyte, namespaces, idempotence, normalize-then-wrap length, wire tests with captured ES XML); mutation-checked. Code review: APPROVE, 0 CRITICAL/HIGH; the two MEDIUMs (ORDER BY scope in subqueries/UNION, HTTP status lost in the text) fixed. Not fixed (LOW): aliases without `AS` are not rewritten (ABAP SQL needs `AS` anyway); `missingSourceHint` cuts the prefix by bytes.
- **Verified live (2026-10-19)** through the deployed binary, dev and read-only prod: E070/E07T join with `e.trkorr ... ORDER BY ... DESC, ... ASC;` runs on both and returns the three `Notes` (qualifiers, DESC/ASC, closing period); unknown column lists the real columns and the closest name (dev `T001`, prod `T000`); missing table in ES gives the DD02L hint; `MANDT` in WHERE and `EDID4-SDATA` (LRAW) get their hints; a 60-element `IN` list without blanks (over 250 characters) runs (wrapped by 2av). Not exercised live: the exact-250-rune last line margin.
- Files: `pkg/adt/query_sql.go` (+`_test.go`), `pkg/adt/client.go`, `internal/mcp/tools_register.go`, `internal/mcp/handlers_help.go`.

### 2bd. Upstream #376 (adapted) -- ATRA traces: typed list, sorted/cut hit list, dbAccesses, explained errors (2026-10-19)
- Supersedes the "`statements`/`dbAccesses` intentionally unimplemented" note of 2o: `dbAccesses` and `statements` (the call tree) are parsed now. The call tree needs a non-aggregated trace; it was captured live in a second step (see below) and is parsed from that real shape.
- `ListTraces`: Atom feed with `trc:extendedData` -> `ABAPTrace` (bare `id`, `uri`, state R/S + text, `complete`, aggregation, size KB, runtimes). The feed comes oldest first and ignores `$top`, so vsp sorts newest first and caps client-side. `GetTrace`/`GetTraceAnalysis` parse by root element (`hitlist`, `dbAccesses`; unknown root -> `Note`), take `sort_by` (net|gross|calls|none), `top` (default 50) and report `totalEntries`/`truncated`/`sortedBy`; `raw=true` returns ADT's XML cut at 256 KB. `NormalizeTraceID` accepts id, ADT path or `adt://` link and whitelists letters/digits.
- Errors: only 416/400 are blamed on an incomplete recording (and only when the list entry says so); the aggregated case (`invalidRequestForAggregatedTraces`) is checked first and tells to use hitlist; 401/403/404/5xx pass through untouched. For a cut-off trace the advice depends on the evaluation: hitlist/dbAccesses say "Hit List aggregation, or a larger maximum file size"; `statements` says to record again WITHOUT aggregation with a larger file size or a shorter program (aggregating is no way out for a call tree). Unit-tested, mutation-checked. **Verified live (2026-10-19, S4D)** on a real size-violation trace (limit 2048 KB exceeded, SAT message 405): hitlist and statements answer 416 (`SY/530`), dbAccesses answers 400 (`SADT_REST/006`); all three give the explained error (the live run was before this advice tweak).
- `GetTraceInfo` reads the trace's own list entry. **Found live**: the single-entry resource answers 400 "Accept header invalid" to `application/atom+xml;type=entry`; it now sends `application/atom+xml` and falls back to looking the entry up in the list. A failure is reported in `note` (never silent). Raw `dbAccesses` of an aggregated trace carries `totalDbTime="0"`; the DB time comes from the list entry.
- `TraceExecution`: an unreadable trace, or a `Note` with no entries, goes to the new `Unsearched` (`pkg/adt/unsearched.go`) instead of comparing nothing; `ExtractCallEdgesFromTrace` uses calling/called programs (any `NAME=====xxx` context is a class).
- Sanitized in the same commit: the old fixture with a real host/user was replaced by upstream's sanitized ones (`pkg/adt/testdata/abaptraces-*.v758.xml`, SID DEV, TESTUSER); `trace_test.go` keeps only the ST05 tests. `traces_test.go` (new) uses an httptest stub; mutation-checked (newest-first sort, 403 not blamed on the recording, Unsearched append).
- Code review: APPROVE, 0 CRITICAL/HIGH. Fixed: MEDIUM-1 (`GetTraceInfo` returned an empty entry for a non-entry answer), MEDIUM-2 (class include contexts), LOW-1 (check order). Not fixed (LOW): UTF-8 cut of `raw` by bytes, namespaced object URIs not encoded, silent `NormalizeTraceID` error in `toTrace`. No unit test for the `GetTraceInfo` list fallback.
- **Verified live (2026-10-19, S4D)**: `list_traces` (3 traces, newest first), `get_trace` hitlist with `sort_by`/`top`, `dbAccesses`, `raw=true`, `statements` on an aggregated trace -> explained error, and `trace`/`totalTime` filled after the Accept fix. **Not verified**: size-violation (416) trace (none exists on the system), prod (read-only prod has no traces: `list_traces` answers `[]`).
- **Call tree captured live (2026-10-19, S4D)** on a trace recorded with Aggregation = None (SAT; `DEFAULT` is another user's variant and cannot be changed, so the user copied it): root `trc:statements` (attribute `m:count`), a **flat** list of `trc:statement` (`index`, `callLevel` = depth, `text`, `variable`, `hitlistAnchor`, `dbAccessAnchor` on DB events) each with `callingProgram`, optional `calledProgram`, `grossTime`, `traceEventNetTime`, `proceduralNetTime` (`time=-1` = n/a). Mapped to `TraceEntry` (`Variable` is new). **The answer is declared UTF-8 but carries Latin-1 bytes in `componentDescription`**, which `encoding/xml` refuses: `parseTraceAnalysis` replaces invalid bytes first (mutation-checked: without it the test fails with "invalid UTF-8"). The raw answer was 344 KB for 119 KB of trace, so `raw` is cut at 256 KB. Fixture is a trimmed synthetic sample inside `traces_test.go`; **verified live through the parsed path** (`get_trace statements top=5` on the unaggregated trace: 430 entries, `callLevel`/`variable` filled, trace card and `totalTime` present). Reviewer notes kept: the default `top=50` cuts a call tree to its first events (documented in the help text), `sort_by` on a tree breaks the `callLevel` order, a missing `callLevel` means 0.
- Files: `pkg/adt/client.go`, `pkg/adt/unsearched.go`, `pkg/adt/traces_test.go`, `pkg/adt/trace_test.go`, `pkg/adt/testdata/`, `internal/mcp/handlers_traces.go`, `handlers_analysis.go`, `tools_register.go`, `handlers_help.go`.

### 2be. Upstream #271, option D -- a refused activation now takes along the parts THIS process wrote (2026-10-20)
- Follows 2bb (option B: name the pending parts, never activate them). Option D activates, but only what this vsp process wrote and nobody has touched since, so the golden rule (only activate what was modified in this session) holds. Anything in doubt stays as in option B: named, not activated.
- **Registry** (`pkg/adt/written_parts.go`): `noteSourceWritten` runs after every accepted PUT in `UpdateSource` and `UpdateClassInclude` (these two cover EDITSOURCE, WriteSource, UPDATE_SOURCE, deploy, rename). It keeps, per normalized URI (`normalizeActivationURI`: lower case, no query, no `/source/main`, `%2f` decoded), the URL written to and `SourceHash` of what was sent. Other PUTs (text pool, description, MSAG, DTEL labels, `writeXMLObject`) are not recorded: they activate themselves. It lives only as long as `vsp.exe` (no expiry -- user decision); after a restart or in a one-shot CLI call the registry is empty and vsp behaves as option B.
- **Rule** (`explainSilentRefusal` -> `activateOwnParts`): when SAP refuses wordlessly and parts are pending, ALL of them must be ours: in the registry, listed under the session user, and the hash of their INACTIVE version (`GET <written url>?version=inactive`, read fresh) equal to the hash sent. One part that is not provably ours means nothing is activated and every part is named (all or nothing: an own part may depend on a foreign pending one). If the object itself is pending too (SAP lists the parent of a changed include as inactive), it must also be ours: written here with a matching hash, or its own `source/main` inactive version is identical to the active one (`parentIsOurs`), i.e. it is pending only because of the part. Reads that fail count as "not ours". If all hold, one `ActivateMultiple(object + parts)` goes out (it never consults the list, so no recursion); the result carries an I message "Activated together with the parts written by this session"; activated parts leave the registry.
- **Known limits (second review, APPROVE, 0 CRITICAL/HIGH)**: (1) the "parent has no pending change" exception compares only `source/main`, so it is looser than the letter of "only what this process wrote": a foreign pending change of the parent that is not in its main source (class description, metadata) would go live with the batch; strict for the parts, laxer for the parent. (2) TOCTOU: the batch activates whatever inactive version exists at that instant, ADT cannot activate by hash. (3) Only `UpdateSource`/`UpdateClassInclude` register writes; text pool, description, MSAG, DTEL labels and creation-with-source never do, so those parts are only ever named. (4) A source SAP normalizes beyond CRLF/trailing newline will not match its hash: fails closed. The registry entry of the parent is forgotten with its parts.
- **Off switch**: `SAP_ACTIVATE_OWN_PARTS=false` (also `0`/`no`/`off`) restores option B; read on every call.
- **Behaviour to remember**: callers that discard `Activate`'s result (`RenameObject`, the creators in `crud.go`) can now activate own parts without anyone reading the I message. TOCTOU between the hash check and the batch POST is small and inherent (SAP keeps inactive versions per user; only the same user editing from Eclipse/SAPGUI could slip in).
- **Findings that shaped it**: (1) `?version=inactive` and `?version=active` exist on ADT include and `source/main` resources of S4D and return different content when a source is pending (verified live: 54 vs 131 bytes). (2) Changing an include of an ACTIVE function group also puts the group itself in the inactive list, so a strict "parent must be ours" rule made the feature inert in real use (found only by the live run, after the reviewer's HIGH asked for the parent check); the unchanged-source criterion fixes it. (3) `explainSilentRefusal` gained the `objectName` parameter.
- **Tests**: `pkg/adt/written_parts_test.go`; mutation-checked (hash compare, user compare, not calling it, `forgetWritten`, part without a record, parent always accepted, parent not checked). A test for "inactive version never served from the response cache" was dropped as vacuous (the refused activation POST empties the cache first); `InvalidateCache` stays as a commented belt-and-braces call.
- **Verified live (2026-10-20, S4D)** with a throwaway Go program and a scratch function group `ZVSP_TST_ACTP` in `$TMP`: (A) include pending from another process -> only named; (B) written here, then `Activate(group)` -> group and include go live together, nothing pending afterwards; (C) written here, changed by a second client, then `Activate` -> only named; (D) `SAP_ACTIVATE_OWN_PARTS=false` -> only named. **Classes, verified live the same day** (scratch class `ZCL_VSP_TST_ACTP` in `$TMP`, includes `definitions`/`implementations`/`testclasses` written through `UpdateClassInclude`): SAP does NOT refuse a class activation silently -- `Activate(class)` answers success and activates the class together with ALL its pending includes, whoever wrote them (a definitions include written by another client went live with it). So option D never triggers for classes; it is for function groups, where the includes are separate objects. This is SAP's own semantics (a class's includes are part of the class), not something vsp decides, and ADT offers no way to activate a class without them. Observed list shapes: includes are listed as `/oo/classes/<c>/includes/<definitions|implementations|testclasses>` (same URL they are written to), the class itself as `/oo/classes/<c>/source/main#type=CLAS%2FOSU;name=<C>`. **Not verified live**: text pools, namespaced objects (`%2f`; no usable namespace on this system -- only SAP's own and `/0CUST/`; unit-tested normalization only).
- Files: `pkg/adt/written_parts.go` (+`_test.go`, new), `pkg/adt/devtools.go`, `pkg/adt/crud.go`, `pkg/adt/client.go`.

### 2bf. Upstream #211 (adapted) -- `get_dump` explains the dump: message, termination point, SY fields, source, variables, stack (2026-10-20)
- Before: `GetDump` fetched the viewer HTML (`/runtime/dump/<id>`, `text/html`) and returned it whole in `rawHtml` (hundreds of KB of CSS) with only the `<title>` parsed; every other field was empty. Now it reads the plain-text rendering `/sap/bc/adt/runtime/dump/<id>/formatted` once and parses it (`pkg/adt/dumpstack.go`, `pkg/adt/dumpdetail.go`, ported from upstream and adapted): `DumpDetails.Detail` (`DumpDetail`) holds `errorType`, `exception`, `program`, `component`, `shortText`, the termination point (`include`, `line`, `procedure`, `mainProgram`), `sourceChanged` (SAP marks the extract "(Source code changed)" when the program was edited after the dump: the extract is then the current code and the failing line may be missing -- seen on a real dump; `note` says so), `systemFields` (SY-SUBRC, SY-MSGID...), `source` (lines around the failure, the failing one flagged), `variables` (per stack frame, values capped at 512 characters, wrapped hex folded) and `stack`. The legacy `RuntimeDump` fields are filled from it (`title` = short text, `exceptionType`, `program`, `include`, `line`).
- **Language**: the parsers match the English labels and the text follows `sap-language`. Verified live on S4D: with ES the labels come back translated ("Texto breve", "Err.tmpo.ejec.") and nothing would parse, so `formattedDump` always sends `OverrideLanguage: "EN"`. Source, variables and SY fields are language neutral. A document that arrives but yields nothing sets `note` instead of looking like an empty dump; a 404 (`ErrDumpDetailUnavailable`, release without the resource) falls back to the viewer page for the title, with a `note`.
- **Behaviour changes**: `rawHtml` is no longer returned by default (`include_html: true`, cut at 256 KB); `title` is the short text, not the HTML `<title>`; the legacy `stackTrace`/`variables`/`sourceCode`/`errorDetails` fields (never populated) and `StackFrame` are gone (the stack is `detail.stack`); Lua `get_dump` gains `shortText`, `errorType`, `component`, `procedure`, `include`, `systemFields`, `source` and `stack[].event` now holds the frame name. New optional `max_variables` (default: all) -- a dump can hold hundreds (182 seen), nothing is cut unless asked; the cut says so in `note`. A dump id with `..`, `?`, `#` or `\` is refused locally.
- **Not ported** (upstream builds a CLI and a similar-dump ladder on the same parsers): `vsp dumps`, the application-log correlation, `StackPrograms`, impact. `DumpStack` as a public method had no caller.
- Tests: `pkg/adt/dumpdetail_test.go` (synthetic document in the real layout, CRLF, another-language labels, `occurred inABAP`, line 0 / blank include, two-word frame types, `%20` on the wire, id refusals, `include_html`, `max_variables`, 404 fallback), `dumpdetail_enrich_test.go` (upstream), `internal/mcp/handlers_dumps_test.go` (works under `SAP_READ_ONLY`). Mutation-checked: language override, wrapped-line join, failing-line flag, `occurred in\s*ABAP`, HTML opt-in, 404 fallback, unrecognized note, id validation, stack chapter bounds.
- Code review: APPROVE, 0 CRITICAL/HIGH. Fixed: `parseDumpStack` now needs the chapter title on a fenced row and stops at the next chapter even when it found no frames; id validation; UTF-8-safe cut; test clean-up. Not changed (LOW): `/formatted` (~500 KB) is cacheable under `VSP_CACHE` (dumps do not change once written); Lua `user`/`time` stay empty (the feed fills them, `GetDump` never did).
- **Verified live (2026-10-20, S4D)** with a throwaway Go program (not committed) on the 5 latest dumps: all parsed (e.g. CALL_FUNCTION_CONFLICT_TYPE: line 293, procedure, 24 SY fields, 49 source lines, 37 variables, 3 frames; another with 182 variables and 11 frames); a Dictionary-change dump has no stack and no SY fields, only short text and source (real, not a parse failure). **Verified through the deployed MCP tool (2026-10-20, after the binary swap and restart)**: `get_dump` with `max_variables: 3` on dev returned the full detail (24 SY fields, 3 frames, source, 3 of 37 variables, `note` "variables cut"); that call is what showed the "Source code changed" case, handled afterwards (`sourceChanged`, unit-tested; not re-run through MCP). **Production, verified 2026-10-20**: `list_dumps` first answered `[]` with no error although PROD has dumps (SNAP_BEG): the role of the read-only user lacked `S_ABAPDUMP` (SU53: `DUMP_INFO`, `DUMP_CCLNT`, `DUMP_CUSER`, `ACTVT=03`) and the feed silently filters what the user may not see (not a vsp bug). After the role got `S_ABAPDUMP` display, `list_dumps` returns the PROD dumps and `get_dump` parses one (TSV_LIN_ALLOC_FAILED, failing line, 25 variables, stack of 2). **`sourceChanged` verified through the MCP** on the dev dump (flag and note present). Note: dump ids must be copied verbatim from `list_dumps` (the blank padding matters; a retyped id gives 404).

### 2bg. Upstream #299 (adapted) -- exact-name search and package inventory (2026-11-03)
- Commits `e28c340` (exact search), `fbeb572` (inventory). `SearchObject` gains `exact` (+ `objectType`): `SearchObjectExact` asks ADT for a 1000-object window and keeps only the object named exactly; a full window returns `{results, incomplete}` instead of silently hiding hits beyond it. CLI `vsp search --exact`.
- `read DEVC <pkg>` with `inventory: true` (`PackageInventory`, `pkg/adt/package_inventory.go`): TADIR + TDEVC (+ abapGit part) read-only, caps 5000 objects / 1000 subpackages, package name whitelisted. TADIR/TDEVC left the response-cache `stableTables` (they change under you).
- Tests mutation-checked; code review APPROVE. Live verification: see the 2bh entry.

### 2bh. Upstream #299 A-block + #374 (adapted) -- per-call budget for long calls, clean stdio shutdown (2026-11-03)
- `51749bf` transport: `adt.WithCallDeadline(ctx)` marks a context whose own deadline replaces the 60s per-request `Timeout`. Only **stateless** requests are lifted (`Transport.send` builds a client field by field, jar read under `jarMu`); a stateless request under a call deadline always goes through `doIsolated` (no `sap-contextid`, no `contextGate`), so a minutes-long run cannot block a concurrent LOCK. Stateful/unmarked requests keep the 60s cap because they hold the gate. `unlockAfterWrite` retries the UNLOCK on a detached context when the call's ctx expired after a good write (`CreateFromFile`, `UpdateFromFileWithOptions`, `EditSourceWithOptions`, `WithObjectLock`). `CleanupContext` exported.
- `bd78ed6` MCP: `longCall` (`internal/mcp/calltimeout.go`) gives `ExecuteABAP`, `RunUnitTests`, `DeployFromFile`/`ImportFromFile`, `DeployZip` (and, in `1457dc0`, `RunATCCheck`, `GetCodeCoverage`) a budget: `params.timeout` (seconds, max 3600) else `--call-timeout` / `SAP_CALL_TIMEOUT` (default 0 = none). When the budget runs out the result says so and that the work may still run on SAP (check SM50/SM66); the handler's own detail (cleanup warnings) is kept. Invalid timeout is refused before anything runs. Not wrapped: Activate and code writes (other wave).
- `1457dc0` `ServeStdio` uses `StdioServer.Listen` with SIGTERM/SIGINT; EOF/cancel are clean exits; `serveMCP` silences cobra usage so a shutdown no longer dumps every flag on stderr. On Windows SIGTERM is never delivered; stdin EOF is the real path.
- Known limits: reviewer MEDIUM kept by design -- a result that merely contains a timeout marker is relabelled when a budget exists (ExecuteABAP reports an unfinished run as an ordinary result); the handler text stays under "Detail:".
- Code review per commit: APPROVE, 0 CRITICAL/HIGH.
- **Verified live (2026-11-03, S4D + read-only prod, after restart with `SAP_CALL_TIMEOUT=300`)**: exact search (ZTESTRCG1 -> its 3 objects, BUKRS -> 3, same on prod); inventory of `ZABAP01` (dev, `abapgit_repos` empty) and `ZSU01` (prod, 3108 objects, note "abapGit: not installed"); `execute_abap` 5 s ok and cleaned; `timeout:10` on a 30 s wait -> "timed out after 10s ... check SM50/SM66", `Cleaned Up: true`, no new `ZTEMP_EXEC_*` (two old ones from September are not ours), `ENQUEUE_READ` empty; ATC and coverage accept `timeout`, `timeout:0` is refused; prod `execute_abap` with `timeout` still refused by policy. **Not verifiable from this client**: a call over ~60 s (the MCP client answered "Request timed out" for `WAIT 70` with `timeout:150`, before vsp's own deadline), deploy_from_file/deploy_zip, real SIGTERM on Windows. One first ATC run with `timeout:120` also hit "Request timed out" and did not repeat in three later runs (cold ATC, not reproduced).

### 2bi. Upstream #387 (adapted) -- `GetTransaction` rebuilt: VIT header + TSTC/TSTCP, classified by kind (2026-11-03)
- Before: `read TRAN X` failed live with 400 `uriMappingError`. The VIT resource for transactions is `TRANT` (not `TRAN`) and only answers `application/vnd.sap.adt.basic.object.properties+xml`; an unknown tcode returns 200 with an empty mainObject; `IsRouterNotFound` (`pkg/adt/http.go`) tells a router 404 from a resource 404.
- Now (`pkg/adt/transaction.go`): `checkSafety(OpRead)` -> tcode whitelist (`^[A-Z0-9_/-]{1,20}$`, rejected not escaped: freestyle SQL has no bind parameters) -> VIT header (description, package, responsible) -> TSTC (program, screen, CINFO) -> TSTCP only for parameter transactions -> `classifyTransaction` (`report`/`dialog`/`parameter`/`variant`/`oo`/`area_menu`...). If VIT is unavailable the header comes from TSTCT/TADIR; under `BlockFreeSQL` the VIT header is returned with a Note. `TSTC`/`TSTCT`/`TSTCP` joined `stableTables`. `vsp graph` TRAN/TCODE resolution uses it and fails clearly when there is no program.
- Tests: `pkg/adt/transaction_test.go` (stub server, injection refusals, SQL carries the validated code, BlockFreeSQL); mutation-checked. Code review APPROVE; the MEDIUM (CLI swallowed the real error) fixed.
- **Verified live (2026-11-03)**: SE38 (report), ZSU0001 (dialog, program ZRSU_ENTRADA_MERCANCIAS), SM31, SE93, SU3 on dev; SE38 on read-only prod; non-existent code -> "does not exist"; injection payload refused locally. **Not exercised**: a TSTCP parameter transaction, an OO transaction, an area menu (unit tests only).

### 2bj. Upstream #197 (adapted, complete) -- SM37/SP01 over free SQL and XBP: `job_list`, `spool_list`, `spool_read`, `job_log` (2026-11-04)
- Commits `3796641`, `0330f1d`, `7d13f26`, `62478f7`, `b780b7e`, `3dc4f70` (local, not pushed). Surface: `SAP(action="analyze", params={"type": ...})` only (no standalone tools), plus `vsp jobs list`.
- `job_list` (`pkg/adt/jobs.go`): TBTCO + TBTCP (client in `AUTHCKMAN` on TBTCO). `spool_list`/`spool_read` (`pkg/adt/spool.go`): TSP01/TST01/TST03. Defaults: last 7 days, 50 rows, max 200. `pkg/temse` decodes the ABAP list format. Job name/count validators live in `pkg/adt/jobnames.go` (`pkg/adt` must not import `pkg/saprfc`).
- `job_log` and `spool_read` with `via="rfc"` use XBP (`BAPI_XBP_JOB_JOBLOG_READ`, `BAPI_XBP_GET_SPOOL_AS_DAT`) in a pinned XMI session (`pkg/saprfc/joblog.go`). `via=auto` tries SQL and falls back to RFC only when the spool is stored in files (Storage != "D"). Both RFC paths are refused under `SAP_READ_ONLY` before any connection.
- **Deviations from upstream, found live**: `RQ2NAME` carries program + user suffix, so the program filter is a LIKE prefix (upstream's equality never matched); `TSP01-RQCLIENT` is a plain field (rows for 000 and 100 coexist), so the client is filtered explicitly; TST03 is paged by key (`drowno BETWEEN last+1 AND max`) since `UP TO` is not accepted; numeric cells arrive padded; no `<`/`>` in WHERE (BETWEEN). TBTCO/TBTCP/TSP01/TST01/TST03 stay out of the response cache `stableTables`. TemSe object cap 20 MB.
- **Security note**: `spool_read` by SQL reads TST03 directly, so it bypasses `S_SPO_ACT`/owner authorization (user decision: not restricted, documented in the help text). The RFC path applies the RFC user's spool authorization.
- **Not ported**: CLI `vsp jobs log` and `vsp spool export` (no RFC client plumbing in `cmd/vsp`).
- **Verified live (2026-11-03/04)**: dev, all four types; PRD read-only: `job_list`, `spool_list`, `spool_read` by SQL (spool 26278 decoded; 26277/26276 are OTF Smart Forms, reported as "not an ABAP list"), `job_log` correctly blocked. `ENQUEUE_READ` empty after RFC calls.
- Code review: APPROVE, 0 CRITICAL/HIGH. Not applied (LOW): LOGOFF with the request context, duplicated Pin/logon block, no size cap on RFC outputs.

### 2bk. Upstream #213 (adapted) -- read Enhancement Framework objects (ENHO) and list the implementations of a BAdI (2026-11-04)
- Surface: `read ENHO <name>` (via `GetSource`) and `SAP(action="analyze", params={"type":"badi_implementations","badi":"<BAdI>"})`. Read-only (`checkSafety(OpRead)`); nothing locks, writes or activates. Files: `pkg/adt/enhancements.go` (+`_test.go`), `internal/mcp/handlers_enhancements.go`, hooks in `workflows_source.go`, `handlers_source.go`, `handlers_help.go`, `handlers_universal.go`.
- **Deviation from upstream, found live**: upstream's subtype codes (XH/XC/XFB/XD/XBD) do not exist on S4D. Here ENHO is `ENHO/XHH` (source plug-in, `/enhancements/enhoxhh/<n>`, `source/main` text) or `ENHO/XHB` (BAdI implementation, `/enhancements/enhoxhb/<n>`, XML; `source/main` is 404). Other subtypes are rejected naming the code. PR #349 (BADI_IMPL creation) and the include-enhancements listing are out of scope.
- XHH: source plus hook points from `ENHINCINX` (VERSION='A'), prepended as `* ENHANCEMENT n hooked at ... in <program>` comments. XHB: class, BAdI, spot, active flag, short text from the XML, plus the other implementations of the same BAdI from `BADI_IMPL` (active flag read from each ENHO's XML, capped at 25 reads; list capped at 100, ordered by POS). A failed active read becomes a note and a 401/403 stops further reads.
- Free SQL has no bind parameters: names are whitelisted `^[A-Z0-9_/]{1,30}$` and rejected, never escaped. `BlockFreeSQL` is an error, not an empty list. ENHINCINX/BADI_IMPL are not in `stableTables`.
- Review: APPROVE, 0 CRITICAL/HIGH; 3 MEDIUM (silent failed active read, non-deterministic cap, untested caps) fixed. Tests mutation-checked on the name whitelist.
- **Verified live (2026-11-04)**: dev: `ZENH_DELVRY` (XHH, 2 hooks at SAPLV56K), `ZENH_ME_PROCESS_PO_CUST2` (XHB, class ZCL_IM_ME_PROCESS_PO_CUST2, 4 siblings), `badi_implementations ME_PROCESS_PO_CUST` (5, all active), non-existent name -> "not found", quote payload refused locally. Prod (read-only): same BAdI (4) and `ZENH_DELVRY`. Not exercised: an inactive implementation, the 25/100 caps (unit tests only).

### 2bl. Optional follow-ups to 2bk -- `get_transport_info` exposes REQUESTS/LOCKS/MESSAGES; ENHO hook-read failure leaves a note (2026-11-04)
- `parseTransportInfo` (`pkg/adt/transport.go`) used to discard what `/cts/transportchecks` answers beyond the header. It now reuses `parseTransportCheck` (the parser behind the transport auto-choice, 2z) and fills `TransportInfo.Candidates`, `LockedInTask`, `Messages`; fail-open (a parse error keeps the plain header). `handleGetTransportInfo` prints "Locked in request/task", "Open requests the object could go into" and SAP's messages. This probably explains the oddity noted in 2ar ("`get_transport_info` did not list the test request").
- `GetEnhancementText` (XHH): a failed `ENHINCINX` read now prepends `* NOTE: ...` instead of silently showing no hook points.
- Tests: `TestParseTransportInfo_RequestsLocksMessages`, `TestGetEnhancementText_HookFailureLeavesANote`. Code review: APPROVE.
- **Verified live (2026-11-04)**: dev, `get_transport_info` on `ZCL_VSP_RFC_SERVICE` (package `ZABAP01`, not `ZADT_VSP`) lists `S4DK928661 ZRCHAPADO ZADT_VSP` as candidate; `read ENHO ZENH_DELVRY` unchanged (no NOTE). `get_transport_info` is refused on the read-only production connection (type X, by design), so prod was not exercised. Not exercised: the LockedInTask line and SAP messages (unit tests only).

### 2bm. Strict transport choice -- no LOCK without a request when one is needed (golden rule) (2026-11-04)
- Closes the open decision of 2ao and reverses the fail-open of 2z. Before: a LOCK could go out with no `corrNr` when (a) creating a request failed, (b) the check found a request was needed, none of the user's fit and `--enable-transports` was off, or (c) the transport check itself failed -- SAP then generated "Generated Request for Change Recording" on its own.
- `planTransport` now returns `TransportChoice.Err` in those three cases (`errTransportRequired`, message tells to pass `transport=<request>`; `TransportChoice.Required` marks case b). A failed check on an object known to be local (`$` package passed, or resolved through `getObjectPackage` -- `objectIsLocal`, stateless, before the LOCK) is still not a refusal. `lockCorrNr(supplied)` now returns `(string, error)`: the plan's error is returned and every LOCK site (description, i18n x2, lock_scope, textpool, workflows x3, workflows_deploy, workflows_edit, workflows_source x5) returns before sending the LOCK. The creators (`CreateObject`, the DDIC/MSAG ones) already returned `plan.Err`.
- Review follow-up: `DeleteObjectWithAutoLock` and `RenameObject` (lock of the old object) also plan before the LOCK now and pass the corrNr; their transport is resolved through `resolveWriteTransportFor`. Tests `TestDeleteObjectWithAutoLock_PlanRefuses_NoLockSent` / `_NamedTransport_LockCarriesIt`.
- **Fail-closed note**: a failing `/cts/transportchecks` (404 on a release without the resource, 5xx, authorization) on an object not resolvable as local now refuses the write; `transport=<request>` or `SAP_TRANSPORT_CHOICE=off` are the ways out.
- **Known gaps (review, not fixed)**: the test-include branch of `writeSourceUpdate` returns `Success=true` with a warning appended when the plan refuses (main source already written); LOCKs on just-created objects (`workflows.go` creation paths, `workflows_deploy.go:155`, `workflows_fileio.go:134`) still carry no corrNr -- the request was already chosen at creation and SAP returns it; no per-workflow "no LOCK" refusal test for `WriteInclude`/`WriteClass`/`UpdateFromFile`/`EditSource`/`SetDescription`/`WriteTextPool`/DTEL labels/MSAG (covered by the shared `lockCorrNr` table and the WriteProgram/WithObjectLock/Delete tests).
- **Still allowed, by design**: `SAP_TRANSPORT_CHOICE=off` (explicit opt-out, plan is nil) sends the LOCK without `corrNr`; an explicit `transport` is never second-guessed.
- **Behaviour change**: with `SAP_ENABLE_TRANSPORTS` off, writing a transportable object without naming a `transport` is now refused when no open request fits (before: SAP generated one).
- Tests: `pkg/adt/transport_refusal_test.go` (plan refusals, local controls, WriteProgram and WithObjectLock send no LOCK), `lock_corrnr_test.go` (`lockCorrNr` table), `transport_choice_test.go`. Fixtures: stubs that left `cts/transportchecks` empty used to pass only through the fail-open; `withDefaultTransportCheck` (httptest) and defaults in the two mock transports now answer "local object".
- **Phase 0, live (2026-11-04)**: `get_transport_info` (same endpoint) answers correctly for a `$TMP` program (Recording empty), a message class, a DTEL and a class in transportable packages (candidates listed), so the check works for the types vsp writes.
- **Verified live (2026-11-04, deployed binary)**: `$TMP` control — `EDITSOURCE` on `ZTESTRCG1` (change and revert) activates, no enqueue left. Named-transport path — `EDITSOURCE` with `transport: S4DK928661` on `ZCL_VSP_RFC_SERVICE` (change and revert) works, no new request created today (E070/E07T), no enqueue left. **Refusal NOT verified live**: it needs zero candidate requests, and the dev user has ~65 open ones (`ZUTIL_ACTIVIDAD` in `ZSU01` lists them all, so the plan would pick one rather than refuse); covered by unit tests only.

### 2bn. Package whitelist on `SET_TEXT_ELEMENTS` and on the SOURCE package of `MOVE` (2026-11-04)
- Closes the two "Known gaps" of 2ap. With `SAP_ALLOWED_PACKAGES` set, both WebSocket writes now check the package the object lives in. New `Client.CheckObjectPackageByName(ctx, type, name, op)` (`pkg/adt/package_by_name.go`): no-op without a whitelist; otherwise one stateless `SearchObjectExact`, matches by TADIR type root (`CanonicalObjectType` first, so `FUNC` finds `FUGR/FF`; `INCL` is a `PROG`) and fails closed on a search error, no match, a full search window (`incomplete`), an empty package, or any matching hit in a package that is not allowed. It runs before the WebSocket and before any lock (neither handler opens an ADT lock; issue #91).
- `handleSetTextElements`: order is `refuseUnderSafety` -> arguments -> package check (`PROG`) -> WebSocket. Function/class pools are refused (fail-closed). The tool is now marked DEPRECATED in favour of `SAP(action="i18n", params={"op":"texts_set"})` (it records nothing in a transport request); the output says so. `handleMoveObject` additionally checks the source package (destination check from 2ap kept); no transport requirement.
- Tests: `pkg/adt/package_by_name_test.go`, `internal/mcp/handlers_package_gate_test.go` (refusal tests with controls; mutation-checked, 4 fail without the gate). Code review: APPROVE, 0 CRITICAL/HIGH; MEDIUM (type aliases such as FUNC) fixed.
- Known limits: `DEVC` as the object to move may come back with an empty `packageName` and be refused (not checked live); TOCTOU between the search and the write; `TestMoveObject_DestinationPackageHonoursAllowedPackages` (2ap) now has a vacuous control (empty-body stub gives a "not found" refusal).
- **Verified live (2026-11-04, S4D, deployed binary)**: `SET_TEXT_ELEMENTS` on `PROG RSUSR002` -> "source package of PROG RSUSR002: operations on package 'SUSR_IS' are blocked by safety configuration (allowed: [Z* $TMP])"; same call on `ZTESTRCG1` (rewriting the existing selection text) succeeds and prints the deprecation note; `MOVE` of `RSUSR002` to `$TMP` -> refused on the source package; `ENQUEUE_READ` for the dev user empty afterwards. **Real `MOVE` (2026-11-04)**: throwaway `ZVSP_TST_MOVE` ($TMP) -> `ZABAP01` with `transport` S4DK928661 passed both gates and reached ZADT_VSP, but answered "MoveObject failed: request timeout" and TADIR stayed in `$TMP` (nothing moved, no enqueue left; object deleted afterwards). Consistent with the suspicion in 2aq that `zadt_cl_tadir_move` contains a `COMMIT` (illegal in APC); **`MOVE` is probably broken independently of this change** -- not investigated. Note too that with `SAP_ALLOWED_PACKAGES=Z*,$TMP` the only valid local destination is `$TMP`.

### 2bo. `MOVE` fixed (no COMMIT in APC) and extended: local object -> transportable package, recorded in an explicit request (2026-11-04)
- **Bug found by 2bn's real attempt**: `ZADT_CL_TADIR_MOVE` did `COMMIT WORK AND WAIT` inside the APC handler -> `APC_ILLEGAL_STATEMENT` (client saw "request timeout", ST22 dump 14:29Z). The COMMIT is gone (the framework commits on return). Also, `TR_TADIR_INTERFACE` records nothing in E071, so a move into a transportable package was never in any request.
- **New**: `MoveObject` accepts `transport` (workbench request). With it, a PROG/CLAS/INTF in a local (`$`) package goes to a transportable one: `ZADT_CL_TADIR_MOVE=>move_object_to_request` checks TADIR (this system's original), source local, request (format, exists, not a task, workbench, modifiable, own modifiable task, no lock in another request), dry-run `TR_TADIR_INTERFACE`, real change, then `TR_RECORD_OBJ_CHANGE_TO_REQ` (R3TR) and a read-back of E071; if recording fails the old package is restored (`RECORD_FAILED_MOVED_BACK`) or reported (`MOVED_NOT_RECORDED`, repeat the call). Repeating the call on an already moved+recorded object answers `already_recorded`. Order is mandatory because TR_RECORD treats a `$` devclass as local and records nothing. The request is never autogenerated (golden rule). Without `transport` MoveObject only moves `$`->`$`; a transportable destination without it is refused.
- **Go** (`handleMoveObject`): refuseUnderSafety -> transport format/type whitelist/`CheckTransportableEdit` -> destination `CheckPackage` -> source package by name (`alreadyThere` allowed only with transport) -> WebSocket. If a transport was given and the answer carries no recording, an error says the backend predates the feature and the object may be in the package unrecorded (reviewer MEDIUM; `moveNotRecordedMessage`).
- **Code review**: APPROVE (PARTIAL: checklists not loaded), 0 CRITICAL/HIGH. Fixed: silent success with an old backend. Not fixed: (MEDIUM) `alreadyThere` lets a transport record any PROG/CLAS/INTF already in an allowed package unless it is locked in another request; (MEDIUM) if `TR_RECORD_OBJ_CHANGE_TO_REQ` fails half-way the "not recorded" message may be false (leftover E071/TLOCK); (LOW) `src/zcl_vsp_rfc_service` lacks the `transport` branch (pre-existing drift; installs from `src/` do the TADIR-only move), JSON values in the transport branch not passed through `escape_json`, no ABAP Unit for the class.
- **Verified live (2026-11-04, S4D)** with `ZVSP_TST_MOVE` ($TMP) and a dedicated request: negatives (no transport, malformed number, task number `TRANSPORT_IS_TASK`, `$TMP`+transport, `FUNC`) left TADIR in `$TMP`; the positive move gave TADIR `ZABAP01` and one `R3TR PROG` row in E071 under the request; repeating gave `already_recorded` with no new row; `ENQUEUE_READ` empty; the next WebSocket call worked (APC session alive); no new dump after the fix; the read-only production connection refuses MOVE. **Verified live later (2026-11-05)**: CLAS and INTF moved `$TMP`->`ZABAP01` with an explicit request: TADIR `ZABAP01`, one `R3TR CLAS`/`R3TR INTF` row each in E071 under the request, no enqueues left. **Incident**: the first PROG create of the session failed ('Parameter corrNr could not be found') because a stale TADIR row of the earlier test program still pointed at `ZABAP01`; my follow-up `edit` without `transport` then wrote to that transportable object and a NEW request (S4DK928953/54) was created by the write path -- golden-rule breach, cleaned by the user in SE09. Lesson: after a MOVE test the stale TADIR row survives until the request is released; use fresh object names and always pass `transport`. **Not verified**: compensation paths, the `check_lock` rejection (needs a still-local object locked elsewhere), a request without an own task from another user, local->local move.

### 2bp. Guard against a stale TADIR row: no create in `$` over a name held by a transportable package; no adoption of a request nobody chose (2026-11-05)
- **Incident (2bo)**: after a MOVE test TADIR kept the object in `ZABAP01`. A `WriteSource` create in `$TMP` over that name sent the POST (200, TADIR unchanged), then the LOCK with no `corrNr`: SAP generated "Orden generada para grabación de modificación" and vsp adopted it (`resolveWriteTransport` adopts any `lock.CorrNr`); the PUT then failed with "Parameter corrNr could not be found". Reproduced with an authorized `VSP_HTTP_TRACE` run (it created one more order, S4DK928955/56, deleted by the user).
- **(a) `verifyLocalTarget`** (`transport_choice.go`), called in `CreateObject` before the POST when the target package is local: `CheckTransport` with empty package and op `I` (live: stale object answers `DEVCLASS/TADIRDEVC=ZABAP01`, a fresh one empty); if that fails, TADIR is read filtered by `pgmid`, TADIR type and name. A non-local package refuses the create (`errStaleTransportableTarget`); if check and query both fail the create proceeds (second layer is (b)).
- **(b) `ErrUnexpectedLockTransport`** in `resolveWriteTransportFor`: plan chose no request and the LOCK returned one -> refuse, never adopt (the deferred release frees the lock). **It cannot stop SAP from creating the order inside the LOCK**, only from writing into / adopting it; the order then needs deleting in SE09. `chooseTransport` now checks `LockedIn` before `Recording`, so an object already captured in an open request is still accepted.
- **Not done**: the LOCKs that follow a creation (`workflows.go` 458/566, `workflows_deploy.go` 155, `workflows_fileio.go` 134, `workflows_source.go` creation branches) still send no corrNr; the old adoption tests (#144) now run with `WithTransportChoice("off")`.
- **Tests**: `pkg/adt/local_target_test.go` (refusal and control, fallback via TADIR with type filter, unchosen/planned/locked-in adoption, table for `resolveWriteTransportFor`); both guards mutation-checked. Code review: APPROVE, 0 CRITICAL/HIGH; the 3 MEDIUM (TADIR type filter, LockedIn order, test gaps) fixed.
- **Verified live (2026-11-05, deployed binary)**: `edit CLAS ZCL_VSP_TST_MOVE` in `$TMP` (stale row in `ZABAP01`, object gone) -> refused locally with the stale-TADIR message; E070 unchanged (no new order). **Mistake during verification**: my first attempt used `ZVSP_TST_MOVE`, which still existed; it went the update path and wrote my test source into the user's existing order S4DK928951 without asking; the program was then deleted with confirmation. Lesson: check the object exists before a guard test. **Verified live later (2026-11-05)**: fresh-name create in `$TMP` still works (`ZVSP_TST_GUARD1`, TADIR `$TMP`, no new order); `edit` with an explicit `transport` on a transportable object (`ZVSP_TST_GUARDB`, `ZABAP01`, order created on request) works and creates no extra order. **(b) is not reachable live**: once (a) exists, the plan of a real transportable object always has a candidate or the locked-in request, so the LOCK carries a `corrNr`; (b) needs a plan with no request plus a request in the LOCK, which only the (a)-blocked local-create path produced. It stays covered by mutation-checked unit tests. The throwaway programs were deleted; the stale TADIR row of `ZVSP_TST_GUARDB` in `ZABAP01` and its order (S4DK928957) remain for the user to clean in SE09.
- **Correction to 2bo/2bp**: a stale TADIR row does not last "until the request is released". After a delete it persists with `DELFLAG=X` (SE38 can recreate over it, ADT keeps the old package), so it needs cleaning in SE03/SE09. The guard (a) error message in `transport_choice.go` still says "until its request is released"; not corrected yet.

### 2bq. `MOVE` between transportable packages (T->T) and `dry_run` (2026-11-05)
- `MoveObject` with `transport` now also moves a PROG/CLAS/INTF from one transportable package to another (e.g. ZABAP01 -> ZSU01). `ZADT_CL_TADIR_MOVE=>move_object_to_request` asks `TR_CHECK_CHANGE_OF_DEVCLASS` (as SE03 does) which entries to record (same transport layer: `LIMU ADIR`), changes TADIR with the existing `TR_TADIR_INTERFACE` path and records with `TR_RECORD_OBJ_CHANGE_TO_REQ`. `TR_CHANGE_OBJS_DEVC` (SE03's own) is avoided: it does `ROLLBACK WORK` (illegal in APC).
- Refusals: layer/delivery-unit change (an `R3TR` entry wanted -> `TRANSPORT_LAYER_CHANGE`, use SE03), `DELFLAG=X` source (`OBJECT_DELETED`; a recreated object has the flag cleared and is allowed), lock in another request, any CTS refusal (`PACKAGE_CHANGE_REFUSED`), nothing to record. `are_recorded` also accepts an `R3TR` cover (the CTS adds no partial entry when the object is already in the request).
- `dry_run` (MCP `MOVE`, needs `transport`): everything is checked, nothing changed or recorded; the answer says which entries it would record. It travels as the string "true" (`extract_param` reads quoted scalars only). A stale backend that ignores it would move for real: the Go handler detects that afterwards and reports it.
- Files: `embedded/abap/zadt_cl_tadir_move.clas.abap`, `src/zcl_vsp_tadir_move.clas.abap`, `embedded/abap/zcl_vsp_rfc_service.clas.abap` (`src/zcl_vsp_rfc_service` untouched, pre-existing drift), `pkg/adt/websocket_rfc.go`, `internal/mcp/handlers_crud.go`, `tools_register.go`, tests in `websocket_rfc_move_test.go`, `handlers_package_gate_test.go`.
- **Not implemented** (planned Q4/Q5): rejecting objects with dependents (PROG/CLAS/INTF have a single TADIR row; none found) and warning about objects in released orders. Unfixed MEDIUM: a T->T retry after a half-done move records `R3TR`.
- **Verified live (2026-11-05, S4D)** with `ZVSP_TST_MOVE` (order S4DK928959): `dry_run` ZABAP01 -> ZSU01 changed nothing and announced `LIMU ADIR R3TRPROGZVSP_TST_MOVE`; the real move set TADIR to ZSU01 (E071 gained no new row because the object was already `R3TR` in the task: the cover case); repeating answered `already_recorded`; move back ZSU01 -> ZABAP01 worked; a task number gives `TRANSPORT_IS_TASK`; T->T without `transport` is refused; the read-only production connection refuses MOVE; no new order created. **Not verified live**: whether `TR_RECORD_OBJ_CHANGE_TO_REQ` really inserts a `LIMU ADIR` row (needs an object not yet in any task), `TRANSPORT_LAYER_CHANGE`, `OBJECT_DELETED`, foreign lock, CLAS/INTF T->T, the enqueue check (my `execute_abap` probe returned no output).

### 2br. `WriteSource` create in a transportable package: package check and automatic MOVE over a stale TADIR row (2026-11-05)
- **The defect (found live, 2bq session)**: a create in a transportable package (ZSU01) over a name whose TADIR row survived a delete (`DELFLAG='X'`, old package) answers success but SAP reuses the old row, so the object stays in the OLD package. Guard (a) of 2bp only covers local (`$`) targets.
- **Behaviour** (`internal/mcp/handlers_source_package.go`, wired in `handleWriteSource`; scope = `WriteSource` only, PROG/CLAS/INTF, non-local package, not mode `update`; CreateObject/DeployFromFile/RenameObject are NOT covered, decision D3, to be extended separately):
  - **Before the POST (D1)**: `TADIRRow` reads TADIR (`pkg/adt/package_by_name.go`, name/type whitelisted, no bind params). A row with `DELFLAG=X` in another package means SAP would create it there; if the automatic MOVE would be impossible (no explicit `transport`, malformed number, `CheckOperation`/`CheckTransportableEdit`/`CheckPackage` of the old package refuse) the create is refused with "Nothing was created".
  - **After a successful create (`Mode=="created"`)**: TADIR is read again; if the package is not the requested one, `autoMove` runs `MOVE` with `dry_run` first (D5) and then for real, using ONLY the transport the caller named (D2, never `planTransport`), and re-reads TADIR. Result field `packageCheck`: `ok…` / `corrected…` / `not verified…` / `MISMATCH…`.
  - **MISMATCH** (move impossible or failed): the result is returned with `IsError`, says the object was created and activated, in which package it is, why vsp could not move it, "Nothing was deleted" and offers (1) move it yourself in SE80/SE03 or (2) tell vsp to delete it. vsp never deletes on its own.
  - **D4**: if TADIR cannot be read (before or after) the create is a success with a warning in `packageCheck`. A create that fails half-way with a known stale row adds a NOTE that the object may sit in the stale package.
  - `Server.moveFn` is a test seam for the MOVE; production uses `debugWSClient.MoveObject`.
- Tests: `pkg/adt/tadir_row_test.go`; `internal/mcp/handlers_source_package_test.go` (plan: stale + no transport refuses, stale + transport proceeds, stale in a disallowed package refuses, no stale row, does-not-apply reads nothing, TADIR read failure is a warning; verify: right package, wrong package without transport, unreadable TADIR; `autoMove` table: corrected, dry run refused/error, backend ignoring dry_run, move without recording, move refused, move ok but TADIR still wrong).
- Code review: APPROVE, 0 CRITICAL/HIGH. Its 2 MEDIUM (no tests for `autoMove`; no note when the create fails half-way) were fixed. LOW not changed: `moveBackImpossible` repeats part of `handleMoveObject`'s policy (not shared), checks neither `TransportReadOnly` nor `CheckTransport(write)` explicitly (`CheckTransportableEdit`/`OpUpdate` filter it), `INCL` enters as `PROG` untested live.
- **Verified live (2026-11-05, S4D, deployed binary)**: `edit PROG ZVSP_TST_TRCHOICE` `package ZSU01`, `transport S4DK928959`, `mode create` (stale row `ZABAP01`/`X`): answer `packageCheck: "corrected: SAP created … in ZABAP01 (stale TADIR row); vsp moved it to ZSU01 and recorded it in S4DK928959"`; TADIR afterwards `ZSU01`, `DELFLAG` blank; E071 holds `R3TR PROG` in task `S4DK928960` of that order; no new order created. Negative: the same create for `ZVSP_TST_GUARDB` (stale `ZABAP01`) with no `transport` was refused before the POST and TADIR was unchanged.
- **Not verified live**: MISMATCH path with a real failing move, upsert on an existing object, a CLAS/INTF create, a create with TADIR unreadable.
- Cleanup left: `ZVSP_TST_TRCHOICE` and `ZVSP_TST_MOVE` exist in `ZSU01` inside order S4DK928959 (do not delete without confirmation); stale rows `ZVSP_TST_GUARDB` (PROG, `ZABAP01`, `DELFLAG=X`) and others in TADIR for the user to clean in SE03.
- Files: `internal/mcp/handlers_source_package.go` (+`_test.go`), `internal/mcp/handlers_source.go`, `internal/mcp/server.go`, `pkg/adt/package_by_name.go`, `pkg/adt/tadir_row_test.go`, `pkg/adt/workflows_source.go` (`PackageCheck`).

### 2bs. Package check after create extended to `CreateObject`, `DeployFromFile`/`ImportFromFile` and `RenameObject` (D3 of 2br) (2026-11-05)
- Reduced scope (user-approved): PROG/CLAS/INTF only (`movableType`); live tests showed CLAS, INTF and DTEL do NOT reuse stale TADIR rows (created straight in the requested package), only PROG does. No `DeployZip`.
- **Behaviour**: same flow as 2br (`planPackageCheck` before the write, refusal when the MOVE would be impossible; `checkPackageAfterCreate` after). `handleCreateObject`: shell text "created as an empty inactive shell". `deployFromFile`: `DeployResult.ObjectCreated`/`PackageCheck`; a deploy that fails after creating the object only reports the real package (`reportPackageOnly`, never moves). If the call's context expired, TADIR is read on a detached context and no MOVE is tried. `RenameObjectWithOptions` (`VerifyNew`, `IncompleteNote`): the package check runs after the new object is unlocked and activated and before the old one is locked (issue #91); if it fails nothing is deleted or rolled back (user chooses), `Success=false`.
- Tests: `internal/mcp/handlers_create_paths_package_test.go`, `pkg/adt/rename_verify_test.go`. Code review APPROVE, 0 CRITICAL/HIGH; MEDIUM covered. LOW not changed: misleading text if the context expires during `autoMove`; `return nil, err` after create in `CreateFromFile`; `rollbackShell` in Rename deletes the empty shell on its own (pre-existing).
- **Verified live (2026-11-05, S4D, deployed binary)**: refusals without `transport` for all three routes (TADIR unchanged); `create OBJECT PROG/P` ZVSP_TST_GUARDB ZSU01 + `S4DK928959` -> `corrected` (ZABAP01 -> ZSU01), TADIR ZSU01; `deploy_from_file` to ZABAP01 (stale row now in ZSU01) -> `corrected` to ZABAP01; `rename` in `$TMP` fine; no new order (E070), `ENQUEUE_READ` empty. Test objects deleted (user-authorized; `delete` needs an explicit `transport` for transportable objects). **Not verified**: real MISMATCH, Rename "corrected" in a transportable package.
- Cleanup left: `ZVSP_TST_GUARDB` stale row now `ZSU01`/`X`; test objects in `ZSU01` inside S4DK928959 (`ZCL_VSP_TST_MOVE`, `ZIF_VSP_TST_MOVE`, `ZVSP_TST_DTEL1`, `ZVSP_TST_SHELL`, `ZVSP_TST_TRCHOICE`, `ZVSP_TST_MOVE`).
- Files: `internal/mcp/handlers_source_package.go`, `handlers_crud.go`, `handlers_fileio.go`, `pkg/adt/workflows_deploy.go`, `workflows_fileio.go` (+ the two test files).

## Known Open Issues (Not Fixed)

> **Archived (2026-11-05):** the entries marked RESOLVED/FIXED above their own title (Freestyle SQL,
> SAP_READ_ONLY, WriteMessageClassTexts, RUN_REPORT, SET_BREAKPOINT) and "3. Nuevos tipos DDIC" were
> moved verbatim to [CLAUDE-HISTORY.md](CLAUDE-HISTORY.md) — their how-to content now lives in the
> `vsp-sql-consultas-largas`, `vsp-rfc-reports`, `vsp-textos-mensajes`, `vsp-debug-sap` and
> `vsp-ddic-crear-objetos` user skills respectively. Only the genuinely still-open issue stays below.

### `ListSQLTraces` (ST05 trace directory) — not implementable via ADT as originally designed — intentionally stubbed (2026-08-12)
- **Root cause**: `/sap/bc/adt/st05/trace/directory` (with the correct Accept header,
  `application/vnd.sap.adt.perf.trace.directory.v1+xml`, see 2p above) does **not** return a list of trace
  records. The real body is `<traceDirectory><uri>...</uri></traceDirectory>` — a single link to the Fiori
  `SQL_TRACE_ANALYSIS` UI app, not machine-readable data. There is no other documented ADT endpoint for
  reading individual SQL trace entries (statement, duration, table) — confirmed no reference implementation
  has this (see 2p).
- The returned Fiori URI itself resolves to the application server's **internal** hostname
  (`http://<internal-instance-host>:8000/sap/bc/stmc/ui5/...`), which gave a 403 when the user tried it from
  outside the corporate network — likely an `icm/host_name_full` profile mismatch between the internal
  instance hostname and the externally-published reverse-proxy hostname used for everything else (ADT
  itself works fine through `sapdev.launioncorp.com`). Not something fixable in vsp — a Basis-side SAP
  profile question.
- **Decision (user, 2026-08-12)**: rather than exposing a misleading partial capability (a link that may not
  even be reachable), `ListSQLTraces` now fails immediately with an explanatory error and makes **no** call
  to SAP at all (`pkg/adt/client.go`) — the MCP tool description was updated to say so explicitly
  (`internal/mcp/tools_register.go`, `ListSQLTraces`).
- **Status**: on hold. The user's Basis contact is on vacation until September 2026 — revisit once they
  confirm whether the internal-hostname 403 is fixable (correct `icm/host_name_full`, reverse-proxy rule, or
  similar) and, if so, whether the Fiori UI's own backend network calls (would need inspecting via browser
  DevTools) expose a data endpoint that could be wrapped instead of just linking out.
- Test: `pkg/adt/trace_test.go` — `TestListSQLTraces_FailsWithoutCallingSAP`.

### 4. Graph Engine (`pkg/graph/`) — In Progress
Sequence: unify existing dep logic → SQL/ADT adapters → impact/path queries.
- Done: core types, parser dep extraction, boundary analyzer (11 tests)
- Done (fork): `hardcode_usage` + `hardcode_audit` — ZTCA_HARDCODE caller analysis (2026-05-29)
- Pending: SQL adapters (CROSS/WBCROSSGT/D010INC), ADT adapters, unify `cli_deps.go` + `cli_extra.go` + `ctxcomp/analyzer.go`
- Design: [002](reports/2026-04-05-002-graph-engine-design.md), [003](reports/2026-04-05-003-graph-engine-alignment-for-claude.md)

### 5. GUI Debugger (Issue #2) — Strategic
Plan: MCP debug sessions → DAP → Web UI. ADT REST API mapped from `CL_TPDA_ADT_RES_APP`. Design: [001](reports/2026-04-05-001-gui-debugger-design.md)

### 6. Open Issues
- **Future improvement, not started** — migrate `SET_BREAKPOINT`'s line-breakpoint call (only that call, not
  the whole debug domain) from `ZCL_VSP_DEBUG_SERVICE`'s WebSocket/TPDAPI bridge (fixed in 2ak) to this
  fork's own `pkg/adt/debugger.go`'s `SetExternalBreakpoint` (REST `/sap/bc/adt/debugger/breakpoints`,
  currently unused/marked deprecated). Live-verified this session (2026-09-15, against `RFC_SYSTEM_INFO`):
  the REST POST works with no 403 on this system today (the "DEPRECATED... 403" comment on that function is
  stale) and SAP resolves `main_program`/`include` itself — no `D010INC` lookup needed. Matches the
  direction upstream took in PRs #187/#188, though via a new dependency (`pkg/saprfc`) this fork deliberately
  didn't adopt (see 2ah) — this would reuse code already in this fork instead. The blocker: the REST GET
  (`GetExternalBreakpoints`) still returns empty on this system even when a breakpoint demonstrably exists
  (confirmed live via a direct `ABDBG_EXTDBPS` query) — upstream doesn't fix this either, they track
  breakpoints client-side in memory (`Debugger.bpSet` in `pkg/saprfc`) instead of trusting the server GET;
  `ZCL_VSP_DEBUG_SERVICE` already does the same trick (`mt_breakpoints`). A future attempt at this migration
  would need the same client-side tracking in Go, or a real fix for the GET endpoint itself — no upstream
  issue/PR addresses the GET bug as a bug to fix, only as a known limitation designed around; checked
  thoroughly (issue #184, PRs #184/#186-189 bodies and all comments) before writing this down, nothing to
  port from there. Deliberately not started — the current WS-based fix (2ak) already works and is verified;
  this is a "nicer, smaller mechanism" idea for whenever there's time, not a bug needing a fix.
- **#88** Lock handle bug (EditSource/WriteSource) — same root cause as #132 (session affinity). **Resolved**
  by the #91 port (see 2t/2u above) — the marker migration and enqueue-leak fixes close this on this fork.
  Upstream's own PR #167 lists #88 among the issues it closes.
- **#168** Keep-alive ping has no session affinity, can retire the context inside any lock window. **Fixed**
  by the #178 port (see 2w above) — the ping now skips entirely while a lock is outstanding, and
  `--keepalive` defaults to 0.
- **#169** MCP cross-tool-call window: a lock handle spans separate tool calls, and any read the agent does
  between LOCK and the write that consumes it is a stateless hop — no in-process fix closes this, it needs
  an MCP-level design change. **Addressed by 2ay (#183) and 2ba (#251)**: the write tools lock and write in one call, and concurrent callers of one client no longer break each other's lock chains.
- **#55** RunReport in APC — architectural limit. **Resolved** by the pkg/saprfc Fase 2 migration
  (see 2an above) — `RUN_REPORT` no longer uses APC/WebSocket at all, so the limit no longer applies.
- **#46** / **#45** Sync script flags — closed upstream (script never existed in public repo)

---

## Build & Test

```bash
go build -o vsp ./cmd/vsp              # Build
go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)  # Unit tests (pkg/cache y cmd/vsp fallan siempre por CGO/SQLite — ignorar)
go test ./...                           # Suite completa (cmd/vsp y pkg/cache fallarán — esperado, CGO no disponible)
go test -tags=integration -v ./pkg/adt/ # Integration (needs SAP)
make build-all                          # 9 platforms
```

Key flags: `--mode focused|expert|hyperfocused`, `--read-only`, `--allowed-packages "Z*"`, `--disabled-groups 5THD`

### Deploying a local build for live testing (Windows)

The Claude Desktop MCP config (`claude_desktop_config.json`) launches `vsp` from
`%LOCALAPPDATA%\VSP\vsp.exe` as a child process. Windows locks a running `.exe` for
writes — a plain `cp`/overwrite onto that path fails with "Device or resource busy" / access denied
while any `vsp.exe` process is alive from it. **This is expected and is not a sign anything needs to be
closed first.**

Confirmed working technique (verified 2026-07-29 with 2 `vsp.exe` processes live at the time):

```bash
go build -o vsp_new_build.exe ./cmd/vsp
mv "$LOCALAPPDATA/VSP/vsp.exe" "$LOCALAPPDATA/VSP/vsp.exe.old.$(date +%Y%m%d-%H%M%S)"  # rename, not overwrite
cp vsp_new_build.exe "$LOCALAPPDATA/VSP/vsp.exe"
```

- Windows allows **renaming** an executable that's currently running (exe images are opened with
  share-delete semantics) even though it refuses a direct overwrite. The already-running process(es) keep
  working fine against the renamed-aside file — confirmed empirically: the swap above left two active
  `vsp.exe` PIDs completely unaffected, no crash, no need to kill anything.
  `vsp.exe.old`/`vsp.exe~`/`vsp.exe.old.<timestamp>` sitting in that folder are artifacts of this exact
  pattern used across many past sessions.
- The **only remaining step is restarting Claude Desktop** so it spawns a fresh `vsp.exe` process against
  the new binary — old processes never notice the swap and keep serving stale code until then.
- A separate "cowork" component can hold its own independent `vsp.exe` child process alive outside the
  main Claude Desktop window's process tree; if a restart doesn't seem to pick up a change, check for a
  lingering `vsp.exe` PID (`tasklist /FI "IMAGENAME eq vsp.exe"`) rather than assuming the rename+copy
  itself failed — it almost certainly didn't.

### `SAP_RFC_HOST`/`SAP_RFC_SYSNR` — required for the deployed MCP server's classic-RFC calls (2026-09-17)

The 2am `pkg/saprfc` migration's live verification (Fase 0+1) was done against throwaway Go
programs with the RFC destination supplied directly, not against the actual deployed
`claude_desktop_config.json`-launched `vsp.exe`. When `CALL_RFC`/`RFC_SEARCH`/`RFC_METADATA` were
first tried through the real MCP tool on this Windows machine, they failed — `saprfc.Resolve`
could not derive a gateway host:port from `SAP_URL`, which on this system is a reverse-proxy
hostname (`sapdev.launioncorp.com`) with no port, not the SAP application server's own hostname.
Fixed by adding `SAP_RFC_HOST`/`SAP_RFC_SYSNR` to the `abap-adt` server's `env` block in
`claude_desktop_config.json` (and, as a more durable backup, as Windows **user** environment
variables — see the gotcha below) — confirmed via `Get-EnvironmentVariable`/registry, scope
`User`. **End-to-end verified live through the deployed MCP tool after a Claude Desktop restart**
(not a throwaway program this time): `RFC_SEARCH "RFC_PING*"` found both matches, `CALL_RFC
RFC_PING` returned `subrc: 0`, `RFC_METADATA BAPI_USER_GET_DETAIL` returned the full parameter
schema.
- **Gotcha, confirmed twice in the same debugging session**: `claude_desktop_config.json` can be
  rewritten by Claude Desktop itself (most likely when it persists its own app state) and has, in
  this project's history, lost `env` keys it doesn't recognize from its own settings UI — both
  manual additions of `SAP_RFC_HOST`/`SAP_RFC_SYSNR` to that file were wiped shortly after being
  added, before a third attempt (this time typed by the user directly with correct JSON syntax —
  `"KEY": "value"`, not shell-style `KEY=value`) finally persisted. Because of this, the two
  variables are **also** set as Windows user environment variables (`SAP_RFC_HOST=10.20.147.10`,
  `SAP_RFC_SYSNR=00`) as an independent fallback — a freshly-spawned `vsp.exe` inherits those
  regardless of what the JSON currently contains. Either source requires restarting Claude Desktop
  to take effect (a running `vsp.exe` never re-reads its environment).

---

## Codebase

```
cmd/vsp/              CLI entry + 30 commands
  cli_hardcode.go     hardcode-usage / hardcode-audit (fork-specific, ZTCA_HARDCODE)
internal/mcp/
  handlers_*.go       Domain handlers (read, edit, debug, graph, ...)
  handlers_hardcode.go  ZTCA_HARDCODE analysis (fork-specific)
  server_cli.go       Thin constructor + exported methods for CLI commands
  tools_register.go   Registration + mode logic
  tools_focused.go    Focused mode whitelist
  handlers_universal.go  Hyperfocused single-tool (SAP)
pkg/
  adt/                ADT client (HTTP, CSRF, sessions, all SAP ops)
  graph/              Dependency graph engine (in progress)
  ctxcomp/            Context compression (dep resolution for read)
  abaplint/           ABAP lexer + parser (91 statements, 8 lint rules)
  dsl/                Fluent API, YAML workflows, batch ops
  cache/              In-memory + SQLite
  scripting/          Lua engine
  llvm2abap/          LLVM→ABAP (research)
  wasmcomp/           WASM→ABAP (research)
```

| Task | Files |
|------|-------|
| Add MCP tool | `tools_register.go` + `handlers_*.go` + `tools_focused.go` + `handlers_analysis.go` (route) |
| Add CLI command that needs handler logic | `server_cli.go` (export method) + `cmd/vsp/cli_*.go` |
| Add ADT operation | `pkg/adt/client.go`, `crud.go`, `devtools.go`, `codeintel.go` |
| Add graph feature | `pkg/graph/` |
| Add lint rule | `pkg/abaplint/rules.go` |
| Add integration test | `pkg/adt/integration_test.go` |
| Fix MCP/docs/config | `README.md`, `docs/cli-agents/*`, `handlers_universal.go` |

---

## Adding a New MCP Tool

1. Handler in `handlers_*.go`:
```go
func (s *Server) handleX(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    name, _ := req.GetArguments()["name"].(string)
    result, err := s.adtClient.Method(ctx, name)
    if err != nil { return newToolResultError(err.Error()), nil }
    return mcp.NewToolResultText(format(result)), nil
}
```
2. Register in `tools_register.go` with `shouldRegister("X")`
3. Route in `handlers_analysis.go` (or appropriate router)
4. Add to `tools_focused.go` if needed in focused mode

---

## Common Issues

1. **CSRF errors** — auto-refreshed in `http.go`
2. **Lock conflicts** — edit handler does auto lock/unlock
3. **Session issues** — some CRUD/debugger flows are session-sensitive; verify stateful/stateless before changing transport or auth logic
4. **Auth** — use basic OR cookies, not both
5. **ZADT_VSP** — WebSocket debug/RFC/RunReport require it installed on SAP
6. **Response cache** (`VSP_CACHE`, `pkg/adt/response_cache.go`) keeps GET answers and stable-table data preview queries for a TTL; it is emptied on any write through the client. A change made by someone else within that window is invisible to it — delete `VSP_CACHE_PATH`'s file, or wait it out

## Security

Never commit `.env`, `cookies.txt`, `.mcp.json`, or local agent/MCP config files (all in `.gitignore`).

### Sanitize policy for tracked docs, tests, and examples

The public repo must not contain concrete identifiers that tie code or
docs to a live SAP system, a real user, or a customer's ABAP namespace.
Anything that does belongs under `.local/` (gitignored) and never in
`contexts/`, `reports/`, `docs/`, or any tracked test fixture.

**Never in tracked files:**
- Real SAP usernames — use `TESTUSER`
- Real hostnames or IPs — use `dev.example.local`, `prodsys-a.example`, `trialsys.example`
- System aliases that name a live box — use `devsys`, `devsys-adt`, `prodsys-a`, `prodsys-b`
- Live transport numbers (`DEVK[0-9]+`, `R[0-9]{2}K[0-9]+`, `D[0-9]{2}K[0-9]+`) — use `TR-EXAMPLE`
- Live change request IDs — use `CR-EXAMPLE`
- Customer ABAP namespaces from real projects — use synthetic `ZDEMO_*`, `ZCL_DEMO_*`, `ZIF_DEMO_*`, `$ZDEMO`
- Customer transport attribute names — use `Z_CR_ATTR`
- Real passwords, API keys, bearer tokens (obvious, but stated)
- Real person names tied to private systems (OSS attribution for upstream libraries is fine — "user X on private host Y" is not)

**Always OK in tracked files:**
- `$ZHIRTEST*`, `ZCL_HIRT*`, `ZCUSTOM_DEVELOPMENT` — pre-agreed synthetic fixtures
- Public GitHub handles that are already in the Go module path
- Upstream OSS attribution for library authors

**Operational scratch goes under `.local/`** — session notes, live CR
dumps, bug repros with real identifiers, debugging transcripts. The
`.local/` dir is gitignored. If you need to reference it from a
tracked doc, redact first.

**Before every commit that touches `reports/`, `contexts/`, `docs/`,
or test fixtures:** scan the staged diff for the identifier families
above. The detection signature (concrete literal list of past-leaked
strings) lives at `.local/scripts/check-identifiers.sh` and is
gitignored on purpose — the signature itself would otherwise be the
leak it is trying to prevent. Structural patterns safe to commit:

```bash
git diff --cached | grep -nE \
  '\b[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\b|' \
  '\b[A-Z][0-9]{2}K[0-9]{6}\b|' \
  '\bDEVK[0-9]{6,}\b'
```

That catches IPv4 literals and SAP transport IDs without hardcoding
a specific customer's values. Pair it with the private signature
file for the names-based families (usernames, hostnames, ABAP object
prefixes). If either matches, move the content under `.local/` and
replace the tracked version with a synthetic placeholder. Rule of
thumb: "would a stranger reading this file be able to identify the
customer, the system, or a live account?" If yes, redact.

## Conventions

Reports: `reports/YYYY-MM-DD-NNN-title.md`. SAP objects: `ZADT_<nn>_<name>`, `ZCL_ADT_<name>`, packages `$ZADT*`.

---

## Areas Requiring Care

| Area | Risk | Notes |
|------|------|-------|
| `pkg/graph/` | New, incomplete | Only parser adapter; SQL/ADT adapters pending |
| `handlers_debugger.go` | WebSocket-only | REST breakpoints 403 on newer SAP; use ZADT_VSP |
| `handlers_amdp.go` | Experimental | Session works, breakpoints unreliable |
| `pkg/adt/ui5.go` | Read-only | Write needs `/UI5/CL_REPOSITORY_LOAD` |
| `pkg/llvm2abap/`, `pkg/wasmcomp/` | Research | Not production; don't treat as stable |
| `pkg/adt/debugger.go` (REST) | Deprecated | Prefer `websocket_debug.go` |
| `docs/cli-agents/*` | Config drift | Codex TOML format may differ from Claude/Gemini JSON docs |

---

> Workflow dual-server (vsp + mcp-abap-abap-adt-api): ver `~/.claude/sap-mcp-servers.md` (cargado globalmente).
