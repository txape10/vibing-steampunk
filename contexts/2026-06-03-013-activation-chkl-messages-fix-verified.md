# Sesión 2026-06-03 — Fix real de activación: formato chkl:messages

## Contexto

Continuación de la sesión 012. Al desplegar el fix de namespace (`dedfebc`), la activación seguía devolviendo `success:true` con errores de sintaxis. Investigación con HTTP trace reveló la causa real.

## Causa raíz real del bug de activación (commit `b2f0564`)

El fix de namespace (`dedfebc`, sesión 012) era necesario pero NO suficiente en S/4HANA on-prem.

S/4HANA devuelve un formato XML completamente diferente al documentado:

```xml
<chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist">
  <chkl:properties checkExecuted="true" activationExecuted="false" generationExecuted="false"/>
  <msg objDescr="" type="W" line="0" href="">
    <shortText><txt>Activation was cancelled.</txt></shortText>
  </msg>
  <msg objDescr="Programa ZPROG_WITH_INCL" type="E" line="1"
       href="/sap/bc/adt/programs/programs/zprog_with_incl/source/main#start=2,8" forceSupported="true">
    <shortText><txt>INCLUDE report "ZRCG1_INCL" not found.</txt></shortText>
  </msg>
</chkl:messages>
```

El parser antiguo sólo ramificaba en `<activationLog>` → no encontraba nada → `success: true` siempre.

### Fix en `parseActivationResult` (`pkg/adt/devtools.go`)

Detección de formato + parser separado para cada uno:
- **Format A** (`<activationLog>`) — legacy, ya cubierto
- **Format B** (`<messages>` tras strip, raíz `chkl:messages`) — nuevo parser que lee `<properties activationExecuted="false"/>` y `<msg>` hijos directos

Fixes adicionales en Format B:
- `activationExecuted=false` en properties → `success=false` aunque no haya msgs tipo E
- `ShortText`: `Txts []string \`xml:"txt"\`` en lugar de `Text string` — SAP manda múltiples `<txt>`

### Tests añadidos

- `TestParseActivationResult_ChklMessages_DetectsFailure` — XML capturado en vivo del sistema SAP
- `TestParseActivationResult_ChklMessages_SuccessWhenActivated` — `activationExecuted=true` sin errores

## Verificación en producción

```
SAP(action="edit", target="ACTIVATE_MULTI", params={"objects": ["PROG ZPROG_WITH_INCL"]})
→ success:false, E: "INCLUDE report ZRCG1_INCL not found." (línea 1)

SAP(action="edit", target="ACTIVATE_MULTI", params={"objects": ["INCL ZREDI_CREATE_IDOC_FILE_CL1"]})
→ success:false, E: "IV_TITLE is not type-compatible with formal parameter I_GRIDTITLE" (línea 1107)
```

Ambos objetos devolvían `success:true, messages:[]` antes del fix.

## Issues comentados en upstream

- **#136** — comentario follow-up: el namespace fix era insuficiente; causa real es Format B chkl:messages; fix completo en commit `b2f0564`
- **#137** — verificado en producción con objetos reales
- **#116** — comentario con nuestra solución INCL (da499ff): causa raíz + fix + uso MCP

## Estado del fork

- Rama: `fix/lock-nomodification-with-transport` en `txape10/vibing-steampunk`
- Commit de esta sesión: `b2f0564` (chkl:messages fix)
- Todos los tests pasan
- Binario desplegado: `C:\Users\devuser\AppData\Local\VSP\vsp.exe`

## Error real del proyecto IDoc

El include `ZREDI_CREATE_IDOC_FILE_CL1` tiene un error en línea 1107:
```
"IV_TITLE" is not type-compatible with formal parameter "I_GRIDTITLE"
```
Esto es lo que había que corregir en el proyecto de vales para que el include pueda activarse.
El error era conocido pero silenciado por el bug de activación — ahora es visible.
