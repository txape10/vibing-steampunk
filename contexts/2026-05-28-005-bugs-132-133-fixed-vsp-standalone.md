# Session 2026-05-28-005 — Bugs #132 y #133 resueltos, vsp standalone

## Estado al cierre

Rama activa: `fix/lock-nomodification-with-transport` en `txape10/vibing-steampunk`  
Binario instalado: `C:\Users\devuser\AppData\Local\VSP\vsp.exe` (commit `1ffc9cc`)  
Config Claude Desktop: sin `SAP_SESSION_TYPE`, con `SAP_ENABLE_TRANSPORTS=true`

---

## Qué se hizo en esta sesión

### PR #126 — Search server-side type filter

Cherry-pick de los 2 commits de frd1201 + extensiones nuestras:

- `SearchObjectByType` con filtro `objectType` en el query ADT
- `CanonicalObjectType` movida a `pkg/adt/client.go` (exportada) — antes era función local en `cli.go`
- `SearchObjectByType` llama a `CanonicalObjectType` internamente → todos los callers reciben expansión de formas cortas (`CLAS→CLAS/OC`) sin esfuerzo adicional
- Handler MCP `handleSearchObject` actualizado para usar `SearchObjectByType` + acepta `type`/`objectType`/`max`
- Sin el fix MCP, `SAP(action="search", params={"type":"CLAS","max":5})` devolvía ~90 objetos mezclados

### PR #125 — Skip redundant mutation gate after lock

Cherry-pick del commit de Dominik Miescher. Fix arquitectural del bug #132:

- `mutationGateSkipKey` context key en `pkg/adt/mutation_gate.go`
- Helpers `withMutationGateAlreadyRan` / `mutationGateAlreadyRan`
- `checkMutation` hace short-circuit si la flag está set
- Todos los workflows outer marcan el contexto tras su propio gate:
  `EditSourceWithOptions`, `WriteProgram`, `WriteClass`, `CreateAndActivateProgram`,
  `CreateClassWithTests`, `CreateFromFile`, `UpdateFromFile`, `RenameObject`,
  `ExecuteABAP`, `writeSourceUpdate`, `writeSourceCreate`, `writeClassMethodUpdate`
- 3 tests de regresión: happy-path sin search entre Lock y PUT, flag mechanics, no leak across contexts

### Resultado del test sin `SAP_SESSION_TYPE=stateful`

- PROG (`ZREPORT_PRUEBA`): ✅
- PROG/I include (`ZREPORT_PRUEBA_TOP`): ✅
- CLAS con transporte (`ZCL_IDOC_VALE_SERVICE`): ✅
- `list_transports`, `get_transport`, `create_transport`, `delete_transport`: ✅

**Conclusión**: PR #125 es suficiente. `SAP_SESSION_TYPE=stateful` eliminado del config.

---

## Fixes propios (de sesiones anteriores, ya en la rama)

### Bug #133 — Includes de programa fallaban antes de llegar a SAP

Dos bugs con el mismo patrón (check `/includes/` demasiado amplio):

1. `normalizeObjectURLForPackageCheck` en `client.go` — truncaba `/programs/includes/NAME` a `/programs`
2. `isClassInclude` en `workflows_edit.go` — disparaba en cualquier URL con `/includes/`, incluyendo program includes → skip de `/source/main` + Accept header incorrecto (406)
3. `SyntaxCheck artifactURI` en `devtools.go` — mismo patrón

Fix: añadir condición `strings.Contains(objectURL, "/oo/classes/")` a los tres sitios.

### Bug #132 — 423 ExceptionResourceInvalidLockHandle

Root cause: el gate interior de `UpdateSource` hacía `getObjectPackage → SearchObject` (hop stateless) entre el Lock stateful y el PUT stateful. SAP ICM retiraba la sesión → lock handle inválido.

También incluido:
- CSRF HEAD→GET fallback (`http.go`) con guard 401/403 (cherry-pick PR #120 + bugfix)
- `corrNr` adoption del lock result en `EditSourceWithOptions` y `writeClassMethodUpdate`
- `NoModification+CorrNr` guard en `crud.go` (permite objetos ya en transporte)

---

## Lectura de includes con vsp

**Usar tipo `INCL`, no `PROG`:**
```
SAP(action="read", target="INCL ZREPORT_PRUEBA_TOP")   ✅
SAP(action="read", target="PROG ZREPORT_PRUEBA_TOP")   ❌ 404
```
La URL es `/sap/bc/adt/programs/includes/<nombre>`, no `/programs/programs/`.

---

## Config Claude Desktop al cierre

```json
"abap-adt": {
  "command": "vsp",
  "env": {
    "SAP_URL": "https://...",
    "SAP_USER": "...",
    "SAP_PASSWORD": "...",
    "SAP_CLIENT": "100",
    "SAP_ALLOW_TRANSPORTABLE_EDITS": "true",
    "SAP_ALLOWED_PACKAGES": "Z*,$TMP",
    "SAP_ALLOWED_TRANSPORTS": "S4DK*",
    "SAP_ENABLE_TRANSPORTS": "true",
    "SAP_MODE": "hyperfocused"
  }
}
```

---

## Estado de PRs upstream

| PR | Estado | Nuestras aportaciones |
|---|---|---|
| #120 (CSRF fallback) | Abierto | Fix 401/403 + corrNr adoption |
| #121 (INCL write) | Abierto (Draft) | Test contribution aceptada |
| #125 (mutation gate skip) | Abierto | Cherry-picked, verificado |
| #126 (search type filter) | Abierto | MCP handler + CanonicalObjectType |

Nuestra rama está en standby. Cuando el mantenedor merge #120/#125/#126, rebase y PR limpio solo con nuestro diff.

---

## Próximo paso si aparece un bug

1. Leer este contexto
2. Revisar `git log --oneline -10` en la rama para ver el estado exacto
3. Comprobar si hay nuevos PRs upstream que afecten al área del bug

## No hay tareas pendientes en este proyecto
