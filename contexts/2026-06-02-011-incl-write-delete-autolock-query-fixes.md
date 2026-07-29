# Sesión 2026-06-02 — INCL write support + DELETE auto-lock + RunQuery/tableContents fixes

## Contexto

Análisis de errores observados en la sesión del proyecto "Carga de IDoc Vales desde Excel" con vsp como servidor MCP.

## Causa raíz identificada

**Todos los errores del proyecto IDoc tenían una única causa raíz**: `WriteSource` no soportaba el tipo `INCL`. Cuando vsp intentaba crear includes, caía en el caso `default` de la validación y devolvía "Unsupported object type" — o en algunos flujos los creaba como `PROG` por usar el tipo incorrecto. Esto causó una cascada:

1. Includes creados como `PROG` (tipo incorrecto) → URL ADT incorrecta → todas las operaciones posteriores fallan
2. Modificaciones fallidas (ADT URL de program no coincide con el include real)
3. DELETE rechazado con "lock handle not accepted" — el objeto tenía el tipo equivocado, no era un bug de session affinity puro

El usuario tuvo que convertir manualmente los objetos a tipo INCL, tras lo cual todo funcionó.

## Fixes implementados (commit `da499ff`)

### Bug B — INCL write support (issue #116, PR #121 parcialmente portado)

Ficheros:
- `pkg/adt/workflows.go` — nuevo `WriteInclude()` workflow: SyntaxCheck → Lock → UpdateSource → Unlock → Activate. Solo bloquea en E/A/X, los warnings pasan (compatibles con SAP_IGNORE_WARNINGS=true).
- `pkg/adt/workflows_source.go` — `INCL` añadido al switch de validación, al check de existencia, a `writeSourceCreate` y `writeSourceUpdate`.
- `pkg/adt/client.go` — `CanonicalObjectType("INCL")` ahora devuelve `"PROG/I"` (eliminado TODO pendiente de PR #121).
- `internal/mcp/handlers_source.go` — INCL añadido al case de routeSourceAction.

Uso tras el fix:
```
SAP(action="edit", target="INCL ZREDI_CREATE_IDOC_FILE_F01", params={"source": "..."})
SAP(action="edit", target="EDITSOURCE", params={"object_url": "/sap/bc/adt/programs/includes/zredi_create_idoc_file_f01", ...})
```

### Bug A — DELETE auto-lock (issue #88 / session affinity)

Ficheros:
- `pkg/adt/crud.go` — nuevo `DeleteObjectWithAutoLock()`: prueba accessMode=DELETE, fallback a MODIFY; lock+delete atómicos en una sola sesión stateful.
- `internal/mcp/handlers_crud.go` — `lock_handle` pasa a ser opcional en `handleDeleteObject`. Si se omite, usa `DeleteObjectWithAutoLock`.

Nota: el DELETE session affinity sigue siendo un bug real de ADT (lock y delete en sesiones distintas invalida el handle). La corrección es válida aunque no fuera el causante principal en el proyecto IDoc.

### RunQuery / tableContents

- `pkg/adt/client.go` — `GetTableContents` con `sqlFilter` ahora usa el endpoint freestyle (`/sap/bc/adt/datapreview/freestyle` POST) en lugar del endpoint DDIC (que ignoraba el cuerpo). Auto-construye `SELECT * FROM <table> WHERE <filter>` si el filtro no empieza por SELECT.
- `internal/mcp/handlers_universal.go` — `callHandler` ahora recupera panics y los devuelve como errores de herramienta en lugar de -32603.

## Issues de upstream consultados

- **#116** — WriteSource no soporta INCL — nuestro fix lo cierra para el fork
- **#121** (DRAFT PR, frd1201) — INCL write support — portado parcialmente (la parte que nos faltaba: write path)
- **#125** (PR, dme007) — mutation gate skip — ya estaba en nuestro fork desde sesión 008
- **#98** — Invalid lock handle LOCK/UPDATE_SOURCE en sesiones distintas — mismo patrón que nuestro DELETE fix
- **#118** — varios bugs observados con Kiro — documentación útil sobre casos de uso

## Regla de oro añadida

En `~/.claude/CLAUDE.md`: nunca borrar objetos SAP sin preguntar antes, explicar qué se borra y por qué, esperar confirmación explícita.

## Estado del fork

- Rama: `fix/lock-nomodification-with-transport` en `txape10/vibing-steampunk` — **PUSHED** (6 commits tras sesión anterior)
- Binario: `%LOCALAPPDATA%\VSP\vsp.exe` — actualizado
- Todos los tests pasan (go test ./pkg/... ./internal/...)
