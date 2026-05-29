# 2026-05-26 Seed — Probar mcp-abap-abap-adt-api contra bug #132

Continuación de la sesión anterior. El objetivo inmediato es verificar si el servidor
TypeScript `mcp-abap-abap-adt-api` (usa `abap-adt-api`) puede escribir fuente a
`ZCL_VSP_APC_HANDLER` — un objeto que está en una orden de transporte activa.
Si lo consigue, comparar qué envía en el PUT para terminar de validar el fix en vsp.

---

## Contexto del bug #132

### Síntoma
vsp devuelve 423 `ExceptionResourceInvalidLockHandle` al intentar `UpdateSource` en
objetos que ya están en una orden activa del usuario.

### Causa raíz confirmada
Cuando SAP devuelve `NoModification + CorrNr` en el LOCK, **no crea una entrada ENQUEUE
real** — el handle es simbólico. Si vsp lo manda en el PUT (`?lockHandle=...`), SAP
rechaza con 423. La autorización de escritura viene del `CorrNr` (orden de transporte),
no del handle.

### Fix ya aplicado en `workflows_edit.go`
`pkg/adt/workflows_edit.go` (~línea 397):
```go
if isTransportOwned {
    writeLockHandle = ""               // no real ENQUEUE lock to reference
    writeTransport = lockResult.CorrNr // transport IS the authorization
}
```
Este archivo tiene el fix **completo y correcto**.

### Fix incompleto en `workflows_source.go`
`pkg/adt/workflows_source.go` (~línea 1011):
```go
writeLockHandle := lock.LockHandle  // ← BUG: debería ser "" cuando isTransportOwned
writeTransport := transport
if isTransportOwned {
    writeTransport = lock.CorrNr
    // FALTA: writeLockHandle = ""
}
```
Necesita la misma corrección que `workflows_edit.go`.

---

## Estado del servidor MCP TypeScript

El servidor `mcp-abap-abap-adt-api` está **disponible en esta sesión** como tools
`mcp__mcp-abap-abap-adt-api__*`. Credenciales ya configuradas en
`C:\Users\devuser\mcp-abap-abap-adt-api\.env` (SAP_URL, SAP_USER, SAP_PASSWORD).

No es necesario hacer `login` — el cliente `abap-adt-api` gestiona la sesión
automáticamente en la primera llamada.

---

## Tarea inmediata: test de escritura con el server TypeScript

### Pasos

1. **Buscar el objeto**
   ```
   searchObject(query: "ZCL_VSP_APC_HANDLER")
   → anota la URI, ej. /sap/bc/adt/oo/classes/zcl_vsp_apc_handler
   ```

2. **Leer fuente actual**
   ```
   getObjectSource(objectSourceUrl: "<uri>/source/main")
   ```

3. **Obtener info de transporte**
   ```
   transportInfo(objSourceUrl: "<uri>")
   → anota el TRKORR de la orden activa
   ```

4. **Lock**
   ```
   lock(objectUrl: "<uri>")
   → observar qué devuelve: ¿lockHandle? ¿CorrNr? ¿ModificationSupport?
   ```

5. **Escribir fuente** (mínimo cambio: añadir/modificar un comentario)
   ```
   setObjectSource(
     objectSourceUrl: "<uri>/source/main",
     lockHandle: "<handle del paso 4>",
     source: "<fuente con cambio mínimo>",
     transport: "<TRKORR>"
   )
   ```

6. **Observar resultado**: ¿éxito o 423?
   - Si **éxito** → el server TypeScript lo resuelve. Capturar qué lockHandle mandó (vacío, handle completo, o ausente).
   - Si **423** → mismo bug que vsp; el problema no es específico de Go.

7. Si éxito: **activar y unlock**
   ```
   activateByName(...)
   unLock(objectUrl: "<uri>", lockHandle: "<handle>")
   ```

### Qué comparar con vsp
| Parámetro PUT | vsp (actual, buggy) | mcp-abap-abap-adt-api |
|---|---|---|
| `lockHandle` query param | handle simbólico | ¿vacío? ¿ausente? |
| `corrNr` | presente | ¿presente? |

---

## Limpieza pendiente antes del PR (independiente del test)

### `pkg/adt/workflows_source.go`
- Líneas ~994-1004: borrar bloque `lockDebug` + `os.WriteFile`
- Línea ~1028: quitar `%s", lockDebug` del mensaje de error
- Imports huérfanos: `"os"`, `"path/filepath"`, `"time"` (verificar con `go build`)

### `pkg/adt/workflows_edit.go`
- Línea 24: borrar campo `LockDebug string` de `EditSourceResult`
- Líneas ~370-392: borrar los `result.LockDebug = fmt.Sprintf(...)` de diagnóstico
- Líneas 15-17: borrar los `var _ = filepath.Join` / `var _ = time.Now` (y sus imports)

### `pkg/adt/workflows_source.go` — fix incompleto
- Añadir `writeLockHandle = ""` dentro del bloque `if isTransportOwned` (~línea 1013)

### Verificación final
```bash
go build -o vsp_fix.exe ./cmd/vsp
go test ./pkg/adt/...
```

---

## Fix pendiente en `pkg/adt/crud.go` (issue #132 upstream)

El fix de fondo (guard en `LockObject`) que va al PR del fork:

```go
// pkg/adt/crud.go ~línea 62
// Antes:
if accessMode == "MODIFY" && strings.EqualFold(result.ModificationSupport, "NoModification") {

// Después:
if accessMode == "MODIFY" &&
    strings.EqualFold(result.ModificationSupport, "NoModification") &&
    result.CorrNr == "" {
```

Test que acompaña: `TestLockObject_AllowsNoModificationWithExistingTransport`
(detalle completo en `contexts/2026-05-26-001-seed-lock-nomodification-incl-bugs.md`).

---

## Referencias

| Recurso | |
|---|---|
| Issue #132 | https://github.com/oisee/vibing-steampunk/issues/132 |
| Fork del contribuidor | https://github.com/txape10/vibing-steampunk |
| Rama de trabajo | `fix/lock-nomodification-with-transport` |
| Context anterior | `contexts/2026-05-26-001-seed-lock-nomodification-incl-bugs.md` |
