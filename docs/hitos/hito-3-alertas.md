# Hito 3 — Alertas

- [x] **Hito 3 completado** — cerrado el 2026-11-07; marcar solo cuando todos los entregables y criterios de cierre estén verificados.

> **Pendiente de validación real:** `PublishZeros` sigue en `false`, así que la rama de racha larga de `no_data` está implementada y probada de forma unitaria pero inactiva hasta confirmar si OCI publica ceros. El cableado de los evaluadores dentro de `run`, la cola y el envío pertenecen al Hito 4.

> **Archivo autocontenido.** Incluye el contexto general y las secciones 0, 4.1 y 5 del roadmap. No necesitas abrir `Roadmap.md` para ejecutar este hito.
> **Depende de:** [Hito 2](hito-2-persistencia-acumulacion.md). **Habilita:** [Hito 4](hito-4-telegram-orquestacion.md).
> Índice: [`../index.md`](../index.md) · Progreso: [`../progress.md`](../progress.md) · Decisiones: [`../decisions.md`](../decisions.md).

## 1. Rol y contexto de trabajo

Actúa como ingeniero de software senior especializado en Go, Oracle Cloud Infrastructure (OCI), Linux, systemd, observabilidad y seguridad.

**Nombre del proyecto:** `oci-infrastructure-monitor`

**Objetivo de la v1:** monitorear el tráfico de red saliente de una instancia OCI, acumular su consumo mensual y enviar alertas preventivas por Telegram.

**Objetivo a largo plazo:** poder añadir monitoreo de CPU, RAM, disco, servicios Linux, respaldos y almacenamiento OCI sin reestructurar el proyecto. No implementes esos módulos ahora ni crees paquetes vacíos para ellos.

### 1.1. Punto de partida

- **El proyecto ya está inicializado por mí** (repositorio y `go.mod`). No ejecutes `go mod init`.
- **Usa la versión de Go instalada en esta máquina.** Ejecuta `go version` al comenzar. No cambies la directiva `go` ni `toolchain` de `go.mod`, ni instales otra versión. Si alguna dependencia exige una versión superior, detente y avísame.
- Antes de escribir código, revisa el estado actual del repositorio (`ls`, `git log`, `go.mod`, `docs/progress.md` si existe) y continúa desde ahí.
- Este hito asume que el Hito 2 dejó los buckets horarios, el acumulado mensual, el estado persistido y el comando `status` funcionando. Revisa `docs/progress.md` para confirmar el estado real.

### 1.2. Qué puedes validar y qué no

No tienes acceso a OCI, a Telegram ni al servidor de producción, y no debes conectarte por SSH ni modificarlo. La instalación la hará el responsable del proyecto manualmente.

| Lo valida el agente | Lo valida el usuario después del despliegue |
|---|---|
| Compilación, `go vet`, `go test`, `-race` | `check` contra OCI real |
| Lógica con mocks y servidores HTTP de prueba | Pertenencia de la instancia al Dynamic Group |
| Formato de mensajes, persistencia, recuperación | Envío real a Telegram |
| Sintaxis de unidades systemd (si hay herramienta) | Ejecución real del timer |

Nunca afirmes que una integración real funciona. Distingue siempre entre "probado con mocks" y "pendiente de validación real".

### 1.3. Reglas de trabajo

1. **No inventes** capacidades de APIs, unidades de métricas ni resultados de pruebas. Verifica en la documentación oficial.
2. **Detente y pregunta** únicamente si: no puedes determinar la semántica o unidad de una métrica, una decisión cambiaría el diseño de forma significativa, o hay un conflicto entre requisitos. Las decisiones técnicas menores las tomas tú.
3. **Registra cada decisión relevante** en `docs/decisions.md` (fecha, decisión, motivo, alternativas descartadas).
4. **Cada hito termina** con `go build ./...`, `go vet ./...` y `go test ./...` en verde, y con `docs/progress.md` actualizado.
5. No avances de hito con errores críticos sin resolver.
6. Dependencias: SDK oficial de OCI para Go (justificado) y librería estándar para todo lo demás. Cualquier otra dependencia requiere justificación en `decisions.md`.

## 2. Datos disponibles (sección 4.1)

El evaluador no consulta OCI: trabaja sobre los buckets horarios persistidos por el Hito 2.

- La unidad de almacenamiento es el **bucket horario UTC**, identificado por la clave `(recurso OCID, dirección, hora UTC)`.
- Cada bucket guarda bytes, marca de tiempo de captura y estado `complete`, `provisional`, `no_data` o `incomplete`.
- `no_data` cuenta 0 y no invalida por sí solo; una VNIC nunca visible es `incomplete` y cuenta para el error persistente.
- **El acumulado mensual se calcula sumando los buckets del mes**; no es un contador incremental.
- Los buckets horarios no cruzan el cambio de mes, así que el reinicio mensual se resuelve por construcción.
- Un mes se cierra en la primera consulta exitosa que cubra hasta el fin de mes y cumpla `ahora >= fin de mes + requery_window`. Las alertas del mes anterior no se reevalúan ni se reenvían una vez cerrado el mes; una notificación ya pendiente sí se envía.

El estado persistido (`state.json`) guarda, entre otras cosas, las alertas ya enviadas y las notificaciones pendientes. El evaluador debe leer y proponer cambios de estado, pero **no enviar** por Telegram: el envío es responsabilidad del Hito 4.

## 3. Especificación de alertas (sección 5)

### 3.1. Umbrales mensuales

| Consumo | Severidad |
|---|---|
| 50% | Informativa |
| 75% | Advertencia |
| 90% | Crítica |
| 100% | Límite alcanzado |

- Hay **cuatro niveles fijos**: informativa, advertencia, crítica y límite alcanzado. El campo `alert_percentages` configura exactamente 4 porcentajes, estrictamente crecientes, el primero mayor que 0 y el último igual a 100; la severidad se asigna por posición.
- El porcentaje se calcula sobre `monthly_limit_gib`, que es siempre GiB (1 GiB = 2^30 bytes). No se admiten GB decimales.
- La comparación se hace con bytes exactos. El porcentaje mostrado se trunca a un decimal, no se redondea: un 89,96 % no debe aparecer como “90 %” si no se disparó la alerta crítica.
- Si se cruzan varios umbrales entre ejecuciones, envía **una** alerta con el nivel máximo y registra todos los umbrales cruzados.
- No repitas la misma alerta dentro de un período mensual. Los estados de alerta se reinician al comenzar el mes.
- Si el acumulado es incompleto, la alerta lo indica explícitamente.
- Las alertas del mes anterior no se reevalúan ni se reenvían una vez cerrado el mes.

### 3.2. Consumo anormal

- Alerta independiente sobre la suma de bytes salientes de las **últimas 6 horas completas** (usando los buckets), contra un umbral configurable en GiB.
- Enfriamiento configurable para no repetir notificaciones.
- Si la ventana contiene buckets `incomplete` (incluida una VNIC nunca visible), `provisional` o una racha de `no_data` superior a `no_data_streak_hours` **y** OCI publica ceros explícitos, indícalo y no saques conclusiones indebidas (no dispares una alerta de "normalidad" ni de "anomalía" con datos insuficientes sin advertirlo). Un `no_data` aislado cuenta como 0 y no invalida la ventana por sí solo; si OCI omite los ceros, una racha larga es tráfico cero normal.
- No asumas un valor universal de consumo normal. Deja el diseño preparado para comparar luego contra una línea base histórica, sin implementarlo todavía.

## 4. Entregables

- [x] Evaluador de umbrales mensuales con los cuatro niveles de `alert_percentages`, severidad por posición, nivel máximo, registro de todos los umbrales cruzados y deduplicación por período mensual.
- [x] Evaluador de consumo anormal sobre las últimas 6 horas completas, con umbral configurable en GiB y enfriamiento.
- [x] Manejo explícito de datos `incomplete`/`provisional` y rachas largas de `no_data` cuando OCI publica ceros: la alerta lo indica y no se sacan conclusiones de normalidad o anomalía sin advertirlo.
- [x] Interfaz de evaluadores extensible, para añadir futuros recolectores y evaluadores sin reestructurar.
- [x] Tests de umbrales, múltiples umbrales cruzados, reinicio mensual, cierre de mes, consumo anormal, `no_data`, datos incompletos y enfriamiento.
- [x] `docs/progress.md` actualizado con resultados reales.

## 5. Criterio de cierre

- [x] Tests de umbrales, múltiples cruces en una ejecución, reinicio mensual, cierre de mes, anomalía de 6 h, `no_data`, datos incompletos y enfriamiento en verde.
- [x] Cruzar 50%, 75% y 90% entre dos ejecuciones produce una sola alerta con nivel máximo y registra los tres umbrales.
- [x] La severidad se asigna por posición de `alert_percentages` y el último valor es 100.
- [x] La comparación usa bytes exactos y el porcentaje mostrado se trunca a un decimal (89,96 % no se muestra como 90 %).
- [x] Una misma alerta no se repite dentro del mes y vuelve a estar disponible al cambiar de mes.
- [x] Las alertas del mes anterior no se reevalúan ni se reenvían tras el cierre.
- [x] Una ventana de 6 h con buckets `provisional`, `incomplete` o una racha larga de `no_data` (si OCI publica ceros) no dispara una conclusión silenciosa: el mensaje lo advierte.
- [x] Una VNIC nunca visible se trata como `incomplete`, no como `no_data`, y el acumulado se presenta incompleto.
- [x] `go build ./...`, `go vet ./...` y `go test ./...` ejecutados con resultado real reportado.
- [x] La casilla superior de este hito marcada solo después de todo lo anterior.

## 6. Evidencia y traspaso

```bash
go version
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Al cerrar, añade la entrada del Hito 3 en [`../progress.md`](../progress.md) con: qué se completó, qué quedó parcial, resultados reales, decisiones nuevas, supuestos pendientes de validación real y qué debe hacer la siguiente sesión.

## 7. Fuera de alcance de este hito

- Cliente de Telegram, reintentos, cola de pendientes y orquestación de `run` (Hito 4).
- Envío real de notificaciones.
- Service/timer systemd y documentación de despliegue (Hito 5).
- Línea base histórica de consumo: solo se deja el diseño preparado.

## 8. Siguiente hito

[Hito 4 — Telegram y orquestación](hito-4-telegram-orquestacion.md).
