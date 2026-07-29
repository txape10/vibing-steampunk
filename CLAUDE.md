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

## Known Open Issues (Not Fixed)

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
- **#88** Lock handle bug (EditSource/WriteSource) — same root cause as #132 (session affinity)
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
