# 2026-05-26 Seed — LockObject/NoModification + INCL 404 (branch fix/lock-nomodification-with-transport)

Dos bugs descubiertos en la misma sesión probando edición de objetos en un sistema on-prem.
El fork `txape10/vibing-steampunk` está listo; la rama y los issues (#132, #133) ya están abiertos.

---

## 1. Bug #132 — LockObject rechaza objetos editables que ya están en un transporte activo

### Síntoma
```
object X is not modifiable via ADT on this system
(SAP returned modificationSupport="NoModification" during LOCK)
```
Aparece al intentar editar cualquier objeto que el mismo usuario ya tiene bloqueado
en una orden de transporte abierta en el sistema.

### Causa raíz
El fix de 2026-04-15 (commit `22517d4`, issue #91) añadió un guard en `LockObject`
(`pkg/adt/crud.go`) que rechaza locks cuando SAP devuelve `NoModification`. Es correcto
para BTP/objetos read-only, pero on-prem SAP también devuelve `NoModification` cuando
el objeto ya está en una orden activa del mismo usuario — solo que en ese caso `CorrNr`
viene relleno, que es la señal de que sí es editable.

### Fix — una línea en `pkg/adt/crud.go`

Buscar (~línea 62 en la rama actual):
```go
if accessMode == "MODIFY" && strings.EqualFold(result.ModificationSupport, "NoModification") {
```

Reemplazar por:
```go
if accessMode == "MODIFY" &&
    strings.EqualFold(result.ModificationSupport, "NoModification") &&
    result.CorrNr == "" {
```

**Invariante:** `NoModification` + `CorrNr != ""` = objeto en orden activa del usuario = editable.
`NoModification` + `CorrNr == ""` = objeto genuinamente read-only = rechazar.

### Test a añadir (`pkg/adt/crud_reconcile_test.go`)

```go
func TestLockObject_AllowsNoModificationWithExistingTransport(t *testing.T) {
    const existingTransportLockXML = `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0">
  <asx:values>
    <DATA>
      <LOCK_HANDLE>HANDLE-Y</LOCK_HANDLE>
      <CORRNR>TR-EXAMPLE-1</CORRNR>
      <CORRUSER>TESTUSER</CORRUSER>
      <CORRTEXT>ZADT_VSP</CORRTEXT>
      <IS_LOCAL></IS_LOCAL>
      <IS_LINK_UP></IS_LINK_UP>
      <MODIFICATION_SUPPORT>NoModification</MODIFICATION_SUPPORT>
    </DATA>
  </asx:values>
</asx:abap>`
    mock := &methodPathMock{
        routes: []routedResponse{
            resp("", "discovery", 200, "ok"),
            resp(http.MethodPost, "/oo/classes/ZCL_VSP_APC_HANDLER", 200, existingTransportLockXML),
        },
    }
    cfg := NewConfig("https://devsys-adt.example.local:44300", "user", "pass")
    transport := NewTransportWithClient(cfg, mock)
    client := NewClientWithTransport(cfg, transport)
    result, err := client.LockObject(
        context.Background(),
        "/sap/bc/adt/oo/classes/ZCL_VSP_APC_HANDLER",
        "MODIFY",
    )
    if err != nil {
        t.Fatalf("LockObject should succeed when NoModification but CorrNr present "+
            "(object already in open transport): %v", err)
    }
    if result.LockHandle != "HANDLE-Y" {
        t.Errorf("LockHandle = %q, want HANDLE-Y", result.LockHandle)
    }
    if result.CorrNr != "TR-EXAMPLE-1" {
        t.Errorf("CorrNr = %q, want TR-EXAMPLE-1", result.CorrNr)
    }
}
```

Encaja junto a los tests existentes `TestLockObject_RejectsNoModification` y
`TestLockObject_AllowsNoModificationOnReadLock` en `crud_reconcile_test.go`.

---

## 2. Bug #133 — INCL GetSource/WriteSource → 404

### Síntoma
```
GetSource failed: status 404 at /sap/bc/adt/programs/includes/ZRCG1/source/main
Resource PROGRAM ZRCG1 does not exist.
```
Al intentar leer o editar un objeto de tipo `INCL` (include de programa ABAP).

### Causa
Los includes no son objetos ADT independientes — siempre pertenecen a un programa padre.
La URL correcta requiere el padre, pero `GetSource`/`WriteSource` construyen la URL
solo con el nombre del include.

### Fix pendiente
Requiere analizar `pkg/adt/client.go` para ver cómo se resuelve la URL del objeto
y añadir un paso de lookup del padre cuando `objectType == "INCL"`.
No se abordó en esta sesión — segunda rama después de que #132 esté merged.

---

## 3. Estado al cierre de sesión

### Completado
- Issues #132 y #133 abiertos en `oisee/vibing-steampunk`
- Fork `txape10/vibing-steampunk` creado y clonado
- Rama `fix/lock-nomodification-with-transport` creada
- Go 1.26.3 instalado

### Siguiente acción inmediata: aplicar el fix de #132

| Paso | Qué |
|------|-----|
| 1 | Aplicar la línea de fix en `pkg/adt/crud.go` |
| 2 | Añadir el test `TestLockObject_AllowsNoModificationWithExistingTransport` en `crud_reconcile_test.go` |
| 3 | `go test ./pkg/adt/...` — verificar que pasan los 4 tests del grupo `TestLockObject_*` |
| 4 | `go build -o vsp_fix.exe ./cmd/vsp` |
| 5 | Probar en el sistema real: intentar editar `ZCL_VSP_APC_HANDLER` (que está en orden activa) |
| 6 | Commit + push al fork |
| 7 | Abrir PR en `oisee/vibing-steampunk` desde la rama del fork |
| 8 | Segunda rama para Bug #133 (INCL) |

### Pendiente tras resolver #132
Las clases VSP en el sistema (`ZCL_VSP_APC_HANDLER` y el resto) tienen las descripciones
de clase y de métodos en blanco. No se pudieron completar porque están bloqueadas en
órdenes activas y el bug #132 impedía editarlas. Una vez el fix esté en producción
(o compilado localmente), completar desde Claude directamente.

---

## Referencias

| Recurso | URL |
|---------|-----|
| Issue #132 (LockObject + transporte activo) | https://github.com/oisee/vibing-steampunk/issues/132 |
| Issue #133 (INCL 404) | https://github.com/oisee/vibing-steampunk/issues/133 |
| Commit que introdujo el bug | https://github.com/oisee/vibing-steampunk/commit/22517d4 |
| Fork del contribuidor | https://github.com/txape10/vibing-steampunk |
| Contexto del fix original (sesión 2026-04-15) | `contexts/2026-04-15-001-security-scrub-and-lock-handle-fix.md` |
