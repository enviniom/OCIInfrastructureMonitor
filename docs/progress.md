# Progreso del proyecto

> Registro de traspaso entre sesiones. Actualizar al cerrar cada hito, según la sección 9 del roadmap. No borrar entradas anteriores: añadir una entrada nueva por cierre.

## Estado general

| Campo | Valor |
|---|---|
| Hito en curso | Hito 5 — no iniciado |
| Último hito cerrado | Hito 4 — 2026-11-07 |
| Última verificación ejecutada | 2026-11-07 — `go build`, `go vet`, `go test`, `go test -race` y conteo de tests |
| Resultado de la verificación | OK en 226 tests de nivel superior (270 casos con subtests) en `cmd/monitor`, `internal/config`, `internal/persistence`, `internal/accumulate`, `internal/alerts`, `internal/app`, `internal/telegram`, `internal/outbox` y `internal/logging`; `internal/oci` sin tests propios |
| Pendiente de validación real | `check` contra OCI, Dynamic Group, Telegram real, `429` real, timer systemd, `status` con estado real, publicación de ceros y umbrales de alerta con tráfico real |

## Plantilla de cierre de hito

Copiar este bloque al cerrar cada hito y rellenarlo con datos reales:

```markdown
## Hito N — <título> (cerrado el YYYY-MM-DD)

### Completado

- ...

### Parcial o pendiente

- ...

### Verificación ejecutada

| Comando | Resultado | Fecha |
|---|---|---|
| `go build ./...` | — | — |
| `go vet ./...` | — | — |
| `go test ./...` | — | — |
| `go test -race ./...` | — | — |

### Decisiones nuevas

- Enlazar las entradas añadidas a `docs/decisions.md`.

### Supuestos pendientes de validar en producción

- ...

### Para la siguiente sesión

- Qué leer, qué ejecutar y por dónde continuar.
```

## Registro

### 2026-11-07 — Hito 1A: investigación de OCI Monitoring (Hito 1 sigue abierto)

**Completado**

- `docs/architecture.md`: namespace `oci_vcn`, métricas, unidades, semántica, dimensión `resourceId`, frecuencia de 1 minuto, agregación con `sum()` por hora, resolución, retención de 90 días, límites de 100.000 datapoints y 10 TPS, coste y estrategia de consulta.
- `docs/oci-permissions.md`: Instance Principals, política actual, política restrictiva con `where target.metrics.namespace='oci_vcn'`, jerarquía de verbos y verificación manual del Dynamic Group.
- `docs/decisions.md`: margen de publicación de 2 h (`publication_latency_hours`).
- `docs/hitos/hito-1-...`: configuración actualizada con `publication_latency_hours`.

**Parcial o pendiente**

- Confirmar empíricamente si OCI publica ceros o los omite.
- Confirmar empíricamente que el punto de 1 minuto es un delta y que `sum()` por hora es correcto.
- Medir la latencia real de publicación y ajustar `publication_latency_hours`.
- Verificar la ventana de actividad de `ListMetrics`; no hay una publicada, así que no se usa como prueba de existencia.
- Implementar 1B (config) y 1C (cliente OCI, `check`, `version`).

**Verificación ejecutada**

| Comando | Resultado | Fecha |
|---|---|---|
| `go build ./...` | No ejecutado: aún no hay paquetes Go | 2026-11-07 |
| `go vet ./...` | No ejecutado: aún no hay paquetes Go | 2026-11-07 |
| `go test ./...` | No ejecutado: aún no hay paquetes Go | 2026-11-07 |

**Decisiones nuevas**

- Ventana de publicación provisional de 2 h, configurable. Ver `docs/decisions.md`.

**Supuestos pendientes de validar en producción**

- Publicación de ceros y semántica exacta del punto de 1 minuto.
- Latencia real de publicación.
- Cobertura de la política restrictiva en el compartimento real de la subred.
- Ventana de actividad de `ListMetrics`.

**Para la siguiente sesión**

- Implementar 1B: `internal/config`, `configs/config.example.json` y tests de validación.
- Después 1C: interfaz/cliente de Monitoring con Instance Principals, `check` y `version`.
- No cerrar el Hito 1 hasta completar 1B, 1C y los tests con mocks.

### 2026-11-07 — Hito 1B: configuración estricta (Hito 1 sigue abierto)

**Completado**

- `internal/config`: carga JSON con `DisallowUnknownFields`, defaults documentados, validación de OCIDs, umbrales, rutas absolutas, niveles de log y límites.
- `configs/config.example.json` válido y sin secretos.
- Tests de configuración: ejemplo, defaults, campos desconocidos, JSON inválido, umbrales, OCIDs, duplicados, rutas, niveles y red deshabilitada.

**Parcial o pendiente**

- 1C: interfaz y cliente de OCI Monitoring, `check` y `version`.
- Confirmaciones empíricas de publicación de ceros, semántica de `sum()` y latencia.

**Verificación ejecutada**

| Comando | Resultado | Fecha |
|---|---|---|
| `go build ./...` | OK | 2026-11-07 |
| `go vet ./...` | OK | 2026-11-07 |
| `go test ./...` | OK (`internal/config`) | 2026-11-07 |
| `go test -race ./...` | OK (`internal/config`) | 2026-11-07 |

**Decisiones nuevas**

- Sin decisiones nuevas; se implementaron las reglas ya registradas.

**Para la siguiente sesión**

- Implementar 1C: interfaz `internal/oci`, cliente de Monitoring con Instance Principals y mock, `check` y `version`.
- No cerrar el Hito 1 hasta que `check` no escriba estado y los tests del cliente estén en verde.

### 2026-11-07 — Hito 1 cerrado

**Completado**

- `internal/oci`: interfaz `Client`, `RealClient` con Instance Principals, consultas agregadas y paginación de `ListMetrics`.
- `internal/app`: `Check` de solo lectura, con estados visible / no visible y sin escritura de estado.
- `cmd/monitor`: comandos `check` y `version`, códigos `0/2/3`, salida del warning de VNIC no visible.
- Tests de configuración, `Check` con mock y formato de salida; `go build`, `go vet`, `go test` y `go test -race` en verde.
- Documentación de arquitectura, permisos, configuración y decisiones.

**Verificación ejecutada**

| Comando | Resultado | Fecha |
|---|---|---|
| `go build ./...` | OK | 2026-11-07 |
| `go vet ./...` | OK | 2026-11-07 |
| `go test ./...` | OK (`internal/config`, `internal/app`, `cmd/monitor`) | 2026-11-07 |
| `go test -race ./...` | OK | 2026-11-07 |

**Pendiente de validación real**

- `check` contra OCI real, pertenencia al Dynamic Group y política restrictiva en el compartimento de la subred.
- Publicación de ceros, semántica delta/`sum()` y latencia real de publicación.
- Comportamiento de `ListMetrics` con una VNIC sin actividad.

**Decisiones nuevas**

- Compartimento único de subred para las VNIC de la v1.
- Ventana de publicación provisional de 2 h.

**Para la siguiente sesión**

- Hito 2: persistencia, buckets horarios, cierre de mes, `status` y recuperación.

### 2026-11-07 — Hito 2 cerrado

**Completado**

- `internal/persistence`: `state.json`, historial mensual, escritura atómica (temporal + `fsync` + `rename`), permisos `0600`/`0700`, `flock` exclusivo con `syscall.Flock`, cuarentena `*.corrupt-<timestamp>`, validación estricta de esquema e invariantes, rechazo de escritura sobre un historial finalizado y carga de solo lectura para `--dry-run`.
- `internal/accumulate`: normalización de datapoints a buckets horarios UTC (timestamp de fin de ventana), planificación de la ventana de reconsulta y de la finalización, sobrescritura por clave, clasificación `complete`/`provisional`/`no_data`/`incomplete`, `UpdateVNIC` con `first_visible`/`last_visible`/warning, acumulado mensual por suma de buckets, rachas de `no_data`, huecos irrecuperables y correcciones.
- `internal/app`: `Collect` (pasos 5–8 del ciclo: planificar, consultar, persistir historial y acumular, cerrar mes) y `BuildStatus` (solo lectura, sin OCI).
- `cmd/monitor`: comando `status` en español, sin lock y sin escribir estado ni historial; la única escritura posible es mover un archivo corrupto a `*.corrupt-*`. Códigos `0`, `2` y `4`.
- 114 tests: 26 de persistencia, 36 de acumulación, 26 de app, 11 de `cmd` y 15 de configuración del Hito 1.

**Parcial o pendiente**

- El comando `run`, el lock tomado por el proceso, el logger real y el envío de notificaciones son del Hito 4. `Collect` deja el reporte y el hook `Logf` para que `run` registre los fallos y las correcciones tardías.
- La política `PublishZeros` queda en `false` hasta confirmar si OCI publica ceros; la lógica de racha ya está implementada y probada. Si la validación confirma que OCI publica ceros, al activar la política hay que implementar además la exigencia de datos antes y después de la hora ausente que describe `decisions.md`; hoy no se aplica porque se asume que OCI omite los ceros.
- El registro en log de una corrección tardía es defensa en profundidad: la ruta normal no reconsulta meses cerrados. El descarte se prueba en `accumulate.Reconcile`.

**Verificación ejecutada**

| Comando | Resultado | Fecha |
|---|---|---|
| `go build ./...` | OK | 2026-11-07 |
| `go vet ./...` | OK | 2026-11-07 |
| `go test ./...` | OK (114 tests en `internal/config`, `internal/persistence`, `internal/accumulate`, `internal/app` y `cmd/monitor`) | 2026-11-07 |
| `go test -race ./...` | OK | 2026-11-07 |

**Decisiones nuevas**

- Persistencia, esquema y fuente de verdad del cierre.
- Hora del bucket y ventana de consulta.
- Clasificación de horas sin datapoint y protección de los datos confirmados.
- Visibilidad de VNIC inferida de datapoints y warning de desaparición.
- Correcciones y cierre de mes.
- Meses que se examinan y huecos irrecuperables.
- Lock con la biblioteca estándar y contrato de `status`.

Todas en `docs/decisions.md`.

**Supuestos pendientes de validar en producción**

- Formato real de los timestamps de `SummarizeMetricsData` (fin de ventana) y que `sum()` por hora sea correcto.
- Publicación de ceros: si OCI publica ceros, cambiar `PublishZeros` para que una racha larga invalide el acumulado.
- Latencia real de publicación, para ajustar `publication_latency_hours`.
- Permisos reales de `/var/lib/oci-infrastructure-monitor` con el usuario del servicio y SELinux.
- Comportamiento de `status` con un estado generado por ejecuciones reales.

**Para la siguiente sesión**

- Leer `docs/hitos/hito-3-alertas.md`, `docs/architecture.md#persistencia-y-acumulación-hito-2` y `docs/decisions.md`.
- Implementar el Hito 3 sobre `accumulate.Summary`, `state.alerts.monthly_alerts` y los buckets persistidos; el evaluador no consulta OCI.
- No reabrir meses finalizados: las alertas del mes cerrado no se reevalúan.

### 2026-11-07 — Hito 3 cerrado

**Completado**

- `internal/alerts`: evaluadores puros sobre los buckets persistidos. No hacen I/O, no envían y no escriben estado.
  - `MonthlyThresholds`: cuatro niveles de `alert_percentages`, severidad por posición (informativa, advertencia, crítica, límite alcanzado), comparación con bytes exactos y porcentaje truncado a un decimal. Emite una sola alerta con el umbral máximo y la lista de umbrales nuevos cruzados; ignora meses finalizados y meses anteriores; advierte de acumulado incompleto o provisional.
  - `Anomaly6H`: suma del saliente de las últimas 6 horas completas, umbral en GiB y enfriamiento configurable. La ventana es `[floor(now) − 6 h, floor(now))` y puede cruzar el cambio de mes. Advierte de buckets `incomplete`, `provisional` o ausentes y de una racha de `no_data` superior a `no_data_streak_hours` cuando `PublishZeros` sea true.
  - `Evaluate` con interfaz `Evaluator` extensible; `MarkNotified` registra umbrales y arranca el enfriamiento solo después de un envío correcto; `Alert.Notification` produce la entrada de la cola del Hito 4.
  - Deduplicación doble: umbrales ya notificados en `alerts.monthly_alerts` y alertas del mismo período/tipo que siguen pendientes en la cola.
- `internal/config`: `high_traffic_cooldown_hours` (24 por defecto), validación de mínimo 1 y ejemplo actualizado.
- `internal/persistence`: esquema `2` con `alerts.anomaly_6h`; migración en memoria de `state.json` e historiales v1, que se reescriben como v2 al guardar. Un historial v1 finalizado se lee como v2 y no se reabre. Una versión desconocida se sigue rechazando sin mover ni sobrescribir.
- `accumulate.OptionsFromConfig`: una única traducción de configuración a opciones, compartida por `app`, `status` y `alerts`.
- 154 tests: 36 de alertas, 29 de persistencia, 16 de configuración, 36 de acumulación, 26 de `app` y 11 de `cmd`.

**Parcial o pendiente**

- El cableado de `alerts.Evaluate` dentro de `run`, la cola de pendientes y el envío pertenecen al Hito 4. El run debe pasar los historiales del mes actual y del anterior para que la ventana de 6 h cruce bien el cambio de mes, y llamar a `MarkNotified` solo tras la respuesta correcta de Telegram.
- `PublishZeros` sigue en `false`: la rama de racha larga de `no_data` está implementada y probada de forma unitaria, pero no se activa hasta confirmar empíricamente si OCI publica ceros.
- `status` no muestra el detalle de alertas por umbral; no era un entregable del hito. Sí muestra el esquema migrado, las notificaciones pendientes y las correcciones.

**Verificación ejecutada**

| Comando | Resultado | Fecha |
|---|---|---|
| `go build ./...` | OK | 2026-11-07 |
| `go vet ./...` | OK | 2026-11-07 |
| `go test ./...` | OK (154 tests en `internal/config`, `internal/persistence`, `internal/accumulate`, `internal/app`, `internal/alerts` y `cmd/monitor`) | 2026-11-07 |
| `go test -race ./...` | OK | 2026-11-07 |

**Decisiones nuevas**

- Umbrales mensuales, severidad por posición y truncado del porcentaje.
- Solo se evalúa el mes en curso; el mes en gracia actualiza el total pero no genera alertas nuevas.
- Ventana de anomalía, enfriamiento de 24 h por defecto y advertencias de calidad de datos.
- Registro de umbrales y enfriamiento solo tras un envío correcto; deduplicación también contra la cola pendiente.
- Esquema v2 y migración desde v1.

Todas en `docs/decisions.md`.

**Supuestos pendientes de validar en producción**

- El umbral de 6 h y el enfriamiento de 24 h son valores de partida; se ajustan con tráfico real.
- Las horas recientes serán `provisional` durante `publication_latency_hours`; una alerta de anomalía puede dispararse con esa advertencia y el consumo real podría ser mayor.
- Confirmar si OCI publica ceros: si lo hace, activar `PublishZeros` y revisar el efecto de las rachas de `no_data`.
- Validar con Telegram real la deduplicación, el colapso de umbrales y el enfriamiento (Hito 4).

**Para la siguiente sesión**

- Leer `docs/hitos/hito-4-telegram-orquestacion.md`, `docs/architecture.md#alertas-hito-3` y `docs/decisions.md`.
- Implementar `internal/telegram`, la cola de pendientes y el comando `run` de 13 pasos.
- Al evaluar, pasar a `alerts.Evaluate` el estado ya vaciado de pendientes y los historiales del mes actual y anterior.
- Llamar a `alerts.MarkNotified` solo después de un envío correcto.

### 2026-11-07 — Hito 4 cerrado

**Completado**

- `internal/telegram`: credenciales desde `telegram.env` con permisos `0600` (o variables de entorno), cliente HTTP con timeout por intento, 3 intentos con backoff, reintento de red/timeout/`408`/`5xx`, tratamiento de `429` con `retry_after`, `RetryAfterError` cuando la espera no cabe en el deadline y redacción del token en todos los errores. Nunca se acepta el token por argumentos ni se escribe en log.
- `internal/outbox`: cola acotada a 50 entradas y 7 días, descarte de la más antigua con registro, colapso por `(kind, period)` al nivel más alto y conservación del texto renderizado.
- `internal/logging`: logger real con niveles `debug`/`info`/`warn`/`error`, timestamps UTC y escritura concurrente segura.
- `app.Run`: ciclo completo de 13 pasos. Lock exclusivo y no bloqueante antes de leer estado (también con la red deshabilitada), `collectInternal` sin persistir, historiales guardados en el paso 7, `state.json` guardado tras cada envío correcto y en el paso 12, cola vaciada antes de evaluar umbrales y anomalía, y salud de recopilación planificada antes del vaciado para retirar una alerta de error obsoleta.
- Fallo de OCI no aborta: las horas afectadas quedan `incomplete`, se envían los pendientes y se sale `3`; un fallo parcial de VNIC continúa con las demás. Una VNIC nunca visible cuenta como fallo de recopilación para el error persistente, sin cambiar el código de salida.
- Error persistente de recopilación a los 3 fallos, enfriamiento de 24 h y recuperación en sus tres ramas: enviada, retirada de la cola y sustituida por una sola recuperación, o solo log si nunca se generó.
- Mensajes en español para alerta mensual, consumo anormal, error persistente, recuperación y prueba. Todos incluyen instancia, región, período, consumo, límite, porcentaje, fecha de evaluación, tipo de medición, severidad y la nota de tráfico registrado no facturable. Una notificación de un período anterior se envía con su período y un `AVISO` de que se generó antes.
- CLI: `run [--dry-run] [--config] [--telegram-env]`, `test-telegram`, `check`, `status`, `version` y `--help`. Códigos `0/2/3/4/5/6` con prioridad `2 > 4 > 3 > 5`; `test-telegram` funciona con `telegram.enabled=false`; `status` muestra el mes anterior no finalizado como «no finalizado (en gracia)» con sus correcciones.
- Esquema `3`: `pending_notifications[].text`, `alerts.collection_failure` y `telegram_enabled`. Migración en memoria de v1/v2 y reescritura como v3; un historial finalizado antiguo se lee como v3 y no se reescribe.
- 226 tests de nivel superior (270 casos con subtests), entre ellos servidor HTTP simulado para éxito, `4xx`, `5xx`, `429` corto y largo, timeout, error de red y ausencia del token en logs y errores; `run` de extremo a extremo con mocks para éxito, fallo de OCI, fallo parcial de VNIC, lock ocupado, cola llena/caducada, reintento, transición de Telegram, `--dry-run` y persistencia tras cada envío.

**Parcial o pendiente**

- El envío real a Telegram, la recepción de un `429` real y la latencia real siguen pendientes de validación en producción; todo lo anterior está probado con servidor HTTP simulado.
- `TimeoutStartSec=5min`, el service/timer systemd, los binarios, los checksums y la documentación de despliegue son del Hito 5.
- La comprobación de una VNIC nunca visible no cambia el código de salida (`0` con warning); el acumulado queda `incomplete` y la alerta persistente se dispara a los 3 fallos. Está documentado en `decisions.md`.
- El `Text` de la cola se valida a 4096 caracteres; los mensajes generados quedan muy por debajo.

**Verificación ejecutada**

| Comando | Resultado | Fecha |
|---|---|---|
| `go build ./...` | OK | 2026-11-07 |
| `go vet ./...` | OK | 2026-11-07 |
| `go test ./...` | OK (226 tests de nivel superior; 270 casos con subtests) | 2026-11-07 |
| `go test -race ./...` | OK | 2026-11-07 |

**Decisiones nuevas**

- Transporte de Telegram, credenciales y protección del token.
- Cola de pendientes, colapso, caducidad y texto persistido.
- Orden de `run`, persistencia tras cada envío y transición deshabilitado→habilitado.
- Error persistente de recopilación, enfriamiento y recuperación.
- Esquema v3.
- Códigos de salida y deadline global.

Todas en `docs/decisions.md`.

**Supuestos pendientes de validar en producción**

- Permisos reales de `telegram.env` (`0600`) y del usuario del servicio.
- Latencia real de Telegram y comportamiento real de `429`/`retry_after`.
- Que el mensaje de prueba llegue al chat configurado y que el token no aparezca en `journalctl` ni en `systemctl status`.
- Que la alerta de VNIC nunca visible no sea un falso positivo en una VNIC realmente inactiva; ajustar la interpretación con tráfico real.
- Que el deadline de 4 min sea suficiente con la latencia real de OCI y Telegram.

**Para la siguiente sesión**

- Leer `docs/hitos/hito-5-systemd-compilacion-documentacion.md`, `docs/architecture.md#telegram-y-orquestación-hito-4` y `docs/decisions.md`.
- Implementar el Hito 5: unidad systemd con `TimeoutStartSec=5min`, timer, binarios `linux/arm64` y `linux/amd64`, `dist/SHA256SUMS`, `README.md`, `deployment.md`, `troubleshooting.md` y entrega final.
- No reabrir los hitos 1–4; las validaciones reales quedan para el responsable del proyecto tras el despliegue.

## Validaciones pendientes del Hito 3

- [ ] Ajustar `high_traffic_6h_gib` y `high_traffic_cooldown_hours` con el tráfico real de la instancia.
- [ ] Confirmar que la ventana de las últimas 6 horas completas refleja el consumo esperado cuando las dos últimas horas son `provisional`.
- [ ] Validar con Telegram real que no se repite una alerta mensual y que el colapso registra todos los umbrales cruzados (Hito 4).
- [ ] Confirmar el comportamiento de la racha de `no_data` cuando se sepa si OCI publica ceros.

## Validaciones pendientes del Hito 2

- [ ] `status` contra un directorio de estado generado por ejecuciones reales.
- [ ] Confirmar con una consulta real que el timestamp agregado corresponde al fin de la ventana y que `sum()` por hora suma los 60 puntos.
- [ ] Confirmar si OCI publica ceros u omite datapoints inactivos; ajustar `PublishZeros` si publica ceros.
- [ ] Medir la latencia real de publicación y ajustar `publication_latency_hours`.
- [ ] Verificar los permisos `0600`/`0700` y el `flock` con el usuario del servicio y SELinux.
- [ ] Verificar que un historial finalizado no se reescribe tras el despliegue.

## Validaciones pendientes del Hito 1

- [ ] ¿OCI publica puntos con valor 0 o los omite? (no hay declaración oficial; falta prueba empírica)
- [x] ¿`ListMetrics` sobre `oci_vcn` está cubierto por `read metrics`? Sí: `read` incluye `inspect`, que es el mínimo para listar definiciones.
- [x] ¿La dimensión `resourceId` identifica la VNIC en este namespace? Sí, según VNIC Metrics.
- [x] ¿Qué ventana de actividad usa `ListMetrics`? No hay una publicada; se documenta la limitación y se usa `last_visible`, sin inferir existencia.
- [x] Retención, resolución mínima, latencia, agregación y límites documentados. Retención 90 días; 1m/5m/1h/1d con máximos 7/30/90/90 días; 100.000 datapoints; 10 TPS; latencia no publicada (2 h provisional); agregación `sum()` pendiente de confirmación empírica.
- [x] El rango de finalización de mes cabe: 744 puntos por VNIC y dirección a 1 h; se trocea si se superan los 100.000.
- [ ] Confirmar empíricamente la semántica delta/`sum()` y la latencia real con una consulta a OCI.

## Pendientes de validación real

Estas comprobaciones no pueden hacerse desde el entorno de desarrollo y las valida el responsable del proyecto tras el despliegue:

- [ ] `oci-infrastructure-monitor check` contra OCI real.
- [ ] Pertenencia de la instancia al Dynamic Group `Default/BackupInstances`.
- [ ] Envío real a Telegram.
- [ ] Ejecución real del timer systemd.
- [ ] Comportamiento de SELinux con el binario y los directorios de estado.
