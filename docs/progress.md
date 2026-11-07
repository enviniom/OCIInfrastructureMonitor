# Progreso del proyecto

> Registro de traspaso entre sesiones. Actualizar al cerrar cada hito, según la sección 9 del roadmap. No borrar entradas anteriores: añadir una entrada nueva por cierre.

## Estado general

| Campo | Valor |
|---|---|
| Hito en curso | — (contratos cerrados; implementación no iniciada) |
| Último hito cerrado | — |
| Última verificación ejecutada | — |
| Resultado de la verificación | — |
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

<!-- Añadir aquí cada cierre de hito, del más antiguo al más reciente. -->

## Validaciones pendientes del Hito 1

- [ ] ¿OCI publica puntos con valor 0 o los omite?
- [ ] ¿`ListMetrics` sobre `oci_vcn` está cubierto por `read metrics`?
- [ ] ¿La dimensión `resourceId` identifica la VNIC en este namespace?
- [ ] ¿Qué ventana de actividad usa `ListMetrics`? Es necesaria para `last_visible`.
- [ ] Retención, resolución mínima, latencia de publicación, agregación correcta y límites de consulta/paginación.
- [ ] ¿El rango de la consulta de finalización de mes cabe en los límites de OCI o requiere troceo?

## Pendientes de validación real

Estas comprobaciones no pueden hacerse desde el entorno de desarrollo y las valida el responsable del proyecto tras el despliegue:

- [ ] `oci-infrastructure-monitor check` contra OCI real.
- [ ] Pertenencia de la instancia al Dynamic Group `Default/BackupInstances`.
- [ ] Envío real a Telegram.
- [ ] Ejecución real del timer systemd.
- [ ] Comportamiento de SELinux con el binario y los directorios de estado.
