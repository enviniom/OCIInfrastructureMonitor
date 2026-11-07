# Arquitectura

> Fuente canónica del ciclo de ejecución y, al cerrar el Hito 5, de las reglas vigentes. Hasta entonces, el porqué de las reglas está en [`decisions.md`](decisions.md) y las verificaciones del Hito 1 en [`progress.md`](progress.md#validaciones-pendientes-del-hito-1).

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

Con `--dry-run`: se ejecutan los pasos de lectura y cálculo, pero no se escribe estado, no se encola ni se envía nada, y no se marca ninguna alerta como enviada.

## Decisiones estructurales

- Un único binario, modular pero sin sobreingeniería. Estructura de referencia en el Hito 1.
- Interfaces mínimas para añadir recolectores y evaluadores sin reestructurar; OCI, Telegram, reloj y sistema de archivos detrás de interfaces para tests.
- Persistencia exclusiva en JSON: `state.json`, `lock` e `history/YYYY-MM.json`.
- Escritura atómica: temporal en el mismo directorio, `fsync`, renombrado, permisos 0600/0700, `flock`, detección de corrupción y `*.corrupt-<timestamp>` sin sobrescritura silenciosa.
- Mes calendario UTC. Los buckets no cruzan el cambio de mes.
- Un mes se finaliza con una consulta explícita; después su archivo es inmutable.
- La gracia de reconsulta es `requery_window`, no una constante separada.

## Investigación de métricas (Hito 1)

Pendiente de completar en el Hito 1, con enlaces a la documentación oficial:

1. Unidad exacta de cada métrica.
2. Semántica de los puntos publicados.
3. Agregación correcta para obtener bytes transferidos.
4. Resoluciones disponibles y la mínima utilizable.
5. Retención histórica.
6. Latencia de publicación.
7. Dimensiones para identificar VNIC e instancia.
8. Límites de consulta y paginación.
9. Límites gratuitos y costo de Monitoring.
10. Si OCI publica ceros o los omite.
11. Si `ListMetrics` está cubierto por `read metrics` y si `resourceId` identifica la VNIC.
12. Qué ventana de actividad usa `ListMetrics`, para persistir `last_visible`.

## Reglas vigentes

Hasta el cierre del Hito 5, las reglas de buckets, cierre de mes, códigos de salida y colapso viven en [`decisions.md`](decisions.md). Al cerrar el Hito 5 se consolidan aquí y `decisions.md` queda como lista corta de porqués.
