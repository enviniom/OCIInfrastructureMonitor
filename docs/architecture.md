# Arquitectura

> Fuente canónica del ciclo de ejecución y de las reglas vigentes al cierre de la v1. Los porqués están en [`decisions.md`](decisions.md); la evidencia y las comprobaciones que faltan, en [`progress.md`](progress.md). Instalación y diagnóstico: [`deployment.md`](deployment.md) y [`troubleshooting.md`](troubleshooting.md).

## Alcance

Un único ejecutable `oneshot` en Go. Mide el tráfico saliente de una o varias VNIC de OCI, lo acumula por bucket horario UTC y alerta por Telegram. No hay daemon, base de datos ni interfaz web. La v1 solo cubre red; CPU, RAM, disco, servicios Linux, respaldos y almacenamiento quedan para fases posteriores.

## Ciclo de `run` (13 pasos)

Garantías del ciclo:

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

Con `--dry-run`: se ejecutan los pasos de lectura y cálculo, pero no se escribe estado, no se encola ni se envía nada, y no se marca ninguna alerta como enviada. La lectura tampoco mueve un archivo corrupto: se informa y se sale sin tocarlo.

**Implementación (Hito 4).** `app.Run` es el orquestador. `collectInternal` ejecuta los pasos 4–8 sin persistir; `Run` guarda los historiales en el paso 7 y `state.json` después de cada envío correcto y en el paso 12. El lock se toma siempre antes de leer estado, también con `network.enabled=false`. La salud de recopilación se planifica antes de vaciar la cola para poder retirar una alerta de error obsoleta; los umbrales mensuales y la anomalía se evalúan después del vaciado. Todo el detalle de transporte, cola y códigos está en [Telegram y orquestación](#telegram-y-orquestación-hito-4).

## Métricas de red (Hito 1)

### Namespace y métricas

El namespace es `oci_vcn` y cada métrica se publica por VNIC. La documentación oficial de VNIC Metrics indica:

| Métrica | Unidad | Semántica | Dimensiones |
|---|---|---|---|
| `VnicToNetworkBytes` | bytes | Bytes enviados desde la VNIC hacia la red, **antes** de los descartes por security list. | `resourceId` (OCID de la VNIC) |
| `VnicFromNetworkBytes` | bytes | Bytes recibidos en la VNIC desde la red, **después** de los descartes. | `resourceId` (OCID de la VNIC) |

- La única dimensión documentada para estas métricas es `resourceId`, que es el OCID de la VNIC. **No hay dimensión de instancia** en `oci_vcn`; el OCID de instancia configurado se usa para identificar el recurso en mensajes y estado, no para consultar la métrica.
- El servicio publica **un punto crudo por cada intervalo de 1 minuto**.
- `VnicToNetworkBytes` incluye tráfico privado, interno, hacia servicios OCI y hacia Internet; es la métrica conservadora que pide el roadmap.
- `VnicFromNetworkBytes` se mantiene separada y nunca se suma al contador principal.

### Agregación y consulta

La hipótesis de trabajo es que cada punto de 1 minuto representa el volumen de ese minuto y que `sum()` sobre una ventana de 1 hora suma los 60 puntos. La evidencia es la frecuencia de 1 minuto y que el estadístico por defecto de las gráficas de VNIC es **Sum**. La documentación no afirma explícitamente que el punto sea un delta y no un contador acumulativo, así que **esto debe confirmarse con una consulta real** antes de confiar en los totales de producción.

Consultas previstas:

```text
VnicToNetworkBytes[1h]{resourceId = "ocid1.vnic..."}.sum()
VnicFromNetworkBytes[1h]{resourceId = "ocid1.vnic..."}.sum()
```

- Se usa intervalo y resolución de `1h`, con `startTime` y `endTime` alineados a horas UTC. El timestamp del punto agregado corresponde al **final** de la ventana; el `endTime` de la consulta es exclusivo. Por eso la consulta envía `endTime + 1h` (`Job.QueryEnd`) y el filtro de datos sigue siendo `[start, end)`: así el punto de la última hora del mes no se queda fuera.
- La ejecución normal consulta la ventana de reconsulta (48 h por defecto) a 1 h: unas 48 muestras por VNIC y dirección.
- La finalización de mes consulta el mes completo a 1 h: 744 muestras por VNIC y dirección.
- La anomalía de 6 h se calcula con los buckets horarios persistidos, no con una consulta adicional.
- `SummarizeMetricsData` no pagina; si el rango supera el límite de 100.000 datapoints, se trocea por semanas o por VNIC. `ListMetrics` sí pagina con `page`/`limit` (por defecto 1000).
- El compartimento de la consulta es el **compartimento de la subred**, no el de la instancia. Ver [`oci-permissions.md`](oci-permissions.md).

### Resolución, retención y límites

| Intervalo | Resolución por defecto | Rango máximo devuelto |
|---|---|---|
| `1m` | 1 minuto | 7 días |
| `5m` | 5 minutos | 30 días |
| `1h` | 1 hora | 90 días |
| `1d` | 1 día | 90 días |

- Retención de datos de Monitoring: **90 días**.
- Máximo de **100.000 datapoints** por consulta.
- Límite de **10 TPS por tenencia** para `SummarizeMetricsData` y para `ListMetrics`.
- Límite de página de `ListMetrics`: 1000 por defecto.
- Los intervalos válidos de MQL son `1m`-`60m`, `1h`-`24h` y `1d`; el intervalo debe ser mayor o igual que la resolución.

### Publicación, ceros y visibilidad

- **Latencia de publicación:** la documentación oficial no publica una cifra. Se adopta un margen conservador de **2 horas** (`publication_latency_hours` configurable) para marcar `provisional`; se medirá en producción y se ajustará.
- **Publicación de ceros:** la documentación no declara si `oci_vcn` publica puntos con valor 0 o los omite. No se asume ninguna de las dos cosas: la regla de `no_data` queda condicionada y se verificará empíricamente. Mientras no se sepa, una VNIC visible con ausencia de puntos se trata como `no_data` (0) con la lógica de `last_visible`; una VNIC que nunca fue visible se trata como `incomplete`.
- **`ListMetrics`:** lista definiciones de métricas, no recursos vivos. Requiere `inspect metrics`; `read metrics` incluye `inspect` en la jerarquía de verbos de OCI. No tiene una ventana de actividad documentada, así que **no se usa como prueba de existencia**. `resourceId` sí está confirmado como OCID de la VNIC.
- **`check`:** reporta OCID mal formado, VNIC visible en métricas o VNIC no visible. La tercera solo significa “puede no existir, no tener métricas o no haber tenido tráfico”; no se debe convertir en una afirmación de inexistencia.

### Coste

La lista de precios de Monitoring incluye **500 millones de datapoints de ingesta gratis** y **1.000 millones de datapoints de retrieval gratis** al mes. La aplicación consume una fracción mínima:

- 48 h a 1 h por VNIC y dirección, 4 ejecuciones al día: ~384 datapoints/día por VNIC.
- Finalización mensual: ~744 datapoints por VNIC y dirección.
- Incluso consultando 1 minuto para 48 h, el volumen mensual queda muy por debajo del billón de retrieval gratuito.

No se factura por el uso de Telegram desde la aplicación; el coste relevante es el tráfico de red de OCI, que es independiente de Monitoring.

## Persistencia y acumulación (Hito 2)

### Archivos y esquema

```text
/var/lib/oci-infrastructure-monitor/     # 0700
├── state.json                           # 0600
├── lock                                 # 0600
└── history/                             # 0700
    ├── 2026-11.json                     # 0600
    └── 2026-12.json
```

Todos los archivos llevan `"schema_version": 3` desde el Hito 4. La lectura acepta también `1` y `2`: un estado o historial antiguo se migra en memoria y se reescribe como v3 en el siguiente guardado; un historial finalizado se lee como v3 y no se reescribe. Una versión desconocida se rechaza con código `4` sin mover ni sobrescribir el archivo (`OldestSupportedSchemaVersion` marca el límite inferior). La lectura es estricta: cualquier campo nuevo exige subir la versión de esquema y migrar. Un archivo ilegible o que no valida sus invariantes se mueve a `<nombre>.corrupt-<timestamp UTC>` y el proceso falla con `4`; nunca se sobrescribe en silencio. La excepción es `--dry-run`, que lee con `LoadOptions{Quarantine:false}` y no mueve nada.

`state.json` contiene:

| Campo | Contenido |
|---|---|
| `last_run` | Última ejecución, haya ido bien o mal. |
| `last_successful_query` | Última ejecución con al menos una consulta OCI exitosa. |
| `consecutive_failures`, `last_failure` | Racha de ejecuciones con algún fallo de OCI. |
| `finalized_months` | Espejo de los meses cerrados, para planificar sin abrir historiales antiguos. |
| `alerts.monthly_alerts` | Umbrales ya notificados por período (los escribe `alerts.MarkNotified`). |
| `alerts.anomaly_6h.last_notified_at` | Último envío correcto de la alerta de anomalía; arranca el enfriamiento. |
| `alerts.collection_failure.failure_since` | Primer fallo de la racha actual de recopilación. |
| `alerts.collection_failure.last_notified_at` | Último envío correcto del error persistente; arranca el enfriamiento. |
| `pending_notifications` | Cola de Telegram con `kind`, `period`, `level`, `created_at`, `attempts`, `last_attempt` y el `text` renderizado. |
| `telegram_enabled` | Valor de `telegram.enabled` en la última ejecución; detecta la transición deshabilitado→habilitado. |
| `vnics` | `first_visible`, `last_visible`, `last_data_at`, `visible` y `warning_since` por OCID de VNIC. |
| `months` | `buckets_corrected`, `delta_bytes` y `updated_at` por mes, acumulados durante la gracia. |

`history/YYYY-MM.json`:

```json
{
  "schema_version": 3,
  "month": "2026-11",
  "finalized": false,
  "buckets": [
    {
      "resource_id": "ocid1.vnic...",
      "direction": "outbound",
      "hour": "2026-11-07T12:00:00Z",
      "bytes": 1234567,
      "status": "complete",
      "captured_at": "2026-11-07T13:30:00Z"
    }
  ]
}
```

- La clave de deduplicación es `(resource_id, direction, hour)`. `hour` es el inicio de la hora UTC.
- `finalized` y `finalized_at` viven en el historial, que es la fuente de verdad; `state.finalized_months` es un espejo. Si el proceso cae entre las dos escrituras, la ejecución siguiente adopta el cierre desde el historial.
- Un mes finalizado no se modifica ni se borra. `SaveHistory` rechaza sobrescribirlo con `ErrFinalizedHistory`.

### Modelo de buckets

| Estado | Cuándo se asigna | Bytes |
|---|---|---|
| `provisional` | La hora termina dentro de `publication_latency_hours`, con o sin datapoint. Se reconsulta. | El punto si existe; 0 si no. |
| `complete` | Pasada la latencia, con datapoint, o con un punto ya observado antes. | El último punto observado. |
| `no_data` | Pasada la latencia, sin datapoint, y la VNIC ya fue visible. Cuenta 0. | 0 |
| `incomplete` | Consulta fallida, hora anterior a la primera visibilidad de la VNIC u hora fuera de retención. | 0 |

- La hora del bucket se calcula a partir del timestamp agregado: OCI lo marca al final de la ventana, así que un punto alineado a las 12:00 pertenece al bucket de las 11:00 (`BucketHour`). Un timestamp no alineado cae en la hora que lo contiene. La consulta extiende `endTime` una hora más allá del último bucket para que el punto del límite no se pierda.
- Una consulta con éxito que omite un punto ya confirmado no borra el dato: solo se reescriben las horas con punto recibido y las que son `provisional`, `incomplete` o faltan. Así una respuesta parcial o vacía no destruye el mes.
- `no_data` no invalida el acumulado por sí solo. Una racha mayor que `no_data_streak_hours` solo lo invalida si OCI publica ceros explícitos; la política vive en `accumulate.Options.PublishZeros` y hoy vale `false` a la espera de la validación empírica.
- La visibilidad se infiere de los datapoints, no de `ListMetrics`: una VNIC con datos previos que no devuelve ninguno en una ejecución con consultas exitosas pasa a `visible=false` con `warning_since`; una VNIC que nunca apareció deja sus horas `incomplete` y no genera warning.

### Ventana, correcciones y cierre

1. **Meses a examinar:** desde la última traza (`last_run` o `last_successful_query`) o el horizonte de 90 días, lo que sea posterior, más todo mes con historial dentro de la retención. Una instalación nueva examina solo el mes en curso.
2. **Ventana:** cada ejecución consulta desde `min(ahora − requery_window, bucket más antiguo que necesite atención)` hasta la hora completa actual (o hasta el fin de mes en la finalización), acotada por el inicio del mes y por la retención. Un bucket necesita atención si es `provisional`, `incomplete` o falta.
3. **Fin de mes:** pasado `ahora >= fin de mes + requery_window`, la consulta de finalización va desde el bucket más antiguo que necesite atención hasta el fin de mes. Si no queda nada que recuperar, el mes se cierra sin consulta. Si la consulta falla, el mes no se cierra y se reintenta.
4. **Gracia:** mientras `ahora < fin de mes + requery_window`, el mes anterior se sigue reconsultando con la ventana de 48 h y sus correcciones actualizan el total.
5. **Huecos irrecuperables:** las horas anteriores al horizonte de retención se rellenan como `incomplete` y el mes se puede cerrar con el acumulado marcado incompleto. El resto del mes se consulta si los datos siguen en retención.
6. **Corrección:** un bucket existente cuyos bytes cambian. Se cuenta en `state.months` con su delta. Tras el cierre se descarta y se cuenta en `CollectReport.LateCorrections`, que `Collect` registra por `CollectOptions.Logf`.

### Escritura, bloqueo y corrupción

- Archivo temporal en el mismo directorio, `fsync`, `rename` atómico y `fsync` del directorio; `0600` para archivos y `0700` para directorios.
- `flock` exclusivo y no bloqueante (`LOCK_EX|LOCK_NB`) sobre `<state_dir>/lock`, con la biblioteca estándar (`syscall.Flock`). `Collect` exige el lock tomado; lo toma el comando `run` del Hito 4. `status` lee sin lock. Si dos ejecuciones coinciden, la segunda no obtiene el lock (`ErrLocked`, código `6` en `run`).
- Esquema desconocido: error sin escritura. Corrupción: cuarentena y error (salvo en `--dry-run` y `status`, que informan sin mover). Un historial corrupto solo afecta a ese mes: `status` sigue mostrando el resto y sale `4`; `Collect` aborta para no construir sobre datos ilegibles.

### `status`

`status` no consulta OCI y no escribe estado ni historial; si encuentra un archivo corrupto lo informa y sale `4`, pero no lo mueve. Muestra, en español y en texto:

- directorio, esquema, última ejecución, última consulta exitosa, fallos consecutivos y notificaciones pendientes;
- mes actual y mes anterior: abierto, finalizado, en gracia o pendiente de finalización; GiB y bytes del saliente; acumulado `completo`, `provisional`, `incompleto` o `sin datos`; conteo de `complete`/`provisional`/`no_data`/`incomplete`; entrante aparte si existe; correcciones acumuladas y su delta en bytes;
- avisos de VNIC que dejó de aparecer;
- errores de persistencia, con código `4` si los hay.

La unidad del acumulado es GiB (2^30); el porcentaje se trunca a un decimal (lo hace el evaluador del Hito 3).

`test-telegram` y `status` leen el estado y el historial con la cuarentena desactivada: informan de un archivo corrupto pero no lo mueven. Solo `run` real (con el lock) aplica la cuarentena documentada; `status` sale `4` si la encuentra.

## Alertas (Hito 3)

`internal/alerts` evalúa los buckets persistidos y propone notificaciones. Los evaluadores son funciones puras sobre `Input{Now, Config, State, Histories}`: no consultan OCI, no envían por Telegram y no escriben estado. `Evaluate` recorre una lista de `Evaluator`; hoy son `MonthlyThresholds` y `Anomaly6H`, y un evaluador nuevo (por ejemplo, una línea base histórica) se añade sin tocar los existentes.

### Umbrales mensuales

- `alert_percentages` tiene exactamente cuatro valores crecientes, el último 100; la severidad se asigna por posición: informativa, advertencia, crítica y límite alcanzado.
- `limitBytes = round(monthly_limit_gib × 2^30)`. Un umbral `p` se cruza cuando `usageBytes >= ceil(limitBytes × p / 100)`, con aritmética entera: la comparación usa bytes exactos.
- El porcentaje mostrado es `floor(usageBytes × 1000 / limitBytes) / 10`, truncado y con coma decimal: un 89,96 % se muestra «89,9 %» y no dispara el 90 %.
- Si se cruzan varios umbrales desde la última alerta notificada, se propone **una** alerta con el porcentaje máximo y la lista completa de umbrales nuevos cruzados.
- Solo se evalúa el mes en curso. Un mes finalizado no se reevalúa; el mes anterior en gracia actualiza su total pero no genera alertas nuevas.
- La deduplicación usa dos fuentes: `alerts.monthly_alerts` (umbrales ya enviados) y las notificaciones pendientes del mismo `(período, tipo)`. Si ya hay una pendiente igual o superior no se propone otra; un umbral superior sí se propone.
- Si el acumulado es `incomplete` o `provisional`, la alerta incluye un aviso explícito y el texto lo dice.

### Consumo anormal de 6 h

- La ventana son las seis horas completas inmediatamente anteriores a `floor(now)`: `[floor(now) − 6 h, floor(now))`. Puede cruzar el cambio de mes, por lo que `Histories` debe incluir el mes actual y el anterior.
- Solo se suman buckets `outbound`; el entrante nunca se añade.
- Se dispara si `bytes >= high_traffic_6h_gib × 2^30`. El enfriamiento es `high_traffic_cooldown_hours` (24 por defecto) y se mide desde el último envío correcto.
- Si la ventana contiene buckets `provisional`, `incomplete`, ausentes o —cuando `PublishZeros` sea true— una racha de `no_data` mayor que `no_data_streak_hours`, la alerta sigue pudiendo dispararse pero incluye `AVISO` y queda marcada como no fiable. Un `no_data` aislado cuenta 0 y no invalida la ventana.
- La alerta de anomalía es independiente de la mensual y no se colapsa con ella.

### Registro y entrega

- `alerts.Alert` lleva el tipo, el período, el nivel (el porcentaje máximo en las mensuales), los bytes, el límite, el porcentaje truncado, la ventana y los avisos.
- `Alert.Notification()` produce la entrada de la cola (`kind`, `period`, `level`, `created_at`) que persiste el Hito 4.
- `alerts.MarkNotified(state, alert, at)` solo se llama tras una respuesta correcta de Telegram: fusiona los umbrales cruzados en `monthly_alerts` o fija `anomaly_6h.last_notified_at`. `MarkNotificationNotified` hace lo mismo para una entrada de la cola, que solo guarda el nivel máximo: al entregar el 90 % registra 50, 75 y 90. Los evaluadores nunca marcan nada.
- Desde el Hito 4, `app.Run` encola, envía, persiste tras cada entrega y aplica el colapso y la caducidad. Ver [Telegram y orquestación](#telegram-y-orquestación-hito-4).

## Telegram y orquestación (Hito 4)

### Transporte y credenciales

`internal/telegram` usa `net/http` y la API oficial (`https://api.telegram.org/bot<token>/sendMessage`), sin frameworks de bots. El token y el Chat ID se leen de `/etc/oci-infrastructure-monitor/telegram.env` por defecto (`--telegram-env` permite otra ruta); las variables de entorno no vacías tienen prioridad. El archivo debe tener permisos `0600` (sin bits de grupo u otros); otros permisos se rechazan. El token nunca se acepta por argumentos, no se escribe en log y se redacta de cualquier error de transporte, porque la URL lo contiene.

- Timeout HTTP por intento: 10 s. Hasta 3 intentos con backoff 500 ms → 5 s.
- Se reintentan errores de red, timeouts, `408` y `5xx`; un `4xx` distinto de `429` es permanente.
- Un `429` respeta `retry_after`. Si la espera cabe en el contexto de entrega (45 s o el tiempo restante), se espera y se reintenta; si no cabe, el cliente devuelve `RetryAfterError`, la notificación queda encolada y `run` sale `5`.
- El fallo de Telegram no cuenta para el error persistente de recopilación: va a log y al código de salida.

### Cola de pendientes

- Máximo 50 entradas y caducidad de 7 días (fijas). Al caducar o desbordarse se descarta la más antigua y se registra.
- Colapso por `(kind, period)` al nivel más alto. Una alerta mensual superior sustituye a la pendiente; una igual o inferior se suprime. La anomalía y el error persistente no se duplican para el mismo período. Al enviar el 90 % se registran los umbrales 50, 75 y 90 como cruzados, de modo que el 75 % no se reenvía.
- Cada entrada guarda el texto ya renderizado: una notificación generada en un mes anterior conserva su período, sus datos y su ventana. Al enviarla se antepone un `AVISO` con su período y su fecha de generación.
- Una entrada fallida permanece en la cola, incrementa `attempts`, guarda `last_attempt` y se reintenta en la siguiente ejecución. La cola se vacía antes de evaluar los umbrales mensuales y la anomalía. La salud de recopilación se planifica antes del vaciado para sustituir una alerta de error que ya no procede por su recuperación.

### Escritura, `--dry-run` y transición de Telegram

- `state.json` se persiste después de cada envío correcto y en el paso 12, no solo al final.
- `--dry-run` ejecuta las lecturas y los cálculos, pero no escribe estado ni historial, no mueve archivos corruptos, no encola, no envía y no marca nada. El `flock` se adquiere igualmente, así que el directorio de estado, `history/` y el archivo `lock` pueden crearse; `state.json`, los archivos de historial y los `*.corrupt-*` no se tocan. La simulación cuenta en memoria la cola que se vaciaría y las alertas nuevas (`WouldSend`), pero no persiste esa cola. Los códigos de configuración, persistencia y OCI se conservan; no puede devolver `5` porque no envía.
- `network.enabled=false` toma el lock y sale `0` sin consultar OCI ni escribir estado; los pendientes esperan a que se reactive.
- `telegram.enabled=false` aplica la caducidad de 7 días, evalúa y registra en log y `status`, pero no encola, no envía pendientes y no marca como enviado. Al reactivarlo se descartan las alertas mensuales pendientes y se emite una sola con el nivel actual; las de anomalía y de recopilación conservan su semántica. `state.telegram_enabled` recuerda el valor anterior para detectar la transición.

### Error persistente y recuperación

- `collection_failure_threshold` fallos consecutivos (3 por defecto) disparan `collection_failure`, con enfriamiento de `collection_failure_cooldown_hours` (24 h) desde el último envío correcto y sin duplicar una pendiente.
- Cuenta como fallo una consulta fallida y una VNIC que nunca fue visible en consultas exitosas. Una VNIC visible que deja de aparecer no cuenta como fallo: se conserva `last_visible` y se emite un warning en la transición.
- Recuperación: si el error se envió, se envía una `collection_recovery` con el rango; si sigue en cola, se retira y se sustituye por una sola recuperación; si nunca se generó, solo se registra en log. Con `telegram.enabled=false` la alerta pendiente se retira igualmente, pero la recuperación solo se registra en log: no se encola nada mientras el canal está deshabilitado.
- Una VNIC nunca visible deja el acumulado `incomplete` y se avisa, pero no cambia el código de salida: solo una consulta fallida produce `3`.

### Códigos de salida y deadline

- Prioridad `2` configuración > `4` persistencia > `3` OCI > `5` Telegram. `6` concurrencia se devuelve solo, antes de leer estado. Todos los errores quedan en log.
- Deadline global de 4 min, con los últimos 10 s reservados para persistir; 30 s por consulta OCI y 45 s por mensaje, siempre recortados por el tiempo restante.
- La unidad systemd fija `TimeoutStartSec=5min`, por encima del deadline interno, para no matar el proceso entre una respuesta de Telegram y la escritura del estado. Ver [systemd, compilación y operación](#systemd-compilación-y-operación-hito-5).

## systemd, compilación y operación (Hito 5)

### Rutas de la instalación

```text
/usr/local/bin/oci-infrastructure-monitor          # 0755 root:root, binario estático
/etc/oci-infrastructure-monitor/
├── config.json                                    # 0640 root:oci-monitor
└── telegram.env                                   # 0600 oci-monitor:oci-monitor
/var/lib/oci-infrastructure-monitor/               # 0700 oci-monitor:oci-monitor
├── state.json                                     # 0600
├── lock                                           # 0600
└── history/YYYY-MM.json                           # 0600
/etc/systemd/system/oci-infrastructure-monitor.{service,timer}
```

El binario es estático (`CGO_ENABLED=0`), así que no depende de glibc ni de tener Go instalado en el servidor. La configuración y el estado son las únicas rutas que necesitan permisos especiales; el detalle de SELinux y de la instalación está en [`deployment.md`](deployment.md).

### Service

`systemd/oci-infrastructure-monitor.service`:

| Directiva | Valor | Efecto |
|---|---|---|
| `Type` | `oneshot` | Una ejecución por disparo; el timer decide cuándo |
| `User` / `Group` | `oci-monitor` | Sin privilegios; el binario nunca corre como root |
| `EnvironmentFile` | `-/etc/oci-infrastructure-monitor/telegram.env` | Credenciales en el entorno, fuera de `ExecStart`; opcional para poder arrancar con Telegram deshabilitado |
| `ExecStart` | `run --config … --telegram-env …` | Ciclo completo de 13 pasos |
| `StateDirectory` | `oci-infrastructure-monitor` | Crea `/var/lib/oci-infrastructure-monitor` con `0700` y la hace escribible bajo `ProtectSystem=strict` |
| `StandardOutput` / `StandardError` | `journal` | Logs a journald con `SyslogIdentifier=oci-infrastructure-monitor` |
| `TimeoutStartSec` | `5min` | Margen sobre el deadline interno de 4 min |
| `TimeoutStopSec` | `20s` | Espera tras `SIGTERM` antes de `SIGKILL`. Una parada puede cortar la ejecución; la escritura atómica evita dejar JSON a medias, no que la ejecución termine |

Endurecimiento: `NoNewPrivileges=yes`, `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `PrivateDevices=yes`, `ProtectKernelTunables/Modules/Logs`, `ProtectControlGroups`, `ProtectClock`, `ProtectHostname`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`, `RestrictNamespaces`, `RestrictRealtime`, `RestrictSUIDSGID`, `LockPersonality`, `MemoryDenyWriteExecute`, `SystemCallArchitectures=native`, `SystemCallFilter=@system-service`, `CapabilityBoundingSet=` vacío, `UMask=0077` y `RemoveIPC=yes`.

**Restricción deliberada:** no se usan `PrivateNetwork` ni `IPAddressDeny`. La aplicación necesita `169.254.169.254` para Instance Principals, los endpoints de OCI Monitoring y `api.telegram.org`. Cualquier endurecimiento futuro que filtre red debe incluir excepciones documentadas para esos destinos. `systemd-analyze security --offline` califica la unidad con exposición `1.6 OK` en la revisión local.

El `service` no tiene sección `[Install]`: solo se habilita el `timer`. Así se evita que `systemctl enable` sobre el servicio lo arranque en cada boot por error.

### Timer

`systemd/oci-infrastructure-monitor.timer`:

- `OnCalendar=*-*-* 00/6:00:00`: 00:00, 06:00, 12:00 y 18:00 de la hora local del servidor. Los buckets se guardan siempre en UTC.
- `RandomizedDelaySec=15min`: reparte la carga y evita que varias instancias consulten OCI en el mismo minuto.
- `AccuracySec=1min`.
- `Persistent=true`: si el servidor estaba apagado en el disparo, la ejecución se recupera al arrancar.
- `Unit=oci-infrastructure-monitor.service`, `WantedBy=timers.target`.

### Compilación y artefactos

La vía recomendada es el `Makefile`, que compila el binario ARM64 con el commit embebido y exige un árbol limpio:

```bash
make dist-monitor              # release ARM64; falla si hay cambios sin commitear
```

El equivalente manual es:

```bash
VERSION=$(git describe --tags --always)
COMMIT=$(git rev-parse --short=12 HEAD)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=true \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$DATE" \
  -o dist/oci-infrastructure-monitor-linux-arm64 ./cmd/monitor
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=true \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$DATE" \
  -o dist/oci-infrastructure-monitor-linux-amd64 ./cmd/monitor

sha256sum dist/oci-infrastructure-monitor-linux-* > dist/SHA256SUMS
sha256sum -c dist/SHA256SUMS
```

- `-trimpath` evita rutas de la máquina de compilación en el binario.
- `-s -w` elimina información de depuración; el binario sigue siendo localizable con `version`.
- `-X main.version/commit/buildDate` alimenta el comando `version`; `-buildvcs=true` graba además `vcs.revision`, `vcs.time` y `vcs.modified`. `version` los imprime y `go version -m` los muestra en el host de build.
- El servidor de despliegue no necesita Git ni Go: se consulta con `oci-infrastructure-monitor version`.
- `dist/` está en `.gitignore`: los binarios son artefactos de entrega, no código versionado.

### Operación y códigos

- `systemctl enable --now oci-infrastructure-monitor.timer` habilita solo el timer.
- `systemctl start oci-infrastructure-monitor.service` fuerza una ejecución manual; el `flock` evita el solapamiento con el timer.
- `systemctl list-timers oci-infrastructure-monitor.timer` y `journalctl -u oci-infrastructure-monitor.service` son las dos comprobaciones básicas.
- Cualquier código distinto de `0` deja la unidad en `failed`; el timer vuelve a intentarlo en la siguiente ventana. `6` (concurrencia) no es un fallo de datos.
- Un fallo de Telegram (`5`) no impide guardar el estado: la notificación queda en cola y se reintenta.

## IAM y compartimentos

- Autenticación exclusiva con **Instance Principals**; sin API keys ni archivos de usuario.
- La política actual es amplia: `Allow dynamic-group 'Default'/'BackupInstances' to read metrics in tenancy`.
- La política restrictiva recomendada es:

```text
Allow dynamic-group 'Default'/'BackupInstances' to read metrics in compartment <subnet_compartment> where target.metrics.namespace='oci_vcn'
```

- El detalle, los comandos de verificación del Dynamic Group y las limitaciones de `check` están en [`oci-permissions.md`](oci-permissions.md).

## Decisiones estructurales

- Un único binario, modular pero sin sobreingeniería.
- Interfaces mínimas para añadir recolectores y evaluadores sin reestructurar; OCI, Telegram, reloj y sistema de archivos detrás de interfaces para tests.
- Persistencia exclusiva en JSON: `state.json`, `lock` e `history/YYYY-MM.json`.
- Escritura atómica: temporal en el mismo directorio, `fsync`, renombrado, permisos 0600/0700, `flock`, detección de corrupción y `*.corrupt-<timestamp>` sin sobrescritura silenciosa.
- Mes calendario UTC. Los buckets no cruzan el cambio de mes.
- Un mes se finaliza con una consulta explícita; después su archivo es inmutable.
- La gracia de reconsulta es `requery_window`, no una constante separada.

## Riesgos y límites conocidos

- La semántica exacta del punto de 1 minuto (delta vs. acumulado) no está afirmada explícitamente; confirmar con tráfico conocido.
- La latencia de publicación no está documentada; el margen de 2 h es una suposición conservadora.
- No está documentado si los ceros se publican u omiten; la regla de rachas queda pendiente de evidencia.
- La ventana de actividad de `ListMetrics` no está documentada; no se usa para afirmar existencia.
- El tráfico registrado por VNIC no equivale al facturable; la aplicación lo advierte en README y alertas.
- La v1 asume que todas las VNIC configuradas están en el mismo compartimento de subred (`network.compartment_ocid`). Si alguna está en otro compartimento, la consulta no la verá; una versión futura puede mapear compartimento por VNIC.
- El endurecimiento de systemd se revisó con `systemd-analyze verify` y `systemd-analyze security --offline`, pero no se ejecutó en el servidor real. Una syscall fuera de `@system-service` o una dependencia futura que necesite memoria ejecutable daría `operation not permitted`; el diagnóstico está en [`troubleshooting.md`](troubleshooting.md).
- El binario `linux/arm64` se verificó con `file` y `sha256sum`, pero no se ejecutó en un host ARM64 desde el entorno de desarrollo. El binario `amd64` sí ejecutó `version` localmente.
- Los contextos SELinux, los permisos efectivos del usuario `oci-monitor` y el timer real no se validaron en el servidor; quedan en [`progress.md`](progress.md).

## Referencias

- [VNIC Metrics](https://docs.oracle.com/en-us/iaas/Content/Network/Reference/vnicmetrics.htm)
- [Monitoring Overview — Storage Limits](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Concepts/monitoringoverview.htm#Storage)
- [MQL Reference — Interval](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Reference/mql.htm#Interval)
- [Selecting a Time Range](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Tasks/query-metric-time-range.htm)
- [Listing Metric Definitions](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Tasks/list-metric.htm)
- [Securing Monitoring — IAM](https://docs.oracle.com/en-us/iaas/Content/Security/Reference/monitoring_security.htm#metric-groups)
- [IAM Verbs](https://docs.oracle.com/en-us/iaas/Content/Identity/policieshow/Verbs.htm)
- [Monitoring Pricing](https://www.oracle.com/cloud/cloud-native/monitoring/pricing/)
