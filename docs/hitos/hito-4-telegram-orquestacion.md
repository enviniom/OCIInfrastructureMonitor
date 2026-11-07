# Hito 4 — Telegram y orquestación

- [x] **Hito 4 completado** — marcar solo cuando todos los entregables y criterios de cierre estén verificados.

> **Archivo autocontenido.** Incluye el contexto general y las secciones 0, 6 y 7.2 del roadmap. No necesitas abrir `Roadmap.md` para ejecutar este hito.
> **Depende de:** [Hito 2](hito-2-persistencia-acumulacion.md) y [Hito 3](hito-3-alertas.md). **Habilita:** [Hito 5](hito-5-systemd-compilacion-documentacion.md).
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
- Este hito asume que el Hito 2 dejó la persistencia y el acumulado, y el Hito 3 los evaluadores de alertas. Revisa `docs/progress.md` para confirmar el estado real.

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

## 2. Especificación de Telegram (sección 6)

- API oficial por HTTPS con `net/http`. Sin frameworks de bots.
- Token y Chat ID en `/etc/oci-infrastructure-monitor/telegram.env` (permisos restrictivos). Nunca en el repositorio, en argumentos CLI, en logs ni en mensajes de error.
- Mensajes en **español**. Tipos: informativa, advertencia, crítica, límite alcanzado, consumo anormal, error persistente de recopilación, recuperación del funcionamiento normal, y mensaje de prueba.
- Todo mensaje incluye: instancia, región, período, consumo acumulado, límite configurado, porcentaje, fecha de evaluación, tipo de medición, severidad, y la nota de que es tráfico registrado, no facturable.
- Fiabilidad: timeouts, reintentos con backoff, tratamiento de HTTP 429 (`retry_after`), errores de red, **cola de notificaciones pendientes en `state.json`**.
- **Una alerta solo se marca como enviada tras respuesta exitosa de Telegram.**
- Documenta que Telegram no garantiza entrega exactamente una vez; puede haber duplicados raros tras un fallo entre el envío y la persistencia.

### 2.1. Estado y cola de pendientes

`state.json` ya existe desde el Hito 2 y contiene, entre otros campos:

- alertas enviadas;
- notificaciones pendientes (máximo 50, caducidad 7 días);
- última ejecución y última consulta exitosa;
- contador de fallos consecutivos de OCI y estado de la alerta de error;
- versión de esquema.

Este hito añade la lógica de cola: una notificación se encola antes del envío, se reintenta en ejecuciones posteriores y solo se elimina de la cola tras una respuesta exitosa de Telegram. `state.json` se persiste después de **cada** envío exitoso, no solo al final. La cola se vacía antes de evaluar alertas nuevas. Si se desborda o caduca, se descarta la más antigua y se registra; las alertas de umbral pendientes se colapsan al nivel más alto. Si el proceso muere entre el envío y la persistencia, el reintento puede duplicar el mensaje; documéntalo.

## 3. CLI (sección 7.2)

```text
oci-infrastructure-monitor run [--dry-run]
oci-infrastructure-monitor status
oci-infrastructure-monitor check
oci-infrastructure-monitor test-telegram
oci-infrastructure-monitor version
oci-infrastructure-monitor --help
```

- `run`: consulta OCI, actualiza estado y evalúa alertas; un fallo de OCI no aborta.
- `status`: muestra el estado persistido sin consultar OCI, indicando si el acumulado es incompleto.
- `check`: valida el formato de los OCIDs y su visibilidad en métricas con `ListMetrics`, sin permisos nuevos; no modifica estado ni configuración. Distingue OCID mal formado (sale `2`), VNIC visible o VNIC no visible (puede no existir, no tener métricas o no haber tenido tráfico; sale `0` con warning muy visible). Solo una consulta fallida sale `3`.
- `test-telegram`: envía un mensaje de prueba explícito.
- `version`: versión, arquitectura, SO y fecha de compilación (inyectados con `-ldflags`).
- `--dry-run`: simula sin modificar estado ni enviar notificaciones.
- Todos los comandos aceptan `--config PATH` y usan `/etc/oci-infrastructure-monitor/config.json` por defecto; `version` y `--help` no lo necesitan.
- Códigos de salida: `0` éxito, `2` configuración, `3` OCI, `4` persistencia, `5` Telegram, `6` ejecución concurrente; `1` reservado. Detalle en la sección 5.

## 4. Orquestación del comando `run`

### 4.1. Los 13 pasos del ciclo

La versión canónica de este ciclo vive en [`../architecture.md`](../architecture.md). El orden está confirmado por el autor del roadmap. El ciclo debe garantizar:

- El lock se adquiere antes de leer el estado.
- La cola de pendientes se vacía antes de evaluar alertas nuevas.
- `state.json` se persiste después de cada envío exitoso, no solo al final.
- Un fallo de OCI no aborta la ejecución: se evalúan alertas con los datos disponibles (marcados incompletos), se envían pendientes y se registra el error.

1. Cargar y validar la configuración (`--config`; por defecto `/etc/oci-infrastructure-monitor/config.json`).
2. Inicializar el logger y resolver la identidad OCI con Instance Principals.
3. Adquirir `flock` exclusivo y no bloqueante sobre el archivo `lock` del directorio de estado.
4. Leer `state.json` y el historial del mes; detectar corrupción.
5. Calcular la ventana de reconsulta, los buckets que necesitan atención y, si corresponde, la consulta de finalización del mes.
6. Consultar OCI Monitoring; si una VNIC falla, marcar la afectada como incompleta, continuar con las demás y registrar el error sin abortar.
7. Normalizar los datapoints a buckets horarios UTC y sobrescribir por clave; persistir el historial de forma atómica si hubo datos.
8. Calcular el acumulado del mes y su estado de completitud.
9. Vaciar la cola de pendientes: enviar y persistir `state.json` tras cada envío exitoso.
10. Evaluar umbrales mensuales y consumo anormal con los datos disponibles.
11. Encolar y enviar las notificaciones nuevas; persistir `state.json` tras cada envío exitoso.
12. Actualizar la última ejecución, la última consulta exitosa, los contadores de fallos y el estado de alertas.
13. Registrar el resumen, liberar el `flock` y salir con el código correspondiente.

Con `--dry-run`: se ejecutan los pasos de lectura y cálculo, pero no se escribe estado, no se encola ni se envía nada, y no se marca ninguna alerta como enviada. Documenta exactamente qué se simula y qué no.

### 4.2. Lock y cola

- Exclusión con `flock` advisory del kernel, no bloqueante, para `run` y `--dry-run`. Se libera solo cuando el proceso muere; no hay detección de lock obsoleto. Si se guardan PID y timestamp en el archivo, son solo informativos.
- Si el lock está ocupado, se sale con el código de ejecución concurrente antes de leer el estado.
- `status` lee de forma atómica sin lock; `check` no usa lock.
- Cola de pendientes: máximo 50 notificaciones y caducidad de 7 días (fijas). Al caducar o desbordarse se descarta la más antigua y se registra.
- Las alertas se colapsan por `(período, tipo)` al nivel más alto. Al enviar el 90 % se registran como cruzados el 75 % y el 90 %, para que el 75 % no se reenvíe después. Las alertas de consumo anormal no se colapsan con las mensuales.
- La cola se persiste tras cada envío exitoso. Si el proceso muere entre el envío y la persistencia, el reintento puede duplicar el mensaje; ya está documentado como limitación de Telegram.
- Una notificación pendiente del mes anterior se envía igualmente; el mensaje indica su período y que se generó antes, para que no parezca del mes nuevo.

### 4.3. Errores persistentes y recuperación

- **Error persistente de recopilación:** 3 fallos consecutivos (configurable con `collection_failure_threshold`) disparan la alerta. Enfriamiento de 24 h entre repeticiones (configurable con `collection_failure_cooldown_hours`).
- Solo cuentan los fallos de recopilación de OCI. Una VNIC que nunca fue visible cuenta como fallo de recopilación para el error persistente; una VNIC visible que deja de aparecer no cuenta como fallo. Los fallos de Telegram no cuentan para esta alerta, porque no se puede avisar de ellos por Telegram: van a log y al código de salida.
- Recuperación: si la alerta de error ya se envió, se envía la recuperación; si sigue en cola, se elimina de la cola y se sustituye por una sola recuperación que menciona el rango de la falla; si la alerta de error nunca se generó, solo se registra en log.
- Estas alertas usan el mismo canal y la misma cola de pendientes.

## 5. Contrato de configuración y salida

- Configuración en `/etc/oci-infrastructure-monitor/config.json`; todos los comandos aceptan `--config PATH` y usan esa ruta por defecto.
- Credenciales en `/etc/oci-infrastructure-monitor/telegram.env`: `TELEGRAM_BOT_TOKEN` y `TELEGRAM_CHAT_ID`.
- Códigos de salida: `0` éxito, `2` error de configuración (incluidos flags inválidos), `3` error de OCI, `4` error de persistencia, `5` error de Telegram, `6` ejecución concurrente. `1` queda reservado. `--help` sale con `0`.
- Si coinciden varios errores, el código final sigue la prioridad `2` > `4` > `3` > `5`, pero **todos** quedan en log. Un `run` con fallo de OCI que aun así envía pendientes sale `3`. Un acumulado incompleto sale `0` con warning. Cualquier código distinto de `0` deja la unidad en `failed`, visible en `systemctl --failed`.
- Deadline global interno de 4 min, menor que `TimeoutStartSec` de 5 min. Cada llamada individual tiene un timeout menor y se reserva margen para persistir el estado. Si un `429` trae un `retry_after` que no cabe en ese deadline, se persiste el estado, se encola la notificación y se sale `5` antes de que systemd mate el proceso.
- `network.enabled=false`: `run` sale `0` sin consultar OCI.
- `telegram.enabled=false`: se evalúa y se registra en log y `status`, pero no encola, no envía pendientes ni marca como enviado. La caducidad de 7 días sigue aplicando a la cola existente; al habilitar se colapsa lo vigente y se emite una sola alerta con el nivel actual. `test-telegram` sigue funcionando si hay credenciales, porque es una prueba explícita.
- `status` muestra el mes actual y el anterior; si el anterior no está finalizado, lo marca como “no finalizado (en gracia)” con las correcciones acumuladas.
- Si una VNIC que antes era visible deja de aparecer en `ListMetrics`, se emite un warning en log y `status`; no se marcan buckets como `incomplete`, porque sin permisos de red no se distingue “eliminada” de “sin actividad”. Se persiste `last_visible` por VNIC y el warning se emite en la transición.
- Si una VNIC falla y otras responden, la afectada queda `incomplete`, se continúa con las demás y se sale `3` con el total marcado como incompleto.
- Una versión de esquema desconocida hace salir `4` sin sobrescribir.
- La comparación de umbrales usa bytes exactos; el porcentaje mostrado se trunca a un decimal, no se redondea.

## 6. Entregables

- [x] Paquete `internal/telegram` con timeouts, reintentos con backoff, tratamiento de HTTP 429 (`retry_after`), errores de red y protección del token.
- [x] Cola de notificaciones pendientes en `state.json`, máximo 50, caducidad 7 días, descarte del más antiguo y colapso por `(período, tipo)` al nivel más alto, registrando los umbrales cruzados.
- [x] Exclusión con `flock` no bloqueante para `run` y `--dry-run`, adquirida antes de leer el estado, con salida por ejecución concurrente si está ocupado.
- [x] Orquestación completa del comando `run` con los 13 pasos, incluido el vaciado de pendientes antes de evaluar alertas nuevas y la persistencia tras cada envío exitoso.
- [x] Fallo de OCI no aborta: se evalúa con los datos disponibles marcados incompletos, se envían pendientes y se registra el error. Si una VNIC falla y otras responden, la afectada queda `incomplete`, se continúa y se sale `3` con el total incompleto.
- [x] Comando `test-telegram` con mensaje de prueba explícito.
- [x] Alertas de error persistente (3 fallos, enfriamiento 24 h) y recuperación según la regla de enviada, en cola o nunca generada.
- [x] Mensajes en español con todos los campos exigidos y la nota de "tráfico registrado, no facturable"; la pendiente del mes anterior indica su período y que se generó antes.
- [x] Configuración por `--config` y variables `TELEGRAM_BOT_TOKEN`/`TELEGRAM_CHAT_ID`; códigos de salida 0/2/3/4/5/6 con prioridad 2 > 4 > 3 > 5 y todos los errores en log.
- [x] Deadline global de 4 min y `TimeoutStartSec` de 5 min; timeout por llamada menor y margen para persistir. Un `429` largo se encola y sale `5` sin que systemd mate el proceso.
- [x] `telegram.enabled=false` evalúa y registra, pero no encola, no envía pendientes ni marca como enviado; la caducidad de 7 días sigue aplicando a la cola existente; al habilitar se colapsa lo vigente y se emite una sola alerta con el nivel actual.
- [x] `status` muestra el mes anterior no finalizado como “no finalizado (en gracia)” con sus correcciones.
- [x] `last_visible` por VNIC y warning en la transición cuando una VNIC visible deja de aparecer; sin marcar buckets `incomplete`.
- [x] Tests con servidor HTTP simulado para errores, 429, timeouts y ausencia del token en logs.
- [x] Test de `run` de extremo a extremo con mocks, incluidos fallo de OCI, cola con pendientes y `--dry-run`.
- [x] `docs/progress.md` actualizado con resultados reales.

## 7. Criterio de cierre

- [x] Tests con servidor HTTP simulado cubren éxito, error HTTP, 429 con `retry_after`, timeout y error de red.
- [x] El token nunca aparece en logs, mensajes de error ni argumentos CLI (verificado por test).
- [x] El lock se adquiere antes de leer el estado y una segunda ejecución sale con código 6.
- [x] La cola de pendientes se vacía antes de evaluar alertas nuevas.
- [x] `state.json` se persiste tras cada envío exitoso, no solo al final.
- [x] Una notificación fallida permanece en la cola y se reintenta; una cola llena o caducada descarta la más antigua y lo registra.
- [x] Una notificación solo se marca enviada tras una respuesta exitosa.
- [x] Un fallo de OCI no aborta: se registra el error, se marcan los datos como incompletos y se envían los pendientes.
- [x] Si una VNIC falla y otras responden, la afectada queda `incomplete`, se continúa y se sale `3` con el total incompleto.
- [x] La alerta de error persistente se dispara a los 3 fallos; la recuperación sigue la regla: enviada, reemplazada en cola o solo log.
- [x] El colapso por `(período, tipo)` envía el 90 %, registra el 75 % y el 90 % como cruzados y no reenvía el 75 %.
- [x] Una notificación pendiente del mes anterior se envía con su período y el aviso de que se generó antes.
- [x] Los códigos de salida respetan la prioridad 2 > 4 > 3 > 5 y todos los errores quedan en log.
- [x] El deadline global es de 4 min y el `TimeoutStartSec` de 5 min; cada llamada tiene timeout menor.
- [x] Un `429` con `retry_after` largo se encola y sale `5` antes de superar el deadline global.
- [x] `telegram.enabled=false` no encola, no envía pendientes ni marca como enviado; la caducidad de 7 días sigue aplicando y al habilitar se colapsa lo vigente en una sola alerta.
- [x] `status` muestra el mes anterior no finalizado como “no finalizado (en gracia)” con sus correcciones.
- [x] `last_visible` se persiste y el warning de VNIC que deja de aparecer se emite en la transición.
- [x] `--dry-run` no escribe estado, no encola y no envía nada (verificado por test).
- [x] `run` de extremo a extremo con mocks produce el estado y las notificaciones esperadas.
- [x] `go build ./...`, `go vet ./...` y `go test ./...` ejecutados con resultado real reportado.
- [x] La casilla superior de este hito marcada solo después de todo lo anterior.

## 8. Evidencia y traspaso

```bash
go version
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Al cerrar, añade la entrada del Hito 4 en [`../progress.md`](../progress.md) con: qué se completó, qué quedó parcial, resultados reales, decisiones nuevas, supuestos pendientes de validación real y qué debe hacer la siguiente sesión.

### Evidencia de cierre (2026-11-07)

| Comando | Resultado |
|---|---|
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `go test ./...` | OK (226 tests de nivel superior; 270 casos con subtests) |
| `go test -race ./...` | OK |

El envío real a Telegram, el `429` real y el timer systemd quedan fuera de este hito (sección 9) y pendientes de validación del usuario.

## 9. Fuera de alcance de este hito

- Service/timer systemd, binarios, checksums y documentación de despliegue (Hito 5).
- Envío real a Telegram: solo se prueba con servidor HTTP simulado.
- Interfaz web, daemon o base de datos.

## 10. Siguiente hito

[Hito 5 — systemd, compilación y documentación](hito-5-systemd-compilacion-documentacion.md).
