# PDCA

[English](README.md) · [Русский](README.ru.md) · [Français](README.fr.md)

PDCA es una habilidad para Claude Code y Codex que organiza cambios amplios en el código mediante el ciclo **Plan → Do → Check → Act** (planificar, hacer, comprobar y actuar). Describe el resultado que quieres conseguir. PDCA divide el trabajo en tareas, las implementa en commits de Git separados y comprueba el resultado según los criterios de aceptación. Si la revisión falla, PDCA envía el trabajo a corregir o replantea el plan.

PDCA resulta útil para cambios que se benefician de dependencias explícitas y una revisión independiente, como una funcionalidad que afecta a varios módulos, una migración o una refactorización con requisitos de compatibilidad. Si interrumpes una ejecución, puedes retomarla en una sesión nueva y consultar qué se hizo y qué decisiones tomaron los agentes.

## Requisitos

- Un shell de tipo Unix y Go 1.22 o posterior. El script `scripts/pdca` compila la CLI la primera vez que se usa y la vuelve a compilar cuando cambian los archivos fuente de Go.
- Git y un repositorio de destino con al menos un commit, un árbol de trabajo limpio y la identidad del autor de los commits configurada.
- La CLI de Beads (`bd`) disponible en `PATH`. Consulta la [guía de instalación de Beads](https://github.com/gastownhall/beads/blob/main/docs/getting-started/installation.md).
- Una sesión de Claude Code o Codex que pueda iniciar subagentes personalizados. Para usar `pdca once`, el ejecutable `claude` o `codex` debe estar en `PATH`.

## Instalación y uso

### Instala la habilidad

```sh
git clone https://github.com/funkyra/pdca.git
cd pdca
./scripts/pdca install
export PATH="$HOME/.local/bin:$PATH"
```

El instalador crea enlaces simbólicos a la habilidad en `~/.claude/skills/pdca` y `~/.agents/skills/pdca` y enlaza el script en `~/.local/bin/pdca`. Mantén el repositorio clonado en ese lugar para que los enlaces sigan funcionando. Si `~/.local/bin` ya está en `PATH`, puedes omitir la línea `export`. En caso contrario, añádela a la configuración de tu shell para usar `pdca` en terminales nuevas.

### Configura el repositorio que vas a modificar

Abre una sesión nueva de Claude Code o Codex en el **repositorio de destino** y usa los comandos del cliente correspondiente.

| Acción | Claude Code | Codex |
| --- | --- | --- |
| Configurar el repositorio | `/pdca init` | `$pdca init` |
| Iniciar una tarea | `/pdca <tarea>` | `$pdca <tarea>` |
| Reanudar una ejecución pendiente | `/pdca resume` | `$pdca resume` |

`init` inicializa Beads si hace falta, solicita el modelo y el nivel de esfuerzo de razonamiento para cada fase si aún no están configurados y crea los agentes dentro del proyecto, en `.claude/agents/` para Claude Code o en `.codex/agents/` para Codex. Revisa los cambios de configuración que indica la salida, guárdalos en un commit y abre una sesión nueva para cargar los agentes. El árbol de trabajo debe estar limpio antes de iniciar la primera ejecución.

También puedes configurar el repositorio de destino desde la terminal. Este ejemplo usa alias de modelos de Claude Code.

```sh
pdca init --harness claude \
  --models 'plan=opus/high do=sonnet/medium check=sonnet/high act=haiku/medium' \
  --review epic
```

### Asigna una tarea a PDCA

Aquí tienes un ejemplo de solicitud en una sesión de Codex.

```text
$pdca Añade un modo de simulación a la importación de CSV. Muestra cuántas filas se insertarían o se omitirían, conserva los datos almacenados y cubre ambos modos con pruebas.
```

Antes de empezar, PDCA comprueba Beads, el árbol de trabajo, los agentes y la configuración de los modelos. Si estás en una rama protegida como `main`, PDCA pasa a una rama `pdca/<nombre-corto-de-la-tarea>`. Si ya estás en una rama de desarrollo, continúa en esa rama. El informe final incluye la rama, el número de commits, las tareas de seguimiento y las decisiones y supuestos que registraron los agentes. Los cambios quedan en la rama local para que puedas revisarlos.

### Inicia una ejecución puntual desde la terminal

Una vez instalada la habilidad, ejecuta uno de estos comandos desde el repositorio de destino para iniciar una sesión con la tarea.

```sh
pdca once --harness claude 'Añade un modo de simulación a la importación de CSV'
pdca once --harness codex 'Añade un modo de simulación a la importación de CSV'
```

`once` inicializa Beads si es necesario y abre el cliente elegido con la tarea. En Claude Code, pasa los agentes de cada fase directamente a la sesión. En Codex, los instala en `.codex/agents/` dentro del proyecto. La ejecución también requiere los modelos de cada fase. Escribe los argumentos adicionales del cliente después de `--`.

## Cómo funciona

La sesión principal inicia un agente nuevo para cada fase. La CLI escrita en Go verifica el resultado de la fase e indica a la sesión principal qué agente iniciar a continuación. Las instrucciones de Plan y Check recurren a las herramientas de grafos de Codebase Memory para explorar el código del repositorio de destino.

| Fase | Qué hace |
| --- | --- |
| **Plan** | Convierte la solicitud en una épica de Beads que reúne el diseño, los criterios de aceptación, las subtareas y sus dependencias. Registra los supuestos y las decisiones de diseño en la épica. |
| **Do** | Implementa y prueba una tarea. Cada ronda de implementación o corrección que se completa produce un commit con la línea `Bead: <task-id>` al final del mensaje. |
| **Check** | Revisa el diff, ejecuta las pruebas y comprueba el resultado según los criterios de aceptación. Registra un veredicto `PASS` o `FAIL` y comprueba que el resultado integrado cumple los criterios de la épica. |
| **Act** | Analiza los hallazgos después de un `FAIL`. Puede cerrar el trabajo, devolver una corrección concreta a Do, pedir que se revise el plan, crear tareas de seguimiento o solicitar una decisión al usuario. |

Beads guarda la épica, las dependencias, las referencias a commits, las revisiones y las decisiones. Git conserva los cambios de código. El directorio `.pdca/`, que Git ignora, contiene el estado local, incluida la fase pendiente. Al reanudar una ejecución, PDCA usa ese estado, Beads y Git para comprobar dónde quedó el trabajo y determinar la fase siguiente.

## Coste frente a una sola sesión

Medido en noviembre de 2026 con Claude Code 2.1.332. La tarea consiste en añadir transacciones recurrentes y presupuestos mensuales a una app de Python de unas 2.3k líneas; el resultado cambió alrededor de 1k líneas. PDCA usó la sesión principal en Sonnet/medium, Plan y Check en Opus y Do en Sonnet. La sesión única fue Opus/high con la misma petición y sin la skill.

| | PDCA (2 ejecuciones) | Sesión única (1 ejecución) |
| --- | --- | --- |
| Coste | $1.41–1.43 | $1.57 |
| Tokens | 1.04–1.09M | 1.31M |
| Tiempo | 374–375 s | 347 s |
| La funcionalidad funciona (todos los tests y comprobaciones de extremo a extremo) | sí | sí |
| Commits | uno por tarea, en una rama aparte | ninguno: todo quedó sin confirmar en `main` |
| Plan, criterios de aceptación, revisión y decisiones registrados | sí, en Beads | no |

A este tamaño, un modelo potente por sí solo también implementa bien la funcionalidad. Con PDCA el mismo resultado cuesta un 10% menos, tarda un tiempo similar y llega como commits revisados por tarea con las decisiones registradas. La variación entre ejecuciones es de un 5–10%.

## Configuración

PDCA lee el primer archivo `AGENTS.md` que contiene una sección `PDCA models` para el cliente elegido. Busca desde la raíz del repositorio hacia los directorios superiores y, por defecto, termina consultando `~/.codex/AGENTS.md`. `pdca init --models` escribe la sección en el archivo `AGENTS.md` del repositorio. Si no hay ninguna sección que coincida, `pdca models --set` permite guardar la configuración localmente en `.pdca/`.

```markdown
## PDCA models: claude
- plan: opus high
- do: sonnet medium
- check: sonnet high
- act: haiku medium
- review: epic
- parallel: no
```

Para Codex, usa `## PDCA models: codex` con los identificadores de los modelos y los niveles de esfuerzo de razonamiento que admite tu sesión. Las cuatro líneas de las fases son obligatorias. Puedes configurar `do_L` y `check_L` para las tareas de tamaño `L`, `epic_check` y `epic_act` para las fases que abarcan toda la épica, y `merge` para la integración después de un lote en paralelo. Por defecto, `merge` usa la configuración de `check`.

| Opción | Comportamiento |
| --- | --- |
| `review: epic` (por defecto) | Check revisa toda la épica una vez creados los commits de sus tareas. Act puede añadir tareas de corrección si la revisión falla. |
| `review: task` | Check revisa el commit de cada tarea. Durante la revisión de la última tarea, Check también verifica el resultado integrado de la épica. |
| `parallel: no` (por defecto) | Do ejecuta las tareas de una en una. |
| `parallel: yes` | Cuando hay varias tareas listas, PDCA inicia hasta ocho a la vez en árboles de trabajo de Git separados a partir del mismo commit. Las dependencias determinan qué tareas pueden ejecutarse en el mismo lote. Antes de Check, un agente Merge integra los commits en la rama de trabajo, resuelve los conflictos y elimina el código duplicado. |

## Consulta o reanuda una ejecución

Ejecuta estos comandos desde el repositorio de destino.

| Comando | Qué muestra o hace |
| --- | --- |
| `pdca models --harness codex` | Muestra los modelos que se usan, los niveles de esfuerzo de razonamiento, el modo de revisión y el origen de la configuración de Codex. Usa `claude` para Claude Code. |
| `pdca current` | Localiza la épica abierta de la rama actual. |
| `pdca status <epic-id>` | Muestra el estado de las tareas, las referencias a commits y los veredictos de las revisiones. |
| `pdca brief <epic-or-task-id>` | Muestra el plan guardado o los detalles de una tarea, incluidos los criterios de aceptación. |
| `pdca report <epic-id>` | Muestra la rama, el número de commits, las tareas de seguimiento y las decisiones registradas. Ejecuta el comando en la rama de la épica para obtener el número correcto de commits. |
| `pdca next [<epic-id>]` | Verifica la fase pendiente e indica qué fase ejecutar a continuación. Para reanudar el trabajo desde el cliente, usa `/pdca resume` o `$pdca resume`. |

- `FAIL no-beads` indica que la comprobación inicial no pudo encontrar un espacio de trabajo de Beads. Comprueba que `bd` está en `PATH` y ejecuta el comando `init` del cliente.
- `FAIL dirty-tree` enumera los cambios locales. Guárdalos en un commit o apártalos temporalmente antes de volver a intentarlo.
- `FAIL agents-stale` muestra el comando para actualizar las definiciones de los agentes. Ejecútalo y abre una sesión nueva.
- `FAIL no-models` indica que faltan los modelos de las fases. Configúralos en `AGENTS.md` o usa el comando `models --set` que indica la comprobación inicial.
- `STUCK` indica que quedan tareas abiertas con dependencias pendientes de resolver. `ESCALATE` solicita una decisión antes de continuar el trabajo.

## Estructura del repositorio y pruebas

| Ruta | Contenido |
| --- | --- |
| [`SKILL.md`](SKILL.md) | Instrucciones para la sesión principal que coordina la ejecución. |
| [`phases/`](phases/) | Instrucciones de configuración, de trabajo de los agentes de cada fase y de planificación en paralelo. |
| [`scripts/pdca`](scripts/pdca) | Script de shell que compila y ejecuta la CLI. |
| [`scripts/src/`](scripts/src/) | CLI en Go, comprobaciones del flujo de trabajo, integración con Beads y Git, y pruebas. |
| [`evals/evals.json`](evals/evals.json) | Escenarios de evaluación de la habilidad y resultados esperados. |

Ejecuta las pruebas de Go desde el repositorio clonado de PDCA.

```sh
cd scripts/src
go test ./...
```

Las pruebas del flujo de trabajo que usan Beads se omiten si `bd` no está disponible.

## Licencia

[Mozilla Public License 2.0](LICENSE).
