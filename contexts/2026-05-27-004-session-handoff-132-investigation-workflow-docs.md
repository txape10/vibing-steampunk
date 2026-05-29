# 2026-05-27 Handoff — Bug #132 investigation + dual-server workflow docs

Sesión de investigación profunda del bug #132 (no resuelta) + creación del sistema de
documentación de workflow para los dos servidores MCP SAP.

---

## 1. Bug #132 — Estado actual (guard aplicado, PUT sigue fallando)

### Qué hay en la rama / working tree

**`pkg/adt/crud.go` — `LockObject`:**
```go
if accessMode == "MODIFY" &&
    strings.EqualFold(result.ModificationSupport, "NoModification") {
    if result.CorrNr == "" {
        // Genuinamente read-only (sin transporte) — rechazar (issue #91).
        return nil, fmt.Errorf("object %s is not modifiable via ADT on this system ...")
    }
    // Objeto en orden activa del usuario (issue #132).
    // SAP devuelve NoModification+corrNr — el handle es pseudo/informacional, NO válido para PUT.
    // Causa raíz no resuelta; ver reports/2026-05-27-004-issue-132-investigation.md
    // Workaround: usar mcp-abap-abap-adt-api para objetos con transporte.
}
```

**`pkg/adt/crud_reconcile_test.go`:**
- Test `TestLockObject_AllowsNoModificationWithExistingTransport` añadido y correcto.

**`pkg/adt/workflows_edit.go` y `workflows_source.go`:**
- Transport fallback: `if writeTransport == "" && lockResult.CorrNr != "" { writeTransport = lockResult.CorrNr }`
- Eliminados: `isTransportOwned`, `LockDebug`, debug file write (`~/vsp_lock_debug.txt`)
- Firmas revertidas a simple (sin `stateful ...bool` variadic)
- El bloque de re-lock (segundo POST a `/lock?corrNr=...`) fue probado y eliminado

**Estado del working tree:** Uncommitted. Los cambios son correctos y listos para commit.

### Por qué sigue fallando

SAP devuelve `NoModification + corrNr` para objetos en transporte activo. El handle que
entrega NO es un ENQUEUE lock real — SAP no creó entrada en la tabla ENQUEUE. Cualquier
`PUT .../source/main?lockHandle=<ese handle>` falla con `423 ExceptionResourceInvalidLockHandle`.

Tres enfoques probados, todos con 423:
1. Handle tal cual → rechazado (no en tabla ENQUEUE)
2. Re-lock con `POST .../lock?corrNr=ORDEN` → nuevo handle, también rechazado
3. Omitir handle, pasar solo corrNr → `423 "invalid lock handle: (empty)"`

### Por qué mcp-abap-abap-adt-api funciona

El wrapper Node.js (`abap-adt-api`) ignora `modificationSupport` y usa el handle tal cual.
Resultado: el write funciona en el mismo sistema. Explicación más probable:

**Hipótesis session affinity:** SAP crea el ENQUEUE lock ligado a la sesión HTTP que hizo
el POST de lock. Si el PUT subsiguiente llega con una sesión diferente (distinto
`sap-contextid` cookie), SAP no encuentra el lock. vsp podría no estar reutilizando la
misma sesión entre lock y write.

**Fichero a investigar:** `pkg/adt/http.go` — behavior del cookie jar entre requests
stateful, y si el `sap-contextid` del lock response se persiste para el write request.

### Siguiente paso concreto

Interceptar tráfico HTTP de `mcp-abap-abap-adt-api` con mitmproxy:
- mitmproxy instalado en `C:\Users\devuser\AppData\Local\Programs\Python\Python313\Scripts\`
- Capturar: headers del lock request (esp. `X-sap-adt-sessiontype`), cookies en lock response,
  cookies en write request — ¿son las mismas `sap-contextid`?
- Comparar con vsp (activar logging de HTTP en `pkg/adt/http.go`)
- Si difieren: fix en `pkg/adt/http.go` cookie jar

---

## 2. Bug #133 — Dos failure modes documentados, fix pendiente

### Failure mode 1 — Lock en PROG URL, PUT rechazado (423)
```
URL usada: /sap/bc/adt/programs/programs/ZREPORT_PRUEBA
Lock: OK
PUT: 423 ExceptionResourceInvalidLockHandle — Resource INCLUDE ZREPORT_PRUEBA is not locked
```
vsp bloquea el contenedor PROG pero SAP requiere que el write apunte al componente REPS.
El scope del lock no cubre la fuente del include.

### Failure mode 2 — REPS URL, package resolution falla antes de SAP
```
URL usada: /sap/bc/adt/programs/includes/ZREPORT_PRUEBA_TOP
Error: package metadata not found for /sap/bc/adt/programs/includes/zreport_prueba_top
```
vsp falla internamente antes de llamar a SAP: la lógica de resolución de paquete en el
mutation policy gate (`checkMutation`) no maneja el subpath `/programs/includes/`.

### Fix pendiente
Añadir soporte para `/programs/includes/` en el bloque de resolución de paquete del
mutation gate. Fichero: buscar `checkMutation` o `package metadata not found` en `pkg/adt/`.

---

## 3. Documentación dual-server creada

### `~/.claude/sap-mcp-servers.md` (global, se carga siempre)
Fichero nuevo con:
- Tabla comparativa vsp vs mcp-abap-abap-adt-api
- Regla general: lee con vsp, escribe con mcp-abap cuando hay transporte activo
- Workflow Caso A (4 llamadas: lock+write+unlock+activate con mcp-abap)
- Workflow Caso B (5 llamadas: vsp read + Caso A)
- Workflow Caso C (1 llamada: vsp EDITSOURCE para $TMP)
- URIs por tipo de objeto (CLAS, PROG, INTF, FUGR, TABL)
- Coste en tokens por operación
- Referencia rápida de tools de ambos servidores
- Notas sobre ORDER vs TASK (siempre usar ORDER desde transportInfo → TRKORR)

### `~/.claude/CLAUDE.md` (global)
- Sección `## Servidores MCP para SAP` actualizada a `@~/.claude/sap-mcp-servers.md`

### `CLAUDE.md` del proyecto
- `Current Priorities` actualizada con estado real de #132, #133, #45/#46
- Sección verbosa de mcp-abap eliminada (reemplazada por puntero al global)

---

## 4. Textos para actualizar en GitHub

Listos en formato Markdown para copy-paste:
- `docs/github-issue-132.md` — texto completo de issue #132 con findings de investigación
- `docs/github-issue-133.md` — texto completo de issue #133 con dos failure modes

---

## 5. Investigación de forks

Ningún fork existente resuelve #132 o #133:
- `BurnerPat/vsp-enterprise` — fork más activo (UI + enterprise features), sin trabajo en transport lock
- `txape10/vibing-steampunk` — nuestro propio fork, con la rama `fix/lock-nomodification-with-transport`
- Los demás son mirrors inactivos

**Monitor:** `BurnerPat/vsp-enterprise` — si resuelven transport lock, portear el fix.

---

## 6. Issues #45 y #46

**NO están cerrados ni son "no aplica"**. Son mejoras pendientes al sync script:
- **#45** — Sync script: handle renamed objects (renombrados en SAP pero no en el repo)
- **#46** — Sync script: add `--dry-run` flag

Ambos son low effort y siguen válidos. Abordarlos cuando no haya bugs críticos activos.

---

## 7. Working tree — commits pendientes

| Fichero | Cambio | Estado |
|---|---|---|
| `pkg/adt/crud.go` | Guard `&& result.CorrNr == ""` + comentario issue #132 | Listo para commit |
| `pkg/adt/crud_reconcile_test.go` | Test `TestLockObject_AllowsNoModificationWithExistingTransport` | Listo para commit |
| `pkg/adt/workflows_edit.go` | Transport fallback, sin isTransportOwned/LockDebug | Listo para commit |
| `pkg/adt/workflows_source.go` | Transport fallback, sin debug/isTransportOwned | Listo para commit |
| `CLAUDE.md` | Priorities actualizadas, sección mcp-abap eliminada | Revisar antes de commit |

**Rama:** `fix/lock-nomodification-with-transport` (fork `txape10/vibing-steampunk`)

Antes de commit: escanear diff con `.local/scripts/check-identifiers.sh` para descartar
identificadores reales (usernames, hostnames, transport numbers reales).

---

## 8. Próxima sesión — opciones

**Opción A — Investigación mitmproxy (resolver #132 de raíz)**
1. Levantar mitmproxy: `mitmdump -p 8080 -w traffic.log`
2. Configurar Node.js para usar proxy: `NODE_EXTRA_CA_CERTS` + `http_proxy`
3. Ejecutar `mcp-abap-abap-adt-api lock → setObjectSource` en objeto con transporte
4. Capturar y analizar: ¿el `sap-contextid` cookie del lock response se reutiliza en el PUT?
5. Comparar con vsp haciendo lo mismo (activar logging en `pkg/adt/http.go`)
6. Si hay diferencia: fix en cookie jar de vsp

**Opción B — Fix #133 (más acotado)**
1. Buscar `checkMutation` o `package metadata not found` en `pkg/adt/`
2. Añadir handling para `/programs/includes/` URL subpath
3. Probar con includes de `ZREPORT_PRUEBA`

**Opción C — Commit + PR del trabajo actual**
1. `go test ./pkg/adt/...` para verificar tests pasan
2. Commit con mensaje descriptivo
3. Push al fork
4. PR a `oisee/vibing-steampunk`
