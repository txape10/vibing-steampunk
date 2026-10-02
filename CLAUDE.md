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
  before, no `AllowedPackages` enforcement at all) — thought at the time to be inert on this project's
  config. **Correction (2026-09-10, see 2ae)**: this checked `claude_desktop_config.json`'s `args` and
  missed that the config sets `env.SAP_ALLOWED_PACKAGES=Z*,$TMP`, which `vsp` reads via viper's `SAP`
  env prefix. `--allowed-packages` **is** effectively active here, so `CreateTable`'s new package check
  (and every "inert because AllowedPackages is not set" claim in 2t/2z) does run against `Z*,$TMP` in
  practice — benign for Z*/$TMP work, but not the no-op it was described as.
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

### 2v. MSAG/SE91 message-class bugs — 4 fixed; the 5th ("permanent limitation") was a missing language, fixed in 2ao (2026-09-08/09)
> **Correction (2026-10-02, see 2ao):** the "permanent known limitation" below turned out to be a missing
> `adtcore:language` in the PUT body, plus a wrong delete element name. Both are fixed and live-verified.
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

### 2aa. Ported upstream PR #199 — a response cache in the transport, and the `--cache`/`VSP_CACHE` flag finally does something (2026-09-09)
- **El punto de partida**: exactamente lo que decía la propia PR upstream del suyo — `cache: true` en
  `.vsp.json` y `VSP_CACHE` ya llegaban a `pkg/config.SystemConfig`/`systemParams` (resueltos en
  `GetSystem`/`resolveSystemParams`) pero no hacían nada: `getClient` nunca los usaba. Ahora el transporte
  guarda las respuestas GET (y las consultas de data preview sobre tablas estables DDIC/repositorio —
  DD03L, TADIR, CROSS, T100, etc.) durante un TTL (10 min por defecto), y vacía todo el cache ante
  cualquier petición que modifique el sistema. Nunca cachea peticiones `Stateful` — las secuencias
  lock→write→unlock que ya han dado tantos quebraderos de cabeza (#91/#132/#133/#168) quedan intactas.
- **Desviaciones deliberadas del diff literal de upstream, por las divergencias estructurales de este fork**:
  - **A** — `Transport.Request` de este fork no tenía ningún método interno tipo `t.request` al que
    upstream asume que puede delegar: era un único método largo que ya incluía CSRF, reintentos y
    detección de sesión expirada (con `retryRequest` como hermano separado). Se renombró el cuerpo actual
    a `doRequest` (elegido porque `retryRequest`/`fetchCSRFToken`/`fetchCSRFTokenFor` ya estaban ocupados);
    `Request` pasa a ser el wrapper cache-aware que construye la clave, decide `cacheable()`, sirve el hit,
    invalida en escrituras (salvo data preview) y cachea solo 2xx — juzgando el cacheo una sola vez, sobre
    la respuesta final que `doRequest` devuelve, sin tocar la lógica de reintento/CSRF/sesión existente.
  - **B** — el logging `[adt] METHOD path  FROM TABLE` reutiliza la variable `LogOutput` que YA EXISTE en
    este fork desde el port del keep-alive (2w, `pkg/adt/features.go`) — no se creó ninguna variable nueva.
    Como `LogOutput` en este fork nunca es `nil` (por defecto `io.Discard`), la llamada es incondicional,
    igual que los `[KEEPALIVE]`/`[feature]` ya existentes — sin el `if LogOutput != nil` que trae el diff
    de upstream (innecesario aquí, habría sido código muerto).
  - **C** — `NewTransport` de este fork construye el `Transport` directamente en vez de delegar en
    `NewTransportWithClient` (como asume upstream) — la inicialización del cache se añadió idéntica en
    ambos constructores en lugar de una sola vez por delegación.
  - **D** — `pkg/config.SystemConfig.Cache`/`CachePath` (y su resolución `VSP_CACHE`/`VSP_<SISTEMA>_CACHE`
    en `GetSystem`) YA EXISTÍAN en este fork, pensados en su día para un futuro "analysis cache" SQLite (el
    graph cache, sin wiring CLI todavía — ver prioridad 4 "Pending"). **Decisión explícita del usuario**:
    reutilizar esos mismos campos/env vars para este cache de respuestas en vez de crear unos nuevos —
    confirmado por grep que nada más los leía, así que no hay colisión funcional; se actualizó el comentario
    de doc para reflejar el nuevo propósito.
  - **E** — `pkg/cache/responses.go` usa deliberadamente un SEGUNDO driver SQLite independiente
    (`modernc.org/sqlite`, puro Go) en el mismo paquete que ya tiene el graph cache con el driver cgo
    `mattn/go-sqlite3` (`sqlite.go`) — es la única forma de que este store funcione en el build de Windows
    de este proyecto con `CGO_ENABLED=0`. Verificado: `go test ./pkg/cache/... -run TestResponseStore`
    pasa limpio; `go test ./pkg/cache/...` sin filtrar sigue fallando por el `Example_withSQLite`
    preexistente (mismo motivo cgo de siempre, documentado ya en la sección Build & Test) — el nuevo
    archivo no lo arregla ni lo empeora, son cosas independientes.
  - **F** — no existe `internal/mcp/handlers_info.go` en este fork (upstream lo asume). Las stats de cache
    se añadieron a `handleGetConnectionInfo` en `internal/mcp/handlers_system.go`, expuesto vía
    `SAP(action="system", params={"type":"CONNECTION"})`.
  - **G** — en `internal/mcp/server.go`, si `cache.NewResponseStore(VSP_CACHE_PATH)` falla, este fork
    escribe un aviso a stderr y cae a cache en memoria, en vez del `if err == nil` silencioso de upstream —
    consistente con cómo este archivo ya avisa de otras configuraciones inválidas (p.ej. `SAP_SESSION_TYPE`
    desconocido).
- Ported casi verbatim (sin desviación): `cacheable()`/`stableQuery()`/`stableTables`/`fromTable`,
  `MemoryResponseStore`, `responseCache` con contadores atómicos, `CacheStats`, los 4 tests de
  `pkg/adt/response_cache_test.go`, el test de `pkg/cache/responses_test.go`, `cmd/vsp/cli.go`
  (`responseCacheTTL`/`lastClient`/`buildClient`), `cmd/vsp/main.go` (`PersistentPostRun`).
- Files: `pkg/adt/response_cache.go` (nuevo), `pkg/adt/response_cache_test.go` (nuevo), `pkg/adt/config.go`
  (`Cache`/`CacheTTL`/`CacheStore` + `WithCache`/`WithCacheStore`), `pkg/adt/http.go` (`doRequest` +
  wrapper `Request` + logging), `pkg/cache/responses.go` (nuevo), `pkg/cache/responses_test.go` (nuevo),
  `go.mod`/`go.sum` (+`modernc.org/sqlite v1.57.0`), `pkg/config/systems.go` (solo comentario de doc),
  `cmd/vsp/cli.go`, `cmd/vsp/main.go`, `internal/mcp/server.go`, `internal/mcp/handlers_system.go`,
  `.gitignore` (+`.vsp-cache/`), `README.md` (env vars + sección "Response cache").
- **Hallazgos del code-reviewer, los 2 MEDIUM corregidos en la misma sesión** (0 CRITICAL/HIGH desde el
  principio): (1) `cacheable()` trataba cualquier GET no-stateful como cacheable sin distinguir estado
  externamente mutable — `GetUserTransports`/`GetTransport` (que alimentan directamente el auto-choice de
  transportes, `chooseTransport`/`transportHoldsPackage`, añadido en esta misma sesión con el port de
  #203) y los endpoints de `debugger/`/`st05/`/`runtime/traces/` podían servirse cacheados durante hasta
  10 min aunque otro proceso `vsp` o SAPGUI cambiara ese estado por debajo. Fix: `neverCacheablePrefixes`
  (`pkg/adt/response_cache.go`) — denylist explícita para `cts/`, `debugger/`, `st05/`,
  `runtime/traces/`, comprobada antes que la regla general de "todo GET es cacheable"; test
  `TestCacheable_NeverCacheablePrefixes`. (2) `GetSourceOptions.NoCache` (2x, port de #191) solo se
  saltaba el `sourceCache` de nivel cliente, no el nuevo cache de respuestas HTTP — con `VSP_CACHE=true`
  a la vez, un baseline de hash "fresco" podía en realidad servirse desde el cache HTTP, dando un
  `SOURCE_DRIFT` confuso más adelante (no un riesgo de sobrescritura: `verifyExpectedSourceHash` relee en
  ventana de lock, siempre `Stateful` y por tanto nunca cacheable, así que la detección de drift real
  seguía funcionando). Fix: `GetSource` llama `c.InvalidateCache()` antes de la lectura sin caché en vez
  de intentar enhebrar un bypass por-petición a través de cada variante de `getSourceUncached`; test
  `TestGetSource_NoCache_BypassesResponseCache` (`pkg/adt/session_affinity_test.go`).
- `go build ./...` limpio; `go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)` en verde
  (incluidos los 2 tests nuevos de la corrección de code-review); `go test ./pkg/cache/... -run
  TestResponseStore` en verde (confirma que el driver cgo-free funciona en este entorno `CGO_ENABLED=0`,
  a diferencia del graph cache existente).
- **Verificado en vivo (2026-09-09, misma sesión)** contra el sistema SAP real, con `VSP_CACHE_PATH` en un
  fichero SQLite temporal: `vsp -v source CLAS ZCL_VSP_RFC_SERVICE` en frío tardó 0.687s (`[adt] GET
  .../ZCL_VSP_RFC_SERVICE/source/main` logueado, `[cache] 0 hits, 1 misses`); el mismo comando repetido en
  un proceso nuevo (cache persistida en SQLite entre ejecuciones) tardó 0.127s (~5.4×), sin ninguna línea
  `[adt]` — cero peticiones reales al servidor — y `[cache] 1 hits, 0 misses`; el código fuente devuelto fue
  byte a byte idéntico entre ambas ejecuciones (`diff` vacío). La invalidación por escritura, el bypass de
  peticiones `Stateful` y el TTL se verificaron solo con la suite de tests `httptest` ya portada — es
  lógica puramente local de transporte HTTP, no depende de ningún estado real de SAP, así que no se hizo
  ninguna escritura real contra el sistema para confirmarlo.

### 2ab. Ported upstream PRs #200/#201/#202 — native ADT REST text pool (read + diff-based write) (2026-09-10)
- **The gap**: the fork's only text-pool path was the WebSocket bridge (`GET/SET_TEXT_ELEMENTS`,
  ZADT_VSP, see 2m). The pre-existing `GetTextPoolInLanguage` (`pkg/adt/i18n.go`, from PR #42) hit
  `/sap/bc/adt/programs/programs/{name}/textelements` — a 404 "No suitable resource found" on this
  system (documented in 2g) — so it had never returned a text to anybody here. The text pool is its
  own ADT resource, a container of three plain-text documents:
  `/sap/bc/adt/textelements/programs/{name}/source/{symbols|selections|headings}` (and
  `/classes/{name}/...` for a class's text symbols), each under its own vocabulary Accept type.
- **Ported**:
  - `pkg/adt/textpool.go` (new) — `TextPoolTarget` (PROG default, CLAS), `textDocument`
    parser/serializer (selection-text keys padded to 8, symbol/heading keys **not** — SAP answers
    "Cannot parse the source code" to a padded symbol; `@MaxLength`/`@DDICReference` directives on an
    untouched entry survive a whole-document rewrite), plan taxonomy
    (`TextPoolPlan`/`KindPlan`: added / changed old→new / unchanged / unknown (not on the screen) /
    refused (with reason) / removed / untouched), `WriteTextPool`, `MasterLanguage` (TADIR
    `MASTERLANG` via `RunQuery`), `TextPoolGaps`/`symbolsUsed`, local `spras` (ISO→SAP 1-char).
  - `pkg/adt/i18n.go` — `GetTextPoolInLanguage` rewritten to the three-document resource (one missing
    kind is not a missing pool — a report with no selection screen just has no selection texts) +
    `parseTextPoolSource` (keeps empty values: `columnHeader_1=` means "exists, untranslated").
  - `pkg/adt/client.go` — `Language()` accessor (the session language is the default target).
  - MCP: `internal/mcp/handlers_i18n_route.go` (new) revives `s.i18nTypes()` + `routeI18nAction`,
    wired into the universal route chain — `SAP(action="i18n", params={"op": "texts_get"|"texts_set"|
    "data_element_labels"|"write_labels"|"message_class_texts"|"compare_languages"|...})`. This makes
    the whole i18n domain reachable from the hyperfocused single-tool surface (the mode that ships),
    which it was not before. New `TextsGet`/`TextsSet` standalone tools too (expert/focused;
    `TextsSet` is not in focused — writes are not). `textPoolHint`/`withHint` appended to the
    happy-path of `handleWriteSource` for PROG/CLAS (a hint about screen fields with no selection
    text and `TEXT-xxx` used but undefined — never a write).
  - CLI: `cmd/vsp/texts.go` (new) — `vsp texts get|set` with `--kind --lang --json --transport
    --delete --dry-run --allow-unknown`.
- **Session-affinity discipline (issue #91)**: `WriteTextPool` reads the documents once before the
  lock (for the plan, stateless) and again under the lock (`Stateful: true`) for the write; the PUT
  is `Stateful: true`; `gateAndMark` runs the package gate above the lock so nothing networked hops
  inside the window. The compensating unlock uses `releaseLockAfterFailure`/`strandedLockAdvice` +
  an `unlocked bool` (plain `UnlockObject` on the happy path). Transport is chosen via
  `planTransport`/`resolveWriteTransportFor` (consistent with 2z).
- **The #201 fix carried over**: a text-pool PUT lands as an INACTIVE version and activating the
  *program* does not carry it — `WriteTextPool` calls `c.Activate(t.resource(), t.Name)` on the
  text-elements resource itself, after the unlock. (Same "PUT that doesn't really activate" shape as
  the closed MSAG investigation — noted there as a possible hint.)
- **Master-language guard**: writing a language whose SAP key differs from the object's TADIR
  `MASTERLANG` is a translation and is refused unless `AnyLanguage` (MCP: `language` named) /
  `--lang` (CLI) is set.
- **Fork⇄upstream deviations**: `sqlQuote`/`cell` → this fork's `escapeQuote`/`getString`; `spras`
  added locally (upstream's lives in an un-ported cluster PR); the MCP surface is this fork's
  individual-tools + a revived router rather than upstream's router-only; `docsClient(cmd)` → this
  fork's `resolveSystemParams`/`getClient`. **Out of scope** (bundled in #201 upstream, their own
  items): `vsp update` (self-updater), `vsp description`/`set_description`. The `~t:`
  selection-text-from-comment convention from #200 was removed by upstream in #201 — not ported.
- Tests: `pkg/adt/textpool_test.go` (the 4 pure-function tests ported verbatim + `spras` +
  `parseTextPoolSource`), `internal/mcp/handlers_i18n_route_test.go` (the `action="i18n"` route,
  against a recording stub — `texts_get` hits the native REST resource, an unknown op lists the
  valid ones). `pkg/adt/i18n_test.go` — the two existing wire-format tests rewritten for the new
  resource shapes.
- `go build ./...` clean; `go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)` green.
  Code-reviewed: 0 CRITICAL/HIGH, 2 MEDIUM + 5 LOW (see 2ac for the shared ones — length guard, nil
  err wrap, master-lang guard when no language configured — fixed same session).
- **Verified live (2026-09-10)** against the real SAP system via the deployed `vsp` CLI:
  `vsp texts get ZTESTRCG1` listed the native REST text pool (selection text `PA_IDOC`, symbol
  `001`, the five heading keys); a `--dry-run` then a real `vsp texts set ZTESTRCG1 --kind I
  002="VSP item6 test"` reported "1 text written … text elements activated"; a REST re-read
  confirmed `002` landed; `vsp texts set ZTESTRCG1 --kind I --delete 002` reverted it and a final
  re-read confirmed `ZTESTRCG1` is back to only symbol `001`.

### 2ac. `WriteDataElementLabels` — proper read-modify-write, beyond upstream + `GetDataElementLabels` 406 fix (2026-09-10)
- **`GetDataElementLabels` (`pkg/adt/i18n.go`)**: sent `Accept: application/xml`, which
  `/sap/bc/adt/ddic/dataelements/{name}` answers 406 "The message content is not acceptable" on
  every name — so it had never returned a label here (matches upstream's #201 diagnosis and issues
  #153/#154). Fixed to `application/vnd.sap.adt.dataelements.v2+xml`; the response is a `blue:wbobj`
  carrying the whole element, labels as `dtel:shortFieldLabel`/`mediumFieldLabel`/`longFieldLabel`/
  `headingFieldLabel` children of `dtel:dataElement`. Dead `xml:"...,attr"` tags dropped from
  `DataElementLabels` (a struct a caller holds; the wire shape is `dataElementDoc`'s business).
- **`WriteDataElementLabels` rewritten as read-modify-write** — upstream neuters it to a
  "not implemented" error; this fork implements it properly (a deliberate scope extension the user
  chose). New signature: `func (c *Client) WriteDataElementLabels(ctx, name, lang string, patch
  DataElementLabelPatch, transport string) error` — **auto-locking, no caller lock handle** (mirrors
  `WriteMessageClassTextsAutoLock`). `DataElementLabelPatch` has `*string` fields: a nil pointer
  leaves that label unchanged, a non-nil one (including a pointer to `""`) sets it.
  - The resource serves and takes the element's **whole** representation — domain, type, lengths, a
    dozen flags, search help, `atom:link`s. A four-field PUT would be rejected or, worse, accepted as
    a replacement for everything. So: GET the document, substitute **only** the requested
    `dtel:*FieldLabel` values **in the raw XML bytes** (`replaceDataElementLabel` — regex over both
    `<dtel:x>old</dtel:x>` and self-closing `<dtel:x/>`, `$` in a value escaped so it is not a
    regexp group ref, a missing label element → error **before** the LOCK), PUT the whole document
    back (`ContentType: application/*` — the value `CreateDataElement`'s whole-`wbobj` PUT proves
    against this same resource — `Accept` the versioned type). A struct remarshal was rejected
    precisely because it would drop the unmodelled elements.
  - Session-affinity: pre-GET stateless before the lock (also fails early on a missing label
    element); `gateAndMark` above the lock; re-GET under the lock `Stateful: true`; PUT
    `Stateful: true`; `releaseLockAfterFailure`/`strandedLockAdvice` + `unlocked bool` on the
    failure path. Activate as its own object after the unlock (a PUT lands inactive).
  - `verifyDataElementLabelWrite` — read back and turn a silent no-op (label did not land,
    trailing-space-tolerant) **and** a whole-object replacement (`typeName`/`dataTypeLength` moved)
    into a returned error. Best-effort: a read-back that itself fails is not a write failure.
  - `patch.checkLengths()` refuses a label past the fixed DDIC width (short 10, medium 20, long 40,
    heading 55) before any lock is taken.
- **Non-master-language behaviour, documented not guarded**: SAP serves untranslated labels in the
  master language, so patching only Short in a non-master language copies the master-language
  Medium/Long/Heading into that language's row. A caller translating an element should pass all four
  labels. (Planner's open question #1 — resolved as document-and-ship.)
- **MCP** (`internal/mcp/handlers_i18n.go`, `tools_register.go`): `handleWriteDataElementLabels`
  drops the required `lock_handle`, builds the patch from the args present (`v, ok :=
  args["short"].(string)`), requires at least one label. `routeI18nAction` fills `name` from a
  `target="DTEL ZED_X"` form as well as `object_name`.
- Tests: `pkg/adt/dataelement_labels_test.go` — `replaceDataElementLabel` (paired / self-closing /
  `$` literal / missing element), `applyDataElementLabelPatch` preserves the unmodelled elements
  (`atom:link`, `typeName`, `dataTypeLength`, an untouched label), and end-to-end against the
  recording stub: window stateful (LOCK→GET→PUT all stateful), GET+PUT carry `sap-language`, a
  separate activation POST after the unlock, element-not-found aborts before the LOCK, a failed PUT
  still releases the lock, the read-back detects a whole-object replacement, an over-long label is
  refused before the lock. `pkg/adt/i18n_test.go` `TestGetDataElementLabels` rewritten for the
  `blue:wbobj` shape.
- Code review (shared with 2ab): 0 CRITICAL/HIGH. Fixed same session — the `application/*` PUT now
  also sends the versioned `Accept`; `verifyDataElementLabelWrite` compares trailing-space-tolerant;
  `patch.checkLengths()` added; `joinLockReleaseErr` hardens the deferred-unlock error wrap against
  a nil base error; the master-language guard is skipped when no language is resolvable; the i18n
  router fills `name` from `target=`.
- **Verified live (2026-09-10)** against the real SAP system, end to end, via a throwaway program
  driving the deployed `pkg/adt.Client` directly (`cmd/verifyitem6/`, deleted after the run — not
  committed; same technique as 2w's #178 verification):
  - `GetDataElementLabels` with `Accept: application/vnd.sap.adt.dataelements.v2+xml` returned the
    full `blue:wbobj` with all four populated `dtel:*FieldLabel` children (the same request with
    `application/xml` 406s).
  - Created scratch `ZVSP_TST_DTEL_LBL` in `$TMP` (`predefinedAbapType` CHAR10). Read the labels.
  - **Partial RMW** — patched `short` + `heading` only: read back
    `short="new-s" medium="orig-medium" long="orig-long" heading="new-heading"` — the two untouched
    labels survived. A raw `blue:wbobj` GET confirmed `dtel:dataType`, `dtel:dataTypeLength` and the
    field-max-lengths were all unchanged (no whole-object replacement).
  - **Full patch** — all four labels set, read back correct.
  - Deleted the scratch DTEL; a follow-up GET returned 404.

### 2ad. Ported upstream PR #201 (the `set_description` half) — change an existing object's SE80/SE11 short text without rewriting source (2026-09-10)
- **The gap**: both forks set `adtcore:description` in the shell POST when an object is *created*, but
  neither could change it afterwards — this fork's `WriteSourceOptions.Description` is only read on
  `WriteSource`'s create branch (`workflows_source.go`), the update branch ignores it, and there was no
  `SetDescription`/`UpdateDescription` anywhere in `pkg/adt/` (grep: zero). Closing that gap is the whole
  of this port. The `vsp update` self-updater bundled into the same upstream PR is **deliberately not
  ported** — it would overwrite this fork's binary with upstream's.
- **The resource**: the description is an attribute (`adtcore:description`, plus an optional
  `adtcore:descriptionTextLimit`) on the object's own metadata document — which is the object's whole
  representation (type, package, flags, `atom:link`s). A struct remarshal would drop everything not
  modelled, so `SetDescription` does the same **read-modify-write on raw bytes** discipline as
  `WriteDataElementLabels` (2ac): GET the document → substitute only the `adtcore:description` attribute
  value in the raw XML → PUT the whole document back → UNLOCK → activate → verify read-back. All
  substitution is confined to the root element's opening tag (`rootOpenTag`/`tagCloseIndex`, quote-aware)
  so a child element carrying its own `description=` is never touched; when the root has no description
  attribute at all it is inserted after `name="..."` (the insert branch — see the live-verification note
  below). Files: `pkg/adt/description.go` (new), `pkg/adt/description_test.go` (new).
- **Types**: upstream #201's set of 8 — PROG, INCL, CLAS, INTF, FUGR, FUNC (needs parent), TABL, DDLS.
  DOMA/DTEL/TTYP/ENQU (this fork's own DDIC creators) are **not** included: their "description" is the
  `ddtext` inside a per-type `blue:wbobj`, a different shape needing its own investigation, and DTEL's is
  easily confused with `WriteDataElementLabels`' field labels (2ac).
- **Session-affinity (issue #91)**: `gateAndMark` runs the package gate above the lock and marks the
  context so the re-read under the lock skips the networked package lookup; the pre-write re-read is
  `Stateful: true`, the PUT is `Stateful: true`; `planTransport`/`resolveWriteTransportFor` choose the
  transport before/after the lock (consistent with 2z — no transport for a `$TMP` object); the
  compensating unlock uses `releaseLockAfterFailure`/`strandedLockAdvice` + an `unlocked bool`, plain
  `UnlockObject` on the happy path. **No `--lang`**: the description is written in the session language,
  it is not a translation (matches upstream — no master-language guard). **No-op without a lock**: when
  the current text already equals the requested one, `SetDescription` returns `Changed:false` and takes
  no lock.
- **`verifyDescriptionWrite`**: reads the object back and turns both a silent no-op (the text did not
  land — trailing-space-tolerant) and a whole-object replacement (`rootLocalName` changed — the PUT was
  accepted as a replace-everything, not a description edit) into a returned error. A read-back that
  itself fails is not treated as a write failure. Activation after the write is best-effort (a note, not
  an error) — whether a description-only PUT even needs activation is unconfirmed.
- **MCP**: `routeDescriptionAction` (`internal/mcp/handlers_description.go`, new) is registered **first**
  in `handlers_universal.go`'s route chain — `routeSourceAction`'s `action=="read"` branch calls
  `handleGetSource` unconditionally for PROG/CLAS/etc. and would shadow a description read otherwise. The
  guards are strict (exact `action` + exact `params.type` of `description`/`set_description`).
  `SAP(action="read", target="PROG ZX", params={"type":"description"})` /
  `SAP(action="edit", target="PROG ZX", params={"type":"set_description","description":"..."})`.
  `handleSetDescription` requires a non-empty `description` (to clear one, use SE80/SE11). New standalone
  tools `GetDescription`/`SetDescription` (`tools_register.go`); `GetDescription` in focused mode,
  `SetDescription` not (it is a write). CLI: `vsp description [TYPE] NAME ["new text"]`
  (`cmd/vsp/description.go`, new). Help text in `handlers_help.go`.
- **Fork⇄upstream deviations**: `docsClient(cmd)` → `resolveSystemParams`/`getClient`; `pkg/adt/textpool.go`
  and `pkg/adt/description.go` do not overlap the way upstream's diff assumes (this fork's text pool went
  in via 2ab); the MCP surface is this fork's individual-tools + a first-in-chain router rather than
  upstream's router-only. **Out of scope, bundled in #201 upstream**: `vsp update`; the `fileparser`
  `withDescription()` integration point (deferred — a separate follow-up); touching
  `WriteSourceOptions.Description`'s update branch (kept orthogonal).
- Code-reviewed: 2 passes, both APPROVE, 0 CRITICAL/HIGH/MEDIUM in the final state. Fixed across the
  passes: regex anchored to the root element (`rootOpenTag`), whole-object-replacement detection
  (`rootLocalName`/`rootBefore`), `tagCloseIndex` quote-awareness so a literal `>` in a root attribute
  value cannot cut the tag short, `vsp description` prints `Notes` to stderr even on the error path.
- **Verified live (2026-09-10)** against the real SAP system with a throwaway Go program
  (`cmd/verifydesc/`, deleted after the run — not committed; same technique as 2w/2ac) driving the real
  `pkg/adt.Client`:
  - **PROG `ZTESTRCG1`**: read (`"Prueba BDC"`, limit 70) → change → read-back correct → restore to
    `"Prueba BDC"` → read-back confirms. A fresh no-cache client confirms the final state. (SE80's quick
    search index lagged the change — an async index, not a code fault; the authoritative ADT metadata
    resource was always correct.)
  - **Read-only GET** on `CLAS ZCL_VSP_RFC_SERVICE`, `INTF ZIF_VSP_SERVICE`, `FUGR ZFG_AGRICULTORES`,
    `DDLS ZCDS_PEDIDO`: `Accept: application/*` does **not** 406 on any of them; descriptions and limits
    (60/60/40/0) read correctly.
  - **Throwaway `$TMP` writes**: `PROG ZVSP_TST_DESC`, `CLAS ZCL_VSP_TST_DESC`, `TABL ZVSP_TST_DESC_T` —
    all three reported `Changed:true` and the read-back matched the new text. The DDIC resource shape
    (`/sap/bc/adt/ddic/tables/`) and the CLAS path (previously structure-only-verified) are confirmed.
  - **The attribute-insert branch could not be exercised live** — SAP requires a description at object
    creation, so a document with no `adtcore:description` never occurs. Covered by the unit test
    `TestSetDescription_InsertsAttributeWhenAbsent`.
- **Stranded `SEOCLSENQ` lock hit while cleaning up the throwaway `ZCL_VSP_TST_DESC` — root-caused and
  fixed in 2ae**: the DELETE of that class kept 403'ing "user is already editing" with a stranded enqueue.
  Follow-up work (2ae) traced it to `DeleteObjectWithAutoLock` trying an `accessMode=DELETE` LOCK first,
  which on this system returns `200` with an empty body — no handle for the caller, yet the enqueue is
  taken — after which the MODIFY retry 403'd and orphaned it. `SetDescription`'s own LOCK/PUT/UNLOCK was
  not the culprit; the `EDITSOURCE` that also reproduced it was going through the same
  `DeleteObjectWithAutoLock` path during cleanup, not the edit itself. Fixed in 2ae by trying MODIFY
  first. `verifyDescriptionWrite` still does not detect a stranded lock (an immediate re-LOCK to check
  would itself strand), but with 2ae the delete path no longer creates one.

### 2ae. Object delete — `accessMode=DELETE` lock strands an enqueue on every class delete; `SearchObject` inside the delete lock window (2026-09-10)
- **Bug A — `DeleteObjectWithAutoLock` stranded a `SEOCLSENQ` enqueue on every class delete**
  (`SAP(action="delete", target="OBJECT")` with no `lock_handle`). The function tried
  `LockObject(objectURL, "DELETE")` first, falling back to `"MODIFY"` on error. Confirmed live with
  `VSP_HTTP_TRACE=1` on a throwaway `$TMP` class: `POST …?_action=LOCK&accessMode=DELETE` returns
  **HTTP 200 with an empty body** on this S/4HANA (2023 FPS03) — `parseLockResult` fails (`EOF`),
  `LockObject` returns an error, the caller discards it — **but the `SEOCLSENQ` X-lock is granted
  server-side** (verified via `ENQUEUE_READ`). The `"MODIFY"` retry then 403s "user is already editing"
  against that just-granted lock, and the enqueue is orphaned because no handle ever reached the client.
  A bare `DELETE` without a handle 423s ("invalid lock handle: ()"), so the handle-less DELETE-mode lock
  is unusable here. `MODIFY`-mode LOCK, by contrast, returns a normal handle and the subsequent `DELETE`
  accepts it — verified by deleting two throwaway classes cleanly that way.
  - **Fix**: `DeleteObjectWithAutoLock` now tries **`MODIFY` first**, and `accessMode=DELETE` only if
    `MODIFY` is refused with something other than a lock conflict (a flat rejection — for a hypothetical
    system that needs DELETE-mode). A `MODIFY` lock conflict returns `strandedLockAdvice` (SM12 / wait
    for the session timeout / it is your own user) instead of falling through to a second strand.
    Not chosen: making `parseLockResult` tolerate the DELETE-mode "shape" — there is no shape, the body
    is empty; `tryCleanupOrphanLock` before the retry — it takes a `MODIFY` lock, which is exactly what
    the DELETE-mode enqueue blocks.
  - **`parseLockResult` + `LockObject` guard**: an empty/whitespace body now returns the sentinel
    `errLockNoHandle`; `LockObject` also rejects a 2xx whose parsed body carries no `LOCK_HANDLE`. A
    handle-less "success" was previously propagated as a valid `*LockResult{LockHandle:""}` — every
    caller then did a handle-less write that 403/423'd. One test mock (`TestClient_WriteSource_Create`)
    relied on that tolerance and was given a real `testLockXML` lock response.
- **Bug B — `DeleteObject` ran `SearchObject` inside the caller's lock window** (`checkMutation` →
  `checkObjectPackageSafety` → `getObjectPackage` → `SearchObject`, an unconditional stateless GET for
  any object, `$TMP` included, whenever a package whitelist is configured). **This system's MCP server
  has one**: `claude_desktop_config.json` sets `env.SAP_ALLOWED_PACKAGES=Z*,$TMP`, and `vsp` (launched
  with no args) reads it via viper's `SAP` env prefix into `cfg.AllowedPackages` → `safety.AllowedPackages`.
  So Bug B is **live-exposed here, not latent** — 2t's claim that `--allowed-packages` "is not set for
  the `abap-adt` MCP server here" checked the CLI `args` and missed the `env`; corrected below.
  The victims are the code paths that lock an object then call `DeleteObject` in the same session:
  `RenameObject` (deletes the old object after building the new one — MCP tool `RenameObject`) and
  `cleanupPartialObject` (runs automatically after a half-failed create — MCP tool `RecoverFailedCreate`).
  On this system, before the fix, both `SearchObject` between LOCK and DELETE → session retired → the
  DELETE 423s (`ExceptionResourceInvalidLockHandle`) → "delete manually" + a stranded lock.
  - **Fix**: `gateAndMark` (the existing `checkMutation` + per-object marker, see 2t) runs the package
    lookup **above** the lock and marks the context; `DeleteObject`'s own `checkMutation` then skips the
    networked step for that object. Applied in `RenameObject`'s old-object delete
    (`pkg/adt/workflows_fileio.go` — a real gate: the old object's package was never checked, only the
    new one's), `cleanupPartialObject` (`pkg/adt/crud.go` — with `Package` known, so no lookup at all),
    and `DeleteObjectWithAutoLock` (was already `checkMutation` above its own lock; now `gateAndMark`).
    `ExecuteABAP`'s cleanup defer already marked its context — unchanged.
- **The MCP `delete` tool now ignores `lock_handle`** (`handleDeleteObject`, `internal/mcp/handlers_crud.go`).
  A handle only ever reaches that handler from a prior MCP `LOCK` call, and a lock handle cannot be
  reused across two tool calls (**issue #169** — each is its own server-side session; the live test
  below 423'd on exactly this). `DeleteObjectWithAutoLock` locks and deletes atomically in one session
  and is always the right thing here. A passed `lock_handle` is noted-and-dropped in the response. The
  now-unused exported `PrepareDelete` helper was removed with it.
- **A PROG delete strands a `TRDIR`/`ESRDIRE` enqueue on this system** even though the `DELETE` returns
  200 with a valid lock handle. Found while live-verifying 2ae; **fixed in 2af below.** Pre-existing, not
  a regression from the MODIFY-first switch (`accessMode=DELETE` and `accessMode=MODIFY` LOCK return the
  identical handle for a PROG, so the final `DELETE` request was byte-for-byte the same as before — SAP
  just keeps the enqueue). Same family as the MSAG limitation (2v).
- **`isLockConflictError` hardened** (`pkg/adt/crud.go`): it only matched the EN substring
  `"currently editing"`, but this system renders the 403 in the logon language ("El usuario X ya está
  tratando Y") — a real MODIFY conflict would have been read as a flat refusal and fallen through to a
  DELETE-mode attempt that strands a second `SEOCLSENQ`. Now keys on the language-independent message
  key `EU/510` (plus the ADT exception-type id and several rendered phrasings as fallbacks).
- Tests (`pkg/adt/session_affinity_test.go`): `TestParseLockResult_EmptyBodyIsNoHandle`,
  `TestLockObject_TwoHundredWithNoHandle_IsError`,
  `TestDeleteObjectWithAutoLock_ModifyFirst_NeverTriesDeleteMode`,
  `TestDeleteObjectWithAutoLock_FallsBackToDeleteModeWhenModifyRefused`,
  `TestDeleteObjectWithAutoLock_ModifyConflict_GivesStrandedAdvice` (realistic ES + EU/510 body),
  `TestIsLockConflictError_LanguageIndependent`,
  `TestDeleteObject_AfterExternalLock_NoSearchInsideWindow` (marker + `assertWindowStateful` with
  `WithAllowedPackages`).
- **Cleanup technique learned**: an orphaned `SEOCLSENQ` (owner session dead) is cleared by
  `ENQUE_DELETE` — the FM SM12 uses — called from `SAP(action="analyze", params={"type":"execute_abap"})`
  (which has the exact in-memory `SEQG3` row, no JSON round-trip of the `0xFF`-padded `GARG`). `ENQUEUE_READ`
  + filter + `ENQUE_DELETE` in one throwaway report. `RS_ACCESS_PERMISSION` and `DEQUEUE_ESEOCLASS` via
  `CALL_RFC` do **not** work (the former has a `REF TO IF_ADT_LOCK_HANDLE` param the RFC bridge can't
  bind — same class of issue as 2y; the latter is session-scoped and won't touch another session's lock).
- Files: `pkg/adt/crud.go` (`parseLockResult`, `LockObject`, `isLockConflictError`,
  `DeleteObjectWithAutoLock`, `cleanupPartialObject`), `pkg/adt/workflows_fileio.go` (`RenameObject`),
  `internal/mcp/handlers_crud.go` (`handleDeleteObject` — ignores `lock_handle`),
  `internal/mcp/tools_register.go` + `internal/mcp/handlers_help.go` (`lock_handle` doc),
  `pkg/adt/session_affinity_test.go`, `pkg/adt/workflows_test.go` (mock lock XML).
- `go build ./...` clean; `go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)` green.
- Code-reviewed: two passes, both APPROVE, 0 CRITICAL/HIGH. Pass 2's 1 MEDIUM — `isLockConflictError`
  matched only the EN message text and would misclassify the ES-rendered 403 this system sends — was
  fixed (EU/510 key). LOW, not changed: the stale MODIFY `err` after a DELETE-mode fallback is captured
  as `modifyErr` for the messages and never read as live state; `cleanupPartialObject` returns before
  the orphan-lock sweep when the gate refuses (not taking a lock policy forbids is more correct).
- **Verified live (2026-09-10)** against the real SAP system, twice — first with a throwaway Go program
  (`cmd/verify2ae/`, deleted) driving the real `pkg/adt.Client` directly, then through the **deployed MCP
  tool** after a Claude Desktop restart — on a throwaway `$TMP` class `ZCL_VSP_TST_DELLOCK`:
  - **Auto-lock path** (Go: `DeleteObjectWithAutoLock`; MCP: `SAP(action="delete", target="OBJECT",
    params={"object_url": ".../zcl_vsp_tst_dellock"})` with no `lock_handle`): Go trace showed `LOCK
    accessMode=MODIFY` → 200, `DELETE` → 200, `err=nil`, **no `accessMode=DELETE` LOCK issued**; the MCP
    call returned `"Object deleted successfully"` on the **first** try (this is the exact call that 403'd
    "user is already editing" over and over in the 2ad session). `ENQUEUE_READ` after each: **zero
    `SEOCLSENQ`** — the strand that used to happen on every class delete is gone.
  - **Bug B path (Go)** (`gateAndMark(OpDelete)` → `LockObject(MODIFY)` → `DeleteObject`, client built
    with `WithAllowedPackages` — what `RenameObject`/`cleanupPartialObject` now do): trace showed the
    `informationsystem/search` package lookup fire *before* the LOCK, then `LOCK` → 200, `DELETE` → 200
    with **no request between them**, `err=nil`, zero stranded enqueues. The pre-fix shape (`DeleteObject`
    running that search itself, after the caller's lock) is what 423'd.
  - **`lock_handle` ignored (MCP)**: before the `handleDeleteObject` change, `SAP(action="edit",
    target="LOCK")` then `SAP(action="delete", ..., "lock_handle": ...)` in a separate call →
    **423 `ExceptionResourceInvalidLockHandle`** (issue #169 — the handle's session is gone by the
    second call). After the change the handler ignores the handle and auto-locks, so the same two calls
    delete cleanly; a `LOCK` left dangling by the caller still needs its own `UNLOCK` (or it is a
    self-conflict the MODIFY-first delete reports via `strandedLockAdvice`).
  - **`RenameObject` live (Bug B, MCP)**: could not be exercised end to end — `SAP(action="edit",
    params={"type":"rename", "objType":"PROG/P", ...})` on a throwaway `$TMP` program fails at step 4
    (write source to the new shell) with `400 ExceptionInvalidData` "Elemento abapProgram previsto" — a
    **separate pre-existing `RenameObject`/PROG bug**, unrelated to Bug B (which is step 6, the
    old-object delete, never reached). The failed rename cleaned up its own locks (zero stranded
    enqueues — the `newUnlocked`/`oldReleased` defers from 2t/2u). Bug B's mechanism is covered by the
    Go-level `gateAndMark→LOCK→DELETE` verification above and `TestRenameObject_ReleasesOldLockWhenDeleteFails`.

### 2af. Object delete — best-effort `UNLOCK` after a successful DELETE, to release the PROG `TRDIR`/`ESRDIRE` enqueue (2026-09-10)
- **The bug** (found while live-verifying 2ae, flagged there as a follow-up): deleting an ABAP **program**
  via ADT on this S/4HANA (2023 FPS03) leaves the `ESRDIRE`/`TRDIR` enqueue that `LockObject`'s
  `accessMode=MODIFY` LOCK acquired stranded in SM12. The `DELETE` returns 200 and the program is genuinely
  gone, but ADT's *program* delete handler does not dequeue that lock. It is released only by an explicit
  `UNLOCK` or when the stateful ADT session ends. A long-running MCP server clears it on its next stateless
  request (any later tool call) — which is why `SAP(action="delete")` immediately followed by an
  `ENQUEUE_READ` check showed nothing; a one-shot `vsp` CLI that deletes and exits leaves it until SAP's
  session reaper (~60 min).
- **Live diagnosis** (throwaway `cmd/deltrace` Go program driving `pkg/adt.Client` directly with
  `VSP_HTTP_TRACE=1`, holding the session open 30 s while an `ENQUEUE_READ`-in-`execute_abap` inspected the
  lock table; deleted after, not committed):
  - PROG delete, nothing after → `TRDIR/<PROG>/X` enqueue stranded, **persists after the client process
    exits** (TCP close / process exit does not release it — the stateful ADT session survives the
    disconnect via `sap-contextid`).
  - PROG delete → explicit `POST {objectURL}?_action=UNLOCK&lockHandle=<same handle>` (stateful): SAP
    returns **200 with no error** even though the DELETE already consumed the handle, and the enqueue is
    **released**.
  - PROG delete → any stateless request (`GET /sap/bc/adt/compatibility/graph`, abap-adt-api's
    `dropSession`): session retired, enqueue also released — but a session-retiring hop is the wrong tool
    in a long-running server (it would drop other in-flight lock windows).
  - **CLAS / INTF / TABL delete → never strand** — their ADT delete handlers dequeue their own enqueue.
    This is PROG-specific, not general to non-CLAS.
  - `marcellourbani/abap-adt-api` (`src/api/delete.ts`) sends no post-delete `UNLOCK` either — but Eclipse
    keeps one long session and eventually `logout()`s (`/sap/public/bc/icf/logoff`), so it never notices.
- **Fix**: new `(*Client).bestEffortUnlockAfterDelete(ctx, objectURL, lockHandle)` (`pkg/adt/lock_release.go`)
  — a detached-context (`context.WithoutCancel` + 30 s, same pattern as `releaseLockAfterFailure`) `UNLOCK`
  whose error is logged to `LogOutput` (`[adt] post-delete unlock failed …`) but never returned. Called
  after a successful `DELETE` in **`DeleteObject`** (covers `cleanupPartialObject`, `RenameObject`'s
  old-object delete, and `ExecuteABAP`'s cleanup defer — the last also closes the `ZTEMP_EXEC_*`
  orphan-enqueue leak 2ae listed as untouched) and in **`DeleteObjectWithAutoLock`** (its own inline
  DELETE), before the existing `noteLockClosed`. Stateful, same session as the DELETE, scoped to one
  handle — no effect on other concurrent operations. Harmless for CLAS/INTF/TABL (200 on the
  already-consumed handle here; a 404 on a stricter system is swallowed).
- Tests (`pkg/adt/session_affinity_test.go`): `TestDeleteObjectWithAutoLock_UnlocksAfterDelete`,
  `TestDeleteObject_UnlocksAfterDelete`, `TestDeleteObjectWithAutoLock_UnlockFailureDoesNotFailDelete`,
  `TestBestEffortUnlockAfterDelete_EmptyHandleSkipsTheUnlock`,
  `TestBestEffortUnlockAfterDelete_RunsOnACancelledContext`.
- Files: `pkg/adt/lock_release.go`, `pkg/adt/crud.go` (`DeleteObject`, `DeleteObjectWithAutoLock`),
  `pkg/adt/session_affinity_test.go`.
- `go build ./...` clean; `go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)` green.
- Code-reviewed: 1 pass, APPROVE, 0 CRITICAL/HIGH/MEDIUM. 3 LOW: (1) log the swallowed UNLOCK error —
  **applied**; (2) the synchronous UNLOCK can add up to 30 s to a delete's return on a hung session —
  noted, matches the existing `releaseLockAfterFailure` tradeoff, not changed; (3) add empty-handle and
  cancelled-context tests — **applied**.
- **Verified live (2026-09-10)**: rebuilt `cmd/deltrace` against the fixed `pkg/adt`, ran a PROG
  `DeleteObjectWithAutoLock` on throwaway `$TMP` `ZVSP_TST_DELPROG`; the trace showed `LOCK MODIFY → DELETE
  → POST _action=UNLOCK` (same handle, `err=nil`), and an `ENQUEUE_READ` during the 30 s session-hold
  returned **zero** enqueues (`TOTAL=0`) — the case that showed `TRDIR/ZVSP_TST_DELPROG/X` before the fix.
  Also verified through the **deployed MCP tool** after a Claude Desktop restart: `SAP(action="edit",
  target="PROG ZVSP_TST_DELPROG")` then `SAP(action="delete", ...)` → `"Object deleted successfully"`, the
  program gone, and an `ENQUEUE_READ` sweep clean — and the chain of `execute_abap` checks around it left
  **no `ZTEMP_EXEC_*` TRDIR orphans** (pre-fix, every `execute_abap` call stranded one — `ExecuteABAP`'s
  cleanup defer calls `DeleteObject`, which now unlocks).

### 2ag. `RenameObject` — source write went to the bare object URL, not `…/source/main` (400 on every rename) (2026-09-10)
- **The bug** (`task_b894bc6f`, live-confirmed): `SAP(action="edit", params={"type":"rename", …})` failed at
  step 4 ("write source to the new object") with `400 ExceptionInvalidData` ("Elemento `abapProgram`
  previsto") for **every** object type. `RenameObject` (`pkg/adt/workflows_fileio.go`) did
  `newURL, _ := c.buildObjectURL(objType, newName)` then `c.UpdateSource(ctx, newURL, …)` — but
  `buildObjectURL` returns the *bare* object URL and `UpdateSource` (`pkg/adt/crud.go`) PUTs the body to
  exactly the URL it is given. Plain ABAP landed on `/sap/bc/adt/programs/programs/<name>` where SAP expects
  the metadata XML → 400. Step 1 (the source GET) already used `oldURL+"/source/main"` correctly; step 4
  just dropped the suffix. The new shell object *was* created, so a failed rename left an empty shell + the
  original object intact.
- **Fix** (`pkg/adt/workflows_fileio.go`): `c.UpdateSource(ctx, newURL+"/source/main", …)`. Minimal — not
  a delegation to `WriteProgram`/`WriteClass` (no `WriteInterface` exists, so a type switch would keep the
  hand-rolled path anyway; delegating would also add `SyntaxCheck`/`planTransport` behaviour changes). The
  `withMutationPackageChecked` mark still matches via `canonicalizeObjectURL` (collapses `/source/main`), so
  no new stateless hop inside the lock window (issue #91).
- **Type allowlist guard** at the top of `RenameObject`, *before* any network call or shell creation: only
  `CLAS/OC`, `PROG/P`, `INTF/OI`, `PROG/I` (one editable `/source/main` document). `FUGR/F` (source split
  across the top include, `UXX` includes and function modules — a copy-and-delete cannot reproduce it),
  `FUGR/FF` (no `parentName` param), DDIC/RAP types → rejected with a plain `error` the MCP handler
  surfaces. MCP tool doc + handler hint now list `PROG/I` instead of the never-working `FUGR/F`.
- **Shell rollback**: on a step-4 (source write) or step-5 (activate) failure, `rollbackShell()` unlocks the
  new object (flipping `newUnlocked` so the deferred `releaseLockAfterFailure` no-ops) then
  `DeleteObjectWithAutoLock(newURL)` to remove the empty shell; reports the rollback outcome in
  `result.Errors`, clears `result.Transport`/`TransportNote`, keeps `Success=false`. Avoids an orphan shell
  that would block a retry with "already exists".
- **Transport correctness (the golden rule)**: the hand-rolled step 4/5 passed the raw `transport` param
  straight through — a transportable rename with `transport=""` let SAP auto-generate a "Generated Request
  for Change Recording" per write. Now `CreateObject` gets `Chosen: &shellChoice` to capture the request it
  picked for the shell; step 4 calls `resolveWriteTransportFor(&shellChoice, transport, lockResult.CorrNr,
  "RenameObject")` (mirrors `WriteProgram` `workflows.go:99-107`) and writes the source under that
  `effectiveTransport`; step 6 (old-object delete) calls `resolveWriteTransport(transport,
  oldLockResult.CorrNr, "RenameObject")` to adopt the *old* object's own open request. New
  `Transport`/`TransportNote` fields on `RenameObjectResult`.
- **Step 6 (old-object delete) reached + verified for the first time**: the `gateAndMark(OpDelete, oldURL)`
  fix from commit `25220db` / CLAUDE.md 2ae had never executed end-to-end because step 4 always failed
  first. Live traces now show `LOCK MODIFY <old> → DELETE <old> → UNLOCK` with nothing between the lock and
  the DELETE, and `ENQUEUE_READ` sweeps clean.
- Tests (`pkg/adt/session_affinity_test.go`): `TestRenameObject_SourcePutTargetsSourceMain` (asserts the
  PUT path is `newURL+"/source/main"`, `assertWindowStateful`, no `informationsystem/search` in the window,
  order source-PUT < activate < delete-old), `TestRenameObject_RejectsUnsupportedType` (FUGR/F → error,
  zero wire calls), `TestRenameObject_RollsBackShellWhenSourceWriteFails` (400 on the source PUT → a DELETE
  for the new shell, none for the old object, `Success=false`),
  `TestRenameObject_TransportableRename_AdoptsChosenRequest` (transportable package, `transport=""`, single
  candidate `TR-A` → `result.Transport=="TR-A"` and source PUT `corrNr=="TR-A"`). Existing
  `TestRenameObject_ReleasesOldLockWhenDeleteFails` unchanged and still green.
- Files: `pkg/adt/workflows_fileio.go`, `pkg/adt/session_affinity_test.go`,
  `internal/mcp/tools_register.go`, `internal/mcp/handlers_fileio.go`.
- `go build ./...` clean; `go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)` green.
- Code-reviewed: 1 pass, APPROVE, 0 CRITICAL/HIGH. 1 MEDIUM (Phase 2 had no test coverage) — **fixed**
  (`TestRenameObject_TransportableRename_AdoptsChosenRequest`). 3 LOW: comment overstated where the
  old-object delete's transport comes from — **fixed**; `result.Transport` left populated after a rollback —
  **fixed** (cleared); pre-existing `gofmt`/CRLF non-compliance in the file — noted, not this change's.
- **Verified live (2026-09-10)** against the real SAP system, both ways:
  - **Throwaway Go program** (`cmd/verifyrnm/`, deleted — not committed) driving `pkg/adt.Client` with
    `VSP_HTTP_TRACE=1`: PROG `ZVSP_TST_RNM_A`→`_B` and CLAS `ZCL_VSP_TST_RNM_A`→`_B` in `$TMP` both
    succeeded (new object has the substituted name, old object 404s), the trace showed every source PUT
    going to `…/source/main` and step 6 deleting the old object with no request between LOCK and DELETE,
    `FUGR/F` rejected before any call, `ENQUEUE_READ` sweep `SWEEP=[]`, zero `423` in the whole trace.
  - **Deployed MCP tool** after a Claude Desktop restart: `SAP(action="edit", params={"type":"rename",
    "objType":"PROG/P"|"CLAS/OC", …})` on the same throwaway names → `success:true`, new object reads back
    with the substituted name, old object 404s; `FUGR/F` → `RenameObject failed: … only CLAS/OC, PROG/P,
    INTF/OI and PROG/I …`; `ENQUEUE_READ` sweeps `SWEEP=[]` / `FINAL=[]`; both `_B` objects deleted clean.
- **Known limitation, documented not fixed** (pre-existing, flagged by code review): the name substitution
  is `strings.ReplaceAll(source, oldName, newName)` — a short old name that is a substring of another
  identifier (`ZCL_A` inside `ZCL_ABC`) would be mangled. Out of scope for this fix; use non-substring
  names.

### 2ah. `vsp debug ui` — local web UI for the ABAP debugger, ported from upstream #186-190 (own WebSocket bridge) + `pkg/adt/debugger.go` session-affinity fix (2026-09-11)
- **Scope decision (made explicitly, before implementation)**: upstream's own rewrite of this prototype
  (PRs #187/#188) moves the debugger session onto a new `pkg/saprfc` — classic RFC via
  `github.com/oisee/open-rfc-go`, a pure-Go but self-described "early" library — with its own parallel
  ADT-over-RFC debugger session, replacing the WebSocket (ZADT_VSP) bridge this fork already has and relies
  on throughout (breakpoints, RunReport/RunRFC triggering, text pool, RFC search/metadata — see 2h, 2g,
  2ab). **Only the UI was ported, not that dependency**: `cmd/vsp/debug_ui.go` drives exactly the two
  clients `cmd/vsp/debug.go`'s existing REPL already uses — `*adt.Client`'s `Debugger*` methods
  (`pkg/adt/debugger.go`) for the session itself (listen, attach, step, stack, variables, detach), and
  `*adt.DebugWebSocketClient` (ZADT_VSP) for breakpoints and for triggering a report/function module. No
  new dependency, no second parallel debugger implementation.
- **What was built**: `cmd/vsp/debug_ui.go` (a `//go:embed`-ed static page + a small JSON API, no build
  step, no CDN — works on a laptop behind a proxy), `cmd/vsp/debug_ui.html`, `cmd/vsp/debug_ui_test.go` (8
  tests: embed actually serves content, unknown paths 404, detached-state shape, step-type allowlist, the
  bespoke read-only guard blocks `/api/bp` before touching a nil `wsClient`, read-only still allows Listen,
  `tryStart`/`finish` serialize concurrent sessions, a missing `object` param reports a note instead of
  reaching a nil client). `vsp debug ui [--port 7799] [--user DEVELOPER]`.
- **Deliberate differences from upstream's own UI PR**, each a consequence of the WebSocket-bridge
  decision above: no `/api/sys` (system listing — this fork resolves via `-s`/`.vsp.json`/env like every
  other command, not a runtime system switcher); `Run report`/`Run RFC` are two distinct endpoints
  (`/api/run/report`, `/api/run/rfc`), not one generic "run" that upstream's `pkg/saprfc` session can
  dispatch by target type; a bespoke `readOnly` guard (`debugUIServer.readOnly`) blocks only the
  SAP-side-action handlers (`/api/bp`, `/api/run/*`) — stepping/stack/detach are never blocked, since this
  domain has no object URL/package for `pkg/adt/safety.go`'s `checkMutation` to gate, so the usual mutation
  gate does not apply here.
- **`-s` (named system) fix in `cmd/vsp/debug.go`**, found while building the UI (the REPL and the new UI
  share `runDebug`'s system-resolution path): `vsp debug -s a4h` reported "SAP URL is required" while `vsp
  deploy -s a4h` worked against the same system. Root cause: `runDebug` read only the global `cfg`
  (`resolveConfig`/`validateConfig`/`createADTClient()`), which a named `-s` system never populates — every
  other `-s`-aware command (`texts`, `description`, ...) instead calls `resolveSystemParams(cmd)` +
  `getClient(params)`. Fixed by switching `runDebug` to that same pattern; `wsClient`/`printDebugBanner`
  updated to read from `params` instead of `cfg`.
- **Code review of the UI itself** (before this session's live verification): 0 CRITICAL/HIGH, 1 MEDIUM + 1
  LOW fixed — `triggerAndCatch`'s listener goroutine is explicitly waited-for on every exit path (including
  a fast trigger failure) rather than left to outlive the request, since the caller's deferred `finish()`
  releasing the "busy" guard while that goroutine is still live against SAP could let a retry start a second
  concurrent listener on the same debug session; `ensureBreakpoint`'s fail-open-on-`GetBreakpoints`-error
  behavior got an explanatory comment (both share the same WS transport, so a connection problem serious
  enough to matter almost always fails the following `SetLineBreakpoint` too — failing open on the read
  alone, not the write, keeps a transient hiccup from blocking a Run it would not otherwise have blocked).
- **Timeout-margin bug found live, fixed** (own new code, no user approval needed — a direct correctness
  fix to files already in this task's own deliverable): `handleRunReport`/`handleRunRFC` used
  `TimeoutSeconds: 60` and `handleListen` defaulted `seconds` to 60, colliding with `pkg/adt/config.go`'s
  hard `Config.Timeout = 60 * time.Second` (`http.Client.Timeout`, an absolute per-request cutoff Go
  enforces regardless of any context deadline or the `timeout` query param sent to SAP) — a `TimeoutSeconds`
  at or above 60 always loses that race, surfacing as `"context deadline exceeded (Client.Timeout exceeded
  while awaiting headers)"` after a ~70s wait instead of the clean "nobody stopped within Ns" a Listen
  timeout is meant to return. `cmd/vsp/debug.go`'s `runProgram` had already hit the same ceiling and backs
  off to 30s for exactly this reason — the new UI code just hadn't copied that margin. Fixed with a new
  `maxListenSeconds = 45` constant (standalone Listen) and `30` (trigger-and-catch, matching `runProgram`'s
  own value) for `handleRunReport`/`handleRunRFC`; `debug_ui.html`'s matching UI copy updated. Confirmed
  live: after the fix, "Listen only" + an external trigger (`execute_abap`) produced a genuine catch
  (`{"attached":true,...,"note":"stopped at ZVSP_TST_DBGUI:11"}`) instead of the timeout.
- **The main fix this session — `pkg/adt/debugger.go` had the #91 session-affinity defect, never fixed
  anywhere on this line**: live verification of the new UI (breakpoint set on a throwaway `$TMP` report,
  caught via `execute_abap` as the trigger — sidesteps the documented `RUN_REPORT`/#113
  `APC_ILLEGAL_STATEMENT` bug entirely, since breakpoints fire regardless of which session/mechanism
  executes the target code) got past `DebuggerListen`/`DebuggerAttach` cleanly, but the very next call
  (`DebuggerGetStack`, invoked internally by the UI's `snapshot()`) failed with HTTP 404 / SAP error
  `noSessionAttached` (T100 key `SY/530`). None of the debugger domain's HTTP methods ever set
  `Stateful: true` in their `RequestOptions` — the same #91 defect this codebase has fixed repeatedly
  elsewhere (CLAUDE.md §1, 2t, 2w, 2x, 2z, 2ab-2ag) had simply never been applied to this REST surface.
  Confirmed pre-existing (not introduced by the new UI code) and confirmed to affect the already-deployed
  production MCP tool too, identically, via `SAP(action="debug", target="GET_STACK")`.
  - **Fix**: added `Stateful: true` to the `RequestOptions` literal in 11 methods, in three tiers — **Tier
    1** (proven necessary by the live failure): `DebuggerAttach`, `DebuggerStep` (also backs
    `DebuggerDetach`), `DebuggerGetStack`, `DebuggerGetVariables`, `DebuggerGetChildVariables`,
    `DebuggerGoToStack`, `DebuggerSetVariableValue`. **Tier 2** (same domain, consistency —
    `DebuggerListen` got a code comment on its own tradeoff: a long Listen now holds a dedicated stateful
    session for up to `TimeoutSeconds+30s` instead of sharing the pool): `DebuggerListen`,
    `DebuggerCheckListener`, `DebuggerStopListener`. **Tier 3** (dead code, no caller anywhere in the
    codebase — fixed to avoid a future landmine): `DebuggerBatchRequest`. **Deliberately not touched**: the
    five `/debugger/breakpoints*` functions in the same file (`SetExternalBreakpoint`,
    `GetExternalBreakpoints`, `DeleteExternalBreakpoint`, `DeleteAllExternalBreakpoints`,
    `ValidateBreakpointCondition`) — already marked `DEPRECATED` in favor of ZADT_VSP, out of scope.
  - Tests: `pkg/adt/session_affinity_test.go` — `TestDebuggerSession_TierOneCallsAreStateful`
    (table-driven, all 7 Tier-1 methods), `TestDebuggerListen_IsStateful`,
    `TestDebuggerCheckListener_IsStateful`, `TestDebuggerStopListener_IsStateful`,
    `TestDebuggerBatchRequest_IsStateful`. A POST/DELETE with no cached CSRF token triggers a leading
    token-fetch request first (correctly never stateful), so the Tier-2/3 tests search the wire trace for
    the specific method+path rather than assuming exactly one call.
  - Code-reviewed: 0 CRITICAL/HIGH/MEDIUM/LOW. Verdict APPROVE — all 11 methods confirmed correctly fixed
    (including that `DebuggerDetach`/`DebuggerStepWithBatch` inherit `Stateful: true` transitively, since
    they delegate rather than call `transport.Request` themselves), deprecated functions confirmed
    untouched, `Stateful: true` confirmed as this codebase's one established mechanism (backed by the
    persistent `http.CookieJar` every other `Stateful: true` fix already relies on — `pkg/adt/config.go`),
    tests independently confirmed non-vacuous (fail before the fix, pass after).
  - **Verified live (2026-09-11)**, against the real SAP system, through the rebuilt throwaway UI server:
    two independent `Listen → Attach → GetStack (+ GetChildVariables)` round trips — each issued as
    separate HTTP requests, not one call — both succeeded with real stack data and no `noSessionAttached`
    on either. `DebuggerStep` (stepOver) on a live-attached session also completed cleanly (`"debuggee
    terminated"`, a normal successful outcome — no exception). `DebuggerDetach` cleanly released a
    still-attached session with no error. Note: an early `DebuggerStep(stepContinue)` attempt on a dynpro
    (`SAPMSSY0`, `PAI SCREEN`) frame errored (`SADT_REST/006`, a different subtype than the fixed `SY/530`)
    and a subsequent Attach once briefly hit "Debuggee already attached" — both are session-churn artifacts
    of rapid repeated manual test attempts against one single-threaded debug session/terminal ID, resolved
    cleanly by one Detach, not a recurrence of the fixed defect; not investigated further as out of scope
    for this fix.
- **Live-verification checklist status**: breakpoint set + Listen + external trigger + catch — verified.
  `DebuggerGetStack`/`DebuggerGetChildVariables` after Attach — verified (this was the specific defect).
  Stepping (Over) — verified once, cleanly. Detach — verified, clean. `Run report`/`Run RFC`, `--read-only`
  gating, source display, and the `-s` REPL fix itself (no `.vsp.json` with a named system exists in this
  project to test against) were **not** exercised live this session — noted as a gap, not fixed.
- Files: `cmd/vsp/debug_ui.go` (new), `cmd/vsp/debug_ui.html` (new), `cmd/vsp/debug_ui_test.go` (new),
  `cmd/vsp/debug.go` (`-s` fix), `pkg/adt/debugger.go` (`Stateful` fix), `pkg/adt/session_affinity_test.go`
  (5 new tests).
- `go build ./...` clean; full suite green (`go test $(go list ./pkg/... ./internal/... | grep -v
  pkg/cache)`).

### 2ai. CLI safety-flag propagation — ported upstream #122650182/#b9769d4/#ae5f684, closes the `debug ui --read-only` gap 2ah left open (2026-09-11)
- **The gap this closes**: 2ah's own live-verification checklist explicitly listed `--read-only` gating
  as "not exercised live, noted as a gap" — investigating it surfaced a much bigger problem than a single
  missing test. `--read-only`/`--allowed-packages`/`--enable-transports`/`--transport-read-only`/
  `--allowed-transports`/`--allow-transportable-edits`/`--block-free-sql` are registered on
  `rootCmd.Flags()` (`cmd/vsp/main.go`), which Cobra scopes to the root command only — never inherited by
  any subcommand. `vsp debug ui --read-only` (and `vsp --read-only debug ui`) both fail with `unknown
  flag: --read-only`, confirmed empirically. Worse: `cfg.ReadOnly` (the field these flags populate) is
  only ever read from `resolveConfig()`, called solely by the bare/MCP-server `RunE` path — none of the 16
  files using the `resolveSystemParams(cmd)` + `getClient(params)` CLI pattern (`debug_ui.go` included)
  ever saw it. The documented per-system `.vsp.json` `"read_only": true` / `"allowed_packages": [...]`
  (already parsed into `pkg/config.SystemConfig`, used in `ExampleConfig()`'s `"prod"` example) were
  silent no-ops on this whole code path — `systemParams` had no such fields at all.
- **Not a new design — already fixed upstream**, in 3 separate merged commits found via `gh` search/API
  before planning anything (`122650182b4e8046235834f8f252b6c133f00ae7`, `b9769d490a83430c086bb7029c2d8fd7`,
  `ae5f684228a548ac71c39cffe9f7b90e52b517b6`). Ported and adapted to this fork's actual file layout, not a
  mechanical `git cherry-pick` — this fork's `cmd/vsp/cli.go`/`workflow.go` differ enough from upstream's
  that a literal patch would not apply.
- **`systemParams` (`cmd/vsp/cli.go`) gains 7 fields**: `ReadOnly`, `AllowedPackages`, `EnableTransports`,
  `TransportReadOnly`, `AllowedTransports`, `AllowTransportableEdits`, `BlockFreeSQL`. `SystemConfig`
  (`pkg/config/systems.go`) gains the 5 transport ones as new JSON keys (`ReadOnly`/`AllowedPackages`
  already existed there, parsed but previously dead). `resolveSystemParams` populates all 7 in **both**
  branches — the named-system branch (`sys.X`, merged with `envFlag("SAP_X")`/`splitList(os.Getenv(...))`
  via new `firstNonEmptyList` so a `.vsp.json` value isn't silently overridden by an unset env var) and the
  bare-`SAP_*`-env-var fallback branch. **Deliberate deviation from upstream's literal diff**: upstream
  only added the 5 transport fields to the named-system branch, leaving the fallback branch asymmetric (a
  gap its own `ReadOnly`/`AllowedPackages` commit didn't have). Decided to make all 7 symmetric in both
  branches instead, because this project's actual MCP deployment
  (`claude_desktop_config.json`'s `env.SAP_ALLOWED_PACKAGES=Z*,$TMP` etc., see 2t's correction) uses the
  bare-env-var fallback path exclusively — no `.vsp.json` in production here — so the fallback branch is
  the one that matters in practice, not an edge case to leave asymmetric.
- **`getClient`/`buildClient`** now builds an `adt.SafetyConfig` via `adt.UnrestrictedSafetyConfig()` +
  conditional overrides + `adt.WithSafety(safety)`, gated behind a local `restricted bool` (pure
  optimization — `adt.NewConfig`'s own default is already unrestricted, so the gate changes no behavior,
  confirmed by code review). New helpers `splitList(v string) []string` (comma-separated, trims, drops
  blanks) and `envFlag(name string) bool` (`"true"/"1"/"yes"/"on"`, case-insensitive).
- **`cmd/vsp/workflow.go`**: new `backfillGlobalConfig(params *systemParams)` copies
  URL/User/Password/Client/Language/Insecure into the global `cfg` struct — for the handful of call sites
  (`debugSession.printInfo()`, `lua.go`'s verbose banner) that still read `cfg.*` directly rather than
  taking a `systemParams`. New `createADTClientFor(cmd) (*adt.Client, error)` = resolve + backfill +
  `getClient`. `runWorkflow`/`runTestWorkflow` migrated to it, keeping their `processCookieAuth(cmd)` call
  (some workflow steps read `cfg.Cookies` directly — matches upstream's own, slightly inconsistent, real
  diff rather than "cleaning it up"). Old `createADTClient()` (reads only the global `cfg`, never resolves
  `-s`) kept as-is with a `// Deprecated` comment — still used by `cmd/vsp/lsp.go`, deliberately **not**
  ported: confirmed upstream's own `lsp.go` is byte-identical to this fork's (fetched via raw GitHub) and
  has the same unfixed bug, despite the upstream commit message claiming "lsp" was fixed. Not this fork's
  gap to close alone.
- **`cmd/vsp/lua.go`**: `runLua` migrated to `client, err := createADTClientFor(cmd)` directly, no
  `processCookieAuth` call — matches upstream's actual diff for this file.
- **`cmd/vsp/debug.go`**: already fixed for `-s` in the 2ah session (`resolveSystemParams`/`getClient`
  instead of `resolveConfig`/`createADTClient()`). One line added: `backfillGlobalConfig(params)` right
  after `getClient` succeeds — closes a smaller gap found while building this fix:
  `debugSession.printInfo()` printed a blank `System:` line under `-s` since `cfg.BaseURL` was never
  populated. Deliberately **not** switched to `createADTClientFor(cmd)` — that would re-run
  `resolveSystemParams` a second time, duplicating its verbose stderr logging.
- **`cmd/vsp/debug_ui.go`** — the actual bug this whole investigation started from: `readOnly:
  cfg.ReadOnly` (line 150, always `false` on this code path) → `readOnly: client.Safety().ReadOnly`
  (`client` already in scope, and `client.Safety()` now genuinely reflects the resolved policy).
- Tests: `cmd/vsp/cli_safety_test.go` (new) — upstream's 4 (`TestGetClientHonoursDeclaredSafety`,
  `TestGetClientCarriesAllowedPackages`, `TestGetClientUnrestrictedByDefault`, `TestSplitListIgnoresBlanks`)
  ported, plus a new `TestGetClientCarriesTransportSafety` table (5 subtests, one per transport field) —
  upstream's own transport-safety commit added no test coverage at all, this fork's does.
- Code-reviewed: 0 CRITICAL/HIGH/MEDIUM. 1 LOW (fixed same session) — the named-system branch initially
  merged only the 5 boolean transport fields with their env vars, leaving `ReadOnly`/`AllowedPackages`/
  `AllowedTransports` unmerged there (matching `pkg/config.SystemConfig.GetSystem()`'s own pre-existing
  pattern, where only `Password`/`TransportAttribute`/`Cache` merge with env — not a new defect, but
  inconsistent given the other 4 transport-safety fields do merge). Fixed for full symmetry via
  `firstNonEmptyList`.
- **Deliberately out of scope**: no `PersistentFlags()` change in `cmd/vsp/main.go` — `vsp debug ui
  --read-only` as a literal CLI flag on a subcommand still fails with `unknown flag`. Only the
  `.vsp.json`/`SAP_*` env var path is fixed. `cmd/vsp/lsp.go` untouched (see above).
- `go build ./...` clean; full suite green (`go test $(go list ./pkg/... ./internal/... | grep -v
  pkg/cache)`), including the 9 new tests in `cli_safety_test.go`.
- **Verified live (2026-09-11)** via the rebuilt throwaway binary against a scratchpad-only `.vsp.json`
  (never placed in the tracked project directory — it isn't gitignored and a real test config could carry
  real URL/user info, see 2ah/2t's security notes): added a `devsys_ro` system
  (`"read_only": true`, alongside the pre-existing `devsys`). `vsp -s devsys debug`'s `info` command and
  `vsp -s devsys_ro debug`'s `info` command both now print the real system URL (previously blank under
  `-s`, the `printInfo()` gap this session also closed) — confirms `backfillGlobalConfig` in both
  `debug.go` and the `createADTClientFor` path. `vsp -s devsys_ro lua -v -e 'print(1)'` printed a correct
  verbose banner (`Connected to: https://…`, `Client: 100, Language: ES`) and ran — confirms `lua.go`'s
  migration. **`vsp -s devsys_ro debug ui` blocked on ZADT_VSP being unavailable for the rest of this
  session** (whole-system `HTTP 503` on every `/sap/bc/adt/...` call, confirmed with a plain `search` too —
  not scoped to ZADT_VSP or to this fix; SAPGUI and SMICM's HTTP/HTTPS service rows stayed green throughout,
  so it wasn't the ICM process itself; resolved on its own, cause not root-caused). **Completed in a later
  session (2026-09-14)**, once the system recovered: `vsp -s devsys_ro debug ui --port 7801` printed
  `read-only: breakpoints and triggering are disabled` on startup, and `curl -X POST /api/bp`,
  `/api/run/rfc`, `/api/run/report` each returned `403` with the expected `"read-only mode: cannot ..."`
  note — closing the original bug this whole port exists to fix. The `readOnly` wiring itself
  (`client.Safety().ReadOnly` correctly reflecting `systemParams.ReadOnly`) is covered by
  `TestGetClientHonoursDeclaredSafety` and confirmed correct by code review; the guard logic it feeds
  (`handleBreakpoint`/`handleRunReport`/`handleRunRFC` returning 403) was unchanged by this fix and already
  had its own unit tests from 2ah.

### 2aj. Upstream contribution — closed the last 3 open compensating-unlock sites for issue #166 (2026-09-14)
- **Not a fix in this fork's own codebase** — this section documents an upstream contribution (a PR sent
  to `oisee/vibing-steampunk`), not a change to any file under `pkg/`/`cmd/`/`internal/` in this repo. No
  file in this project's tracked tree changed as a result; recorded here only because the project
  convention is to log every upstream interaction, per `docs/upstream-review-2026-09-priority-list.md`
  (gitignored) and prior sections like `2e`.
- **GitHub monitoring**: a full sweep of recent upstream activity found oisee had mentioned/thanked
  txape10 in 4 places from a 2026-09-11 triage run (PR #214 landing PR #150, issues #91/#118/#166), and
  that `mahlzeit1948` confirmed issue #116 fixed on `main` (2026-09-13) — no action needed there. Before
  drafting anything for issue #166, checked thoroughly whether a solution had already been proposed: found
  that upstream contributor `KylinYZ` had opened **PR #227** the same day (2026-09-14), covering the
  `ExecuteABAP` slice of #166 with a notably careful design (the whole cleanup defer runs on a detached
  `context.WithoutCancel`, not just the sub-branches) — deliberately **not** duplicated or competed with.
  The other 3 sites named in oisee's own 2026-09-11 triage report (`workflows_edit.go:404`,
  `workflows_deploy.go:142/354`, `workflows_source.go:670`) had no open PR against them.
- **Comment posted** on
  [issue #166](https://github.com/oisee/vibing-steampunk/issues/166#issuecomment-5663127053): credited
  PR #227, explained why the txape10 fork's own 3 remaining sites don't share `ExecuteABAP`'s
  extra-lock-inside-cleanup wrinkle (so `releaseLockAfterFailure`'s existing `context.WithoutCancel`+
  timeout was already sufficient), and announced a PR covering just those 3.
- **PR sent**: [oisee/vibing-steampunk#231](https://github.com/oisee/vibing-steampunk/pull/231), built in
  an isolated `git worktree` checked out on `upstream/main` (never touched this project's own working
  directory or the `fix/lock-nomodification-with-transport` branch). Converts the same
  `_ = c.UnlockObject(ctx, ...)` → `releaseLockAfterFailure` + `strandedLockAdvice` pattern this fork
  already uses (`2t`/`2u`) at the 3 named upstream sites: `EditSourceWithOptions`'s primary defer
  (`workflows_edit.go`), `CreateFromFile`/`UpdateFromFileWithOptions` (`workflows_deploy.go` — both needed
  converting to named returns `(result *DeployResult, err error)` first, since neither had a shared
  `result` variable for the defer to report through; one `result := &DeployResult{...}` had to become
  `result =` to avoid colliding with the new named return, caught by the `planner` agent before any code
  was written), and the BDEF creation path's source-write failure branch (`workflows_source.go`).
- **Code-reviewed** (against upstream's own code, not this fork's): 1 HIGH finding, fixed same session —
  4 of the 5 new tests passed identically against the pre-fix code (verified empirically by the reviewer
  stashing just the production files), because they never cancelled the caller's `ctx`, so they couldn't
  distinguish the old discarded-error unlock from the fix (both send an identical request on a live
  context). Fixed by adopting PR #227's own technique: cancel `ctx` at the exact moment the failing source
  PUT reaches the stub server, so the compensating unlock has to escape an already-cancelled context —
  re-verified directly that all 5 tests now fail against the pre-fix code and pass against the fix. 1 LOW
  noted, not changed (the `result != nil` guard in the two `workflows_deploy.go` defers protects a path
  proven unreachable today, same class of "near-unreachable gap" already documented elsewhere in this
  project's own history).
- `go build ./...`, `go vet ./...` clean on upstream's `main` + this diff; full `pkg/adt` suite green aside
  from the two pre-existing Windows `sso_test.go` failures already independently noted in upstream PRs
  #221 and #227 (unrelated file-mode assumptions, not introduced here).
- **Scope note**: a `DDLS`/`SRVD` case in `workflows_source.go` with the identical discarded-error pattern,
  one switch-case below the BDEF branch this PR fixes, was found and deliberately left untouched — outside
  the 4-site list oisee's own triage named. Flagged in the PR description for a possible future slice.
- **Merge attempted and correctly refused**: after the user said "cuando termine el CI, mergea si todo
  está en verde," CI passed and `gh pr merge 231 --repo oisee/vibing-steampunk` was attempted — GitHub
  rejected it (`txape10 does not have the correct permissions to execute MergePullRequest`), as expected
  for a contributor with no write access to someone else's repo. The user corrected this sharply — even
  attempting the call was the wrong instinct, not just its (harmless) failure — see the new feedback memory
  this session added. PR #231 sits green, waiting on oisee's own review; no further action from this side
  until she responds.

### 2ak. `ZCL_VSP_DEBUG_SERVICE=>HANDLE_SET_BREAKPOINT` — line breakpoints on function-group includes — FIXED (2026-09-15)
- **Root cause, confirmed live** by reading the real SAP standard interface `IF_TPDAPI_BP_FACTORY` (via
  `SAP(action="read", target="INTF IF_TPDAPI_BP_FACTORY")`): `CREATE_LINE_BREAKPOINT` takes `I_MAIN_PROGRAM`
  (required) and `I_INCLUDE` (optional) as **separate** parameters. For a plain `PROG`/`CLAS` they coincide,
  but for a line inside a function group's generated include, SAP needs the real main program
  (`SAPL<group>`) and the include (`L<group>U<nn>`) passed separately. `handle_set_breakpoint` only ever
  passed `i_main_program = lv_program`, never `i_include` — explaining every variant of the error seen
  across sessions (bare FM name, group main program alone, include name alone — all as `i_main_program`
  alone). This closes the "revises an earlier, narrower theory" entry directly below (kept for its own
  investigation history, marked resolved).
- **Fix**: before calling `create_line_breakpoint`, resolve `lv_program` against the standard DDIC table
  `D010INC` ("Tabla de utilización para Includes ABAP", read live: key fields `MASTER`/`INCLUDE`, both data
  elements on domain `PROGNAME`, CHAR40 — verified via `SAP(action="read", target="DTEL MASTER")` /
  `DTEL INCLUDE`, not assumed). `SELECT SINGLE master FROM d010inc WHERE include = lv_program` — if found
  and `master <> lv_program`, `lv_program` is really an include: call with `i_main_program = <master>` +
  `i_include = lv_program` (both `CONDENSE`d first — see the code-review finding below). Otherwise
  (`PROG`/`CLAS`, no matching row), the original single-parameter call runs unchanged — zero risk of
  regression on the case that already worked.
- Also added, approved by the user during planning: an informational `"include"`/`"resolvedMainProgram"`
  pair in the success JSON response when the include path was taken, so a caller can see what was resolved.
- **Code-reviewed** (0 CRITICAL/HIGH): 1 MEDIUM caught and fixed — `D010INC-MASTER`/`-INCLUDE` are CHAR40,
  so without `CONDENSE` the new JSON fields would carry ~27 trailing spaces before the closing quote (no
  consumer reads them today, but it's a real, reproducible bug in the new code). 1 LOW noted, not fixed
  (pre-existing, unrelated): `cmd/vsp/debug.go`'s `-p`/`--program` CLI flag doesn't uppercase before use,
  unlike every other caller of `SetLineBreakpoint` — a lowercase include name there would silently miss the
  `D010INC` lookup (case-sensitive) and fall through to the old, broken behavior for that one entry point.
- **A second, unrelated syntax error found and fixed during deployment** (not caught by review, since the
  reviewer doesn't have live SAP access): `escape_json`'s `iv_string` parameter is `IMPORTING ... TYPE
  string` **without `VALUE()`** — imported by reference, which in classic ABAP requires the actual argument
  to be exactly `TYPE string`. Passing the new CHAR40 `lv_include`/`lv_master` directly failed the syntax
  check (`"LV_INCLUDE" is not type-compatible with formal parameter "IV_STRING"`) on the first deploy
  attempt (rejected before saving — no broken version was ever activated). Fixed with explicit
  `CONV string( ... )`, matching the pattern already used elsewhere in the same file
  (`CONV string( ls_debuggee-host )`).
- Deployed via 3 surgical `EDITSOURCE` calls (declarations, the `WHEN 'line'.` block, the JSON response
  line) against transport `S4DK928661` (confirmed still open/modifiable, owned by `ZRCHAPADO`, description
  `ZADT_VSP` — the designated transport for `ZCL_VSP_*` classes), each syntax-checked and activated clean.
- **Verified live**: `SET_BREAKPOINT` on `LZVSP_TESTU01:6` (the exact repro from the earlier investigation)
  now succeeds — no more `SET_BREAKPOINT_FAILED`. Regression check: `SET_BREAKPOINT` on `ZTESTRCG1:15` (a
  plain `PROG`, the unchanged `ELSE` path) still succeeds identically to before. Both test breakpoints
  deleted after verification.
- **The full "Listen → trigger → catch" round trip — now verified live too**, in a follow-up same-day
  session, via `vsp debug ui`. The first attempt (via the plain `SAP(action="debug", ...)` hyperfocused
  tool) had `LISTEN` time out twice — once triggering via `execute_abap`, once via `CALL_RFC` — and was
  theorized at the time to be either a single-shared-WebSocket serialization problem (`LISTEN`/`CALL_RFC`
  both ride the one persistent ZADT_VSP APC session) or ABAP Unit deliberately ignoring breakpoints. A
  throwaway `vsp debug ui` instance (built fresh, pointed at the real system via the same env the deployed
  MCP server uses, killed and deleted after) settled it: `debug ui`'s `Listen` runs over a **decoupled REST
  TPDAPI session** (`pkg/adt/debugger.go`'s `DebuggerListen`), not the WS connection breakpoints/RFC use —
  set a breakpoint on `LZVSP_TESTU01:6` via `/api/bp`, started `/api/listen?seconds=30` in the background,
  then triggered with the exact same `execute_abap` call as before while it was actively listening. **Caught
  it**: `{"attached":true,...,"note":"stopped at SAPLZVSP_TEST:6"}`, full 17-frame stack showing
  `programName:"SAPLZVSP_TEST"`, `includeName:"LZVSP_TESTU01"`, `line:6`, `eventName:"ZVSP_TST_RFC_NOOP"` —
  this fix's own resolved main-program/include pair, confirmed live end to end. This **falsifies** the
  "ABAP Unit ignores breakpoints" theory too — it does not; the earlier failures were purely the
  single-WebSocket-connection serialization between `LISTEN` and the trigger in the hyperfocused-tool path
  (a `debug_ui`-specific workaround, not something the hyperfocused `SAP()` tool itself can route around
  today). The `execute_abap` trigger call itself hung to client-side timeout — consistent with its ABAP
  session genuinely parking at the breakpoint; `step continue` then ended the debuggee cleanly
  (`debuggeeEnded`), `detach` confirmed a clean state, and the breakpoint was gone afterward
  (`GET_BREAKPOINTS` → none). `ZVSP_TST_RFC_NOOP`/`ZVSP_TEST` are still left in `$TMP` as a reusable repro.
- Files: `src/zcl_vsp_debug_service.clas.abap`, `abap/src/zadt_vsp/zcl_vsp_debug_service.clas.abap`,
  `embedded/abap/zcl_vsp_debug_service.clas.abap` (all three confirmed identical in this method both before
  and after the change).

### 2al. `ZCL_VSP_RFC_SERVICE=>HANDLE_CALL` — `TABLES` parameters never received the caller's JSON content — FIXED (2026-09-17)
- **Root cause, found while debugging an unrelated project**: the `TABLES` loop built the internal table via
  `create_table_data` but never deserialized the caller's JSON array into it — unlike the sibling
  `IMPORTING`/`kind_table` branch a few lines above, which already does `extract_json_array` +
  `/ui2/cl_json=>deserialize`. Confirmed live: `SAP(action="debug", target="CALL_RFC", params={"function":
  "RFC_READ_TABLE", "params": "{\"QUERY_TABLE\":\"T000\",\"FIELDS\":[{\"FIELDNAME\":\"MANDT\"}]}"})` returned
  the full 17-column `FIELDS` metadata of `T000` instead of just `MANDT` — the `FIELDS` TABLES content never
  reached the function call, so `RFC_READ_TABLE` (and any other FM with `TABLES` input, e.g. `OPTIONS` for a
  WHERE clause) silently ignored whatever the caller passed there.
- **Checked upstream first, thoroughly, before writing any code** (issues, PRs, comments, commit history on
  both `src/` and `embedded/abap/` copies of this file): not reported, not fixed anywhere. The only related
  issue, [#151](https://github.com/oisee/vibing-steampunk/issues/151) ("CallRFC fails with HTTP(400) for FMs
  with non-elementary table parameters"), is a different bug — a table-of-tables `CREATE DATA` failure,
  already independently fixed in this fork (2y) — and its only linked commit (`bc8baf1`) only touches that
  same table-of-tables guard, nothing about content deserialization. Upstream's own `embedded/abap/` copy on
  `main` has the identical bug and is, if anything, further behind than this fork's `src/` copy (no
  `extract_json_array`/`/ui2/cl_json` usage at all, not even for `IMPORTING`-table-type params).
- **Upstream has in fact moved away from this whole mechanism for reading table data**: a new package
  `pkg/saprfc` (classic RFC via `github.com/oisee/open-rfc-go`, native Go, no ABAP bridge involved) now backs
  `vsp rfc call/read-table/describe/search/...`, with its own `RFC_READ_TABLE` wrapper
  (`pkg/saprfc/readtable.go`) that builds `FIELDS`/`OPTIONS` as native Go maps — this class of bug cannot
  occur there. This fork deliberately chose, in 2ah, not to adopt `pkg/saprfc`/`open-rfc-go` as a new
  dependency (to avoid a second parallel RFC/debugger implementation alongside the WebSocket bridge this
  project already relies on throughout). A separate adoption plan for `pkg/saprfc` has been requested and is
  being drafted (see the project's own planning docs once written) — deliberately deferred to a later
  session, not blocking this narrow fix.
- **Fix**: mirrors the existing `IMPORTING`/`kind_table` pattern exactly, applied only to the `TABLES` loop —
  same helpers (`extract_json_array`, `/ui2/cl_json=>deserialize` in a `TRY/CATCH cx_root`), a distinct local
  variable name (`lv_tbl_json_arr`, not `lv_json_arr`) since ABAP classic inline `DATA(...)` declarations
  scope to the whole method, not the lexical block, and reusing the existing name would have been a
  duplicate-declaration syntax error:
  ```abap
  LOOP AT lt_tables INTO DATA(ls_tbl).
    CLEAR: ls_ptab, lo_data.
    lo_data = create_table_data( ls_tbl ).
    IF lo_data IS BOUND.
      DATA(lv_tbl_json_arr) = extract_json_array( iv_params = is_message-params iv_name = CONV #( ls_tbl-parameter ) ).
      IF lv_tbl_json_arr IS NOT INITIAL.
        TRY.
            /ui2/cl_json=>deserialize( EXPORTING json = lv_tbl_json_arr CHANGING data = lo_data->* ).
          CATCH cx_root.
        ENDTRY.
      ENDIF.
      ls_ptab-name = ls_tbl-parameter.
      ls_ptab-kind = abap_func_tables.
      ls_ptab-value = lo_data.
      INSERT ls_ptab INTO TABLE lt_ptab.
    ENDIF.
  ENDLOOP.
  ```
- **Scope decision — only `embedded/abap/zcl_vsp_rfc_service.clas.abap` touched, matching what's live**: read
  the live deployed source first (`SAP(action="read", target="CLAS ZCL_VSP_RFC_SERVICE", params={"method":
  "HANDLE_CALL"}})`) and confirmed it matches `embedded/abap/`, not `src/`. `src/zcl_vsp_rfc_service.clas.abap`
  has no `extract_json_array`/`/ui2/cl_json` usage at all — a much larger, pre-existing drift than the one
  2y already documented for a sibling method — so patching it the same way would not compile. Left untouched
  deliberately, per the code-reviewer's own MEDIUM finding (below) and this project's established precedent
  (2y) of documenting cross-copy drift as separate follow-up work rather than folding a sync into an
  unrelated bug fix. `abap/src/zadt_vsp/zcl_vsp_rfc_service.clas.abap` is an ancient 111-line doc sample
  (only `search`/`ping`, no `call`/`HANDLE_CALL` at all) — out of scope, never deployed.
- **Bug 2, found in the same investigation, deliberately deferred, not fixed here**: every RFC exception in
  the same method is mapped to a single generic `INSERT VALUE #( name = 'OTHERS' value = 99 ) INTO TABLE
  lt_etab.` before the dynamic `CALL FUNCTION`, so `subrc=99` is indistinguishable between "the function
  raised a real declared exception" (e.g. `RFC_READ_TABLE`'s `TABLE_NOT_AVAILABLE`/`DATA_BUFFER_EXCEEDED`)
  and "no data"/other soft conditions — the MCP caller has no way to tell them apart today. Not reported or
  fixed upstream either (checked). Deferred rather than bundled into this fix because it's a materially
  larger change: enumerating the function's real declared exceptions (likely via the existing
  `get_func_interface`/`FUNCTION_IMPORT_INTERFACE` machinery already used for `RFC_METADATA`) and a JSON
  response-schema change the Go/MCP side would need to know about — mixing it with this narrow, low-risk fix
  would make both harder to review and verify independently.
- Code-reviewed: 0 CRITICAL/HIGH. 1 MEDIUM (informational, matches the scope decision above almost exactly:
  flagged that `src/zcl_vsp_rfc_service.clas.abap` did not receive the same fix and now diverges from
  `embedded/abap/` at this exact method) — no fix applied, already the intended, documented scope.
- Deployed via a single `EDITSOURCE` call against the live class, transport `S4DK928661`, syntax-checked and
  activated clean (only 2 pre-existing, unrelated POSIX-regex-deprecation warnings in `EXTRACT_PARAM`/
  `FIND_BALANCED_JSON`, not touched by this change).
- **Verified live (2026-09-17)** — with an important nuance matching 2y's own prior finding: the first
  verification attempt, through the already-running MCP server's `CALL_RFC` tool, still showed the pre-fix
  symptom (all 17 `T000` columns) immediately after activation — the long-running `vsp.exe` process's
  existing `ZADT_VSP` WebSocket session had the old class pool loaded in its ABAP roll area and doesn't
  re-resolve it mid-session, exactly the same artifact 2y hit and documented. Rather than ask for a Claude
  Desktop restart mid-session, verified instead with a throwaway Go program (`cmd/verifyrfctbl/`, deleted
  after the run, not committed — same technique as 2w/2ac/2ag/2y) opening its own **fresh** WebSocket
  connection directly against `pkg/adt.DebugWebSocketClient`: `RFC_READ_TABLE` with `FIELDS=[{"FIELDNAME":
  "MANDT"}]` now returns `DATA` rows containing only the 3-character `MANDT` value and a `FIELDS` metadata
  table with exactly one entry — confirmed fixed. Two regression checks in the same run: `RFC_SYSTEM_INFO`
  (no `TABLES` params at all) unaffected; `RFC_READ_TABLE` with `FIELDS` omitted still returns all columns
  unfiltered, exactly as before — the fix only adds behavior when a `TABLES` param's JSON is actually present,
  no change to the no-filter case. **Practical implication**: this class of fix is only observable through
  the deployed MCP server after a Claude Desktop restart (fresh `vsp.exe`, fresh WS session) — the live
  system itself was correct immediately after activation, confirmed independent of any client-side session
  staleness.

### 2am. `pkg/saprfc` adoption, Fase 0 + Fase 1 — `CALL_RFC`/`RFC_SEARCH`/`RFC_METADATA`/abapGit package export migrated to classic RFC (2026-09-17)
- **Decisión rectora (usuario)**: migrar a `pkg/saprfc` (cliente RFC clásico nativo en Go de upstream,
  sobre `github.com/oisee/open-rfc-go`, contra el gateway SAP directamente) todo lo que se pueda, dejando
  el puente WebSocket `ZADT_VSP` solo para lo que no tenga equivalente en RFC clásico. Documento de
  decisión: `docs/pkg-saprfc-adoption-plan.md`. Planificado con el agente `planner` (Fase 0 + Fase 1),
  ejecutado tras confirmación explícita del usuario en cada punto de parada.
- **El documento de adopción estaba desactualizado el mismo día que se escribió**: al releer el código
  real de upstream (`git fetch upstream`, `git show upstream/main:pkg/saprfc/*.go`) para esta sesión, el
  paquete había crecido sustancialmente desde que el documento se redactó unas horas antes — API más
  simple de lo asumido (`rfc.Client.Call` devuelve un `Result` con `.Get`/`.Table`/`.Has` y
  `MarshalJSON` genérico; `rfc.Client.DescribeTool` genera un JSON Schema de tool MCP directamente,
  mejor que el `RFC_METADATA` anterior; `rfc.ABAPException{Kind, Key, ...}` distingue de forma nativa
  excepción declarada / dump / mensaje T100, cerrando gratis el "Bug 2, deferred" de 2al sobre
  `subrc=99` genérico indistinguible). Config ya resuelta por upstream en `pkg/config.SystemConfig`:
  campos `RFCHost/RFCSysnr/RFCPort/RFCUser/RFCPassword`, JSON `rfc_host`/`rfc_sysnr`/`rfc_port`/
  `rfc_user`/`rfc_password`, fallback a `VSP_<SISTEMA>_RFC_PASSWORD` y `SAP_USER`/`SAP_PASSWORD` —
  portados literalmente, no inventados, siguiendo la política anti-invención del proyecto.

**Fase 0 (setup) — verificada en vivo**:
- Dependencia `github.com/oisee/open-rfc-go@v0.0.0-20260820234724-6ef4d9eeb9cd` añadida. `go.mod` subido
  de `go 1.25.0` a `go 1.26` (requerido por la dependencia; el toolchain local ya era `1.26.3`, sin
  necesidad de instalar nada). Build limpio con `CGO_ENABLED=0` en Windows — el riesgo principal del plan
  (que `open-rfc-go` arrastrara cgo, como ya le pasa a `pkg/cache` con SQLite) **no se materializó**.
- Destino RFC real de este sistema obtenido del propio usuario (parámetros de conexión SAP GUI: servidor
  de aplicación, número de instancia `00`, ID de sistema `S4D` — **no derivable automáticamente** del
  `SAP_URL` configurado, que es un hostname de reverse-proxy HTTPS sin puerto explícito; `RFC_PING` y
  `RFC_SYSTEM_INFO` verificados en vivo con un programa throwaway (`cmd/verifyrfc0/`, borrado tras el
  uso), reutilizando las credenciales ADT existentes (`ZRCHAPADO`) como fallback, tal como diseña
  `saprfc.Resolve`.
- **Hallazgo, no bloqueante**: `saprfc.Resolve()` reduce el idioma a 1 carácter con `lang[:1]` sobre el
  código ISO recibido — con `SAP_LANGUAGE=ES` da `"E"`, que es incorrecto (el código SAP real para
  español es `"S"`, el mismo bug de conversión ISO→SAP que ya se corrigió en `ZCL_VSP_REPORT_SERVICE` en
  2m). No afectó a la verificación porque ni `RFC_PING` ni `RFC_SYSTEM_INFO` dependen del idioma de
  logon. No corregido en este puerto (es código de upstream, `pkg/saprfc/saprfc.go`, sin tocar) —
  apuntado aquí para si se migra algo sensible al idioma en una fase futura.

**Fase 1 (dominio RFC) — verificada en vivo salvo abapGit**:
- Archivos nuevos portados de upstream verbatim: `pkg/saprfc/saprfc.go` (`Resolve`/`Open`/`Params`/
  `Input`/`Secret`), `pkg/saprfc/readtable.go` (`ReadTable`, con el fallback `ET_DATA` para filas anchas
  y el troceo de WHERE a 72 caracteres), `pkg/saprfc/readtable_test.go`, `pkg/saprfc/abapgit.go`
  (`ExportPackage`).
- **`CALL_RFC`** (`internal/mcp/handlers_debugger.go`, `handleCallRFC`): migrado de
  `s.debugWSClient.CallRFC` a `s.ensureRFCClient(ctx)` (nuevo, `internal/mcp/rfc_client.go`) +
  `rfc.Client.Call`. Verificado en vivo el caso exacto del bug 2al (`RFC_READ_TABLE` con `FIELDS` como
  `TABLES`, filtrado a `MANDT`): `fields=1` — el cliente nativo resuelve `TABLES` correctamente sin
  ningún parche ABAP. **Cambio de contrato observable, deliberado**: `Subrc` se hardcodea a `0` en el
  camino de éxito (antes reflejaba, de forma poco fiable, un `OTHERS=99` genérico incluso en llamadas
  "exitosas" con excepción ABAP silenciada); un fallo real ahora se propaga como `err` tipado
  (`*rfc.ABAPException` cuando aplica) con `IsError: true` en el `CallToolResult` — una mejora de
  contrato, no una regresión, confirmada por `code-reviewer`.
- **`RFC_SEARCH`** (`handleRFCSearch`): migrado a `saprfc.ReadTable` sobre `TFDIR` con
  `FMODE IN ('R', 'X')` (el filtro real que usa el propio `cmd/vsp/rfc.go` de upstream — `'X'` son los
  módulos remote-enabled basXML-capable, `SADT_REST_RFC_ENDPOINT` entre ellos, que un filtro `'R'` solo
  ocultaría). Verificado en vivo con `BAPI_USER*`: 10 resultados correctos.
- **`RFC_METADATA`** (`handleRFCGetMetadata`): migrado a `rfc.Client.DescribeTool`, que expande
  estructuras/tablas desde su layout DDIC real — firma más completa que el listado plano
  `[kind] name: type` anterior. Verificado en vivo con `BAPI_USER_GET_DETAIL`: JSON Schema completo y
  correcto con los 4 tipos de parámetro.
- **Export abapGit** (`internal/mcp/handlers_git.go`): híbrido, decisión explícita del usuario tras
  confirmar que `saprfc.ExportPackage` (`Z_ABAPGIT_SERIALIZE_PACKAGE`) solo exporta paquetes completos,
  no objetos sueltos. Un paquete único, sin objetos sueltos, con `IncludeSubpackages` en su valor por
  defecto `true` → nuevo `handleGitExportRFC` (RFC nativo, sin `ZADT_VSP`); cualquier otra combinación
  (varios paquetes, objetos sueltos, o `include_subpackages=false` explícito, cuyo comportamiento en el
  serializador RFC no está confirmado) → sigue en el camino WebSocket existente sin cambios.
  `handleGitExportRFC` lista el contenido real del ZIP con `archive/zip` en vez de fiarse de un
  `ObjectCount`/`FileCount` que el serializador RFC no proporciona.
  - **No verificable en vivo en este sistema — no es una regresión**: `Z_ABAPGIT_SERIALIZE_PACKAGE` no
    existe aquí (`RFC_SEARCH "*ABAPGIT*"` solo encuentra `ENQUEUE_EZABAPGIT`/`DEQUEUE_EZABAPGIT`, bloqueos,
    no serialización). Causa confirmada con el usuario: este sistema tiene abapGit instalado como
    **standalone** (`ZABAPGIT_STANDALONE`, `PROG/P`, paquete `ZABAP01` — confirmado vía
    `SAP(action="search", target="ZABAPGIT*")`), un único report autocontenido sin grupo de funciones —
    no instala `ZABAPGIT_PARALLEL` ni por tanto `Z_ABAPGIT_SERIALIZE_PACKAGE`. Ese módulo de función solo
    existe con una instalación de abapGit gestionada como repositorio (clonando el propio repo de abapGit
    en el sistema). **Decisión del usuario**: dejar el código híbrido tal cual — es correcto para
    sistemas con esa instalación completa; en este sistema, simplemente nunca se toma la rama RFC y el
    export sigue funcionando por WebSocket como antes.
- **Revisión de seguridad — 1 CRITICAL encontrado y corregido en la primera pasada del `code-reviewer`**:
  `handleRFCSearch` concatenaba el `pattern` del usuario sin escapar en la cláusula WHERE enviada a
  `RFC_READ_TABLE` (`pkg/saprfc.ReadTable` no tiene bind parameters — el WHERE es un string literal
  enviado tal cual por RFC) — inyección clásica de "RFC_READ_TABLE WHERE-clause injection". Fix: nueva
  `funcnameLikePredicate(pattern) (string, error)` (`handlers_debugger.go`) con whitelist de caracteres
  (A-Z, 0-9, `_`, `/`, `*`) siguiendo el mismo patrón que `as4userPredicate` (`pkg/adt/transport.go`,
  cerrado en 2s por el mismo motivo) — cualquier carácter fuera de la whitelist rechaza la llamada en vez
  de escaparlo. **1 MEDIUM también corregido**: `pkgName` en el nombre de fichero del ZIP de export no
  saneaba `/`/`..` (path traversal) — nueva `sanitizePackageForFilename(pkg) string` (whitelist
  `A-Za-z0-9_-`, fallback `"package"`), aplicada en **ambos** sitios que construyen `pkgName` (el camino
  WebSocket preexistente, que tenía el mismo patrón sin tocar hasta ahora, y el nuevo camino RFC). Tests
  de regresión: `internal/mcp/rfc_search_test.go` (`TestFuncnameLikePredicate_RejectsInjection`/
  `_AcceptsValidPatterns`, `TestSanitizePackageForFilename_RejectsTraversal`). Segunda pasada del
  `code-reviewer`: **APPROVE, 0 CRITICAL/HIGH/MEDIUM** (1 LOW informativo: `DebugWebSocketClient.Search`/
  `GetMetadata` en `pkg/adt/websocket_rfc.go` quedan sin llamadas tras esta migración — no huérfanas del
  todo, el fichero sigue usándose para otras operaciones WS; candidatas a limpieza futura vía
  `refactor-cleaner` si el usuario lo pide, no tocadas proactivamente).
- **Deliberadamente fuera de alcance de esta sesión** (Fase 2/3/4 del plan): `RUN_REPORT` vía background
  job XBP (candidato a cerrar el bug abierto `APC_ILLEGAL_STATEMENT`), debugger de solo lectura vía
  `RFC_READ_TABLE` sobre `ABDBG_*`, y el debugger interactivo completo (requiere desplegar una facade
  ABAP nueva, `ZADT_DEBUG_RFC`, que este sistema no tiene). El código WS existente para
  `SET_BREAKPOINT`/`GET_BREAKPOINTS`/`LISTEN`/`ATTACH`/etc. no se ha tocado.
- Files: `go.mod`, `go.sum`, `pkg/saprfc/saprfc.go` (nuevo), `pkg/saprfc/readtable.go` (nuevo),
  `pkg/saprfc/readtable_test.go` (nuevo), `pkg/saprfc/abapgit.go` (nuevo), `internal/mcp/rfc_client.go`
  (nuevo), `internal/mcp/rfc_search_test.go` (nuevo), `internal/mcp/server.go`,
  `internal/mcp/handlers_debugger.go`, `internal/mcp/handlers_git.go`, `cmd/vsp/main.go`.
- `go build ./...` limpio con `CGO_ENABLED=0`; `go test $(go list ./pkg/... ./internal/... | grep -v
  pkg/cache)` en verde, incluidos los tests portados y los 3 nuevos de seguridad.

### 2an. `pkg/saprfc` adoption, Fase 2 — `RUN_REPORT`/`RUN_REPORT_ASYNC` migrated to classic RFC (XBP background jobs), closes the `APC_ILLEGAL_STATEMENT` bug (2026-09-17)
- **The bug closed**: `RUN_REPORT`/`RUN_REPORT_ASYNC` went through the WebSocket `ZADT_VSP` bridge
  (`SUBMIT ... AND RETURN` inside a stateful APC handler), which SAP rejects with
  `APC_ILLEGAL_STATEMENT` on any report with a selection screen — documented as an architectural
  limit under "Known Open Issues" for a long time (upstream issue
  [#113](https://github.com/oisee/vibing-steampunk/issues/113), never fixed on that transport
  anywhere). Migrated to `pkg/saprfc`'s classic-RFC XBP background-job mechanism (Fase 2 of the
  adoption plan started in 2am): `BAPI_XMI_LOGON` → `BAPI_XBP_JOB_OPEN` →
  `BAPI_XBP_JOB_ADD_ABAP_STEP` (carries `SELINFO` for selection-screen parameters) →
  `BAPI_XBP_JOB_START_ASAP` → poll `TBTCO-STATUS` → `BAPI_XMI_LOGOFF`, spool read via
  `BAPI_XBP_JOB_SPOOLLIST_READ`. No `SUBMIT` anywhere in this path — the whole bug class is gone
  by construction, not patched around.
- **Ported from upstream, verified against its real source first** (not from memory — read
  `pkg/saprfc/report.go` from `upstream/main` directly, 289 lines, before writing anything): the
  code comment there explains an earlier upstream attempt used `SUBST_START_REPORT_IN_BATCH`,
  which picks its own batch server and fails with `BATCH_SCHEDULING_FAILED (XM262)` on systems
  where that selection doesn't resolve — the XBP BAPIs were chosen instead because they take the
  target server explicitly and report errors via a real `BAPIRET2`.
- **Two deliberate deviations from upstream's port**, both because this fork's package already had
  content upstream's fresh copy didn't: `firstNonEmpty` not redeclared (already in this package's
  `saprfc.go` from Fase 1, same package — a duplicate would be a compile error); upstream's
  `asInt32` helper dropped (only used by upstream's debugger code, not ported here — Fase 3/4 are
  still future work per the adoption plan).
- **New `JobStatus(ctx, c, jobName, jobCount) (status, statusText string, err error)`**, not in
  upstream — a thin wrapper needed for the new `GetReportJobStatus` MCP tool (below), which upstream
  has no equivalent of (its CLI always waits synchronously).
- **Contract changes, all deliberate and user-approved before implementation** (via `planner` +
  explicit confirmation, not discovered after the fact):
  - `variant` (report variant name) **dropped entirely** from `RunReport`/`RunReportAsync` — RFC has
    no way to resolve a variant to its stored parameter values; `saprfc.RunReport` upstream never
    had this either. A documented capability loss, not an oversight.
  - `params` now accepts **two shapes**: a flat object (`{"P_X":"value"}`, one EQ parameter per key
    — the pre-existing simple form) or an array of full `saprfc.ReportParam` objects, for
    select-options with a range (`[{"name":"S_WERKS","option":"BT","low":"1000","high":"2000"}]`).
    This also fixes a pre-existing bug: the old tool description already *claimed* range support
    with an example the old `map[string]string` implementation could never actually parse — a
    promise the implementation never kept, not something this change regresses.
  - New `wait_seconds` parameter (default 30, capped at 120 to keep one MCP call from blocking
    indefinitely) replaces the old hardcoded 60s poll timeout.
  - `RunReportAsync`'s result drops the `spool_ids` field — XBP gives one spool per job step, not a
    list of IDs to iterate.
  - New tool `GetReportJobStatus(job_name, job_count, include_spool?)` — closes the gap left by
    dropping the old (structurally broken — see the closed "Known Open Issues" entry below) polling
    model: a job scheduled with `wait_seconds=0` (fire-and-forget) can be checked on later in a
    separate call.
  - `pkg/adt/reports.go` trimmed: `RunReportParams`/`RunReportResult`/`JobStatusResult`/
    `SpoolOutputResult` types and `AMDPWebSocketClient.RunReport`/`.GetJobStatus`/`.GetSpoolOutput`
    removed — these assumed ABAP-side actions (`getJobStatus`/`getSpoolOutput`) that a prior
    session's investigation had already confirmed don't exist anywhere in the deployed ABAP backend.
    `GetVariants`/`GetTextElements`/`SetTextElements` and their shared `sendReportRequest` helper are
    untouched — no RFC equivalent exists, they stay on the WebSocket.
- **Code review, 2 HIGH found and fixed in the same session** (0 CRITICAL from the start):
  - **WHERE-clause injection in `jobStatus`** (`pkg/saprfc/report.go`) — `jobName`/`jobCount`, fully
    caller-controlled via the new `GetReportJobStatus` MCP tool, were interpolated unescaped into an
    `RFC_READ_TABLE` OPTIONS/WHERE clause (`ReadTable`/`RFC_READ_TABLE` has no bind parameters — the
    same class of bug this codebase already found and fixed twice on sibling code,
    `funcnameLikePredicate` for `RFC_SEARCH`/`TFDIR` in 2am and `as4userPredicate` for
    `GetUserTransports`/`AS4USER` in 2s). Fixed with new `validJobName`/`validJobCount` whitelist
    validators (`[A-Za-z0-9_-/]` max 32 chars / digits-only max 8 chars, matching the real
    `TBTCO-JOBNAME`/`TBTCO-JOBCOUNT` DDIC widths) called before the WHERE clause is built; `jobStatus`
    stopped delegating to the general `saprfc.ReadTable` (which requires `*rfc.Client` specifically)
    and now builds the `RFC_READ_TABLE` call inline against a new `rfcCaller` interface. Verified live
    (below) that the guard actually rejects an injection payload with a clean error, not a silent
    bypass.
  - **No RFC session pinning across `BAPI_XMI_LOGON` → `BAPI_XBP_JOB_*` → `BAPI_XMI_LOGOFF`** — each
    step was an independent pooled `Client.Call`; SAP's XMI authorization is tied to the physical
    connection that logged on, and the vendored `open-rfc-go` library's own `rfc/session.go` doc
    comment names exactly this hazard ("a Session is for the stateful protocols where server-side
    state must survive between calls... otherwise fail with a session mismatch"), confirmed by
    reading that file directly rather than trusting the finding's description. Fixed: `RunReport`
    and `ReadSpoolStep` both call `c.Pin(ctx)` once and route every step of their respective
    sequences — including, in `RunReport`'s case, the status-polling loop — through
    `session.Call(...)`, `defer session.Close()`. New `rfcCaller` interface
    (`Call(ctx, functionName string, in rfc.Params) (rfc.Result, error)`) lets `xmiLogon`/
    `applicationServer`/`jobStatus` accept either a `*rfc.Client` or a `*rfc.Session` structurally, so
    the exported `JobStatus` (used by the session-independent `GetReportJobStatus` tool) can still
    pass a plain pooled client. A second review pass confirmed no stray `c.Call` remained in either
    function and `session.Close()` is deferred immediately after a successful `Pin`, before any other
    call — no leak path on any error branch.
  - Noted, not a finding: holding one pinned pool connection for up to 120s (`RunReport`, MCP-capped)
    or 5 minutes (`RunReportAsync`'s internal call) against the vendored pool's `MaxSize=8` is bounded
    backpressure on other concurrent RFC tool calls (`CALL_RFC`/`RFC_SEARCH`/etc.), not a deadlock or
    leak — the accepted tradeoff of the pinning fix itself, not a new problem it introduces.
- Tests: `pkg/saprfc/report_test.go` — pure-function coverage (`jobStatusText`, `selectionRows`
  defaults/explicit/empty, `bapiError` all branches) plus the injection-guard regression tests
  (`TestValidJobName_RejectsInjection`/`_AcceptsRealNames`, `TestValidJobCount_RejectsInjection`/
  `_AcceptsRealCounts`). No test exists for `RunReport`/`ReadSpool` against a mocked `*rfc.Client` —
  this package's own pre-existing test (`readtable_test.go`) never mocks the RFC client either, and
  there is no test-double mechanism for it in this codebase yet; not invented for this narrow fix,
  matching the existing coverage convention for this package. `internal/mcp/handlers_report_test.go`
  (new) — `parseReportParams` (empty/flat-object/array/invalid-JSON/non-string-value) and
  `reportWaitSeconds` (default/cap/explicit/negative-ignored).
- `go build ./...` clean; `go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)` green.
- **Verified live (2026-09-17)** against the real SAP system, through the deployed MCP tool (rebuilt
  binary, Claude Desktop restarted):
  - The exact repro from the closed "Known Open Issues" entry — `RUN_REPORT` on `ZTESTRCG1` with no
    params — now returns `Status: finished` and real spool (`"IDoc 0000000000000000: SIN bloqueo
    activo"`) instead of hanging to a client timeout.
  - `GET_VARIANTS` on the same report: unaffected, still answers via the WebSocket
    (`"No variants found"`, expected — regression check, not a functional one).
  - `RUN_REPORT_ASYNC` + `GET_ASYNC_RESULT(wait=true)`: completed in ~1.5s, correct spool in the
    result.
  - `RUN_REPORT` with `wait_seconds=0`: returned immediately with `Job: VSP_ZTESTRCG1/15572000` and no
    spool (job still running); a follow-up `GET_REPORT_JOB_STATUS(include_spool=true)` on that exact
    job correctly reported `finished` plus the spool — confirms the fire-and-forget-then-check flow
    end to end.
  - `GET_REPORT_JOB_STATUS` with an injection payload (`job_name: "X' OR JOBNAME LIKE 'Z"`): rejected
    cleanly with `invalid job name "...": expected up to 32 letters, digits, or _ - /` — no SAP call
    made, confirming the guard fires before any network request.
  - `params` flat-object form (`{"PA_IDOC":"1234567890"}`) on `ZTESTRCG1`: the value reached the
    report correctly (spool echoed `"IDoc 0000001234567890"`).
  - `params` array form with a select-option range: created a throwaway `$TMP` program
    (`ZVSP_TST_RUNREPORT`, a `SELECT-OPTIONS s_range FOR sy-tabix` that writes the range it receives —
    approved by the user before creation, per the project's golden rule on creating SAP objects), ran
    it with `[{"name":"S_RANGE","kind":"S","sign":"I","option":"BT","low":"10","high":"20"}]` — spool
    showed `"I BT         10          20"`, confirming `SIGN=I OPTION=BT LOW=10 HIGH=20` landed
    exactly as sent (also exercises `BAPI_XBP_JOB_ADD_ABAP_STEP`'s `SELINFO` under the new pinned
    session, since this is the same code path). Program deleted after confirming.
- Files: `pkg/saprfc/report.go` (new), `pkg/saprfc/report_test.go` (new),
  `internal/mcp/handlers_report.go` (rewritten `handleRunReport`/`handleRunReportAsync`, new
  `handleGetReportJobStatus`/`parseReportParams`/`reportWaitSeconds`),
  `internal/mcp/handlers_report_test.go` (new), `internal/mcp/tools_register.go` (schema updates +
  new `GetReportJobStatus` tool), `internal/mcp/tools_focused.go` (whitelist), `pkg/adt/reports.go`
  (trimmed).

### 2ao. Message class (MSAG/SE91) writes — language on the PUT, `corrNr` on the LOCK, delete element, language on the GETs (2026-10-02)
- **Ported from upstream**: [PR #270](https://github.com/oisee/vibing-steampunk/pull/270) (message class
  creation/text persistence with language) and [PR #256](https://github.com/oisee/vibing-steampunk/pull/256)
  (`corrNr` on the LOCK request). Planned together (planner), scope "B acotada" chosen by the user:
  Fases 1+2+3, Fase 3 as its own commit.
- **Root cause of 2v's "permanent limitation"**: `PUT /sap/bc/adt/messageclass/{name}` without
  `adtcore:language` is stored by `CL_ADT_MC_RES_CONTROLLER=>DO_UPDATE` in `T100` with a blank `SPRSL`.
  `SPRSL` is part of the key (MANDT+ARBGB+MSGNR+SPRSL), so a blank row and a correct `S` row coexist for
  the same message; a read in the logon language never sees the blank one, which looked exactly like "the
  PUT persists nothing". The `sap-language` header is ignored by this resource.
- **Fase 1 — language** (`pkg/adt/client.go`, `pkg/adt/i18n.go`, `pkg/adt/crud.go`): `messageClassWriteBody`
  carries `adtcore:language`; `newMessageClassWriteBody(name, description, language)` takes it as a required
  argument. `WriteMessageClassTexts` defaults an empty language to the session's; `CreateMessageClass`'s
  initial-messages PUT carries it too. New local validation before any lock:
  `validateMessageClassNumber` (exactly 3 digits — **rejected, not padded**, so the verifier compares what the
  caller asked for), `validateMessageClassLanguage` (2-letter ISO that `spras()` can map — see below),
  `validateMessageClassMessages` (also ≤73 characters, the T100 text width).
- **Fase 2 — `corrNr` on the LOCK** (`LockObject(ctx, url, accessMode, corrNr ...string)` in `crud.go`):
  the first corrNr goes on the LOCK query; `checkTransportableEdit` runs **before** the LOCK so a
  disallowed transport never reaches SAP. New `(*TransportChoice).lockCorrNr(supplied)` returns the supplied
  transport, else the plan's chosen one (empty if the plan is nil or failed). `WriteMessageClassTextsAutoLock`
  now runs `gateAndMark` → `planTransport` → LOCK with that transport → `resolveWriteTransportFor`, exactly
  like the other auto-lock workflows (issue #91 discipline: nothing stateless between LOCK and PUT).
- **Fase 3 — every other LOCK site** passes `trPlan.lockCorrNr(...)`: `WriteDataElementLabels`, `WriteProgram`/
  `WriteInclude`/`WriteClass`, `UpdateFromFileWithOptions`, `EditSourceWithOptions`, the `workflows_source.go`
  branches (lines of the 5 LOCK calls), `SetDescription`, `WriteTextPool`. Done as a separate commit-sized
  change on purpose: it touches the LOCK of every write.
- **MCP**: `WriteMessageClassTexts`'s `lock_handle` is now **optional** — without it the handler calls
  `WriteMessageClassTextsAutoLock`; the tool description and the `transport` description were updated.
- **Found only by live testing — two more bugs of our own, both fixed in the same change:**
  - **Delete element**: the PUT used `mc:deletedmessage` (singular, a guess from the upstream issue text).
    The transformation behind the controller, `ST_ADT_MESSAGE_CLASS`, reads `mc:deletedmessages`
    (**plural**) with attributes `mc:msgno`/`mc:msgtext`/`mc:corrno`/`mc:lockhandle`; the singular was ignored
    silently (`tt:extensible="deep"`), so `delete_numbers` never deleted anything. Fixed in
    `messageClassDeletedMessage`/`messageClassWriteBody.Deleted`; only `msgno` is sent. Verified live: after
    the fix the delete removes the message in the language written and leaves the other language's row.
  - **GET language**: `CL_ADT_MC_RES_CONTROLLER=>DO_GET` also ignores `sap-language`; it takes a `language`
    URI query parameter (`sylangu`, the 1-character SAP code) and answers in the session language without
    it. So writing a language other than the session's (EN while logged on in ES) made
    `verifyMessageClassWrite` read the session-language text back and report "the write did not actually take
    effect" although `T100` had the new row. New `messageClassReadQuery(lang)` (uses the existing `spras()`)
    is applied to `GetMessageClassTexts`, to the verifier, and to the description-echo GET inside
    `WriteMessageClassTexts`; that last one retries without `language` **only on a 404** (a language with no
    row yet — read from the controller's `exists( )` check, not confirmed live) and sends no description on
    any other error, so the session-language description is never echoed into another language's row.
    Because `spras()` guesses the first letter for an unmapped ISO code (`ET`→`E`, `LV`→`L`…),
    `validateMessageClassLanguage` refuses any code absent from `isoToSAPLang`.
- **Tests**: `pkg/adt/lock_corrnr_test.go` (new, ~25 tests) — corrNr on the LOCK (with/without/policy refusal
  before the LOCK), `lockCorrNr` table, the language in both PUT paths and in `CreateMessageClass`, the
  verifier reading the language written, the description echo and its 404-only fallback, the plural delete
  element and a delete SAP ignored still being reported, reads inside the lock window being stateful, language
  validation, `GetMessageClassTexts` sending the query; plus `i18n_test.go` updates. The language-GET tests
  were mutation-checked (they fail if `messageClassReadQuery` returns nothing).
- **Code review**: first pass APPROVE (0 CRITICAL/HIGH); second pass on the live-found fixes APPROVE (0
  CRITICAL/HIGH, 2 MEDIUM — retry on any error, `spras()` guessing for unmapped codes — both fixed, plus test
  gaps closed).
- **Verified live (2026-10-02)** on a throwaway `$TMP` class (language ES): create with message 001 → `T100`
  row `SPRSL='S'`; add 002 via `edit MSAG` (auto-lock) → `S`, 001 untouched; translate 001 to EN → new row
  `E`, `S` row unchanged, no false verifier error; delete 002 with `delete_numbers` → gone, 001 still in `S`
  and `E`; `EDITSOURCE` regression on a `$TMP` program (change and revert) → fine. The class was then deleted.
  Pre-existing blank-`SPRSL` rows in two customer message classes are old leftovers (they predate this fix and
  the user cleans them in SE91) — the fix does not write new ones.
- **NOT verified live**: that SAP honors `corrNr` on the LOCK for a **transportable** object without side
  effects (needs a transportable-package object and a user-chosen order; the doc comment on `LockObject` says
  so), and that the response-cache `T100` "stable table" rule never served a stale verification query (queries
  after a write were varied/spaced; use a differently-worded SQL to be safe — with `VSP_CACHE` off this does not
  arise).
- **Open decision, not made**: when `planTransport` fails to create a request (`plan.Err != nil`),
  `lockCorrNr` sends the LOCK with no `corrNr`, so SAP may autogenerate one — the golden-rule scenario the
  rest of the transport-choice design exists to avoid. Pre-existing in kind (it was already the case for the
  write) but now also on the LOCK. A guard before the LOCK in the affected sites would close it; left for the
  user's call.
- **Known small gaps**: the `read MSAG` MCP route does not forward a `language` parameter (it answers in the
  session language, although `GetMessageClassTexts` itself now honors the language); `WriteMessageClassTexts`'
  assumption that an empty description would overwrite the real one remains unconfirmed (only the
  non-overwrite was observed).
- **Cleanup note**: a LOCK→PUT→UNLOCK→DELETE cycle on a message class was previously reported to leave `T100`/
  `T100A` (`ES_MSGSI`) enqueues; not re-checked in this session (would need a temporary report to run
  `ENQUEUE_READ`) — check SM12 if a later write reports "currently being edited".
- Files: `pkg/adt/client.go`, `pkg/adt/crud.go`, `pkg/adt/i18n.go`, `pkg/adt/transport_choice.go`,
  `pkg/adt/workflows.go`, `pkg/adt/workflows_deploy.go`, `pkg/adt/workflows_edit.go`,
  `pkg/adt/workflows_source.go`, `pkg/adt/description.go`, `pkg/adt/textpool.go`, `internal/mcp/handlers_i18n.go`,
  `internal/mcp/tools_register.go`, `pkg/adt/i18n_test.go`, `pkg/adt/lock_corrnr_test.go` (new).
- `go build ./...` clean; `go test $(go list ./pkg/... ./internal/... | grep -v pkg/cache)` green.

## Known Open Issues (Not Fixed)

### Freestyle SQL (`RunQuery`/`action="query", target="SQL"`) silently drops WHERE conditions past 255 characters on a single-line query — SAP-side bug, root-caused, not fixable in vsp alone (2026-09-22)
- **Symptom**: a `COUNT(*)` over a 2-table JOIN with 6 WHERE conditions returns an identical result
  with and without the 6th condition (`AND tv~intercentro <> 'X'`, `558383` both ways) — no error, no
  warning, just a silently wrong result. Two other, more visible failure modes were seen for other
  query shapes near the same length: a misleading `"Solo está permitida una instrucción SELECT"` and
  a misleading `"Boolean expression was expected"`/`"INTO" is invalid`.
- **Root cause, confirmed live with a single unbroken breakpoint chain, byte length measured via
  Detailanzeige (not the classic debugger's 255-char quick-view, which is a separate, real but
  irrelevant-here display cap — SAP KBA 2803361)**: SAP's own `CL_ADT_DP_FREESTYLE_RES=>POST` (the
  ADT REST handler behind `/sap/bc/adt/datapreview/freestyle`) does
  ```abap
  request->get_body_data( EXPORTING content_handler = NEW cl_adt_rest_plain_text_handler( )
                           IMPORTING data = lt_plain_query ).
  lv_query = cl_oo_section_source=>convert_table_to_string( p_source = CONV #( lt_plain_query ) ).
  ```
  `lt_plain_query` is `TYPE sadt_srl_plain_text` (`TABLE OF STRING`, unbounded per row).
  `CONVERT_TABLE_TO_STRING` requires `P_SOURCE TYPE SEO_SECTION_SOURCE` — a table whose row type
  (`SEO_SECTION_SOURCE_LINE`) is `CHAR` length **255** (confirmed via DDIC read). The `CONV #(...)`
  performs an implicit table-type conversion; a STRING→CHAR255 row assignment truncates silently, no
  exception, `sy-subrc` untouched. A single-line query (no embedded `CR`/`LF`, exactly how vsp's own
  `runFreestyleQuery` sends it — see below) becomes one oversized row that gets cut at 255 chars
  **before any of the SQL-parsing classes ever see it** (`CL_ADT_DP_OPEN_SQL_HANDLER`,
  `CL_ADT_DATAPREVIEW_UTIL`). Verified end to end in one breakpoint stop: raw HTTP body bytes
  (`BIN_DATA` in `CL_ADT_REST_PLAIN_TEXT_HANDLER=>DESERIALIZE`) arrive complete (`XString{275}`,
  `intercentro` included); `LT_PLAIN_QUERY[1]` right before the `CONV #()` still has the full 275
  chars; `LV_QUERY` right after has 257 (255 truncated + the 2-char `CR_LF` that
  `CONVERT_TABLE_TO_STRING` appends), missing `intercentro` entirely.
- **The bug was introduced by SAP Note 2807133** ("Fix incorrect crlf and lf handling in ADT SQL
  Console", `BC-DWB-AIE-DP`, 2019, `SAP_BASIS 750→754`) — confirmed reading its own diff: the code it
  *replaced* was `lv_query = request->get_inner_rest_request( )->get_entity( )->get_string_data( ).`,
  a direct unbounded STRING read with no truncation risk at all. The note fixed a real Mac CR/LF
  issue but introduced this length regression as a side effect. This project's system
  (`SAP_BASIS 758`) inherits the note's code by release lineage — it isn't "missing" the note, the
  note's own fix *is* the bug. No later SAP note fixing this regression was found (checked the
  Support Portal's own listing of notes touching `CL_ADT_DP_FREESTYLE_RES POST`: 3807261, 2051046,
  2977495, 2347886 — none address query length/truncation) and no GitHub issue/PR/comment anywhere in
  `oisee/vibing-steampunk`, its forks, or `marcellourbani/abap-adt-api` (the reference TS client, which
  sends the same unsplit raw body) mentions it either.
- **Workaround (server-side, always available, no vsp change needed)**: send the SQL with a real line
  break (`\n`) before any line would exceed 255 characters — `sadt_srl_plain_text`/
  `seo_section_source` truncate **per line**, so a query broken into short-enough lines survives
  intact. Verified live, repeatedly: the exact 275-char single-line query above gives the wrong,
  unchanged `558383`; the same query with one `\n` inserted before the 6th condition gives the
  correct, different `253952`. Documented for users at `~/.claude/sap-mcp-servers.md` ("Cómo escribir
  consultas largas").
- **Not fixed in vsp itself, and no user-facing SAP incident opened (explicit user decision)** — this
  is purely a SAP-side ABAP defect; vsp's `runFreestyleQuery` (`pkg/adt/client.go:1301`) already sends
  the caller's SQL byte-for-byte unmodified (`Body: []byte(sqlQuery)`), which is correct behavior — it
  doesn't corrupt anything itself. A **possible future improvement, not started, not requested**: vsp
  could defensively auto-insert a line break before character 255 in any single-line query passed to
  `runFreestyleQuery`/`GetTableContents`'s freestyle path, protecting every caller (including this
  MCP's own `action="query", target="SQL"`) from ever hitting this SAP bug without needing to know
  about it — would need the same "break before a keyword, never mid-identifier/literal" care as the
  manual workaround above.

### `SAP_READ_ONLY` does not gate `CALL_RFC`/`RUN_REPORT`/`RUN_REPORT_ASYNC` — confirmed gap, not fixed (2026-09-22)
- **Confirmed via grep** (`checkMutation|checkSafety|ReadOnly|Safety\(\)`) across `internal/mcp/handlers_debugger.go`
  (`CALL_RFC`) and `internal/mcp/handlers_report.go` (`RUN_REPORT`/`RUN_REPORT_ASYNC`, even after the Fase 2
  `pkg/saprfc`/XBP migration in 2an): **no matches in either file**. `pkg/adt/safety.go`'s `SafetyConfig.ReadOnly`
  (bound from `SAP_READ_ONLY`) blocks ADT CRUD writes (`create`/`update`/`delete`/`activate`, via `checkMutation`/
  `checkSafety` in `crud.go` and every `workflows_*.go`) — but these three handlers never call into that gate at
  all. A report scheduled via `RUN_REPORT` can itself perform database writes; a function module invoked via
  `CALL_RFC` can be any RFC-enabled BAPI, including write-capable ones. `SAP_READ_ONLY=true` alone does **not**
  make a connection genuinely read-only against these two tools. Mode/group-based restriction (`SAP_MODE`,
  `SAP_DISABLED_GROUPS`) doesn't close the gap either: `CallRFC`/`RunReport` are in no group at all in
  hyperfocused mode (single universal tool, zero group gating at registration — `tools_register.go`'s
  `if mode == "hyperfocused" { s.registerUniversalTool(); return }`), and are baked into focused mode's fixed
  whitelist too (`tools_focused.go`).
- **Discovered while setting up a second, dedicated read-only production MCP connection** (`abap-adt-prod`, a
  separate entry in `claude_desktop_config.json` with `SAP_READ_ONLY=true`) for other project work (SAP
  consulting, not this repo). The real backstop chosen there is **SAP-side authorization**, not this vsp flag:
  the connection's SAP user has a dedicated restricted role with `ACTVT=03` (display) only on
  `S_DEVELOP`/`S_TABU_DIS`/`S_TABU_NAM`/`S_ADT_RES`, and **no `S_RFC`** — so even though `CALL_RFC`/`RUN_REPORT`
  aren't gated by vsp, SAP itself rejects any RFC call for that user regardless of what it's asked to do.
  Live-verified: `ACTIVATE_MULTI` against that connection was correctly rejected by vsp's own safety gate
  ("blocked by safety configuration"), confirming the ADT-level gate works as documented — the gap is specific
  to the RFC-based handlers.
- **Not fixed here** — a real fix would add a `checkSafety`/`checkMutation`-style gate to both handlers (at
  minimum refusing `CALL_RFC` outright, or requiring an explicit allow-list of read-only RFC names, under
  `SAP_READ_ONLY=true`; `RUN_REPORT` is inherently unsafe to allow at all under a read-only policy, since a
  report's own ABAP logic can write regardless of any parameter passed to it). Left as a documented gap rather
  than fixed opportunistically — deserves its own planned change (safety semantics, not a narrow bug fix), not
  a drive-by patch during unrelated work.

### `WriteMessageClassTexts`/`CreateMessageClass` — message text does not persist — RESOLVED, see 2ao (2026-10-02; was "closed as a known limitation" 2026-09-09)
> **Resolved.** The root cause was never the body shape or a discarded body: the PUT carried no
> `adtcore:language`, and SAP ignores the `sap-language` header on this resource, so rows landed in `T100`
> with a blank `SPRSL` (invisible to a read in the logon language). Fixed and live-verified in 2ao.
> Everything below is the original, superseded write-up, kept for history — its "do not re-attempt"
> advice no longer applies, and `docs/message-class-write-investigation.md` is marked resolved.
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

### `RUN_REPORT` — hangs on reports with a selection screen — FIXED, see 2an above (2026-09-17)
- **Closed by the pkg/saprfc Fase 2 migration** (2an): `RUN_REPORT`/`RUN_REPORT_ASYNC` no longer go
  through the WebSocket `SUBMIT ... AND RETURN` path at all — they schedule the report as an XBP
  background job over classic RFC, which has no `APC_ILLEGAL_STATEMENT` restriction. The
  `MISSING_PARAM`/`GetJobStatus`/`GetSpoolOutput` structural issues below are moot — that whole
  code path (and the ABAP-side actions it assumed, which never existed) was removed, not patched.
  Live-verified against the exact repro in this entry's original text (`ZTESTRCG1`, no params) — see
  2an for the full verification record.

### `SET_BREAKPOINT` refuses every function-module include, not just standard/kernel-called ones — FIXED, see 2ak above (2026-09-14, root-caused and fixed 2026-09-15)
- **Revises an earlier, narrower theory**: 2ah's investigation (and the Run RFC live-verification gap it
  left open) found `SET_BREAKPOINT` on `RFC_SYSTEM_INFO` failing with `SET_BREAKPOINT_FAILED: Sólo posible
  fijar breakpoints en códigos fuente activos y sin modificar`, and concluded it was "likely a SAP-side
  restriction on debugging standard/kernel-called function modules" (`RFC_SYSTEM_INFO` internally does a
  `CALL 'RFCSystemInfo' ID ...`). That theory is now falsified: a **brand-new custom Z function module**
  (`ZVSP_TST_RFC_NOOP`, `$TMP`, group `ZVSP_TEST`, no kernel calls, freshly created and activated by
  `WriteSource` with `activation.success:true`) hits the **exact same error** when a breakpoint is set on
  its generated include (`LZVSP_TESTU01`, the correct target — confirmed via `SAP(action="search",
  target="LZVSP_TEST*")`, since the FM name itself and the group's main program `SAPLZVSP_TEST` both give
  different, clearly-wrong errors: `SET_BREAKPOINT_FAILED: A breakpoint cannot be set in this place`).
  Re-activating via `ACTIVATE_MULTI` (`FUGR ZVSP_TEST` + `INCL LZVSP_TESTU01`) made no difference.
- **What still works**: the FM itself is genuinely fine — `RFC_SEARCH` finds it, `RFC_METADATA` reports its
  (empty) signature correctly, and `CALL_RFC` executes it with `subrc: 0`. Only the breakpoint step fails.
  2ah's own live verification *did* successfully set and catch a breakpoint on a `PROG`-type `$TMP` object
  this same session cycle — so the defect looks scoped to **function-group includes specifically**, not to
  breakpoints in general.
- **Root cause: not found.** Plausible candidates not yet investigated: `ZCL_VSP_DEBUG_SERVICE`'s breakpoint
  handler may resolve `program`+`line` to a source/version check that doesn't understand a `FUGR/FF`
  include's containing-group relationship the way it does a plain `PROG`; or the include's "active" flag
  genuinely lags its own function group's in a way `ACTIVATE_MULTI` doesn't close (worth checking via a
  direct `RS_INACTIVE_OBJECTS`-style query, not yet done).
- **Practical implication**: the "Run RFC" live-verification checklist item (breakpoint → Listen → trigger →
  catch, on an RFC target) **cannot be completed with any function module on this system** until this is
  fixed — not a vsp CLI/config bug, a `ZADT_VSP` (or its Go client wrapper's) limitation. Use a `PROG`/report
  target instead when a breakpoint-based debug-ui/REPL demo is needed.
- Not fixed this session — `ZVSP_TST_RFC_NOOP`/`ZVSP_TEST` were left in `$TMP` as a ready-made repro for a
  future investigation rather than deleted immediately.

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
  an MCP-level design change (upstream is exploring this per PR #183, not ported here).
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
