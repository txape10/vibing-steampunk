# Session 2026-05-29-009 — ZTCA_HARDCODE audit tool

## Estado al cierre

- Rama: `fix/lock-nomodification-with-transport` en `txape10/vibing-steampunk`
- Binario: `%LOCALAPPDATA%\VSP\vsp.exe` — actualizado con esta sesión

## Qué se hizo

Implementación completa de la herramienta de análisis de `ZTCA_HARDCODE` en el fork.
La herramienta es **customer-specific**: solo funciona en sistemas que tienen esa tabla.

### Ficheros creados

| Fichero | Descripción |
|---|---|
| `pkg/graph/queries_hardcode.go` | Estructuras de datos: `HardcodeEntry`, `HardcodeUsageResult`, `HardcodeAuditResult`, `ClassifyEntry`, `SortHardcodeEntries` |
| `internal/mcp/handlers_hardcode.go` | Handlers `handleHardcodeUsage` + `handleHardcodeAudit` + lógica de adquisición (CROSS + WBCROSSGT) + enriquecimiento por grep |
| `internal/mcp/server_cli.go` | `NewServerForCLI(client)` + métodos exportados `RunHardcodeUsage` / `RunHardcodeAudit` para uso desde CLI |
| `cmd/vsp/cli_hardcode.go` | Comandos `vsp hardcode-usage` y `vsp hardcode-audit` (text / json / html) |

### Ficheros modificados

| Fichero | Cambio |
|---|---|
| `internal/mcp/handlers_analysis.go` | Añadidos casos `hardcode_usage` y `hardcode_audit` al dispatcher `routeAnalysisAction` |
| `internal/mcp/tools_register.go` | Registradas tools `HardcodeUsage` y `HardcodeAudit` en `registerAnalysisTools` |
| `internal/mcp/tools_focused.go` | Incluidas en focused mode |
| `CLAUDE.md` | Actualizado: Graph Engine item, codebase map, task table |
| `~/.claude/sap-mcp-servers.md` | Añadida sección hardcode + referencia rápida |

## Uso

**MCP (hyperfocused):**
```
SAP(action="analyze", params={"type":"hardcode_usage","field":"FRA_ABONO"})
SAP(action="analyze", params={"type":"hardcode_usage","program":"ZXEDFU02"})
SAP(action="analyze", params={"type":"hardcode_audit"})
SAP(action="analyze", params={"type":"hardcode_audit","grep":false})
```

**CLI:**
```bash
vsp hardcode-usage --field FRA_ABONO
vsp hardcode-usage --program ZXEDFU02
vsp hardcode-audit --format html > hardcode-catalog.html
vsp hardcode-audit --no-grep
```

## Diseño clave

### Fuentes de datos
- **CROSS** `WHERE NAME='ZTCA_HARDCODE' AND TYPE='DA'` → programas con SELECT directo
- **WBCROSSGT** `WHERE OTYPE='CL' AND NAME='ZCL_GET_HARDCODE'` → programas usando el accessor

### Confirmación por grep (con `grep=true`, defecto)
Para cada FIELD único en las entradas, se grep-confirma en el source de cada caller global.
Solo los callers que contienen ese literal en su código se consideran confirmados (HIGH).
Sin grep, todos los callers globales son MEDIUM.

### Clasificación por entrada
```
STANDARD     — SUBKEYFLD == un único caller confirmado   (convención sy-repid correcta)
REUSE        — SUBKEYFLD == uno de N callers confirmados  (reutilización intencional)
MISCONFIGURED — SUBKEYFLD no coincide con ningún caller   (configuración incorrecta)
DEAD         — sin callers de ningún tipo                (entrada huérfana)
DYNAMIC      — callers MEDIUM pero ninguno HIGH           (i_subkey construido dinámicamente)
```

### Graceful failure en sistemas sin la tabla
`checkHardcodeTableExists` consulta `DD02L` antes de hacer nada.
Mensaje de error: `table ZTCA_HARDCODE not found in this system's DDIC — this tool is customer-specific and only works on systems where this table exists`

## Constantes en handlers_hardcode.go
```go
const hardcodeTableName    = "ZTCA_HARDCODE"
const hardcodeAccessorClass = "ZCL_GET_HARDCODE"
```
Usadas en todas las queries — no hay strings mágicos dispersos.
