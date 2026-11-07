# Decisiones del proyecto

> Reglas vigentes y su porqué. Al cerrar el Hito 5, estas reglas se mueven a [`architecture.md`](architecture.md) y este archivo queda como una lista corta de porqués. Las validaciones pendientes del Hito 1 viven en [`progress.md`](progress.md#validaciones-pendientes-del-hito-1).

## Cierre de mes y finalización

- **Decisión:** cada ejecución consulta desde `min(ahora − requery_window, bucket más antiguo que necesite atención)`, acotado por la retención de OCI. Un bucket necesita atención si es `provisional`, `incomplete` o falta. Un mes se cierra en la primera ejecución con consulta exitosa que cumpla `ahora >= fin de mes + requery_window` y cubra hasta el fin de mes; la consulta de finalización va desde el bucket más antiguo del mes que necesite atención hasta el fin de mes. Tras el cierre, el archivo es inmutable y las correcciones posteriores se descartan y se registran en log. La gracia real es `requery_window` (48 h por defecto), sin una constante de 7 días aparte. Un mes sin ejecuciones se recupera mientras los datos sigan en retención; solo lo irrecuperable se marca `incomplete`, visible en `status` y sin alerta nueva. Una alerta pendiente del mes anterior se envía con su período y el aviso de que se generó antes; solo se inhibe la reevaluación de umbrales del mes cerrado. Las correcciones durante la gracia actualizan el total, no reevalúan alertas y dejan en `status` el número de buckets corregidos y la diferencia total en bytes.
- **Motivo:** una gracia fija de 7 días no era compatible con la ventana de 48 h; con esta regla la gracia y la ventana son la misma cosa.
- **Consecuencias:** el estado debe recordar qué meses se finalizaron; la finalización es un paso explícito; hay que probar el mes sin ejecuciones y los datos fuera de retención.

## `no_data`, `incomplete` y visibilidad de VNIC

- **Decisión:** si OCI publica ceros explícitos, `no_data` exige que la VNIC tenga datos antes y después de la hora ausente. Si OCI omite los ceros y la VNIC es visible en `ListMetrics` durante la retención, la ausencia es `no_data`. Si la VNIC nunca fue visible, la ausencia es `incomplete` y cuenta para el error persistente. Solo el caso “visible antes y ahora ausente” es `no_data` con warning. Se persiste `last_visible` por VNIC, el warning se emite en la transición y `status` lo muestra mientras dure. La racha superior a `no_data_streak_hours` solo marca `incomplete` si OCI publica ceros. Una consulta fallida, un dato fuera de retención o una hora anterior a la VNIC siempre marcan `incomplete`.
- **Motivo:** si OCI omite ceros, una VNIC inactiva no tiene datapoints; marcarla incompleta para siempre sería un falso positivo. Pero una VNIC que nunca aparece puede ser un OCID mal escrito, y contar 0 en silencio sería peor.
- **Consecuencias:** el estado guarda `last_visible`; los tests cubren los cuatro casos (con datos, omitiendo ceros, visible antes, nunca visible).

## Ventana de publicación provisional

- **Decisión:** los buckets cuya hora termina dentro de las últimas `publication_latency_hours` (2 por defecto, configurable) se marcan `provisional`. Pasado ese margen, la reconsulta de 48 h los vuelve a evaluar y los sobrescribe.
- **Motivo:** OCI no publica una cifra de latencia para `oci_vcn`; 2 h es un margen conservador que evita dar por completos datos recientes.
- **Consecuencias:** la cifra se medirá en producción y se ajustará; un `provisional` no se usa para conclusiones definitivas de anomalía.

## Umbrales, unidad y presentación

- **Decisión:** `alert_percentages` sustituye a `warning_thresholds`. Debe haber exactamente 4 valores enteros, estrictamente crecientes, el primero mayor que 0 y el último igual a 100. La severidad se asigna por posición: informativa, advertencia, crítica y límite alcanzado. La unidad es siempre GiB (1 GiB = 2^30 bytes): `monthly_limit_gib` y `high_traffic_6h_gib`; se elimina la opción de GB decimales. La comparación usa bytes exactos; el porcentaje mostrado se trunca a un decimal, no se redondea, para que un 89,96 % no se muestre como “90 %” sin haber disparado la alerta crítica.
- **Motivo:** cuatro niveles fijos evitan una asignación de severidad ambigua; una sola unidad elimina casos límite y tests innecesarios; el truncado evita mensajes engañosos.
- **Consecuencias:** hay que migrar el nombre del campo en configuración, ejemplo, validación y documentación; los tests cubren el truncado.

## `check` sin permisos nuevos

- **Decisión:** no se añaden permisos IAM. `check` usa `ListMetrics` sobre `oci_vcn`, que está cubierto por `read metrics` (verificación en [`progress.md`](progress.md#validaciones-pendientes-del-hito-1)), y reporta tres estados: (1) OCID mal formado, validado localmente; (2) VNIC visible en métricas durante la retención; (3) VNIC no visible, con el mensaje exacto de que puede no existir, no tener métricas o no haber tenido tráfico. El caso (3) sale `0` con un warning muy visible; un OCID mal formado sale `2`; solo una consulta fallida sale `3`. `check` no escribe configuración ni estado. Una verificación de existencia real queda como opt-in futuro, documentada en `oci-permissions.md`, nunca como comportamiento por defecto.
- **Motivo:** ampliar la superficie IAM solo para un diagnóstico no compensa; `ListMetrics` da una señal útil con los permisos actuales, y el warning evita el falso “todo bien”.
- **Consecuencias:** `docs/oci-permissions.md` documenta la limitación y la opción opt-in; los tests cubren los tres estados de `check`.

## Lock, cola y contrato de configuración y salida

- **Decisión:** `run` y `--dry-run` usan `flock` advisory exclusivo y no bloqueante; `status` lee de forma atómica sin lock; `check` no usa lock. La cola de pendientes tiene máximo 50 elementos y caducidad de 7 días (fijas); al desbordarse o caducar se descarta lo más antiguo y se registra. Las alertas se colapsan por `(período, tipo)` al nivel más alto; al enviar el 90 % se registran como cruzados el 75 % y el 90 %. Configuración en `/etc/oci-infrastructure-monitor/config.json` con `--config`; `TELEGRAM_BOT_TOKEN` y `TELEGRAM_CHAT_ID` en `telegram.env`. Códigos de salida: `0` éxito, `2` configuración, `3` OCI, `4` persistencia, `5` Telegram, `6` concurrencia; `1` reservado. Con varios errores, el código final sigue la prioridad `2` > `4` > `3` > `5`, pero todos quedan en log; cualquier código distinto de `0` deja la unidad en `failed`. Un `run` con fallo de OCI que aun así envía pendientes sale `3`; un acumulado incompleto sale `0` con warning. El deadline global interno es de 4 min, menor que `TimeoutStartSec` de 5 min; cada llamada individual tiene un timeout menor y se reserva margen para persistir el estado. Un `429` con `retry_after` que no cabe en el deadline se persiste, se encola y sale `5`.
- **Motivo:** `flock` elimina el lock obsoleto; la cola acotada evita crecimiento sin control; el deadline evita que systemd mate el proceso a mitad de escritura; el contrato permite cerrar systemd, documentación y tests.
- **Consecuencias:** hay que probar la prioridad de códigos, el deadline y el colapso; documentar los códigos en el README.

## Errores persistentes, colapso y recuperación

- **Decisión:** 3 fallos consecutivos de OCI (configurable con `collection_failure_threshold`) disparan la alerta de error persistente; enfriamiento de 24 h entre repeticiones (configurable con `collection_failure_cooldown_hours`). Una VNIC que nunca fue visible cuenta como fallo de recopilación; una VNIC visible que deja de aparecer no cuenta como fallo. Los fallos de Telegram no cuentan para esa alerta: van a log y al código de salida. Recuperación: si la alerta de error ya se envió, se envía la recuperación; si sigue en cola, se elimina y se sustituye por una sola recuperación que menciona el rango de la falla; si nunca se generó, solo se registra en log. Las alertas de consumo anormal no se colapsan con las mensuales porque son otro tipo.
- **Motivo:** evitar ráfagas, recuperaciones sin contexto y alertas de error que no se pueden entregar por el mismo canal caído.
- **Consecuencias:** la cola guarda tipo, período y rango de la falla; hay que probar el reemplazo de error + recuperación por un solo mensaje.

## Casos de operación

- **Decisión:** `network.enabled=false` hace que `run` salga `0` sin consultar OCI. `telegram.enabled=false` evalúa y registra en log y `status`, pero no encola, no envía pendientes ni marca como enviado; la caducidad de 7 días sigue aplicando a la cola existente y, al habilitar, se colapsa lo vigente y se emite una sola alerta con el nivel actual. `test-telegram` funciona si hay credenciales, aunque esté deshabilitado. `status` muestra el mes actual y el anterior; si el anterior no está finalizado, lo marca como “no finalizado (en gracia)” con las correcciones acumuladas. Si una VNIC que antes era visible deja de aparecer en `ListMetrics`, se emite un warning en log y `status`; no se marcan buckets como `incomplete`. Una versión de esquema desconocida sale `4` sin sobrescribir. Si una VNIC falla y otras responden, la afectada queda `incomplete`, se continúa con las demás y se sale `3` con el total marcado como incompleto.
- **Motivo:** cerrar los casos de borde sin convertir cada uno en configuración nueva.
- **Consecuencias:** tests de `network.enabled`, `telegram.enabled`, `status` con mes en gracia, esquema desconocido y fallo parcial de VNIC.
