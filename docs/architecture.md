# Arquitectura

> Fuente canónica del ciclo de ejecución y, al cerrar el Hito 5, de las reglas vigentes. Las reglas y su porqué están en [`decisions.md`](decisions.md); las comprobaciones que faltan, en [`progress.md`](progress.md#validaciones-pendientes-del-hito-1).

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

- Se usa intervalo y resolución de `1h`, con `startTime` y `endTime` alineados a horas UTC. El timestamp del punto agregado corresponde al **final** de la ventana; el `endTime` de la consulta es exclusivo.
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

## Referencias

- [VNIC Metrics](https://docs.oracle.com/en-us/iaas/Content/Network/Reference/vnicmetrics.htm)
- [Monitoring Overview — Storage Limits](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Concepts/monitoringoverview.htm#Storage)
- [MQL Reference — Interval](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Reference/mql.htm#Interval)
- [Selecting a Time Range](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Tasks/query-metric-time-range.htm)
- [Listing Metric Definitions](https://docs.oracle.com/en-us/iaas/Content/Monitoring/Tasks/list-metric.htm)
- [Securing Monitoring — IAM](https://docs.oracle.com/en-us/iaas/Content/Security/Reference/monitoring_security.htm#metric-groups)
- [IAM Verbs](https://docs.oracle.com/en-us/iaas/Content/Identity/policieshow/Verbs.htm)
- [Monitoring Pricing](https://www.oracle.com/cloud/cloud-native/monitoring/pricing/)
