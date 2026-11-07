# Progreso del proyecto

> Registro de traspaso entre sesiones. Actualizar al cerrar cada hito, según la sección 9 del roadmap. No borrar entradas anteriores: añadir una entrada nueva por cierre.

## Estado general

| Campo | Valor |
|---|---|
| Hito en curso | Hito 1 — 1A (investigación) completado; 1B (config) y 1C (cliente/check/version) pendientes |
| Último hito cerrado | — |
| Última verificación ejecutada | 2026-11-07 — documentación; sin código Go todavía |
| Resultado de la verificación | H1A documentado; no se ejecutó `go build`/`go test` porque no hay paquetes |
| Pendiente de validación real | `check` contra OCI, Dynamic Group, Telegram, timer systemd |

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
