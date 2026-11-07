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

## Compartimento de las métricas

- **Decisión:** `network.compartment_ocid` identifica el compartimento de la subred donde viven las VNIC y sus métricas. En la v1 todas las VNIC configuradas deben estar en ese mismo compartimento; si alguna está en otro, `check` la reporta como no visible.
- **Motivo:** `SummarizeMetricsData` exige un compartimento y las métricas de una VNIC residen en el compartimento de su subred. Derivar el compartimento por VNIC requeriría permisos de red que no se conceden.
- **Consecuencias:** se documenta la limitación; una versión futura puede aceptar un compartimento por VNIC o consultar el árbol con permisos de tenencia.

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

## Persistencia: archivos y esquema (Hito 2, 2026-11-07)

- **Decisión:** `state.json` e `history/YYYY-MM.json` con `schema_version: 1`. El historial guarda `finalized`/`finalized_at` y es la fuente de verdad del cierre; `state.finalized_months` lo replica para planificar sin abrir historiales antiguos. Una versión desconocida se rechaza sin mover ni sobrescribir el archivo (código `4`). Un archivo corrupto se mueve a `*.corrupt-<timestamp UTC>` y el proceso falla con `4`; nunca se sobrescribe en silencio.
- **Motivo:** el cierre debe sobrevivir a un corte entre la escritura del historial (inmutable) y la del estado; si el historial manda, el estado se reconcilia solo en la siguiente ejecución.
- **Alternativas descartadas:** guardar el cierre solo en `state.json` (un estado perdido reabriría un mes inmutable); usar SQLite u otro motor (fuera de alcance); reescribir el archivo corrupto (pierde evidencia y puede perder datos).
- **Consecuencias:** dos escrituras atómicas en el cierre; `status` sigue mostrando el resto si un historial está corrupto y sale `4`. La lectura es estricta (`DisallowUnknownFields`): cualquier campo nuevo exige subir `schema_version` y migrar el estado; así una versión más nueva se rechaza sin cuarentena y una más vieja no pierde campos al reescribir.

## Hora del bucket y ventana de consulta (Hito 2, 2026-11-07)

- **Decisión:** el timestamp agregado de OCI marca el final de la ventana, así que un punto alineado a la hora se almacena en la hora anterior (`BucketHour`); un punto no alineado cae en la hora que lo contiene. La ventana de cada ejecución empieza en `min(ahora − requery_window, bucket más antiguo que necesite atención)`, acotada por el inicio del mes y por la retención, y termina en la hora completa (o en el fin de mes para la finalización).
- **Motivo:** evita duplicar o desplazar una hora y da un único criterio para la reconsulta de 48 h y para la recuperación tras un apagado.
- **Alternativas descartadas:** usar el timestamp tal cual (una hora de desfase); truncar siempre sin mirar la alineación (mezcla puntos de borde); consultar siempre el mes completo (gasto y latencia innecesarios).
- **Consecuencias:** los tests cubren puntos alineados y no alineados; si OCI cambiara la semántica, se revisa una sola función.

## Clasificación de horas sin datapoint (Hito 2, 2026-11-07)

- **Decisión:** una hora reciente (fin dentro de `publication_latency_hours`) es `provisional` aunque no tenga punto. Pasado el margen: con punto → `complete`; sin punto y VNIC visible → `no_data` (0); sin punto y VNIC nunca visible o hora anterior a su primera visibilidad → `incomplete`. Una respuesta con éxito que omite un punto ya confirmado no borra el dato: solo se reescriben las horas con punto recibido y las que son `provisional`, `incomplete` o faltan.
- **Motivo:** no declarar ceros antes de que OCI publique y no perder datos por una respuesta parcial o vacía; el acumulado nunca debe presentarse como exacto si no lo es.
- **Alternativas descartadas:** marcar `no_data` en cuanto falta el punto (adelanta un cero falso); sobrescribir todo el rango con la respuesta (una respuesta vacía borraría el mes); tratar la ausencia como 0 sin mirar visibilidad (un OCID mal escrito pasaría inadvertido).
- **Consecuencias:** el bucket guarda `captured_at`; `no_data` no invalida el acumulado; una racha solo invalida si OCI publica ceros. La política `PublishZeros` queda en `false` hasta la validación empírica y no es configuración de usuario: se ajusta en código cuando se confirme. Con `PublishZeros=false` (se asume que OCI omite los ceros) no se aplica la exigencia de datos antes y después; al activarla, esa exigencia y su test son parte del mismo cambio.

## Visibilidad de VNIC y warning (Hito 2, 2026-11-07)

- **Decisión:** la visibilidad se infiere de los datapoints, no de `ListMetrics`: se persisten `first_visible`, `last_visible`, `last_data_at` y `visible` por VNIC. Si una VNIC con datos previos no devuelve ninguno en una ejecución con al menos una consulta exitosa, se marca `visible=false`, se fija `warning_since` una sola vez y `status` muestra el aviso. Una VNIC que nunca apareció no genera warning: sus horas son `incomplete` y cuentan para el error persistente.
- **Motivo:** `ListMetrics` no prueba existencia y no tiene ventana de actividad documentada; los datapoints son la señal que ya se consulta y no requieren permisos nuevos.
- **Alternativas descartadas:** usar `ListMetrics` en `run` (permisos y semántica ya descartados en el Hito 1); alertar en cada ejecución (ruido); borrar los buckets confirmados de una VNIC que desaparece (perdería datos reales).
- **Consecuencias:** una VNIC con 48 h de tráfico cero puede parecer “desaparecida”; el aviso lo dice y `last_visible` da contexto. El warning se emite en la transición, no en cada ejecución.

## Correcciones y cierre de mes (Hito 2, 2026-11-07)

- **Decisión:** una corrección es un cambio de bytes de un bucket existente; se cuenta por mes en `state.months` con el delta acumulado. Durante la gracia (`requery_window`) actualiza el total y `status`; después del cierre el historial es inmutable, la corrección se descarta y se cuenta en `CollectReport.LateCorrections`, que `Collect` registra por `CollectOptions.Logf` cuando el llamador lo proporciona. Un mes se finaliza en la primera ejecución con consulta exitosa que cubra hasta el fin de mes y cumpla `ahora >= fin de mes + requery_window`; si no queda nada que recuperar, basta con marcarlo sin consultar.
- **Motivo:** evita reenviar o reevaluar meses cerrados y mantiene un criterio único para la gracia; la diferencia en bytes es la señal útil de una corrección.
- **Alternativas descartadas:** gracia fija de 7 días; reabrir meses finalizados; contar como corrección un cambio de estado sin cambio de bytes (inflaría el conteo con cada `provisional → complete`).
- **Consecuencias:** `status` muestra buckets corregidos y delta; las alertas del mes cerrado no se reevalúan (Hito 3).

## Qué meses se examinan y huecos irrecuperables (Hito 2, 2026-11-07)

- **Decisión:** se examinan los meses desde la última traza (`last_run` o `last_successful_query`) o el horizonte de retención de 90 días, lo que sea posterior, más cualquier mes con historial dentro de la retención. Una instalación nueva examina solo el mes en curso. Las horas anteriores al horizonte de retención se rellenan como `incomplete` y el resto del mes se consulta si los datos siguen en retención; el mes se finaliza aunque el acumulado quede incompleto.
- **Motivo:** recuperar lo que OCI todavía conserva sin inventar meses previos a la instalación ni crear meses fantasma; un hueco irrecuperable debe verse como incompleto, no como cero exacto.
- **Alternativas descartadas:** examinar siempre el mes anterior (una instalación nueva “recuperaría” un mes que nunca vigiló); ignorar los huecos fuera de retención (el acumulado se presentaría exacto); no finalizar nunca un mes con huecos (se reconsultaría para siempre).
- **Consecuencias:** el historial existente es el ancla de los meses que se recuperan; un mes totalmente anterior a la retención no se examina ni se marca.

## Lock y `status` (Hito 2, 2026-11-07)

- **Decisión:** `flock` exclusivo y no bloqueante con `syscall.Flock` (sin dependencia nueva) sobre `<state_dir>/lock`; `status` lee sin lock. `Collect` exige que el llamador tenga el lock; lo toma `run` en el Hito 4. `status` no escribe estado ni historial; la única escritura posible es la cuarentena de un archivo corrupto a `*.corrupt-<timestamp>`, tras la cual sale `4`. `--dry-run` carga estado e historial sin cuarentena (`persistence.LoadOptions{Quarantine:false}`). `status` sale `4` también con un esquema desconocido, pero sigue mostrando lo legible; sale `2` si la configuración es inválida.
- **Motivo:** no añadir dependencias, no crear locks obsoletos y no bloquear una lectura de diagnóstico.
- **Alternativas descartadas:** `gofrs/flock` (ya es indirecta, pero no hace falta); lock en `status` (bloquearía el diagnóstico); lock por archivo (el estado y el historial deben bloquearse juntos).
- **Consecuencias:** el test de concurrencia verifica la exclusión y que las escrituras atómicas concurrentes no dejan JSON inválido; el mapeo de `ErrLocked` a código `6` se completa en el Hito 4.

## Casos de operación

- **Decisión:** `network.enabled=false` hace que `run` salga `0` sin consultar OCI. `telegram.enabled=false` evalúa y registra en log y `status`, pero no encola, no envía pendientes ni marca como enviado; la caducidad de 7 días sigue aplicando a la cola existente y, al habilitar, se colapsa lo vigente y se emite una sola alerta con el nivel actual. `test-telegram` funciona si hay credenciales, aunque esté deshabilitado. `status` muestra el mes actual y el anterior; si el anterior no está finalizado, lo marca como “no finalizado (en gracia)” con las correcciones acumuladas. Si una VNIC que antes era visible deja de aparecer en `ListMetrics`, se emite un warning en log y `status`; no se marcan buckets como `incomplete`. Una versión de esquema desconocida sale `4` sin sobrescribir. Si una VNIC falla y otras responden, la afectada queda `incomplete`, se continúa con las demás y se sale `3` con el total marcado como incompleto.
- **Motivo:** cerrar los casos de borde sin convertir cada uno en configuración nueva.
- **Consecuencias:** tests de `network.enabled`, `telegram.enabled`, `status` con mes en gracia, esquema desconocido y fallo parcial de VNIC.
