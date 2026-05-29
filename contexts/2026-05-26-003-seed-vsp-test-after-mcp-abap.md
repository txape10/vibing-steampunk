# 2026-05-26 Seed — Probar vsp contra objetos reales (post-test mcp-abap-abap-adt-api)

Continuación directa de la sesión de hoy. Objetivo: verificar que vsp (con el fix del
bug #132 ya aplicado) puede modificar y activar los mismos objetos que probamos con
`mcp-abap-abap-adt-api`.

---

## Estado del sistema SAP

| Objeto | Tipo | Paquete | Orden/Tarea |
|---|---|---|---|
| `ZCL_VSP_APC_HANDLER` | CLAS/OC | ZABAP01 | S4DK928661 / tarea S4DK928662 |
| `ZREPORT_PRUEBA` | PROG/P | ZABAP01 | S4DK928673 |
| `ZREPORT_PRUEBA_TOP` | PROG/I | ZABAP01 | S4DK928673 |
| `ZREPORT_PRUEBA_F01` | PROG/I | ZABAP01 | S4DK928673 |

- `ZCL_VSP_APC_HANDLER` fue desbloqueado hoy vía SE24 por el usuario (estado LIMU corrupto
  que dejaba Eclipse). Ya está activo y limpio.
- `ZREPORT_PRUEBA` y sus includes fueron creados, modificados y activados hoy dos veces
  (una con objetos nuevos, otra con objetos ya en transporte — el escenario exacto del bug #132).
- La orden `S4DK928673` se llamó "Pruebas: MCP SERVER".

---

## Estado del fix de vsp (bug #132)

### Rama activa
`fix/lock-nomodification-with-transport`

### Fix completo en `pkg/adt/workflows_edit.go` (~línea 397)
```go
if isTransportOwned {
    writeLockHandle = ""
    writeTransport = lockResult.CorrNr
}
```

### Fix incompleto pendiente en `pkg/adt/workflows_source.go` (~línea 1011)
```go
writeLockHandle := lock.LockHandle  // ← BUG: debe ser "" cuando isTransportOwned
writeTransport := transport
if isTransportOwned {
    writeTransport = lock.CorrNr
    // FALTA: writeLockHandle = ""
}
```

### Fix en `pkg/adt/crud.go` (~línea 67) — re-lock con corrNr
```go
if accessMode == "MODIFY" &&
    strings.EqualFold(result.ModificationSupport, "NoModification") {

    if result.CorrNr == "" {
        return nil, fmt.Errorf("object %s is not modifiable...", ...)
    }
    // re-lock pasando corrNr para obtener ENQUEUE real
    params2.Set("corrNr", result.CorrNr)
    ...
}
```

### Código de debug pendiente de limpiar
- `pkg/adt/workflows_source.go` líneas ~994-1004: bloque `lockDebug` + `os.WriteFile`
- `pkg/adt/workflows_source.go` línea ~1028: `%s", lockDebug` en mensaje de error
- `pkg/adt/workflows_edit.go` línea 24: campo `LockDebug string` en `EditSourceResult`
- `pkg/adt/workflows_edit.go` líneas ~370-392: bloques `result.LockDebug = fmt.Sprintf(...)`
- Imports huérfanos: `"os"`, `"path/filepath"`, `"time"` en ambos archivos

---

## Pruebas a ejecutar con vsp

El server vsp está disponible como `mcp__abap-adt__SAP` en esta sesión.

### Prueba 1 — Clase con objeto en transporte activo (bug #132 real)
Objeto: `ZCL_VSP_APC_HANDLER` en orden `S4DK928661` / tarea `S4DK928662`

1. Leer fuente actual
2. Lock → observar si devuelve error o éxito
3. Escribir fuente con cambio mínimo (añadir comentario en `on_error`)
4. Activar
5. Unlock

### Prueba 2 — Report con includes en transporte activo
Objetos: `ZREPORT_PRUEBA`, `ZREPORT_PRUEBA_TOP`, `ZREPORT_PRUEBA_F01` en orden `S4DK928673`

Mismos pasos: lock → write → activate → unlock para los tres.

---

## Qué confirma cada prueba

| Resultado | Interpretación |
|---|---|
| Write exitoso en ambas pruebas | Fix de bug #132 completo y correcto |
| 423 en clase / éxito en report | Fix incompleto en `workflows_source.go` (falta `writeLockHandle = ""`) |
| 423 en ambas | Fix en `crud.go` no funciona — revisar lógica |

---

## Herramienta vsp en esta sesión

El server vsp está registrado como `mcp__abap-adt__SAP`. Para usarlo hay que cargar
el schema con `ToolSearch` primero:

```
ToolSearch query: "select:mcp__abap-adt__SAP"
```

Después las operaciones típicas siguen el workflow ADT estándar (lock, write, activate, unlock).

---

## Referencias

| Recurso | |
|---|---|
| Issue #132 | https://github.com/oisee/vibing-steampunk/issues/132 |
| Context anterior (mcp-abap test) | `contexts/2026-05-26-002-seed-mcp-abap-adt-api-test.md` |
| Context fix original | `contexts/2026-05-26-001-seed-lock-nomodification-incl-bugs.md` |
