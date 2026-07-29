# Session 010 — Hardcode Tool: Integration Tests & Query Fixes

**Date**: 2026-05-29  
**Branch**: `fix/lock-nomodification-with-transport` on `txape10/vibing-steampunk`  
**Binary**: `%LOCALAPPDATA%\VSP\vsp.exe` — updated and validated

---

## Qué se hizo en esta sesión

Esta sesión continuó el trabajo de la 009. El objetivo era ejecutar tests de integración SAP reales contra el sistema live antes de hacer commit+push.

### Tests ejecutados

- `hardcode_usage` con `field=FRA_ABONO` y `grep=false` → ✅ 2 entries, status DYNAMIC (esperado)
- `hardcode_audit` con `grep=false` → ✅ 371 entries encontradas
- `hardcode_usage` con `field=TRIANGULACION` y `grep=true` → ✅ caller `ZRSU_AFE_TRIANGULACION` confirmado HIGH, 3 entries MISCONFIGURED (SUBKEYFLD=`ZMONITOR_PEDIDO_COMPRA*` ≠ caller real)

### Bugs encontrados y corregidos durante los tests

#### Bug 1 — CROSS.TYPE es CHAR(1)
- Error SAP: `'DA' is not a valid value for C(1,0)`
- Causa: el código original usaba `CROSS WHERE NAME='ZTCA_HARDCODE' AND TYPE='DA'` pero `CROSS.TYPE` es un campo CHAR(1). 'DA' tiene 2 caracteres → inválido.
- Fix: reemplazado por `SELECT MASTER FROM D010TAB WHERE TABNAME = 'ZTCA_HARDCODE'`
- `D010TAB` es la tabla SAP estándar para cross-references programa→tabla DB. `MASTER` = nombre del include/programa.

#### Bug 2 — WBCROSSGT.OTYPE='CLAS' demasiado largo
- Error SAP: `'CLAS' is not a valid value for C(2,0)`
- Causa: `OTYPE` es CHAR(2). 'CLAS' tiene 4 chars. El valor correcto para clases es 'CL' (2 chars).
- Fix: revertido `OTYPE='CLAS'` → `OTYPE='CL'`

#### Bug 3 — `~CL_GET_HARDCODE` en la lista de callers
- D010TAB devolvía `~ZCL_GET_HARDCODE===CP` (o similar con prefijo `~`). Estos son sub-includes SAP internos generados para class-pools y function groups. No son objetos invocables reales.
- Intento inicial incorrecto: filtrar por `ZCL_GET_HARDCODE` (el raw MASTER empieza por `~`, no por `Z`).
- Fix correcto: `strings.HasPrefix(include, "~")` — filtra TODOS los sub-includes internos SAP antes de llamar a `NormalizeInclude`.

#### Bug 4 — `/1BCDWB/DBZTCA_HARDCODE` en la lista de callers
- D010TAB incluía un programa generado por SM30 (`/1BCDWB/DBZTCA_HARDCODE`) creado cuando otro desarrollador generó mantenimiento de tabla directo sobre ZTCA_HARDCODE (en lugar de crear una vista de actualización). El programa existe pero no es un caller de negocio.
- Fix: `strings.HasPrefix(include, "/1BCDWB/")` — filtra programas generados por SM30/SE16.

---

## Estado final de `fetchHardcodeGlobalCallers`

```go
// Path A: D010TAB — programa→tabla DB cross-reference
crossQuery := fmt.Sprintf("SELECT MASTER FROM D010TAB WHERE TABNAME = '%s'", hardcodeTableName)
// ...loop con filtros:
//   - strings.HasPrefix(include, "~")          → sub-includes SAP internos
//   - strings.HasPrefix(include, "/1BCDWB/")   → programas generados SM30/SE16
//   - strings.Contains(ToUpper(include), "ZCL_GET_HARDCODE") → accessor class

// Path B: WBCROSSGT — callers del accessor ZCL_GET_HARDCODE
wbQuery := fmt.Sprintf("SELECT INCLUDE FROM WBCROSSGT WHERE OTYPE = 'CL' AND NAME = '%s'", hardcodeAccessorClass)
// (0 resultados en sistema test — los programas usan SQL directo, no el accessor)
```

---

## Git log final (5 commits en el branch)

```
8191a7f  fix(fork): correct ZTCA_HARDCODE caller queries after integration testing
7b8cb5d  chore: add pending session contexts, reports and gitignore docs/
a445ec3  feat(fork): wire hardcode tool into MCP routing and focused mode
13cc5b3  feat(fork): ZTCA_HARDCODE audit tool — hardcode-usage / hardcode-audit
8b65774  feat(adt): in-memory source cache with write-invalidation (TTL 10min)
```

Todos pushed a `origin` (txape10/vibing-steampunk).

---

## Conocimiento adquirido sobre SAP cross-references

| Tabla | Campo clave | Campo resultado | Para qué sirve |
|---|---|---|---|
| `D010TAB` | `TABNAME` | `MASTER` | Programas que hacen SELECT sobre una tabla DB |
| `WBCROSSGT` | `OTYPE='CL'`, `NAME` | `INCLUDE` | Objetos que referencian una clase |
| `CROSS` | — | — | NO USAR — TYPE es CHAR(1), no soporta 'DA' |

Entradas a filtrar de D010TAB:
- Prefijo `~` → sub-includes internos SAP (class-pools, FUGRs)
- Prefijo `/1BCDWB/` → programas generados por mantenimiento de tabla SM30/SE16

---

## Pendiente (no iniciado en esta sesión)

- El branch tiene 5 commits sin PR formal. El upstream original es `oisee/vibing-steampunk`.
- No hay PR abierto. El usuario no lo ha solicitado.
- `pkg/graph/` SQL adapters (D010INC, etc.) — diseño en reports/002 y reports/003, aún pendiente.
- Bugs upstream #88, #55, #46 — no tocados.

---

## Cómo probar la herramienta

```
# MCP (requiere binario actualizado + reinicio de Claude Desktop)
SAP(action="analyze", params={"type":"hardcode_usage","field":"FRA_ABONO"})
SAP(action="analyze", params={"type":"hardcode_usage","field":"FRA_ABONO","grep":true})
SAP(action="analyze", params={"type":"hardcode_audit","grep":false})

# CLI
vsp hardcode-usage --field FRA_ABONO
vsp hardcode-usage --field FRA_ABONO --grep
vsp hardcode-audit --no-grep
vsp hardcode-audit --format html > hardcode-catalog.html
```

---

## Referencia rápida de archivos clave

| Archivo | Rol |
|---|---|
| `internal/mcp/handlers_hardcode.go` | Lógica completa: handlers MCP + helpers |
| `cmd/vsp/cli_hardcode.go` | Comandos CLI `hardcode-usage` / `hardcode-audit` |
| `internal/mcp/server_cli.go` | Métodos exportados que usa el CLI |
| `pkg/graph/types_hardcode.go` | Tipos `HardcodeEntry`, `HardcodeCaller`, `HardcodeAuditResult` |
| `pkg/graph/classify_hardcode.go` | Lógica de clasificación STANDARD/REUSE/MISCONFIGURED/DEAD/DYNAMIC |
| `pkg/graph/graph.go` | `NormalizeInclude`, `ClassifyEntry` |
