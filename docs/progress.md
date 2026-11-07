# Progreso del proyecto

> Registro de traspaso entre sesiones. Actualizar al cerrar cada hito, según la sección 9 del roadmap. No borrar entradas anteriores: añadir una entrada nueva por cierre.

## Estado general

| Campo | Valor |
|---|---|
| Hito en curso | Hito 3 — no iniciado |
| Último hito cerrado | Hito 2 — 2026-11-07 |
| Última verificación ejecutada | 2026-11-07 — `go build`, `go vet`, `go test` y `go test -race` |
| Resultado de la verificación | OK en `internal/config`, `internal/persistence`, `internal/accumulate`, `internal/app` y `cmd/monitor`; `internal/oci` sin tests propios |
| Pendiente de validación real | `check` contra OCI, Dynamic Group, Telegram, timer systemd, `status` con estado real y publicación de ceros |

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
