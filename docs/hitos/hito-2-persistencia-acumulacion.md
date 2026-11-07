# Hito 2 — Persistencia y acumulación

- [ ] **Hito 2 completado** — marcar solo cuando todos los entregables y criterios de cierre estén verificados.

> **Archivo autocontenido.** Incluye el contexto general y las secciones 0, 3.2 y 4 del roadmap. No necesitas abrir `Roadmap.md` para ejecutar este hito.
> **Depende de:** [Hito 1](hito-1-investigacion-configuracion-cliente-oci.md). **Habilita:** [Hito 3](hito-3-alertas.md).
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
- Este hito asume que el Hito 1 dejó `internal/config`, el cliente de OCI Monitoring y los comandos `check` y `version` funcionando. Revisa `docs/progress.md` para confirmar el estado real.

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

## 2. Qué se contabiliza (sección 3.2)

- **Tráfico saliente:** todo lo que registre `VnicToNetworkBytes` en las VNIC configuradas, sin filtrar tráfico privado, interno, hacia servicios OCI ni hacia Internet. Estrategia conservadora: preferimos alertar de más que ignorar tráfico relevante.
- **Tráfico entrante (opcional):** `VnicFromNetworkBytes`, siempre separado. Nunca se suma al contador principal.
- Identificación por **OCID** (instancia y VNIC), configurables. No uses IPs como identificador principal.
- Con varias VNIC, evita duplicaciones por consultas repetidas o dimensiones superpuestas.

El tráfico registrado no es tráfico facturable: el acumulado que calcules es una referencia preventiva, no un consumo confirmado.

## 3. Persistencia por buckets horarios (sección 4)

No uses SQLite, MariaDB, PostgreSQL, Redis ni otro motor. Solo archivos JSON.

### 3.1. Modelo

- La unidad de almacenamiento es el **bucket horario UTC**, identificado por la clave `(recurso OCID, dirección, hora UTC)`.
- Cada ejecución consulta desde `min(ahora − requery_window, bucket más antiguo que necesite atención)`, acotado por la retención de OCI. Un bucket necesita atención si es `provisional`, `incomplete` o falta. La consulta **sobrescribe por clave** los buckets recibidos. Así la deduplicación es automática y las correcciones tardías de OCI se absorben solas.
- Cada bucket guarda: bytes, marca de tiempo de captura y un estado `complete`, `provisional`, `no_data` o `incomplete`. Los buckets más recientes que la latencia de publicación se marcan `provisional`.
- `no_data`: hora sin datapoint dentro de un rango consultado con éxito. Si OCI publica ceros, se exige que haya datos de la VNIC antes y después. Si los omite y la VNIC es visible en `ListMetrics` durante la retención, la ausencia es `no_data`. Si la VNIC nunca fue visible, es `incomplete` y cuenta para el error persistente; solo “visible antes y ahora ausente” es `no_data` con warning y `last_visible` persistido. En todos los casos `no_data` cuenta como 0, se registra y se muestra en `status`; un `no_data` aislado no marca el acumulado como incompleto. Una racha superior a `no_data_streak_hours` (6 por defecto) solo marca incompleto si OCI publica ceros explícitos; si los omite, la racha no invalida el acumulado. Consulta fallida, fuera de retención o anterior a la VNIC marcan `incomplete`.
- `incomplete`: hora sin datapoint por consulta fallida, VNIC nunca visible, anterior a la existencia de la VNIC o fuera de retención. El acumulado se presenta como incompleto.

- **El acumulado mensual se calcula sumando los buckets del mes**; no se mantiene como contador incremental.
- Si tras un apagado prolongado hay horas anteriores a la ventana que aún están dentro de la retención de OCI, amplía la consulta para recuperarlas. Si ya no es posible recuperarlas, márcalas `incomplete` y el acumulado debe presentarse como **incompleto**, nunca como exacto.
- Los buckets horarios no cruzan el cambio de mes, así que el cambio de mes se resuelve por construcción.
- **Cierre de mes:** un mes se cierra en la primera ejecución con consulta exitosa que cumpla `ahora >= fin de mes + requery_window` y que cubra hasta el fin de mes; esa consulta es la de finalización y cubre desde el bucket más antiguo del mes que necesite atención hasta el fin de mes, acotada por retención y límites de OCI. Tras el cierre, el archivo es inmutable y las correcciones posteriores se descartan y se registran en log. La gracia real es `requery_window` (48 h por defecto), sin una constante de 7 días aparte.
- Un mes sin ejecuciones durante la gracia no cierra con huecos mientras los datos sigan en retención: la consulta de finalización los recupera. Solo lo irrecuperable se marca `incomplete`, visible en `status` y sin alerta nueva.
- **Correcciones durante la gracia:** actualizan el total y no reevalúan alertas; `status` muestra el número de buckets corregidos y la diferencia total en bytes. Las alertas del mes anterior no se reevalúan ni se reenvían, pero una notificación ya pendiente sí se envía (Hito 4).

### 3.2. Archivos

```text
/var/lib/oci-infrastructure-monitor/
├── state.json          # alertas enviadas, notificaciones pendientes, última ejecución/consulta exitosa, meses finalizados, contadores de fallos, last_visible por VNIC, versión de esquema
├── lock                # bloqueo contra ejecuciones simultáneas
└── history/
    ├── 2026-11.json    # buckets del mes
    └── 2026-12.json
```

- Un mes finalizado es inmutable y **no se elimina automáticamente**.
- El estado recuerda qué meses ya se finalizaron, para no repetir la consulta de finalización.
- Un archivo de historial corrupto solo afecta a ese mes.
- Todos los archivos incluyen versión de esquema.

### 3.3. Escritura segura

Archivo temporal en el mismo directorio, `fsync`, renombrado atómico, permisos restrictivos (0600 archivos, 0700 directorio), bloqueo exclusivo, detección de corrupción. **Nunca sobrescribas en silencio un archivo corrupto**: muévelo a `*.corrupt-<timestamp>`, registra el error y falla con código de salida explícito.

## 4. Entregables

- [ ] Paquete `internal/persistence` con `state.json`, historial mensual, escritura atómica, bloqueo exclusivo y detección de corrupción.
- [ ] Lógica de buckets: sobrescritura por clave, ventana de reconsulta, recuperación tras apagado, estados `complete`/`provisional`/`no_data`/`incomplete`, cálculo del acumulado mensual, cambio de mes y finalización por `requery_window`.
- [ ] Comando `status`, que muestra el estado persistido sin consultar OCI, indica si el acumulado es incompleto y muestra el conteo de `no_data`, el estado abierto/finalizado del mes actual y del anterior cerrado o en gracia, los buckets corregidos, la diferencia en bytes y el warning de VNIC que dejó de ser visible.
- [ ] Tests de duplicados, cambio de mes, finalización, mes sin ejecuciones, correcciones durante la gracia, recuperación, huecos irrecuperables, `no_data`, rachas largas, VNIC nunca visible, VNIC visible que deja de aparecer, corrupción de archivos y concurrencia.
- [ ] Validación de la versión de esquema: una versión desconocida falla con código `4` y no sobrescribe el archivo.
- [ ] `go test -race ./...` ejecutado (o su limitación documentada).
- [ ] `docs/progress.md` actualizado con resultados reales.

## 5. Criterio de cierre

- [ ] Tests de duplicados, cambio de mes, finalización, mes sin ejecuciones, correcciones durante la gracia, recuperación, huecos irrecuperables, `no_data`, rachas largas, corrupción y concurrencia en verde.
- [ ] La sobrescritura por clave no duplica bytes al reconsultar la misma ventana.
- [ ] Un mes se finaliza en la primera consulta exitosa que cumple `ahora >= fin de mes + requery_window` y cubre hasta el fin de mes; después, una corrección tardía se descarta y se registra en log.
- [ ] Un mes sin ejecuciones durante la gracia se recupera con la finalización si los datos están en retención; solo lo irrecuperable queda `incomplete`.
- [ ] Las correcciones durante la gracia actualizan el total y `status` (buckets corregidos y diferencia en bytes), sin reevaluar alertas.
- [ ] Un `no_data` aislado cuenta 0 sin marcar el acumulado como incompleto; una racha superior a `no_data_streak_hours` solo lo marca si OCI publica ceros explícitos.
- [ ] Una VNIC nunca visible queda `incomplete` y cuenta para el error persistente; una VNIC visible que deja de aparecer genera `no_data` con warning y `last_visible` persistido.
- [ ] Un hueco irrecuperable se marca `incomplete` y el acumulado se presenta como incompleto.
- [ ] Un archivo corrupto se mueve a `*.corrupt-<timestamp>` y el proceso falla con un código distinto; no se sobrescribe en silencio.
- [ ] Una versión de esquema desconocida falla con código `4` sin sobrescribir el archivo.
- [ ] Dos ejecuciones simultáneas no corrompen el estado (bloqueo verificado).
- [ ] `go build ./...`, `go vet ./...` y `go test ./...` ejecutados con resultado real reportado.
- [ ] La casilla superior de este hito marcada solo después de todo lo anterior.

## 6. Evidencia y traspaso

```bash
go version
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Al cerrar, añade la entrada del Hito 2 en [`../progress.md`](../progress.md) con: qué se completó, qué quedó parcial, resultados reales, decisiones nuevas, supuestos pendientes de validación real y qué debe hacer la siguiente sesión.

## 7. Fuera de alcance de este hito

- Evaluadores de alertas y envío de notificaciones (Hitos 3 y 4).
- Comando `run` completo (Hito 4).
- Service/timer systemd y documentación de despliegue (Hito 5).
- Cambiar el formato de persistencia a una base de datos.

## 8. Siguiente hito

[Hito 3 — Alertas](hito-3-alertas.md).
