# Cohert

Agent Runtime de línea de comandos, local-first, para llamadas a herramientas, automatización del navegador, percepción del escritorio, contexto largo, SOP y memoria verificada.

**Idiomas:** [简体中文](../../README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · **Español** · [Français](README.fr.md) · [हिन्दी](README.hi.md)

## Qué Es Cohert

Cohert es un Agent Runtime local escrito en Go. Conecta un LLM compatible con OpenAI, una capa de herramientas controlada, sesiones persistentes, automatización del navegador, Computer Use de escritorio en macOS, compactación de contexto, enrutamiento SOP y memoria de largo plazo verificada.

```text
Intención del usuario
  -> Agent Loop
  -> Context Manager
  -> LLM tool calling
  -> Herramientas locales / navegador / escritorio / shell
  -> Registro de evidencia
  -> Historial de sesión y memoria verificada
```

La regla principal: el modelo razona, pero la ejecución debe ser explícita, auditable, recuperable y respaldada por evidencia.

## Inicio Rápido

```bash
git clone <repo-url>
cd Cohort
export DEEPSEEK_API_KEY="sk-xxx"
go run .
```

Ejecutar una tarea:

```bash
go run . ask "lee README.md y resume las capacidades actuales del runtime"
```

Inspeccionar el runtime:

```bash
go run . config
go run . tools
go run . session list
```

Compilar:

```bash
go build -o cohert ./cmd/cohert
./cohert
```

La configuración predeterminada está en [`configs/config.yaml`](../../configs/config.yaml). La guía completa está en [`docs/usage.md`](../usage.md).

## Capacidades

| Área | Capacidad |
| --- | --- |
| Agent Loop | Chat streaming compatible con OpenAI, tool calling, control de turnos |
| Herramientas locales | Lectura/escritura/patch de archivos, shell, preguntas al usuario, errores estructurados |
| Navegador | Chrome bridge, escaneo de página, JS, snapshot de elementos, click/type/key/wait/screenshot/OCR |
| Desktop Computer Use | Permisos macOS, ventanas, activación por PID, capturas, árbol AX, OCR de escritorio, `AXPress` controlado |
| Sesiones | `history.jsonl`, metadatos, lista, reanudación, auditoría local |
| Context Manager | Compactación de resultados, recorte seguro, session memory, full compact |
| SOP Runtime | Índice SOP, pistas por tarea, working checkpoint |
| Evolution Memory | Entradas con evidencia, memoria de proyecto, deduplicación, read-back, audit |

## CLI

```bash
cohert                         # modo interactivo
cohert ask "task"              # ejecutar una tarea y salir
cohert tools                   # listar herramientas
cohert config                  # ver configuración efectiva
cohert session list            # listar sesiones locales
cohert session resume <id>     # reanudar una sesión
```

Comandos interactivos:

```text
/help
/model
/config
/tools
/session
/session list
/resume <session_id>
/compact
/full-compact
/memory
/exit
```

## Automatización Del Navegador

Cohert controla Chrome mediante un Browser Bridge local:

```text
ws://127.0.0.1:18777/browser
```

Flujo recomendado:

```text
open -> wait -> snapshot -> click/type/key -> wait -> verify
```

Use `browser_ocr` solo cuando el texto DOM y `browser_dom_summary` no puedan leer texto renderizado. Las cajas OCR son `screenshot-local` y no deben tratarse como coordenadas del mouse del sistema.

Dependencias opcionales de OCR:

```bash
python3 -m pip install rapidocr-onnxruntime pillow numpy
```

Si aparece `browser_not_connected`, cargue la extensión de Chrome desde `assert/cohert_browser_bridge`.

## Desktop Computer Use

Cohert ofrece percepción genérica del escritorio en macOS y acciones semánticas AX controladas:

```text
desktop_permissions
  -> desktop_windows
  -> desktop_activate
  -> desktop_ax_snapshot
  -> desktop_screenshot
  -> desktop_ocr
  -> desktop_ax_press
```

Accessibility / AX tiene prioridad sobre OCR. `desktop_ax_press` es la única acción de entrada de escritorio por ahora. Requiere PID en primer plano, metadata AX reciente, revalidación antes de actuar y verificación AX después.

Política de riesgo:

- R1 acciones reversibles pueden ejecutarse directamente.
- R2 efectos externos requieren token de confirmación de un solo uso emitido por `ask_user`.
- R3 acciones de alto riesgo como pago, aprobación, autorización, verificación de login o eliminación se rechazan para ejecución manual.

Todavía no hay click por coordenadas, teclado ni entrada de texto de escritorio.

Dependencias del helper macOS:

```bash
python3 -m pip install pyobjc-framework-Quartz pyobjc-framework-Cocoa pyobjc-framework-ApplicationServices
```

Otorgue permisos de Accessibility y Screen Recording al terminal o IDE que ejecuta Cohert.

## Memoria Y SOP

La memoria de largo plazo usa un flujo estricto de tres pasos:

```text
start_long_term_update
  -> memory_propose_update
  -> memory_apply_update
```

Las escrituras deben referenciar evidencia verificada, rechazar contenido sensible o duplicado y hacer read-back antes de confirmar.

Los SOP son restricciones operativas ligeras. Cohert inyecta [`sops/index.md`](../../sops/index.md) como navegación y pide leer el SOP relevante antes de actuar.

## Desarrollo

```bash
go test ./...
go vet ./...
go run . tools
go run . config
```

## Principios

- Local first.
- Herramientas auditables.
- Historial inmutable.
- Contexto por capas.
- Memoria verificada.
- Evolución progresiva.
