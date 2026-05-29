# Session 2026-05-28-006 — Fix $TMP + NoModification

## Estado al cierre

Rama activa: `fix/lock-nomodification-with-transport` en `txape10/vibing-steampunk`  
Binario instalado: `C:\Users\devuser\AppData\Local\VSP\vsp.exe` (commit `4d4adfc`)

---

## Qué se hizo en esta sesión

### Bug — objetos en $TMP bloqueados por el guard NoModification

**Síntoma:** al intentar editar cualquier clase del paquete `$TMP`, vsp devolvía:
> object X is not modifiable via ADT on this system (SAP returned modificationSupport="NoModification" during LOCK)

**Root cause:** El guard añadido en la sesión anterior (fix #132) en `LockObject` (`crud.go`) rechazaba `NoModification + corrNr=""` asumiendo "objeto read-only sin transporte". Pero los objetos `$TMP` también tienen `corrNr=""` — no necesitan orden de transporte. SAP devuelve `IS_LOCAL=X` en la respuesta del lock para indicar que el objeto es local, campo que ya estaba parseado en `LockResult.IsLocal` pero que el guard no usaba.

**Fix:** `pkg/adt/crud.go`, función `LockObject`:

```go
// Antes:
if result.CorrNr == "" {
    return nil, fmt.Errorf("object %s is not modifiable...")
}

// Después:
if result.CorrNr == "" && !result.IsLocal {
    return nil, fmt.Errorf("object %s is not modifiable...")
}
// Allow if IsLocal=true ($TMP) or CorrNr!="" (already in transport)
```

**Verificado en SAP real:** edición quirúrgica de `ZCL_TST_STOCK_PARTIDAS` (clase en `$TMP`) → lock ✅ → syntax check ✅ → write ✅ → activate ✅.

---

## Estado de la rama

```
4d4adfc fix(adt): allow NoModification lock for local ($TMP) objects
1ffc9cc docs(claude): mark bug #132 as fixed, update workaround notes
c40b1bf fix(adt): skip redundant mutation gate after lock to keep stateful session alive
...
```

---

## Cuándo comentar en issues upstream

El guard `NoModification+CorrNr` referencia `issue #91` en el código. Si ese issue sigue abierto en el repo upstream y el mantenedor está trabajando en esa área, vale la pena añadir un comentario explicando el caso `$TMP` (`IsLocal=true`). No es urgente — el fix está en nuestra rama y funciona.

---

## No hay tareas pendientes en este proyecto

Rama en standby hasta que upstream merge #120/#125/#126.
