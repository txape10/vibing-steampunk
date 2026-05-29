# Sesión 2026-05-28 — Implementación cache de source (punto 5)

## Estado al cierre

**Rama activa:** `fix/lock-nomodification-with-transport` en `txape10/vibing-steampunk`  
**Binario:** `C:\Users\devuser\AppData\Local\VSP\vsp.exe` — commit `8b65774` — **EN PRODUCCIÓN**  
**Todos los tests `pkg/adt` verdes** ✅

---

## Hecho en esta sesión

### Cache de source implementado — commit `8b65774`

**Ficheros nuevos/modificados:**
- `pkg/adt/source_cache.go` — `SourceCache` struct + `cacheNameFromURL` helper
- `pkg/adt/source_cache_test.go` — 5 tests (HitMiss, TTL, InvalidateByName, InvalidateByURL, cacheNameFromURL)
- `pkg/adt/client.go` — campo `sourceCache *SourceCache` en `Client`
- `pkg/adt/workflows_source.go` — `GetSource` → check cache; refactored a `getSourceUncached` + `WriteSource` invalida en éxito
- `pkg/adt/workflows_edit.go` — `EditSourceWithOptions` invalida tras unlock

**Diseño:**
- Clave: `TYPE:NAME:METHOD:INCLUDE:PARENT`
- TTL: 10 minutos (constante `sourceCacheTTL`)
- Write-invalidation: `InvalidateByURL(objectURL)` tras edit, `InvalidateByName(name)` tras write
- `cacheNameFromURL` maneja URLs de class includes (`/oo/classes/{name}/includes/type` → `NAME`)
- El cache está al nivel de `GetSource` — `EditSourceWithOptions` lee SIEMPRE desde SAP (bypass implícito: usa `transport.Request` directamente, no `GetSource`)

---

## Mediciones comparativas (6 clases de la sesión 007)

### Corrección al análisis previo

La predicción de la sesión 007 ("462K tokens → 2K tokens") era **incorrecta**. El cache devuelve la fuente completa en cada llamada. **No hay ahorro de tokens por lectura MCP.**

### Beneficios reales confirmados

| Beneficio | Evidencia |
|---|---|
| Contenido idéntico cold/warm | ZCL_EXPEDICION: 163.877 bytes × 2; ZCL_EM: 369.851 bytes × 2 |
| Deps resueltas desde cache | ZCL_ALV: 11 deps ✅, ZCL_ABAP_UTILITIES: 6 deps ✅ — mismo contenido warm |
| Latencia: 0 HTTP calls a SAP en warm | Reads paralelos de ZCL_EXPEDICION + ZCL_EM en ~1s total |
| Carga SAP backend | 0 HTTP calls en warm reads |

### Lo que el cache NO mejora

- Tokens por llamada MCP: idénticos (misma respuesta)
- `EditSourceWithOptions` interno: siempre lee desde SAP (correcto por diseño — evita edit sobre fuente obsoleta)
- ctxcomp parsing: sigue corriendo en cada read (solo los GetSource internos se cachean)

---

## Pendiente

### Punto 6 — Compress deps en hyperfocused (pendiente análisis)

Del contexto 007:
> `ZCL_TST_STOCK_PARTIDAS`: source=10.449c, context=11.679c → deps solo 1.230c (11.8%)
> Para `ZCL_EM` (369K chars) casi todo será fuente propia — los deps comprimidos ya son pequeños.

Evaluar si vale la pena vs el coste de implementación. Probable conclusión: no.

### Upstream

- PR #108 ya cherry-pickeado. Upstream `oisee` inactivo desde ~47 días.
- PRs pendientes de proponer: `ignore_warnings` global, URL case-normalization, cache.
