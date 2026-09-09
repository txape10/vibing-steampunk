# CLAUDE.md

**vsp** — Go-native MCP server and CLI for SAP ABAP Development Tools (ADT).

> **Doc intent:** CLAUDE.md = dev context. README.md = user onboarding. reports/ = research/history. contexts/ = session handoff.

---

## Current Priorities

### 1. Bug #132 — ALL objects fail PUT with 423 (on-prem) — FIXED (2026-05-28)
- Root cause: the unified mutation gate inside `UpdateSource`/`UpdateClassInclude` ran `getObjectPackage →
  SearchObject` (a **stateless** hop) between the stateful Lock and the stateful PUT. SAP ICM retired the
  stateful session on the stateless hop (`ICMENOSESSION`), invalidating the lock handle → HTTP 423.
- Fix (PR #125): `mutationGateSkipKey` context flag. Outer workflows mark the context after their own gate
  call; inner mutators see the flag and skip their redundant gate, eliminating the stateless hop.
  Files: `pkg/adt/mutation_gate.go`, all `workflows_*.go`.
- Also included: CSRF HEAD→GET fallback (`pkg/adt/http.go`), `corrNr` adoption from lock result
  (`workflows_edit.go`, `workflows_source.go`), `NoModification+CorrNr` guard (`crud.go`).
- Verified on-prem without `SAP_SESSION_TYPE=stateful`: PROG, PROG/I, CLAS all edit+activate correctly.
- `SAP_SESSION_TYPE=stateful` is **no longer needed** in the MCP config.
- Investigation: [004](reports/2026-05-27-004-issue-132-investigation.md) | Issue: [#132](https://github.com/oisee/vibing-steampunk/issues/132)

### 1b. Bug — $TMP objects blocked by NoModification guard — FIXED (2026-05-28)
- Root cause: `LockObject` in `crud.go` rejected `NoModification + corrNr=""` assuming "read-only", but
  local package objects (`$TMP`) also have `corrNr=""` since they need no transport. The guard missed
  `IsLocal=true` in the lock response, which signals the lock handle IS valid for a local object.
- Fix: `if result.CorrNr == "" && !result.IsLocal` — one extra condition. `IsLocal` was already parsed
  from `IS_LOCAL` in the lock XML; the guard simply wasn't using it. File: `pkg/adt/crud.go`.
- Verified on-prem: surgical edit of a class in `$TMP` (`ZCL_TST_STOCK_PARTIDAS`) succeeds — lock,
  syntax check, write, activate all pass. Commit: `4d4adfc`.

### 2. Bug #133 — INCL package resolution + isClassInclude detection — FIXED (2026-05-27)
Two bugs found and fixed:
- `normalizeObjectURLForPackageCheck` stripped `/programs/includes/NAME` to `/programs` (matched the
  `/includes/` class-strip before the `/source/main` strip). Fix: reorder strips, scope `/includes/` strip
  to `/oo/classes/` URLs only. File: `pkg/adt/client.go`.
- `isClassInclude` in `EditSourceWithOptions` fired on any URL containing `/includes/`, making program
  includes skip `/source/main` and send wrong Accept header (406). Fix: require `/oo/classes/` too.
  File: `pkg/adt/workflows_edit.go`.
- Verified on-prem: source reads correctly (`matchCount: 1`). Write fails with #132 (same as CLAS) — not #133.
- Issue: [#133](https://github.com/oisee/vibing-steampunk/issues/133)

### 2b. Bug #136 — Activation silently returns success=true on S/4HANA — FIXED (2026-06-03)
Two separate bugs, both in `parseActivationResult` (`pkg/adt/devtools.go`):
- **Bug A** (commit `dedfebc`): Go `encoding/xml` doesn't match namespace-qualified attributes
  (`adtcomp:type="E"`) against unqualified struct tags (`xml:"type,attr"`). Fix: `stripXMLNamespaces()`
  removes all namespace declarations and prefixes before `xml.Unmarshal`. Also fixes `parseSyntaxCheckResults`.
- **Bug B** (commit `b2f0564`): S/4HANA on-prem returns `<chkl:messages>` root element
  (namespace `http://www.sap.com/abapxml/checklist`), NOT `<adtcomp:activationLog>`. The old parser
  only branched on `<activationLog>` → found nothing → `success: true` always. Fix: format detection
  (`strings.Contains(xmlStr, "<activationLog")`) + Format B parser that reads `<properties activationExecuted="false"/>`.
  Also fixed `ShortText` parsing: SAP sends multiple `<txt>` children (primary + localized key), requiring
  `Txts []string \`xml:"txt"\`` instead of `Text string`.
- Verified on-prem: include with real syntax error now returns `success:false` + error message with line number.
- Issue: [#136](https://github.com/oisee/vibing-steampunk/issues/136)

### 2c. ActivateMultiple — IMPLEMENTED & VERIFIED (2026-06-03)
- `ActivateMultiple(ctx, []ObjectRef)` in `pkg/adt/devtools.go` — single POST with all object references.
- SAP resolves mutual dependencies within the group (same as Eclipse ADT).
- `ActivatePackage` updated to use batch instead of N individual calls.
- MCP: `SAP(action="edit", target="ACTIVATE_MULTI", params={"objects": ["PROG ZPROG", "INCL ZPROG_F01"]})`
- Verified on-prem with real includes that have mutual type dependencies. Commit: `c741c69`.
- Issue: [#137](https://github.com/oisee/vibing-steampunk/issues/137)

### 2d. Bugs #143/#144 — WriteSource package resolution + transport adoption from lock — FIXED (verified on-prem)
- **#143**: `WriteSource` update path never resolved the object's package before running the mutation gate,
  so `AllowedPackages` policy checks failed closed on explicit `Mode: WriteModeUpdate`. Fix: the gate call
  now passes `ObjectURL` when `opts.Package == ""`, letting `checkMutation` resolve the package via
  `SearchObject` itself. File: `pkg/adt/workflows_source.go`.
- **#144**: when the object was already captured in an open transport, SAP returns that `corrNr` on the
  LOCK response; adopting it (instead of leaving transport empty) avoids a spurious 409
  `ExceptionResourceLockConflict`. Fix applied at the `WriteProgram`/`WriteInclude`/`WriteClass` call sites.
- Live-verified against the real SAP system: `ZTESTRCG1` ($TMP, full-source `WriteSource → WriteProgram`
  path) and `ZREDI_CREATE_IDOC_FILE` (PROG in `ZABAP01`, already captured in an open transport). Both test
  objects were restored to their original content after verification.

### 2e. Security fix — transport adoption from lock bypassed `--allow-transportable-edits` policy — FIXED (upstream PR #145 gap)
- Upstream PR #145 (`zooloo303`) fixes #144 too, but additionally re-validates `CheckTransportableEdit`
  after adopting `lock.CorrNr` — the #144 fix above was missing that re-check, so a transport discovered
  only *after* Lock (not known yet when the top-level gate first ran) could silently bypass
  `AllowTransportableEdits`/`AllowedTransports`.
- Fix: new `resolveWriteTransport(supplied, lockCorrNr, opName string) (string, error)` in `pkg/adt/client.go`
  — returns the supplied transport unchanged if present, `""` if the object has no lock transport, otherwise
  re-runs `checkTransportableEdit` before adopting `lockCorrNr`.
- Applied at all 6 call sites that adopt a transport from a lock result: `WriteProgram`, `WriteInclude`,
  `WriteClass` (`workflows.go`), `EditSourceWithOptions` (`workflows_edit.go`),
  `DeleteObjectWithAutoLock` (`crud.go`), `writeClassMethodUpdate` (`workflows_source.go` — found by a
  code-reviewer CRITICAL finding after the first five sites were fixed).
- Tests: 5 new unit tests for `resolveWriteTransport` (`mutation_gate_test.go`) + adoption/blocking coverage
  for all 6 call sites (`workflows_test.go`). Code-reviewed: 0 CRITICAL/HIGH (1 MEDIUM investigated and
  confirmed a false positive — Go `defer` never registers past an earlier `return`).
- GitHub: reacted 👍 to upstream PR #145 (no text comment, per user decision — the equivalent fix was
  built independently in this fork rather than cherry-picked, since this fork's `WriteX` call sites differ
  from upstream's).

### 2f. Bug — `WriteSource` `objectExists` never computed under explicit Update/Create mode — FIXED
- The 9-type existence-check switch (PROG/CLAS/INTF/INCL/DDLS/BDEF/SRVD/SRVB/FUNC) in `WriteSource` was
  gated behind `if opts.Mode == WriteModeUpsert`, so explicit `Mode: WriteModeUpdate` always treated the
  object as nonexistent (failing every explicit update with "does not exist"), and explicit
  `Mode: WriteModeCreate` never detected an already-existing object (never blocking accidental
  re-creation). Fix: removed the `if` guard so the switch always runs regardless of mode.
  File: `pkg/adt/workflows_source.go`.
- New tests: `TestClient_WriteSource_Update_ExplicitMode_ObjectNotExists`,
  `TestClient_WriteSource_Create_ExplicitMode_ObjectAlreadyExists` (the latter had zero prior coverage).
- Code-reviewed: 0 CRITICAL/HIGH/LOW. 1 MEDIUM (informational, pre-existing, not introduced by this fix):
  `writeSourceUpdate`'s PROG case doesn't mark `withMutationGateAlreadyRan` before delegating to
  `WriteProgram`, causing a redundant second `SearchObject` call under `AllowedPackages` — a performance
  inefficiency, not a correctness/security issue. Not fixed; noted here for a future optimization pass.

### 2g. Text pool (program text elements) — WORKING via WebSocket/ZADT_VSP (REST attempt reverted)
- **Read AND write already work end-to-end** via the pre-existing `ZADT_VSP` WebSocket service (domain
  `report`, actions `getTextElements`/`setTextElements`) — no new code was needed. Verified live against
  the real SAP system (read + a throwaway text-symbol write on `ZTESTRCG1`, approved by the user).
  - `SAP(action="debug", target="GET_TEXT_ELEMENTS", params={"program": "ZTESTRCG1", "language": "ES"})`
  - `SAP(action="debug", target="SET_TEXT_ELEMENTS", params={"program": "ZTESTRCG1", "text_symbols": "{\"001\": \"...\"}"})`
- A separate REST-based attempt (`GetTextPoolInLanguage` at `/sap/bc/adt/programs/programs/{name}/textelements`,
  routed via `routeI18nAction`) was built this session and found to return HTTP 404 "No suitable resource
  found" on this SAP system for both a `$TMP` test object and a real production program. Since the
  WebSocket path already covers the same need, the REST wiring (`routeI18nAction` in
  `internal/mcp/handlers_i18n.go`, its registration in `internal/mcp/handlers_universal.go`'s route chain,
  and the `TEXT_POOL` mention in `internal/mcp/handlers_help.go`) was **reverted in full** — it was never
  committed, so the revert left no trace. `GetTextPoolInLanguage` itself (`pkg/adt/i18n.go`, pre-existing
  from PR #42) was left untouched since it predates this session and may still be useful for objects where
  the WebSocket service isn't deployed.
- ⚠️ **`heading_texts` parameter is non-functional**: `SetTextElements`/`handleSetTextElements` accept and
  forward a `heading_texts` map to SAP, but the live `ZCL_VSP_REPORT_SERVICE=>handle_set_text_elements`
  never reads it (only id='S' selection texts and id='I' text symbols are round-tripped through
  `READ TEXTPOOL`/`INSERT TEXTPOOL`). Always reports `heading_texts_set: 0`, silently. Confirmed against
  the live ABAP source. Kept as-is (not removed) per user decision, documented via code comments in
  `pkg/adt/reports.go` (`SetTextElementsParams`) and `internal/mcp/handlers_report.go` (`handleSetTextElements`).

### 2h. RFC domain `SEARCH`/`GET_METADATA` — IMPLEMENTED (2026-07-29)
- `ZCL_VSP_RFC_SERVICE` (domain `rfc`) already exposes `search` (function module name lookup via
  `TFDIR`, `*` wildcard) and `getMetadata` (full IMPORT/EXPORT/CHANGING/TABLES signature via
  `FUNCTION_IMPORT_INTERFACE`) — same as upstream — but neither had Go/MCP wiring on either this fork or
  upstream `oisee/vibing-steampunk` (confirmed via a byte-for-byte diff of `pkg/adt/websocket_rfc.go`).
  Checked the two most-diverged forks too (`BurnerPat/vsp-enterprise`, 109 commits ahead — took a
  different Java/JCo-sidecar RFC architecture instead; `marianfoo/vibing-steampunk`, 38 commits ahead —
  never ported the Go report/RFC client at all): neither has this either.
- New Go client methods in `pkg/adt/websocket_rfc.go`, following the existing `CallRFC`/`MoveObject`
  pattern (`SendRawRequest`, 30s timeout): `Search(ctx, pattern) ([]RFCSearchResult, error)`,
  `GetMetadata(ctx, function) (*RFCMetadataResult, error)`. Response schemas were read directly from the
  live-verified ABAP source (`src/zcl_vsp_rfc_service.clas.abap`) rather than guessed: `search` returns a
  JSON array `[{"name": "..."}]`; `getMetadata` returns `{"function": "...", "parameters": [{"name",
  "kind": "importing"|"exporting"|"changing"|"tables", "type", "optional"}]}`.
- New MCP handlers `handleRFCSearch`/`handleRFCGetMetadata` in `internal/mcp/handlers_debugger.go`, routed
  as `RFC_SEARCH`/`RFC_METADATA` in the existing `routeDebuggerAction` (already in the universal tool's
  route chain — no change needed there). Help text updated in `internal/mcp/handlers_help.go`.
  - `SAP(action="debug", target="RFC_SEARCH", params={"pattern": "BAPI_USER*"})`
  - `SAP(action="debug", target="RFC_METADATA", params={"function": "BAPI_USER_GET_DETAIL"})`
- No unit tests added — matches existing coverage level for the RFC domain (`CallRFC`/`MoveObject` also
  have no unit tests; these are thin WebSocket wrapper methods in the same style).

### 2i. CSRF token fetch — HEAD 403 wrongly treated as auth failure, blocked GET fallback — FIXED (2026-07-29)
- Root cause: `fetchCSRFToken` (`pkg/adt/http.go`) excluded both 401 and 403 from the HEAD→GET fallback,
  assuming both meant "bad credentials". Upstream issue [#104](https://github.com/oisee/vibing-steampunk/issues/104)
  documents (with `curl` repro + two independent user confirmations) that on some systems — **S/4HANA
  public cloud, and on-prem 2023 FPS03+, which is this project's own connected system** — the ICF handler
  `CL_ADT_WB_RES_APP` simply doesn't implement HEAD and returns 403 with no CSRF token, while the same user
  works fine via GET (what Eclipse ADT uses). Only 401 is a genuine auth failure.
- Fix: `headIsAuthFailure` now checks only `http.StatusUnauthorized`; 403 falls through to the existing GET
  fallback. File: `pkg/adt/http.go`.
- Tests: `TestFetchCSRFToken_Head403_FallbackToGet`, `TestFetchCSRFToken_Head401_NoFallback` (`http_test.go`).
- Ported from upstream #104's community-verified fix rather than invented independently — see the issue for
  the original diagnosis and diff this fork's fix follows.

### 2j. `InstallZADTVSP` reported "✓ Deployed" even when the write failed — FIXED (2026-07-29)
- Root cause: the deploy loop in `handleInstallZADTVSP` (`internal/mcp/handlers_install.go`) called
  `WriteSource` without `Description` and discarded the result (`_, err :=`), checking only `err`. A
  `WriteSource` failure that returns `(result{Success:false}, nil)` — the normal way this codebase reports
  failures — was silently printed as "✓ Deployed", potentially leaving empty/broken object shells while
  reporting success. Matches upstream issue [#138](https://github.com/oisee/vibing-steampunk/issues/138)
  exactly (verified there against a live S/4HANA 2025 install).
- Fix: pass `Description: obj.Description`; capture `result, err :=` and treat `!result.Success` as a
  failure too (reporting `result.Message`). File: `internal/mcp/handlers_install.go`.
- Not ported: #138 also proposes marking `ZCL_VSP_AMDP_SERVICE` optional (its `if_amdp_dbg_*` dependency is
  absent on S/4HANA 2025) — out of scope here since this fork targets S/4HANA 2023 FPS03, left for a future
  session if AMDP install failures are seen on newer releases.

### 2k. `GetCallGraph` sent generic Content-Type, 415 on systems with CAI enabled — FIXED (2026-07-29)
- Root cause: `GetCallGraph` (`pkg/adt/client.go`) POSTed to `/sap/bc/adt/cai/callgraph` with
  `ContentType: "application/xml"` (generic). SAP's Code Analytics Infrastructure (CAI) endpoint requires
  the specific media type `application/vnd.sap.adt.cai.callgraphconfig.v1+xml` and returns 415 otherwise.
  Matches upstream issue [#142](https://github.com/oisee/vibing-steampunk/issues/142).
- Fix: one-line `ContentType` change. Also fixes `GetCallersOf`/`GetCalleesOf`, which are thin wrappers
  around `GetCallGraph`. File: `pkg/adt/client.go`.
- Test: `TestClient_GetCallGraph_ContentType` (`client_test.go`) asserts the header sent to the mock
  transport. Along the way, fixed an unrelated latent bug in the shared test helper `newTestResponse`
  (constructed `http.Header{"X-CSRF-Token": ...}` via a non-canonical map literal, which `Header.Get`
  can't find since it canonicalizes the lookup key to `X-Csrf-Token` — invisible until a test actually
  needed a real mutating call to complete, which none did before this one).

### 2l. Flaky `TestListRecordings` — recording ID collision on coarse Windows clock resolution — FIXED (2026-07-29)
- Root cause: `generateRecordingID()` (`pkg/adt/recorder.go`) formatted `time.Now()` down to nanoseconds
  for uniqueness, but the underlying OS clock resolution (notably on Windows) can be coarser than a
  nanosecond — two recordings created in a tight loop could get the identical ID string, and `SaveRecording`
  uses the ID both as the filename and as the index's unique key, so a collision silently overwrote the
  earlier entry. Found via test flakiness (`TestListRecordings` failing ~2/6 runs), confirmed unrelated to
  any other change in the same commit (0/6 failures on `git stash`, reproducible only with the collision
  mechanism present).
- Fix: added a package-level `atomic.AddUint64` counter suffixed onto the timestamp, guaranteeing
  uniqueness regardless of clock resolution. File: `pkg/adt/recorder.go`.
- Verified: 15/15 passes after the fix (previously flaky). Nothing else in the codebase parses the ID's
  internal structure (grep-confirmed) — it's treated as an opaque string everywhere (filenames, map keys,
  JSON), so the added suffix is safe.

### 2m. `GET_TEXT_ELEMENTS`/`SET_TEXT_ELEMENTS` — language param mishandled + phantom empty entries on delete — FIXED (2026-07-30)
- **Bug 1 — language code**: `ZCL_VSP_REPORT_SERVICE`'s `HANDLE_GET_TEXT_ELEMENTS`/`HANDLE_SET_TEXT_ELEMENTS`
  accept a `language` param that can arrive as either a 1-char SAP code (`"S"`) or a 2-char ISO code
  (`"ES"`). The old code (`lv_lang = lv_language(1)`) just truncated to 1 char, which is wrong whenever the
  ISO code's first letter doesn't match the SAP code (Spain: ISO `"ES"` → SAP `"S"`, not `"E"`). Fix: use
  the standard SAP conversion exit `CONVERSION_EXIT_ISOLA_INPUT`; on `unknown_language`/`OTHERS`, return a
  proper `INVALID_LANGUAGE` error instead of silently continuing with a wrong code. Files:
  `src/zcl_vsp_report_service.clas.abap`, `embedded/abap/zcl_vsp_report_service.clas.abap`.
- **Bug 2 — phantom entries on delete**: calling `SET_TEXT_ELEMENTS` with an empty value (`""`) for an
  existing key always wrote an (empty) row back into `lt_textpool` instead of removing it — leaving
  "phantom" empty-value entries in the real SAP text pool after `INSERT TEXTPOOL`. Fix: `IF lv_val IS
  INITIAL. DELETE lt_textpool WHERE id = ... AND key = ... .` for both selection texts (`id = 'S'`) and text
  symbols (`id = 'I'`).
- **Documentation gap (not fixed, stated as-is)**: the language param's real semantics (1-char SAP vs.
  2-char ISO, conversion exit involved) were not documented anywhere — neither in `SAP(action="help")` nor
  in code comments — only a bare example value existed in `sap-mcp-servers.md`. Left undocumented in the
  tool's own help text; noted here instead.
- Verified live against the real SAP system (S/4HANA on-prem 2023 FPS03): rewrote and then genuinely
  deleted a selection text (`PA_TOLER`) on a production program (`ZRPP_VOLCADOS_VS_FLEJADOS`), confirmed via
  `GET_TEXT_ELEMENTS` after each step (rewritten text appeared correctly; deleted key showed `(none)`
  instead of a phantom empty entry).
- Code-reviewed: 1 MEDIUM raised (confirm the `DELETE lt_textpool` isn't inside a `LOOP AT lt_textpool`,
  which would be undefined behavior) — verified false positive: the enclosing loop is `WHILE lv_work CS
  '"'`, a manual JSON parser advancing a string offset (`lv_work`), not a `LOOP AT lt_textpool`; the DELETE
  targets a separate internal table it never iterates over. 0 CRITICAL/HIGH remained.

### 2n. `GetTableContents` — DDIC endpoint sent as GET instead of POST, 400 "Tabla/Vista no existe" on every table — FIXED (2026-07-31)
- Root cause: a self-inflicted regression in this fork's own commit `4d731ca` (2026-06-02, "INCL write
  support + DELETE auto-lock + RunQuery/tableContents fixes"). That refactor added the sqlFilter→freestyle
  routing branch to `GetTableContents` and, in the process, dropped the `Method: http.MethodPost` that the
  no-filter branch's `RequestOptions{}` literal used to have — leaving `Transport.Request` to apply its
  default `http.MethodGet` (`pkg/adt/http.go:177`). SAP's `/sap/bc/adt/datapreview/ddic` endpoint only
  accepts POST (documented in `docs/adt-api-reference.md:304`; upstream `oisee/vibing-steampunk` still has
  `Method: http.MethodPost` there, and this file's own `runFreestyleQuery` sibling already had it correctly)
  — so every unfiltered table-contents read failed with a generic 400 `ExceptionDataPreviewGeneral`
  ("Tabla/Vista no existe") regardless of whether the table existed. No upstream issue or PR mentions this;
  confirmed fork-specific via a GitHub search across `oisee/vibing-steampunk` issues/PRs (zero hits) and a
  diff against upstream's current `client.go`.
- Fix: restore `Method: http.MethodPost` in that one `RequestOptions{}` literal. File: `pkg/adt/client.go`.
- Verified live against the real SAP system: `T001` (standard table) and `ZTSU_SOC_AFE` (customer table)
  both returned 400 before the fix and correct data — including full column metadata (Spanish descriptions,
  lengths) — after rebuilding and redeploying the binary.
- No unit tests existed for `GetTableContents` (only `pkg/adt/integration_test.go`, which doesn't pin the
  HTTP method), so there was no regression risk from the fix itself. Code-reviewed: 0
  CRITICAL/HIGH/MEDIUM/LOW.

### 2o. `GetTrace` (SAT/ATRA hitlist) — trace_id URI not normalized + wrong XML schema assumed — FIXED (2026-08-12)
- **Bug 1 — trace_id**: `list_traces` returns each trace's `id` as the full ADT URI
  (`/sap/bc/adt/runtime/traces/abaptraces/<GUID>`), but `GetTrace` concatenated that straight into the
  hitlist endpoint path without stripping the prefix, producing a doubled path and a 404 whenever a caller
  passed the `id` as returned instead of the bare GUID. Fix: `normalizeTraceID()` strips the known prefix if
  present, passes bare GUIDs through unchanged.
- **Bug 2 — XML schema**: `parseTraceAnalysis` assumed flat attributes on `<entry>` (`program`, `event`,
  `line`, `grossTime`, `netTime`, `calls`, `percentage`) that don't exist in the real ADT response — every
  hitlist read silently returned empty `{}` entries regardless of trace_id correctness. The real schema
  (captured live) nests `hitCount`/`description` as attributes and `callingProgram`/`grossTime`/
  `traceEventNetTime` as sub-elements. Confirmed no reference implementation had this either —
  `marcellourbani/abap-adt-api` (`build/api/traces.js`, the library this fork's own backup MCP server
  depends on) implements `tracesList`/`tracesHitList`/`tracesDbAccess`/`tracesStatements`, so its TS parser
  schema was cross-checked, then the exact shape was verified against a real S/4HANA response before writing
  the fix — not inferred from the TS types alone. `statements`/`dbAccesses` tool types remain intentionally
  unimplemented (`parseTraceAnalysis` returns empty entries, no schema guessed) — not live-verified.
- Files: `pkg/adt/client.go` (`normalizeTraceID`, `GetTrace`, `parseTraceAnalysis`, `hitlistXML` types).
- Verified live against the real SAP system: read two real SAT traces (modes "OLD" vs "NEW" of the same
  report, ZSU0088) end-to-end through the MCP tool, both fully populated (4.891 and 4.756 entries), used to
  do an actual before/after performance comparison (2.341ms → 50.36ms).
- Tests: `pkg/adt/trace_test.go` — `TestNormalizeTraceID`, `TestParseTraceAnalysis_Hitlist` (real 2-entry
  fixture captured live), `TestParseTraceAnalysis_UnverifiedToolTypesReturnEmpty`.
- Code-reviewed: 0 CRITICAL/HIGH/MEDIUM, 1 LOW (optional — `normalizeTraceID` doesn't explicitly guard
  empty-string input; SAP would return a clear error anyway, left as-is). Verdict: approve.
- **Deploy gotcha (cost real time)**: the binary Claude Desktop actually loads is
  `C:\Users\rchapado\AppData\Local\VSP\vsp.exe` — building with `go build -o .../vsp` (no `.exe` extension)
  produces a file Windows `CreateProcess` cannot resolve via bare-command PATH lookup (PATHEXT resolution
  only applies to bare command names, not explicit paths, and only for names ending in a known extension),
  so the MCP server silently failed to start after the first deploy. Always build/copy to `vsp.exe`
  explicitly.

### 2p. `GetSQLTraceState` (ST05) — 406 (wrong Accept header) + wrong XML schema assumed — FIXED (2026-08-12)
- **Bug 1 — Accept header**: sent `application/xml`; SAP's `/sap/bc/adt/st05/trace/state` only accepts
  `application/vnd.sap.adt.perf.trace.state.v1+xml` (SAP's own 406 error body states the required
  media type explicitly — no guessing needed).
- **Bug 2 — XML schema**: after fixing the Accept header, the real body is a per-application-server-instance
  table (`<traceStateInstanceTable><traceStateInstance>...</traceStateInstance>...</traceStateInstanceTable>`,
  one instance per app server, each with `instance`/`host`/`isLocal`/`isSelected`/`modificationUser`/
  `modificationDateTime`/`traceTypes` (`sqlOn`/`bufOn`/`enqOn`/`rfcOn`/`httpOn`/`apcOn`/`amcOn`/`authOn`) —
  not the flat `<traceState active="..." user="..." .../>` the old parser assumed (which doesn't exist).
  `marcellourbani/abap-adt-api` has **zero** ST05 support (grep-confirmed, `build/api/traces.js` only
  implements `abaptraces`/SAT) — there is no reference implementation for ST05 anywhere; today's live
  capture is the only evidence this schema is based on.
- Files: `pkg/adt/client.go` (`SQLTraceState`, `SQLTraceInstanceState`, `SQLTraceTypes`, `parseSQLTraceState`).
- Verified live: real system returned one instance (`srvdevsaps4d_S4D_00`), all trace types off (expected —
  captured right after the user manually deactivated their SQL trace).
- Tests: `pkg/adt/trace_test.go` — `TestParseSQLTraceState` (real fixture captured live 2026-08-12).
- Code-reviewed together with 2q below: 0 CRITICAL/MEDIUM, 1 HIGH (fixed same session — `ListSQLTraces`'s
  MCP tool registration still advertised now-removed `user`/`max_results` params and a stale description;
  corrected in `internal/mcp/tools_register.go`), 1 LOW (pre-existing, unrelated `fmt.Sscanf` error-ignoring
  pattern in the SAT hitlist parser — not blocking). Verdict: approve after the HIGH fix.

### 2r. `GetUserTransports` — returned empty despite user having real modifiable transports — FIXED then SUPERSEDED (2026-09-07, see 2s)
- Matches upstream issue [#111](https://github.com/oisee/vibing-steampunk/issues/111). Symptom:
  `get_user_transports` reported no requests for a user that demonstrably had one (`S4DK928661`, confirmed
  via `get_transport` and `list_transports`).
- **Root cause (confirmed live via `VSP_HTTP_TRACE=1` raw XML capture, not guessed)**: `GetUserTransports`
  calls `/sap/bc/adt/cts/transportrequests?user=...&targets=true`. On this system, with `targets=true`,
  SAP genuinely returns a bare, childless `<tm:root/>` (300 bytes) — not a parsing bug, SAP itself gives
  nothing. Ruled out the `<tm:project>`-wrapping theory from
  [abap-adt-api#46](https://github.com/marcellourbani/abap-adt-api/issues/46): no such wrapper appears
  anywhere in the response. Cross-checked: the same query **without** `targets=true` (what `ListTransports`
  sends) returns a real, 18KB+ populated tree — but in a shape (`<tm:workbench><tm:released><tm:request>`,
  no `<tm:target>` layer) that `parseTransportList` doesn't handle either, so it also parsed to empty and
  silently fell through to `ListTransports`'s `listTransportsViaSQL` (E070/E07T) fallback.
- **Original fix (this entry, now replaced)**: a narrow SQL fallback with a one-off converter
  (`convertTransportSummaryToUserTransports`). Verified live at the time: `get_user_transports` listed
  `S4DK928661` and 74 other requests, matching `list_transports`.
- **Superseded same day by 2s**: upstream merged [PR #173](https://github.com/oisee/vibing-steampunk/pull/173)
  (closes the same #111) with a strictly more general fix — a shape-tolerant tree parser instead of a
  fallback-on-empty — plus an independent wildcard-user bug (#140) this entry never touched. Ported
  in full; `convertTransportSummaryToUserTransports` no longer exists. See 2s for the current state.

### 2s. Ported upstream PR #173 — shape-tolerant CTS transport parser, replacing 2r's narrower fix (2026-09-07)
- Upstream's [#173](https://github.com/oisee/vibing-steampunk/pull/173) (merged) rewrites transport parsing
  entirely instead of falling back to SQL when the tree looks empty: `/sap/bc/adt/cts/transportrequests`
  answers with a `tm:root` whose depth depends on the system — `workbench>target>modifiable>request` with
  transport targets configured, the same without the `target` level, or a flat `tm:request` straight under
  `tm:root` for a single-request document. The two old hand-written parsers each hardcoded one shape;
  `encoding/xml` answers a non-matching path with an empty struct and a nil error, so the mismatch read as
  "no transports" rather than a parse failure.
- **Fix**: new `pkg/adt/transport_tree.go` — `parseCTSRequests` walks the document namespace-agnostically
  (`ctsNode`/`ctsRequest`/`ctsObject`/`ctsScope`) instead of asserting a path, collecting every `tm:request`
  wherever it sits and remembering the section/target/bucket it was nested in. `parseUserTransports` and
  `parseTransportList` in `transport.go` now delegate to it. `GetUserTransports` keeps a fallback to
  `userTransportsViaSQL` (replaces 2r's `convertTransportSummaryToUserTransports`) only when the tree
  parses to genuinely empty.
- **Bonus fix, not in 2r**: `as4userPredicate()` fixes upstream issue
  [#140](https://github.com/oisee/vibing-steampunk/issues/140) — `listTransportsViaSQL` compared
  `AS4USER = '*'` literally, so asking for every user's transports (`user="*"`, the wildcard Eclipse ADT
  uses) matched no row and looked like an empty system. `'*'` now drops the predicate; a `*` inside a name
  becomes a `LIKE` prefix. The same function also closes the MEDIUM finding 2r's review left open
  (unescaped string concatenation of `user` into SQL): it validates the name against a character whitelist
  (letters, digits, `_ - . / *`) before interpolating — refuses anything that could close the SQL literal
  instead of quoting it. **The standalone follow-up task flagged for that MEDIUM (`task_c207b70e`,
  "Parametrizar user en listTransportsViaSQL") is now moot and was withdrawn.**
- One upstream identifier (`firstNonEmpty`) was referenced by the ported diff without being defined in it
  (must already exist elsewhere in upstream's codebase) — added locally at the bottom of
  `transport_tree.go` since this fork had no equivalent.
- Ported `pkg/adt/transport_tree_test.go` (12 tests, `httptest`-based, no live SAP needed) verbatim — already
  sanitized upstream (`TESTUSER`, `TR-EXAMPLE-*`, `/ZDEMO/`). Removed 2r's 3 tests for the now-deleted
  `convertTransportSummaryToUserTransports`; existing `TestParseUserTransports`/`TestParseUserTransportsEmpty`
  needed no changes and still pass. One-line doc tweaks in `cmd/vsp/devops.go` and
  `internal/mcp/tools_register.go` (mention `'*'` for every user).
- Files: `pkg/adt/transport_tree.go` (new), `pkg/adt/transport_tree_test.go` (new), `pkg/adt/transport.go`,
  `pkg/adt/transport_test.go`, `cmd/vsp/devops.go`, `internal/mcp/tools_register.go`.
- `go build ./...` clean, `go test ./pkg/... ./internal/...` (minus the always-failing `pkg/cache` CGO
  case) green. Code-reviewed: 0 CRITICAL/HIGH. 1 MEDIUM (informational — `firstNonEmpty` is a generic-purpose
  helper placed in a transport-specific file; not a correctness issue). 2 LOW (informational — the SQL
  fallback path no longer explicitly drops an unreachable "unknown transport type" case the old code did,
  but that branch is provably unreachable given `listTransportsViaSQL`'s own `WHERE ... IN ('K','W')`; and
  `_` acts as a single-character SQL wildcard when a user name mixes `_` and `*`, a low-probability
  functional nuance, not a security issue).
- **Verified live** (same session, after deploying the rebuilt binary): `get_user_transports` and
  `list_transports` now return the identical set (53 workbench + 19 customizing requests, including
  `S4DK928661`) on the real system — previously they disagreed, which was #111's exact symptom.

### 2t. Ported upstream PR #167 — the rest of issue #91 (session affinity beyond #132/#133) (2026-09-07)
- Our own #132/#133 fixes (see "1." above) closed *a* stateless-hop-between-lock-and-write bug — the
  `getObjectPackage → SearchObject` hop inside `UpdateSource`'s mutation gate — using a boolean
  `mutationGateSkipKey` that made an outer workflow's gate skip *all three* of the inner gate's checks
  (op-type, package, transport) as a unit. Upstream's [PR #167](https://github.com/oisee/vibing-steampunk/pull/167)
  (merged, closes the live leg of upstream #91) diagnosed the same root cause independently and fixed it
  more precisely, plus found three more mutations with the identical defect that #132/#133 never touched.
  Ported in full, migrating away from our own boolean mechanism in the process.
- **The security-relevant part**: our old `mutationGateSkipKeyT` skipped the *entire* gate — op-type and
  transport checks included — whenever an outer workflow had run its own gate with a possibly-different
  `Op`. A workflow gated as `OpWorkflow` delegating to an inner mutator that should itself be gated as
  `OpUpdate` meant `--disallowed-ops U` (or `--allow-transportable-edits`) never got enforced on that inner
  call — a real, live policy hole, not just an architectural nicety. Fixed by replacing the boolean with a
  **per-object** marker (`pkg/adt/mutation_gate_marker.go`: `withMutationPackageChecked`/
  `mutationPackageAlreadyChecked`, keyed by `canonicalizeObjectURL`) that skips *only* the networked
  package-lookup step of `checkMutation` (`pkg/adt/mutation_gate.go`), and only for the exact object an
  outer gate already resolved and approved. Steps 1 (op-type) and 3 (transport) now run unconditionally,
  every time. New helper `gateAndMark`/`PrepareSourceUpdate` for the common "gate above the lock, mark the
  context, pass it down" pattern.
- **Two more mutations were themselves stateless, unconditionally, on any configuration** (not just with
  `--allowed-packages` set): `CreateTable`'s source PUT (`pkg/adt/crud.go`) — previously a hand-rolled
  `transport.Request` with no `Stateful` field, so creating a DDIC table 423'd every single time,
  independent of any policy flag — and `WriteMessageClassTexts`' PUT (`pkg/adt/i18n.go`), missing the same
  `Stateful: true`. `CreateTable` also gained the package-ownership check it never had (`checkSafety` alone
  before, no `AllowedPackages` enforcement at all) — a behavior change confirmed inert on this project's own
  config (`--allowed-packages` is not set for the `abap-adt` MCP server here, verified by reading
  `claude_desktop_config.json`'s `args` directly, no secrets involved).
- **A CSRF-refetch hop no configuration gated**: `pkg/adt/http.go`'s token-refresh logic (triggered by no
  cached token, a 403, a session-expiry retry, or a 401) only ever consulted the client-wide
  `SessionType` default, never the statefulness of the in-flight request that triggered it. A stateful write
  whose CSRF probe landed mid-window could retire its own lock's session. This fork's `fetchCSRFToken` has a
  materially different shape from upstream's (no separate `probeCSRFToken`/`fetchCSRFTokenWithReauth` split,
  and it already carries its own HEAD→GET 403 fallback from fix 2i) — so this was **not a mechanical port**,
  it was reimplemented against this fork's actual structure: `fetchCSRFToken(ctx)` is now a thin wrapper over
  new `fetchCSRFTokenFor(ctx, stateful bool)`, and the 4 internal refresh call sites in `Request()` now pass
  `opts.Stateful` instead of relying only on the global default. The keep-alive ping (`Ping()` →
  `fetchCSRFToken` → `fetchCSRFTokenFor(ctx, false)`) is deliberately left non-stateful, unchanged — an
  explicitly-stateful keep-alive would hold a server-side session slot on a timer, a separate tradeoff
  (upstream's own #168, not fixed here either at the time). **#168 itself is now fixed — see 2w below**,
  via a different mechanism (skip the ping entirely during an open lock window) that leaves this
  non-stateful design untouched.
- **`RenameObject`** (`pkg/adt/workflows_fileio.go`) had two independent defects beyond the marker
  migration: an unconditional `defer UnlockObject` *plus* an inline `UnlockObject` on the happy path sent a
  second UNLOCK for a handle already released on every successful rename; and the old object's lock, taken
  to DELETE it, had no `defer` at all — a failed DELETE (reported to the user as "delete manually") left the
  ENQUEUE stranded with nothing said about it. Fixed with explicit `newUnlocked`/`oldReleased` bool flags
  gating each `defer`, both routed through the new `releaseLockAfterFailure`/`strandedLockAdvice` (below).
- **New `pkg/adt/lock_release.go`**: `releaseLockAfterFailure(ctx, objectURL, lockHandle) error` runs the
  compensating UNLOCK on `context.WithoutCancel(ctx)` with its own 30s timeout — every compensating unlock
  in the package used to reuse the caller's `ctx`, so a mutation that failed *because* that context was
  cancelled (an MCP client timeout, Ctrl-C, an HTTP deadline) never sent the UNLOCK at all: it died inside
  `http.NewRequestWithContext` before a byte went out, stranding the ENQUEUE until SAP's own session reaper
  cleared it (~60 min) or someone found it in SM12. `strandedLockAdvice(objectURL, unlockErr) string` turns
  a failed release into an actionable message (own user, not a colleague; SM12 or wait ~60 min; the next
  edit fails with "is currently editing" naming the user themselves).
- **Scope note — Fase C (the "enqueue leak") only partially applied**: `releaseLockAfterFailure`/
  `strandedLockAdvice` were wired into 3 reference sites (`crud.go`'s `CreateTable` and
  `DeleteObjectWithAutoLock`, `workflows_fileio.go`'s `RenameObject`). This fork has **more** such sites than
  upstream's ~14 (roughly 20 remain unmigrated) because of the DDIC-type creators upstream doesn't have
  (`CreateStructure`/`CreateDomain`/`CreateDataElement`/`CreateTableType`/`CreateLockObject` in `crud.go`)
  plus the extra `WriteSource` branches in `workflows_source.go`. Deliberately deferred as a separate,
  mechanical, independently-reviewable follow-up (`task_f620aae2`) rather than done in the same sitting as
  the security-relevant marker migration — not an oversight.
- Files: `pkg/adt/mutation_gate_marker.go` (new), `pkg/adt/lock_release.go` (new),
  `pkg/adt/session_affinity_test.go` (new, 14 of upstream's 15 tests ported —
  `TestSetFunctionModuleProcessingType_HonoursAllowedPackages` omitted, no equivalent standalone function in
  this fork's `WriteSource`/FUNC branch), `pkg/adt/mutation_gate.go`, `pkg/adt/mutation_gate_skip_test.go`
  (old mechanism's 2 unit tests removed, 1 still-valid regression test kept), `pkg/adt/crud.go`,
  `pkg/adt/i18n.go`, `pkg/adt/http.go`, `pkg/adt/workflows.go`, `pkg/adt/workflows_deploy.go`,
  `pkg/adt/workflows_edit.go`, `pkg/adt/workflows_execute.go`, `pkg/adt/workflows_fileio.go`,
  `pkg/adt/workflows_source.go`.
- `go build ./...` clean, full suite green (all 14 ported tests pass, including the one that specifically
  detects the CSRF/statefulness bug fixed in `http.go`). Code-reviewed: 0 CRITICAL/HIGH. 1 MEDIUM — matches
  upstream's own deliberate tradeoff exactly (`RenameObject` with `packageName=""` leaves the new object
  unmarked, so `UpdateSource` re-resolves its package inside the lock window rather than silently skipping
  a check that was never actually run for it; not reachable via the MCP tool, which requires `packageName`).
  1 LOW (fixed same session — a comment in `mutation_gate_skip_test.go` still named the retired
  `mutationGateSkipKey` mechanism).
- **Verified live** (same session, after deploying the rebuilt binary): `EditSource` (`ZTESTRCG1`, a
  full Lock→SyntaxCheck→PUT→Unlock→Activate cycle, change applied then reverted) and `CreateTable` (new
  throwaway table `ZVSP_TST_SESSAFF` in `$TMP`, the exact mutation that used to 423 on every attempt
  regardless of configuration) both succeeded with no 423, confirmed by reading the created table back,
  then deleted. The remaining ~20 unmigrated compensating-unlock sites (`task_f620aae2`, closed by 2u
  below) were not exercised live this session — only the marker migration and the two
  previously-broken-on-every-attempt mutations were.

### 2u. Follow-up `task_f620aae2` closed — the rest of the "enqueue leak" compensating unlocks (2026-09-07)
- Finished what 2t deliberately deferred: migrated the remaining ~19 compensating-unlock sites (unlocks
  that only run on a failure branch or a `defer` conditioned on `!success`, never the ones that run on
  every path) from a bare `_ = c.UnlockObject(ctx, ...)` to `releaseLockAfterFailure` +
  `strandedLockAdvice`, same as the 3 reference sites 2t already covered.
- **A real defer-vs-`result.Success` bug found along the way, in 10 of those sites** (`workflows.go`
  ×4: `WriteProgram`, `WriteInclude`, `WriteClass`, `CreateAndActivateProgram`, `CreateClassWithTests`;
  `workflows_source.go` ×6): the compensating `defer` was keyed off `!result.Success`, and
  `result.Success` only flips to `true` at the very end of the function, after activation. If activation
  failed *after* the happy-path unlock had already succeeded, the defer still fired and sent a second,
  spurious UNLOCK for a handle already released. Harmless against SAP (an UNLOCK on an already-released
  handle just errors and is ignored) but would have made `strandedLockAdvice`'s new user-facing message
  say "left LOCKED" on an object that was in fact released cleanly. Fixed by introducing an explicit
  `unlocked bool` (the pattern `WriteInclude`/`EditSourceWithOptions`/`RenameObject` already used),
  flipped to `true` immediately after the happy-path unlock call, before checking its error.
- **`workflows_deploy.go`'s `CreateFromFile`/`UpdateFromFile` had no `result` for the defer to write
  into at all** — both returned `&DeployResult{...}` literals on every path, not a shared/named variable.
  Refactored both to named returns (`func (...) (result *DeployResult, err error)`); a bare
  `return &DeployResult{...}, nil` still works identically (Go assigns the named returns before running
  deferred functions), so no other line needed to change. One `return nil, err` between the lock and the
  end of each function needed a `result != nil` guard in the defer to avoid a nil dereference when that
  path fires — code-reviewed, confirmed no other `return` in either function clobbers an already-built
  `result`.
- **Two more bugs of the same root cause found and fixed opportunistically while editing adjacent code**,
  neither in the original follow-up's site list:
  - `CreateMessageClass`'s (`crud.go`) PUT of initial messages was missing `Stateful: true` — the exact
    same defect already fixed in `WriteMessageClassTexts` (2t) and `CreateTable` (2t), just never noticed
    in this sibling function. Fixed alongside its compensating-unlock migration.
  - `ExecuteABAP`'s (`workflows_execute.go`) cleanup `defer` locked the temp program then called
    `DeleteObject`, but if `DeleteObject` itself failed, the lock the defer had just taken was never
    released — no unlock at all, compensatory or otherwise. Fixed with `releaseLockAfterFailure` in that
    failure branch.
- Code-reviewed: 0 CRITICAL/HIGH. 1 MEDIUM (fixed same session — `CreateTable`'s unlock-before-activation
  error path passed the same `err` to both `%w` and `strandedLockAdvice`, duplicating the error text in
  the message; dropped the redundant `%w`). 2 LOW (fixed same session — `DeleteObjectWithAutoLock`'s
  compensating-unlock-also-failed branch was missing the `"deleting object: "` prefix its sibling branch
  has; `RenameObject`'s `newUnlocked = true` was set *before* the `UnlockObject` call instead of after,
  inconsistent with every other site's ordering though not a functional bug). 1 LOW noted, not fixed
  (informational — a near-unreachable gap where `buildSourceURL` failing between the lock and the end of
  `CreateFromFile`/`UpdateFromFile` returns `nil` for the named `result`, so a `strandedLockAdvice`
  message computed in that exact window has nowhere to attach; the only way `buildSourceURL` fails there
  is `ObjectTypeFunctionMod` with an empty parent name, a case earlier code already rejects first).
- Deliberately excluded from this pass, unchanged from 2t: `crud.go`'s `CreateStructure` (sibling of
  `CreateTable`, out of scope by the user's own earlier decision), and every unlock confirmed to run on
  every path regardless of success (`writeXMLObject`, `CreateMessageClass`'s own final unlock,
  `workflows_source.go`'s CLAS test-source block) — none of those have the cancelled-context leak this
  pass exists to close.
- `go build ./...` clean, full suite green after every file and again at the end.
- **Verified live** (same session, after deploying the rebuilt binary): `EditSource` on `ZTESTRCG1` again
  (change applied then reverted) and a second throwaway `CreateTable` (`ZVSP_TST_SESS2` in `$TMP` — table
  names cap at 16 characters, discovered live when the first attempt at a longer name was rejected by SAP
  with a clear error, not a vsp bug) both succeeded, confirmed by reading the table back, then deleted.
  These exercise the exact code paths this pass touched (`crud.go`'s `CreateTable` unlock-before-activation
  fix, `workflows_edit.go`'s already-covered path) end-to-end on the real system.

### 2v. MSAG/SE91 message-class bugs — 4 fixed, 1 closed as a permanent known limitation (2026-09-08/09)
Four bugs in the `SAP(action="edit"/"read", target="MSAG ...")` path fixed and code-reviewed this session:
dead/incorrect MCP tool schema for `WriteMessageClassTexts`, wrong XML namespace on read
(`http://www.sap.com/adt/MessageClass`, not `/adt/mc`), missing `edit MSAG` routing, and no delete-message
support. Files: `pkg/adt/client.go`, `pkg/adt/i18n.go`, `pkg/adt/crud.go`, `internal/mcp/handlers_i18n.go`,
`internal/mcp/handlers_source.go`, `internal/mcp/tools_register.go`, `internal/mcp/handlers_help.go`, plus
new/updated tests (`pkg/adt/i18n_test.go`, `pkg/adt/session_affinity_test.go`,
`internal/mcp/handlers_source_msag_test.go`, `internal/mcp/server_test.go`).

A 5th, deeper problem was investigated exhaustively and then **closed as a permanent known limitation, not
a bug to keep chasing** — see "`WriteMessageClassTexts`/`CreateMessageClass`" under Known Open Issues below
and the full write-up in `docs/message-class-write-investigation.md`: writing message text does not persist
on this SAP system, even with the correct namespace/shape confirmed live, and the last remaining avenue
(Eclipse ADT traffic capture via Wireshark) is blocked on IT and not worth keeping this task open for.
`verifyMessageClassWrite` (the guard that turns SAP's silent false-success into an explicit error) stays in
the code permanently — it is the fix, not a placeholder.

### 2w. Ported upstream PR #178 — keep-alive skips the ping during an open lock window, closes #168 (2026-09-09)
- **The bug**: a keep-alive ping is an ordinary request, and under the stateless default it is not answered
  on the stateful ADT session a lock handle is bound to — sending one retires that session. A tick landing
  between a LOCK and the write that consumes it silently kills the handle; the write then returns 423. This
  was the "genuinely open on this fork too" entry under Known Open Issues (see 2t's note on
  `fetchCSRFTokenFor`), and is exactly the family of session-affinity bugs #91/#132/#133 already invested
  heavily in — plausibly a contributor to some of the orphaned SM12 locks hit during the MSAG investigation
  (2v / `docs/message-class-write-investigation.md`).
- **Fix, ported from upstream almost mechanically**: new `pkg/adt/lock_window.go` — a `lockWindow` (handle →
  open-time map + mutex) tracked on `Client`, with `noteLockOpened`/`noteLockClosed`/`lockOutstanding`.
  Entries older than 30 minutes (under SAP's own ADT session timeout) are pruned rather than suppressing the
  ping forever, so a leaked entry can't disable keep-alive permanently. `StartKeepAlive`'s ticker now skips
  the `Ping` call entirely (`continue`) whenever `lockOutstanding()` is true.
- **Three hooks ported as-is** (`pkg/adt/crud.go`): `LockObject` calls `noteLockOpened` after a successful
  parse; `UnlockObject` and `DeleteObject` call `noteLockClosed` only on success (a failed unlock may have
  left the lock held — suppressing an extra ping is the cheap mistake, not the expensive one). The DELETE
  hook matters on its own: a delete consumes the handle without any UNLOCK ever being sent, which is exactly
  the case that sank the first design of this feature upstream (pinned by
  `TestLockWindow_DeleteEndsTheWindow`).
- **One hook not in the upstream diff, fork-specific**: `DeleteObjectWithAutoLock` (`pkg/adt/crud.go`, added
  in 2t/2u for the #88 session-affinity problem) issues its own inline DELETE instead of delegating to
  `DeleteObject`, so the DELETE hook above never runs for it. Added its own `noteLockClosed` call on the
  happy path, with its own regression test (`TestLockWindow_DeleteObjectWithAutoLockEndsTheWindow` —
  upstream has no equivalent function to have covered this).
- **Default changed**: `--keepalive` default is now `0` (disabled), matching upstream's decision. With the
  lock-window skip in place, the only remaining reason for a nonzero default (avoiding an idle-session
  timeout) is a soft failure (re-login) versus the hard failure (423, possible stranded lock) a ping with no
  protection used to risk during a write. `--keepalive 5m` (or any value) is still available and now safe to
  use explicitly. File: `cmd/vsp/main.go`.
- No change to `fetchCSRFTokenFor`'s non-stateful keep-alive design from 2t — this fix is orthogonal
  (suppresses the ping outright rather than making it stateful).
- Tests: `pkg/adt/lock_window_test.go` — 5 of upstream's tests ported near-verbatim (suppression, DELETE
  ends the window, stale-entry pruning, concurrency safety with `Client` shared across MCP handler calls,
  and an end-to-end test that drives the real `StartKeepAlive` goroutine) + 1 new fork-specific test for
  `DeleteObjectWithAutoLock`.
- `go build ./...` clean; full suite green (`go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)`).
  `-race` unavailable in this environment (CGO disabled, same constraint as `pkg/cache`/`cmd/vsp`).
- **Verified live (2026-09-09, later session)** against the real SAP system with a throwaway standalone Go
  program (built as a temporary `cmd/verify178/`, deleted after the run — not committed) that drives the
  real `pkg/adt.Client`/`StartKeepAlive` directly rather than the MCP tool, so the exact deployed code path
  is exercised: a real `LockObject` on `ZTESTRCG1` held open for 7s with a 2s keep-alive interval logged
  three consecutive `[KEEPALIVE] Skipped: a lock is outstanding` lines (zero pings during the window), the
  `UpdateSource` PUT that followed succeeded with no 423, and a control run with no lock held showed the
  keep-alive DOES ping normally (`[KEEPALIVE] Ping OK` ×2) — confirming the skip is conditional on the lock
  window, not a global keep-alive breakage.

### 2x. Ported upstream PR #191 — optimistic-concurrency guard via source hash, `SOURCE_DRIFT` (2026-09-09)
- **The feature**: `GetSource(include_hash=true)` returns `{source, sourceHash}` (SHA-256 over the source,
  canonicalized CRLF→LF and trailing-newline-trimmed so ADT's own materialization differences don't count
  as drift). Passing that hash back as `expected_source_hash` to `WriteSource`, `EditSource`,
  `DeployFromFile`, or `ImportFromFile` makes the write conditional: after the MODIFY lock is acquired,
  VSP re-reads the source **inside that same lock window** (stateful — critical, see below) and compares
  hashes; a mismatch aborts with a `*SourceDriftError` before any PUT is sent. A successful guarded write
  also reports `targetSourceHash`/`verifiedSourceHash` from a post-activation read-back; a mismatch there
  flips `Success` to `false` with an explicit "Do not retry blindly" message rather than silently accepting
  whatever SAP actually materialized.
- **Why the pre-write re-read has to be stateful**: this is exactly the family of session-affinity bugs
  this project has repeatedly hit (#91/#132/#133/#168/#169, see 1./2t/2w above) — any stateless hop between
  LOCK and the write that consumes its handle retires the ADT session and turns the eventual PUT into a 423.
  `verifyExpectedSourceHash` (`pkg/adt/source_hash.go`) sends its GET with `Stateful: true` for this reason;
  the post-activation verification read, by contrast, runs *after* UNLOCK and is deliberately non-stateful —
  the lock window is already closed by then, so there is nothing left to protect.
- **Single-use expectation**: the hash to check is stored on the context (`withExpectedSourceHash`) as a
  `sourceHashExpectation` guarded by a mutex + `used` bool, consumed by the *first* source write in a
  workflow. This matters for `WriteSource(CLAS, ..., TestSource: "...")`: the main class body write consumes
  the expectation, so the optional follow-up test-include update (a different, unrelated source) does not
  get compared against the class body's hash. Pinned directly by
  `TestVerifyExpectedSourceHash_ConsumedOnlyOnce` (`pkg/adt/source_hash_test.go`).
- **Six deliberate deviations from upstream's literal diff**, all because this fork's structure differs
  from upstream's (planned before implementation, not discovered after):
  - **A** — upstream bundles an unrelated behavior change into the same `WriteSource` validation line this
    PR touches (`opts.Mode == WriteModeUpsert && !objectExists`, loosening explicit-update-on-nonexistent
    handling). This fork already fixed the underlying bug that motivated it, differently (2f above) — so
    that line was left exactly as it already was; only the new hash-guard validation was added next to it.
  - **B** — this fork has no `writeSourceFunctionModule` dispatch (FUNC is handled inline in
    `writeSourceCreate`/`writeSourceUpdate`), so `expected_source_hash` is rejected for FUNC at its other
    precondition checks in `WriteSource` itself (alongside the `Parent` requirement), not at the entry to a
    dedicated function upstream has and this fork doesn't. Pinned by
    `TestWriteSource_FUNC_RejectsExpectedSourceHash`.
  - **C** — this fork's `buildSourceURL` takes 2 args (objType, name), not upstream's 3-arg FUNC-capable
    version; not extended for this PR. Function-module file deploys already couldn't resolve a source URL
    through this same helper before this port (a pre-existing gap) — left untouched, not this PR's problem
    to fix.
  - **D** — `SourceHash`'s CRLF→LF canonicalization reuses this fork's existing `normalizeLineEndings`
    (`workflows_edit.go`) instead of duplicating `strings.ReplaceAll` inline as upstream does.
  - **E** — the two HTTP-level drift tests (`TestUpdateSourceRejectsDriftBeforePut`,
    `TestUpdateSourceAcceptsMatchingVersionAndWrites`) were reimplemented against this fork's own
    `newStubbedClient`/`adtRecorder` harness (`session_affinity_test.go`) instead of upstream's raw
    `httptest.NewServer`, so they read like the rest of the lock-window/session-affinity suite.
  - **F** — `verifyWriteSourceResult` is invoked inline inside `WriteSource`'s update branch (not as a
    wrapper around the update call the way upstream structures it), so it falls through to the shared
    `sourceCache.InvalidateByName` tail this fork has and upstream doesn't.
- **Two MEDIUM findings from code review, fixed same session** (0 CRITICAL/HIGH from the start):
  - `GetSource(include_hash=true)` could serve a hash computed from a stale `sourceCache` entry (10-minute
    TTL) as if it were a fresh baseline — safe (the pre-write re-verify still catches real drift, so this
    never risked a silent overwrite) but confusing (a `SOURCE_DRIFT` that's really a caching artifact, not a
    concurrent edit). Fixed with a new `GetSourceOptions.NoCache` field, set from `include_hash` in
    `handleGetSource` — forces a fresh read for the hash baseline while leaving the cache itself, and plain
    reads, untouched (the fresh value still repopulates the cache afterward).
  - Insufficient test coverage for the single-use expectation mechanism and the FUNC guard. Added
    `TestVerifyExpectedSourceHash_ConsumedOnlyOnce` (unit-level, direct — deliberately not a full
    `CLAS`+`TestSource` end-to-end stub, which would need a heavier multi-endpoint harness for a property
    this pins just as precisely) and `TestWriteSource_FUNC_RejectsExpectedSourceHash`. The post-activation
    verification-mismatch paths (`verifyWriteSourceResult`, and the equivalent blocks in
    `EditSourceWithOptions`/`UpdateFromFileWithOptions`) remain without dedicated tests — noted here as a
    gap, not fixed, matching this project's own convention of recording deferred coverage rather than
    silently leaving it undiscoverable.
- Files: `pkg/adt/source_hash.go` (new), `pkg/adt/source_hash_test.go` (new), `pkg/adt/crud.go`
  (`UpdateSource`, `UpdateClassInclude`), `pkg/adt/workflows_edit.go` (`EditSourceOptions`/
  `EditSourceResult`, post-activation verify), `pkg/adt/workflows_source.go` (`WriteSourceOptions`/
  `WriteSourceResult`, `GetSourceOptions.NoCache`, `GetSource`, FUNC/method guards, `verifyWriteSourceResult`),
  `pkg/adt/workflows_deploy.go` (`DeployResult`, `DeployFromFileOptions`, `UpdateFromFile`→wrapper +
  `UpdateFromFileWithOptions`, `DeployFromFile`→wrapper + `DeployFromFileWithOptions`),
  `internal/mcp/handlers_source.go` (`GetSource`/`WriteSource` routing, tool schemas, handlers),
  `internal/mcp/handlers_fileio.go` (`handleDeployFromFile`, `handleEditSource`),
  `internal/mcp/tools_register.go` (`DeployFromFile`/`EditSource` tool schemas), `MCP_USAGE.md`,
  `README_TOOLS.md`.
- `go build ./...` clean; full suite green (`go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)`).
- **Verified live (2026-09-09, later session)** against the real SAP system, via the deployed MCP tool
  (`SAP(action=..., target="PROG ZTESTRCG1", ...)`) end to end: `GetSource(include_hash=true)` returned
  `{source, sourceHash}` JSON; `WriteSource` with a trivial change and the correct `expected_source_hash`
  succeeded with matching `targetSourceHash`/`verifiedSourceHash`; a retry with the now-stale original hash
  was rejected with the exact `SOURCE_DRIFT: expected source hash ..., but the locked object is now ...`
  message and made no change to the object (confirmed by re-reading it — same hash as the prior successful
  write); the object was then restored to its original content with a correctly-matching guarded write.
  `ZTESTRCG1` ends the session identical to how it started.

### 2y. Fixed upstream issue #151 — `CallRFC` fails for FMs whose TABLES parameter's line type is itself a table type (2026-09-09)
- **The bug**: `CALL_RFC` (`SAP(action="debug", target="CALL_RFC", ...)`) failed for `DDIF_FIELDINFO_GET`
  with `TABNAME=BSEG` reporting `"Al parámetro FIXED_VALUES debería asignársele un campo cuyo tipo no es
  compatible con este parámetro"` — even though `FIXED_VALUES` is an *optional* TABLES parameter never
  explicitly passed. No remote-debugger breakpoint ever fired, matching the upstream report exactly: the
  rejection happens at ABAP's dynamic `CALL FUNCTION ... PARAMETER-TABLE` binding, before the FM's own code
  runs. No upstream PR exists for this — the diagnosis and fix are original to this session.
- **Root cause (confirmed via RTTI live introspection, not guessed)**: `FUNCTION_IMPORT_INTERFACE` reports
  `FIXED_VALUES`'s line type as `DDFIXVALUES` (`RSTBL-TYP`) — and `DDFIXVALUES` is itself a **table type**
  (`TTYP/DA`, package `SDBT`), not a structure (confirmed via `SAP(action="search", target="DDFIXVALUES")`).
  `ZCL_VSP_RFC_SERVICE=>CREATE_TABLE_DATA` unconditionally built `CREATE DATA ro_data TYPE STANDARD TABLE OF
  (lv_type)` — when `lv_type` already names a table type, this produces a table-of-tables, syntactically
  valid ABAP (`CREATE DATA` never errors) but incompatible with the binding the FM actually expects, hence
  the "type not compatible" error at call time. `CREATE_PARAM_DATA` (used for IMPORTING/EXPORTING/CHANGING)
  was checked and confirmed to NOT have this defect — it already does `CREATE DATA ro_data TYPE (lv_type)`
  directly, no wrapping, so it works correctly regardless of whether `lv_type` is a structure or a table type.
- **Fix**: `CREATE_TABLE_DATA` now resolves `lv_type` via RTTI first (`cl_abap_typedescr=>describe_by_name`,
  classic `EXCEPTIONS type_not_found = 1 OTHERS = 2` — `describe_by_name` has no functional/`RAISING` form
  for a dynamic name, confirmed by reading its live source: it ends in `raise type_not_found.`, the classic
  non-CX-class raise syntax, not `RAISE EXCEPTION TYPE cx_...`). If it resolves and `kind = kind_table`, uses
  `CREATE DATA ro_data TYPE (lv_type)` directly, unwrapped. Otherwise (structure, elementary, or RTTI
  couldn't resolve the name) falls through unchanged to the original `STANDARD TABLE OF (lv_type)` +
  `CATCH cx_sy_create_data_error` path — no behavior change for the majority case.
- **Verification method, in order** (each step ruled out a real alternative explanation before moving on):
  1. Isolated the two candidate parameters (`FIXED_VALUES` alone) via `SAP(action="analyze",
     params={"type": "execute_abap", ...})` (a temporary throwaway `ZTEMP_EXEC_*` program, auto-cleaned) —
     confirmed RTTI resolves `DDFIXVALUES` as `kind=T` and `CREATE DATA ... TYPE (lv_type)` (unwrapped)
     succeeds in isolation.
  2. Deployed the fix to the real `ZCL_VSP_RFC_SERVICE` (transport `S4DK928661`), activated clean.
  3. `CALL_RFC` still failed identically — traced to the WebSocket `ZADT_VSP` session being **persistent**
     across MCP tool calls within one `vsp.exe` process lifetime (`internal/mcp/handlers_debugger.go`'s
     `ensureDebugWSClient` reuses `s.debugWSClient` while `IsConnected()`), and that session's ABAP roll
     area had `ZCL_VSP_RFC_SERVICE` loaded from *before* the activation — a known SAP behavior where an
     already-loaded class pool inside a long-running session context is not guaranteed to re-resolve to a
     newly activated version until the session/roll-area restarts. This is a session-caching artifact, not
     a defect in the fix.
  4. Reproduced the *complete* real call (all IMPORT/EXPORT/TABLES parameters `DDIF_FIELDINFO_GET` actually
     has, built exactly as `handle_call` builds them) via `ExecuteABAP`, which runs in a fresh roll area each
     time — succeeded (`FULL_CALL_OK:subrc=0`), proving the fix correct independent of the stale WS session.
  5. After the user restarted Claude Desktop (spawning a fresh `vsp.exe` and thus a fresh WebSocket
     connection/session), repeated the original real `CALL_RFC` call — **succeeded**: `subrc: 0`,
     `DFIES_TAB` (the old, unwrapped-structure branch) returned all 425 real `BSEG` field rows with no
     regression, `FIXED_VALUES` (the new, table-type branch) returned `[]` correctly instead of erroring.
- **Practical implication for future ZCL_VSP_RFC_SERVICE/ZADT_VSP edits**: a code change to a class used by
  the WebSocket domain may need the WS session (i.e. a `vsp.exe` restart, same as any other binary/ABAP
  redeploy — see "Deploying a local build for live testing" below) recycled before it's observable through
  `CALL_RFC`/`RFC_SEARCH`/`RFC_METADATA`, even though the ADT-side activation itself succeeded immediately.
  Worth remembering the next time a live-verification "didn't take effect" — check for this before assuming
  the fix itself is wrong.
- Files: `src/zcl_vsp_rfc_service.clas.abap`, `embedded/abap/zcl_vsp_rfc_service.clas.abap` (`create_table_data`
  method only — confirmed byte-identical between the two files for this specific hunk; the two files have
  unrelated pre-existing drift elsewhere, noted by code review but out of scope for this fix, see below).
- Code-reviewed: 0 CRITICAL/HIGH/MEDIUM. 2 LOW (both informational, neither fixed): (1) `src/` and
  `embedded/abap/` have ~74–233 lines of pre-existing drift *outside* this method (predates this session —
  class-name casing, blank lines, `ZCL_VSP_TADIR_MOVE` vs `ZADT_CL_TADIR_MOVE`, and three whole methods present
  only in `embedded/abap/`) — a separate sync task, not introduced or worsened by this fix. (2) the new RTTI
  block uses classic `CALL METHOD ... EXCEPTIONS` syntax while the rest of the class calls
  `cl_abap_typedescr` functionally (`DATA(x) = ...describe_by_data(...)`) — likely unavoidable since
  `describe_by_name` has no functional form with class-based exceptions, not a real inconsistency.
- No ABAP Unit tests (matches this class's existing pattern — no tests exist for it, verification is always
  manual against live SAP, as documented in 2h/2m/2v above).

### 2z. Ported upstream PR #203 — transport auto-choice, the way Eclipse's dialog does it (2026-09-09)
- **The bug**: a write to a transportable object with no `transport` named used to leave the choice to SAP,
  which answered with a request of its own — "Generated Request for Change Recording" — one per write. A
  day's work on one feature could end up spread over several such requests beside the developer's own open
  one, exactly the "regla de oro" scenario the project's global `CLAUDE.md` already calls out ("nunca dejar
  que SAP autogenere una orden por omisión").
- **The fix**: before the LOCK — deliberately, since a stateless hop between LOCK and the write that
  consumes its handle retires the session (issue #91, this project's most recurring bug family) — a new
  `planTransport` (`pkg/adt/transport_choice.go`, new file) POSTs to `/sap/bc/adt/cts/transportchecks` (the
  same resource Eclipse's own transport dialog reads) and picks, in order: the request the object is already
  locked in, else the user's only open request that fits, else the open request that already holds an
  object of the same package (checked via a TADIR query, `transportHoldsPackage`), else the newest of
  several; with none and `--enable-transports`, one is created and named after the package and object. A
  failed check is not a reason to refuse the write — the choice is then left to SAP as before (fail-open);
  a failed *creation* is, since the caller explicitly enabled it. `resolveWriteTransportFor` re-applies
  `checkTransportableEdit` to whatever was chosen — a request found or made this way cannot bypass
  `--allow-transportable-edits`/`--allowed-transports` the way naming it explicitly would have to pass.
  New flag `--transport-choice off` / `SAP_TRANSPORT_CHOICE=off` restores the old behaviour. Every
  create/update result now carries `transport` and, when chosen here, `transportNote` explaining why.
- **Alcance ampliado más allá del diff literal de upstream** (decidido explícitamente con el usuario antes
  de implementar, vía planner + `AskUserQuestion`):
  - **`WriteInclude`** incluida por consistencia con `WriteProgram`/`WriteClass` (upstream no la toca).
  - **Los 7 creadores DDIC/MSAG de este fork** (`CreateStructure`, `CreateTable`, `CreateDomain`,
    `CreateDataElement`, `CreateTableType`, `CreateLockObject`, `CreateMessageClass`, en `crud.go`) —
    upstream no los tiene, no pasan por `CreateObject`, así que no heredaban la elección automática.
  - **La rama `FUNC` de `writeSourceUpdate`**, que además de la elección de transporte recibió el fix del
    issue #144 (adopción de `lock.CorrNr`) que nunca tuvo — un descuido simple compartido con ninguna otra
    rama de esa función, cerrado en el mismo cambio.
  - **3 bugs preexistentes encontrados y arreglados al planificar los 7 creadores DDIC/MSAG**, aprobados
    explícitamente para incluir en el mismo cambio en vez de diferir: (a) `writeXMLObject` (helper
    compartido por `CreateDomain`/`CreateDataElement`/`CreateTableType`/`CreateLockObject`) hacía el PUT que
    consume el lock **sin `Stateful: true`** — el mismo defecto de session-affinity que `CreateTable` ya
    tenía arreglado antes de esta sesión; (b) 5 de los 7 creadores solo llamaban `checkSafety`, nunca
    `checkMutation` — `--allowed-packages` no se aplicaba en absoluto a ellas (`CreateStructure` de paso
    pasó de un PUT manual a `UpdateSource`, alineándola con el patrón ya usado en `CreateTable`); (c)
    `CreateMessageClass` gateaba con `ObjectURL` de un objeto que aún no existe en vez de `Package` — con
    `AllowedPackages` configurado esto haría fallar `SearchObject` sobre un objeto inexistente en vez de
    devolver un rechazo de política limpio.
- **Decisiones estructurales del fork, no en el diff upstream**: `sqlQuote`/`cell` de upstream son
  `escapeQuote`/`getString` en este fork (ya existían, sin duplicar); `pkg/adt/description.go` y
  `pkg/adt/textpool.go` no existen en este fork (SetDescription y el text pool van por otras rutas — ver
  2g) así que esas dos partes del diff de #203 no aplican; `cmd/vsp/cli.go` de este fork es un archivo
  distinto del que asume el diff — el wiring de flags va en `cmd/vsp/main.go`; `pkg/config/systems.json` no
  tenía ningún campo de transporte previo, así que `transport_choice` por sistema no se portó (solo
  flag/env global); `vsp adt request` de upstream se portó como `vsp transport request METHOD PATH` (nuevo
  subcomando de `transportCmd`, que ya existía) en vez de crear un grupo `adt` nuevo que este fork no tiene.
- **Hallazgos del code-reviewer, todos corregidos en la misma sesión** (0 CRITICAL/HIGH desde el principio):
  4 MEDIUM — (1) la rama `INCL` de `writeSourceCreate` perdía `TransportNote` al sobrescribirlo con el
  resultado (vacío) de la delegación interna a `WriteInclude`, en vez de conservar el motivo original de
  `CreateObject`; (2) los 6 creadores DDIC pasaban `objectURL=""` a `planTransport` en vez de la URL
  prospectiva real del objeto (que `CreateObject`/`CreateMessageClass` sí construyen), dejando el check ADT
  sin poder identificar el objeto/tipo; (3) faltaba en los 7 creadores el guard de paquete local (`$`) que
  `CreateObject` sí tiene, disparando una llamada de red desperdiciada a `/cts/transportchecks` en cada
  creación en `$TMP`; (4) `chooseTransport` — la lógica de decisión central (única candidata / candidata que
  ya tiene el paquete / la más reciente / crear una nueva) no tenía ningún test directo. Los 4 se
  corrigieron: guard `$` + URL prospectiva añadidos a los 6 creadores DDIC, `INCL` ya no sobrescribe
  `TransportNote`, y 5 tests nuevos de `chooseTransport` contra un servidor stub
  (`TestChooseTransport_SingleCandidate`, `..._PicksTheOneThatHoldsThePackage`,
  `..._NoneHoldsPackage_PicksNewest`, `..._CreatesRequest`, `..._TransportsDisabled_LeavesItToSAP`).
- **Hallazgo colateral, documentado no arreglado**: `transportHoldsPackage` llama a `GetTransport`, que
  tiene su propio gate de lectura (`CheckTransport(..., isWrite=false)`) — requiere `--enable-transports` o
  `--allow-transportable-edits`. Sin ninguno de los dos, la rama "ya tiene el paquete" degrada
  silenciosamente a "la más reciente" (fail-open, consistente con el resto del diseño, pero no anunciado en
  ningún mensaje). Descubierto escribiendo `TestChooseTransport_MultipleCandidates_PicksTheOneThatHoldsThePackage`
  (fallaba hasta añadir `WithEnableTransports()` al cliente de test). No es un bug de este port — es una
  consecuencia del gate ya existente de `GetTransport` — pero vale la pena saberlo antes de asumir por qué
  la elección "no encontró" la orden correcta en un sistema sin esos flags.
- Files: `pkg/adt/transport_choice.go` (nuevo), `pkg/adt/transport_choice_test.go` (nuevo), `pkg/adt/raw.go`
  (nuevo, `Client.RawRequest`), `cmd/vsp/adt_request.go` (nuevo, `vsp transport request`), `pkg/adt/safety.go`,
  `pkg/adt/config.go`, `pkg/adt/crud.go` (`CreateObject` + los 7 creadores + `writeXMLObject`),
  `pkg/adt/workflows.go` (`WriteProgram`, `WriteInclude`, `WriteClass`, `CreateAndActivateProgram`),
  `pkg/adt/workflows_edit.go` (`EditSourceWithOptions`), `pkg/adt/workflows_source.go` (todas las ramas de
  `writeSourceCreate`/`writeSourceUpdate` + `writeClassMethodUpdate`), `pkg/adt/workflows_deploy.go`
  (`CreateFromFile`, `UpdateFromFileWithOptions`), `pkg/adt/session_affinity_test.go` (9 tests nuevos),
  `internal/mcp/server.go`, `internal/mcp/handlers_crud.go` (los 7 handlers DDIC/MSAG exponen
  `transport`/`transportNote`), `internal/mcp/handlers_help.go`, `cmd/vsp/main.go` (flag
  `--transport-choice`), `cmd/vsp/devops.go` (impresión de `Transport`/`TransportNote` en 2 comandos CLI).
- `go build ./...` clean; full suite green (`go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)`),
  incluidos los 15 tests nuevos (5 en `transport_choice_test.go` + 10 en `session_affinity_test.go`).
- **Verificado en vivo (2026-09-09, misma sesión)** contra el sistema SAP real, vía el tool MCP desplegado:
  `SAP(action="edit", target="PROG ZVSP_TST_TRCHOICE", params={"source": "...", "package": "ZABAP01",
  "description": "..."})` — **sin `transport` explícito** — devolvió `transport: "S4DK928807"`,
  `transportNote: "reused S4DK928807 (MM - Compras externas, reparto): it already holds objects of
  ZABAP01"`. Confirmado leyendo la orden (`SAP(action="system", params={"type": "get_transport",
  "transport": "S4DK928807"})`): `ZVSP_TST_TRCHOICE` aterrizó junto a `ZRSU_ENTRADA_MERCANCIAS_RESTO`, un
  objeto de `ZABAP01` ya presente en esa misma orden — exactamente la rama "ya tiene el paquete" de
  `chooseTransport` (`transportHoldsPackage`/TADIR) funcionando en producción, sin generar ninguna
  "Generated Request for Change Recording". El programa throwaway fue borrado después de confirmar con el
  usuario (`SAP(action="delete", target="OBJECT", params={"object_url": "..."})`). No se probó
  `--transport-choice off` en vivo (mecanismo trivial — un solo `if` que corta `planTransport` antes de
  cualquier llamada de red — ya cubierto por `TestResolveWriteTransportFor`/`TestChooseTransport_*`).

## Known Open Issues (Not Fixed)

### `WriteMessageClassTexts`/`CreateMessageClass` — message text does not persist — closed as a known limitation, not under active investigation (2026-09-09)
- **Symptom**: PUT to `/sap/bc/adt/messageclass/{name}` with the live-confirmed correct namespace
  (`http://www.sap.com/adt/MessageClass`), root element (`mc:messageClass`), and literal `mc:`/`msg:` prefix
  technique returns `200 OK`, but the message text is never actually saved — an immediate read-back (even
  inside the same lock window) shows it empty.
- **Root cause: not found.** Every direct-experimentation avenue was exhausted this session: the parent PUT
  in two independently-verified XML shapes; all three lock/write variants against the per-message
  sub-resource (parent-lock → 423, sub-resource `MODIFY`-lock → phantom lock accepted by LOCK but rejected
  by PUT/UNLOCK, sub-resource `INSERT`-lock → real lock but no working create endpoint, 404); and
  `If-Match`/`accessMode` HTTP precondition variants on the parent PUT (both divert SAP's generic REST
  framework into an unrelated error branch). The only avenue left — capturing real Eclipse ADT traffic via
  Wireshark + `SSLKEYLOGFILE` — was blocked on IT enabling the Npcap capture driver, with no committed date.
- **Decision (user, 2026-09-09): stop investigating.** Not worth keeping open indefinitely pending an IT
  permission with no ETA. `verifyMessageClassWrite` (`pkg/adt/i18n.go`) stays active permanently — it
  converts SAP's silent false-success into an explicit error, so callers never believe a message was saved
  when it wasn't. This is the intended, final behavior, not a temporary guard to remove later.
- Also discovered along the way and documented as a caution (not itself fixed — no working release
  mechanism was found): **any** LOCK→PUT→UNLOCK→DELETE cycle against a message class leaves orphaned
  `T100`/`T100A` enqueue entries (`ES_MSGSI`) behind even when every HTTP step reports success; RFC
  `DEQUEUE_ES_MSGSI` in every variant tried did not release them — only manual `SM12` cleanup did.
- **If this is ever revisited**: read `docs/message-class-write-investigation.md` first — it has the exact
  HTTP statuses/SAP error messages for every attempt above, the two failed proxy-capture approaches already
  tried (Fiddler via Eclipse network prefs, Fiddler via `eclipse.ini` JVM properties — both silently ignored
  by ADT's own HTTP client), and the ready-to-resume Wireshark capture plan. Do not re-attempt the parent-PUT
  or sub-resource variants listed above; they are confirmed dead ends, not untested ideas.

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

### `RUN_REPORT` — hangs on reports with a selection screen; secondary `MISSING_PARAM` bug
- **Root cause (confirmed)**: matches upstream issue [#113](https://github.com/oisee/vibing-steampunk/issues/113)
  (open, no fix merged anywhere including this fork and the two most-diverged forks checked). SAP: `SUBMIT
  ... AND RETURN` is invalid inside a stateful APC WebSocket handler and raises `APC_ILLEGAL_STATEMENT`;
  the RFC domain's fire-and-forget `runReport` (`RFC_ABAP_INSTALL_AND_RUN ... STARTING NEW TASK`) has no
  result callback either. Whichever domain the MCP client hits, it can't get a synchronous result.
  Live-reproduced: `SAP(action="debug", target="RUN_REPORT", params={"report": "ZTESTRCG1"})` (a report
  with a non-mandatory selection-screen `PARAMETERS`) with no `params`/`variant` timed out (`MCP error
  -32001`).
- **Secondary bug (found this session, not reported upstream)**: passing an explicit `params` object to
  route around the bare `SUBMIT` (`params={"report": "ZTESTRCG1", "params": "{\"PA_IDOC\":\"...\"}"}`)
  returned `RunReport failed: MISSING_PARAM: Parameter report is required` even though `report` was present
  in the call — not yet diagnosed (likely a parameter-extraction/serialization mismatch between the Go
  client's request envelope and ABAP's `extract_param`, separate from the `APC_ILLEGAL_STATEMENT` issue).
- Also structurally broken regardless of the above: `pkg/adt/reports.go`'s `GetJobStatus`/`GetSpoolOutput`
  and the job-polling model in `handleRunReport`/`handleRunReportAsync` (`internal/mcp/handlers_report.go`)
  assume a `getJobStatus`/`getSpoolOutput` action that **does not exist anywhere** in the ABAP source
  (`grep` for `getJobStatus`/`getSpoolOutput`/`jobname` across `embedded/abap` and `src`: zero matches) —
  `RunReportResult.JobName`/`JobCount` are never populated by the real backend.
- **Do not use `RUN_REPORT`/`RUN_REPORT_ASYNC` on reports with mandatory unfilled selection-screen fields**
  until this is fixed. `GET_VARIANTS`, `GET_TEXT_ELEMENTS`, `SET_TEXT_ELEMENTS` are unaffected (synchronous,
  no `SUBMIT`, verified working).
- Status: under investigation, not started on a fix — needs a rewrite matching the synchronous ABAP
  reality (job-polling model has no server-side counterpart), plus the `MISSING_PARAM` root cause.

### 3. Nuevos tipos DDIC — IMPLEMENTADOS & VERIFICADOS (2026-06-04)

Cuatro nuevos tipos de objeto SE11 implementados en `pkg/adt/crud.go` y expuestos como herramientas MCP:

| Tipo | Tool MCP | Verificado |
|------|----------|-----------|
| DOMA (dominio) | `CreateDomain` | ✅ `ZVSP_TST_DOMA` en `$TMP` |
| DTEL (elemento de dato) | `CreateDataElement` | ✅ `ZVSP_TST_DTEL` en `$TMP` |
| TTYP (tipo de tabla) | `CreateTableType` | ✅ `ZVSP_TST_TTYP` en `$TMP` |
| ENQU (objeto de bloqueo) | `CreateLockObject` | ✅ `EZ_VSP_TST` en `$TMP` |

**Gotchas descubiertos durante las pruebas:**
- DTEL: `dtel:dataType`, `dtel:dataTypeLength`, `dtel:dataTypeDecimals` son **obligatorios** en el PUT aunque el DTEL referencie un dominio. Sin ellos → HTTP 400.
- ENQU: el shell POST (creación inicial) también requiere `<enqu:primaryTable>` — no solo el PUT. Sin ella → HTTP 400 "Primary table name must not be empty". La tabla primaria debe ser una tabla transparente (TABL/DT), no un tipo de tabla.
- TTYP: el type code real en S/4HANA on-prem es `TTYP/DA`, no `TTYP/TT`.

**Archivos modificados:**
- `pkg/adt/crud.go` — `CreateDomain`, `CreateDataElement`, `CreateTableType`, `CreateLockObject`, `writeXMLObject`
- `pkg/adt/client.go` — `CanonicalObjectType`, `ResolveObjectRef`, `GetXMLMetadataObject`
- `pkg/adt/workflows_source.go` — `GetSource` para DOMA/DTEL/TTYP/ENQU
- `internal/mcp/handlers_crud.go` — 4 handlers + routing
- `internal/mcp/tools_register.go` — schemas MCP de los 4 tools
- `internal/mcp/tools_focused.go` — whitelist

**Referencia ADT API:** ver `docs/adt-api-reference.md` (investigación de `marcellourbani/abap-adt-api` + SAP tools SDK).

### 4. Graph Engine (`pkg/graph/`) — In Progress
Sequence: unify existing dep logic → SQL/ADT adapters → impact/path queries.
- Done: core types, parser dep extraction, boundary analyzer (11 tests)
- Done (fork): `hardcode_usage` + `hardcode_audit` — ZTCA_HARDCODE caller analysis (2026-05-29)
- Pending: SQL adapters (CROSS/WBCROSSGT/D010INC), ADT adapters, unify `cli_deps.go` + `cli_extra.go` + `ctxcomp/analyzer.go`
- Design: [002](reports/2026-04-05-002-graph-engine-design.md), [003](reports/2026-04-05-003-graph-engine-alignment-for-claude.md)

### 5. GUI Debugger (Issue #2) — Strategic
Plan: MCP debug sessions → DAP → Web UI. ADT REST API mapped from `CL_TPDA_ADT_RES_APP`. Design: [001](reports/2026-04-05-001-gui-debugger-design.md)

### 6. Open Issues
- **#88** Lock handle bug (EditSource/WriteSource) — same root cause as #132 (session affinity). **Resolved**
  by the #91 port (see 2t/2u above) — the marker migration and enqueue-leak fixes close this on this fork.
  Upstream's own PR #167 lists #88 among the issues it closes.
- **#168** Keep-alive ping has no session affinity, can retire the context inside any lock window. **Fixed**
  by the #178 port (see 2w above) — the ping now skips entirely while a lock is outstanding, and
  `--keepalive` defaults to 0.
- **#169** MCP cross-tool-call window: a lock handle spans separate tool calls, and any read the agent does
  between LOCK and the write that consumes it is a stateless hop — no in-process fix closes this, it needs
  an MCP-level design change (upstream is exploring this per PR #183, not ported here).
- **#55** RunReport in APC — architectural limit
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
