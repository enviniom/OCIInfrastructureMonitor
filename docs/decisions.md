# Decisiones del proyecto

> Lista corta de porqués al cierre de la v1 (2026-11-07). El detalle operativo vive en [`architecture.md`](architecture.md); aquí queda qué se decidió, por qué y qué se descartó. Las validaciones pendientes están en [`progress.md`](progress.md).

## Alcance y datos

- **Solo red, un binario `oneshot`, sin daemon.** La v1 mide el saliente de la VNIC y alerta por Telegram; CPU, RAM, disco, servicios, respaldos y almacenamiento quedan para fases posteriores. **Porqué:** es el alcance cerrado del roadmap y evita una arquitectura de daemon, base de datos o interfaz web que todavía no se necesita. **Descartado:** recolectores vacíos o paquetes “para el futuro”; se añadirán cuando exista el requisito.

- **Instance Principals como única autenticación.** **Porqué:** no hay API keys ni archivos de usuario que rotar o filtrar en el servidor; el SDK obtiene credenciales del servicio de metadatos. **Descartado:** API keys y `~/.oci/config`, que añaden secretos persistentes y una rotación manual.

- **Un solo compartimento de subred para todas las VNIC.** **Porqué:** las métricas de una VNIC viven en el compartimento de su subred y `SummarizeMetricsData` exige uno; derivar el compartimento por VNIC requeriría permisos de red que no se conceden. **Consecuencia:** una VNIC de otro compartimento no se ve; queda documentado y una versión futura podrá mapearlo.

- **Bucket horario UTC y timestamp de fin de ventana.** **Porqué:** OCI marca el punto agregado al final del intervalo; asignarlo a la hora correcta evita un desfase de una hora y duplicados. **Descartado:** usar el timestamp tal cual o truncar siempre sin mirar la alineación.

- **Una sola gracia: `requery_window` (48 h).** **Porqué:** la gracia de cierre y la ventana de reconsulta son la misma cosa; una constante de 7 días aparte se contradecía con la ventana de 48 h. **Consecuencia:** un mes se cierra en la primera consulta exitosa que cubra hasta fin de mes y cumpla `ahora >= fin de mes + requery_window`.

- **Cierre explícito y mes inmutable; el historial es la fuente de verdad.** **Porqué:** si el proceso cae entre la escritura del historial y la de `state.json`, el historial manda y el estado se reconcilia solo; un mes cerrado no se reabre ni se reevalúa. **Descartado:** guardar el cierre solo en el estado (un estado perdido reabriría un mes inmutable) y reescribir archivos corruptos (pierde evidencia).

- **Clasificación de horas por evidencia, no por suposición.** `provisional`, `complete`, `no_data` e `incomplete`; la visibilidad se infiere de los datapoints, no de `ListMetrics`. **Porqué:** no se puede afirmar un cero antes de que OCI publique, y `ListMetrics` no prueba existencia ni tiene ventana de actividad documentada. **Descartado:** marcar `no_data` en cuanto falta el punto (cero falso) y tratar la ausencia como 0 sin mirar visibilidad (un OCID mal escrito pasaría inadvertido).

- **`PublishZeros=false` hasta tener evidencia empírica.** **Porqué:** la documentación no declara si `oci_vcn` publica ceros; asumirlo cambiaría la interpretación de las rachas. La rama de racha larga está implementada y probada, pero inactiva. **Consecuencia:** no invalida el acumulado por una racha de `no_data`; al confirmar la publicación de ceros, se activa junto con la exigencia de datos antes y después.

## Alertas y evaluación

- **Cuatro umbrales fijos, severidad por posición, unidad GiB y truncado a un decimal.** **Porqué:** cuatro niveles evitan una asignación ambigua de severidad; una sola unidad elimina casos límite; truncar evita mostrar “90 %” con 89,96 % sin haber disparado la alerta crítica. **Descartado:** GB decimales y redondeo del porcentaje.

- **Solo se evalúa el mes en curso; el mes en gracia actualiza su total pero no genera alertas nuevas.** **Porqué:** no se reabren meses cerrados y las correcciones no reevalúan alertas históricas. Una notificación pendiente del mes anterior sí se entrega, con su período y un aviso.

- **Deduplicación doble y marca solo tras un envío correcto.** Se consulta `alerts.monthly_alerts` y la cola pendiente del mismo `(período, tipo)`; `MarkNotified` se ejecuta después de la respuesta correcta de Telegram. **Porqué:** marcar al proponer la alerta perdería la notificación si el envío falla; no mirar la cola la duplicaría en el reintento. **Descartado:** colapsar por posición de severidad (menos informativo que el porcentaje).

- **Anomalía de 6 h con enfriamiento configurable y avisos de calidad.** La ventana son las seis horas completas anteriores; se dispara con el umbral en GiB y advierte si hay buckets `provisional`/`incomplete`/ausentes. **Porqué:** una anomalía reciente no debe silenciarse por la latencia de publicación, pero tampoco presentarse como conclusión firme. **Descartado:** exigir seis buckets `complete` (retrasaría la alerta y ocultaría la anomalía en curso) y fijar un valor universal de consumo normal.

- **Error persistente de recopilación a los 3 fallos, con recuperación contextual.** Una VNIC nunca visible cuenta como fallo; una que deja de aparecer no. **Porqué:** sin permisos de red no se distingue un OCID erróneo de una VNIC inactiva; la recuperación debe explicar el rango de la falla y no duplicarse. **Descartado:** contar la desaparición como fallo (falso positivo) y alertar en cada ejecución (ruido).

## Entrega y operación

- **Telegram por la API oficial con `net/http`; token en `telegram.env` 0600 y redacción de errores.** **Porqué:** el token forma parte de la URL y es el riesgo principal de fuga; el archivo con permisos estrictos y la redacción lo protegen. **Descartado:** token por argumentos (visible en `ps` y en el historial), frameworks de bots y `parse_mode=HTML` (más superficie sin necesidad).

- **Cola acotada a 50 entradas y 7 días, colapso por `(kind, period)` y texto ya renderizado.** **Porqué:** Telegram no garantiza entrega exactamente una vez; conservar el texto mantiene la ventana y los datos originales, y el TTL acota el estado y el ruido. **Descartado:** re-renderizar al enviar (perdería la ventana de la anomalía) y una cola en un archivo aparte (más estados inconsistentes).

- **Persistencia JSON con escritura atómica y `flock` de la biblioteca estándar.** Temporal + `fsync` + `rename` + `fsync` del directorio; permisos `0600`/`0700`; `syscall.Flock` exclusivo y no bloqueante. **Porqué:** no añadir dependencias, no crear locks obsoletos y no dejar JSON a medias. **Descartado:** `gofrs/flock` (es indirecta del SDK, pero no hace falta) y SQLite (fuera de alcance).

- **Diagnósticos de solo lectura.** `test-telegram`, `status` y `run --dry-run` leen estado e historial con la cuarentena desactivada; solo `run` real mueve un archivo corrupto a `*.corrupt-*`, y lo hace con el lock tomado. **Porqué:** un diagnóstico no debe alterar el estado que intenta diagnosticar, y `status` no toma el lock; si moviera un corrupto podría renombrar el estado fresco que `run` acaba de escribir. **Descartado:** que `status` aplique la cuarentena, aunque fuera con un lock compartido, porque añade contención y no es necesario para diagnosticar.

- **Códigos de salida con prioridad y deadline interno de 4 min.** `0` éxito, `2` configuración, `3` OCI, `4` persistencia, `5` Telegram, `6` concurrencia; prioridad `2` > `4` > `3` > `5`. **Porqué:** un fallo de OCI no debe impedir enviar los pendientes, pero el operador necesita un código único y predecible; el deadline menor que `TimeoutStartSec` evita que systemd mate el proceso entre el envío y el guardado. **Descartado:** matar el proceso a los 5 min (pierde la persistencia) y un deadline por comando (una consulta lenta consumiría el margen de las demás).

- **Esquema `3` con migración en memoria y rechazo de versiones desconocidas.** **Porqué:** la lectura estricta exige subir la versión por cada campo nuevo; migrar evita perder el estado de una instalación anterior y rechazar una versión futura evita sobrescribir datos que no se entienden. **Descartado:** aceptar campos nuevos sin subir la versión (rompe la detección) y reescribir un archivo corrupto en silencio.

- **Resumen diario dentro de `run`, sin timer propio ni cambio de esquema.** La ejecución cuya hora local coincide con `telegram.daily_report_hour` (18 por defecto; `-1` lo desactiva) envía el resumen del mes en curso y el anterior desde el estado recién persistido; `report` sigue existiendo como envío manual y para probar el mensaje. **Porqué:** reutiliza el timer de 6 h ya instalado y no amplía el esquema de `state.json`; el dato que hacía falta (el total del mes) ya está en el historial. **Descartado:** un timer diario aparte (más unidades que instalar y mantener), marcar el envío en `state.json` (obligaría a subir el esquema a v4 para una publicación periódica) y encolarlo (un resumen caducado no se reenvía). **Consecuencia:** un segundo `run` manual en la misma hora reenvía el resumen, y si el `run` de esa hora no ocurre (servidor apagado, lock ocupado) el resumen no sale ese día; `report` lo reenvía a mano.

- **systemd con usuario dedicado, timer de 6 h y endurecimiento sin bloquear la red de OCI.** `Type=oneshot`, `StateDirectory`, `ProtectSystem=strict`, `NoNewPrivileges`, `PrivateTmp`, `PrivateDevices`, filtro de syscalls y `TimeoutStartSec=5min`; **sin** `PrivateNetwork` ni `IPAddressDeny`. **Porqué:** mínimo privilegio sin romper `169.254.169.254` (Instance Principals), los endpoints de OCI ni `api.telegram.org`. **Consecuencia:** solo se habilita el timer; el `service` no tiene `[Install]`.

- **Compilación cruzada estática y artefactos separados.** `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X main.version/commit/buildDate"` para `linux/arm64` y `linux/amd64`, con `SHA256SUMS`. **Porqué:** el binario no depende de glibc ni de tener Go instalado en el servidor, la versión es auditable con `version` y los artefactos no se versionan (`dist/` en `.gitignore`).

- **Documentación en español y `git tag roadmap-final` antes de retirar el roadmap.** **Porqué:** la entrega es para el responsable del proyecto y el tag permite recuperar `Roadmap.md` y `docs/hitos/` si el equipo decide limpiarlos. La decisión de borrarlos no forma parte de este cierre.

## Referencias

- Reglas operativas y ciclo de 13 pasos: [`architecture.md`](architecture.md).
- Evidencia de pruebas y validaciones reales pendientes: [`progress.md`](progress.md).
- Permisos y Dynamic Group: [`oci-permissions.md`](oci-permissions.md).
- Instalación y diagnóstico: [`deployment.md`](deployment.md) y [`troubleshooting.md`](troubleshooting.md).
