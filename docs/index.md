# OCI Infrastructure Monitor — Índice del roadmap

Aplicación `oneshot` en Go para medir el tráfico de red saliente de una instancia de Oracle Cloud Infrastructure (OCI), acumular el consumo mensual y enviar alertas preventivas por Telegram. La v1 cubre solo red; CPU, RAM, disco, servicios Linux, respaldos y almacenamiento OCI quedan para fases posteriores.

Este índice es el punto de entrada al plan de trabajo. El roadmap original se conserva intacto en [`Roadmap.md`](../Roadmap.md) como referencia; si hay contradicción, prevalece el roadmap y hay que corregir estos archivos.

## Estado

- **Implementación:** no iniciada.
- **Repositorio:** existen `go.mod` (`go 1.27.3`) y `Roadmap.md`; git inicializado con el commit de documentación; aún no hay código ni `README.md`.
- [x] Roadmap dividido en 5 hitos autocontenidos (2026-11-07).
- [x] Índice de hitos con casillas de seguimiento.
- [x] Plantillas [`progress.md`](progress.md) y [`decisions.md`](decisions.md) creadas.
- [x] Contratos iniciales de diseño cerrados con el autor del roadmap (2026-11-07).
- [x] Hito 1A: investigación de OCI Monitoring y permisos documentada (2026-11-07). Ver [`architecture.md`](architecture.md) y [`oci-permissions.md`](oci-permissions.md).

## Cómo se usa este índice

1. Empieza por el hito pendiente de menor número cuyos prerrequisitos estén cumplidos.
2. Lee el archivo del hito completo: incluye el contexto y las secciones del roadmap que necesita.
3. Ejecuta `go version`, `go build ./...` y `go test ./...` antes de tocar nada y al cerrar.
4. Marca una casilla solo con evidencia real de la verificación. No la marques por haber escrito código.
5. Al cerrar un hito: marca las casillas del archivo del hito, actualiza este índice y registra la evidencia en [`progress.md`](progress.md), todo en el mismo cambio.
6. Haz un commit al cerrar cada hito. El repositorio se inicializa con `git init` y `.gitignore` excluye `dist/` y cualquier `*.env`.

### Convención de casillas

- `[ ]` pendiente.
- `[x]` cumplido y verificado.
- El detalle vive en el archivo del hito; este índice es el resumen.
- Un hito solo se marca cuando **todos** sus entregables y criterios de cierre están cumplidos.
- Las validaciones que requieren OCI, Telegram o el servidor reales quedan como **pendientes de validación real** y no se marcan desde aquí.

## Hitos

- [ ] **[Hito 1 — Investigación, configuración y cliente OCI](hitos/hito-1-investigacion-configuracion-cliente-oci.md).** Documenta unidades, semántica, agregación y límites de OCI Monitoring; implementa `config`, el cliente de Monitoring con Instance Principals, `check` y `version`.
- [ ] **[Hito 2 — Persistencia y acumulación](hitos/hito-2-persistencia-acumulacion.md).** Implementa `state.json`, historial mensual, buckets horarios, escritura atómica, bloqueo, corrupción y el comando `status`.
- [ ] **[Hito 3 — Alertas](hitos/hito-3-alertas.md).** Implementa umbrales mensuales y consumo anormal de 6 h, con deduplicación, enfriamiento y manejo de datos incompletos.
- [ ] **[Hito 4 — Telegram y orquestación](hitos/hito-4-telegram-orquestacion.md).** Implementa el cliente de Telegram, la cola de pendientes, el ciclo completo de `run`, `--dry-run` y `test-telegram`.
- [ ] **[Hito 5 — systemd, compilación y documentación](hitos/hito-5-systemd-compilacion-documentacion.md).** Cierra con service/timer, binarios, checksums, documentación de despliegue y entrega final.

## Mapa de hitos

| # | Hito | Archivo | Depende de | Cierre |
|---|---|---|---|---|
| 1 | Investigación, configuración y cliente OCI | [hito-1](hitos/hito-1-investigacion-configuracion-cliente-oci.md) | — | Tests de config y cliente con mocks; límites y unidades documentados con fuentes oficiales |
| 2 | Persistencia y acumulación | [hito-2](hitos/hito-2-persistencia-acumulacion.md) | Hito 1 | Tests de duplicados, cambio de mes, recuperación, huecos, corrupción y concurrencia |
| 3 | Alertas | [hito-3](hitos/hito-3-alertas.md) | Hito 2 | Tests de umbrales, múltiples cruces, reinicio mensual, anomalía, datos incompletos y enfriamiento |
| 4 | Telegram y orquestación | [hito-4](hitos/hito-4-telegram-orquestacion.md) | Hitos 2 y 3 | Tests con servidor HTTP simulado; `run` de extremo a extremo con mocks |
| 5 | systemd, compilación y documentación | [hito-5](hitos/hito-5-systemd-compilacion-documentacion.md) | Hitos 1–4 | `go test`, `go test -race` y `go vet` reales; binarios verificados; entrega final |

## Checklist de cierre global

Estos criterios aplican al proyecto completo, no a un hito aislado:

- [ ] `go build ./...` sin errores en cada cierre de hito.
- [ ] `go vet ./...` sin advertencias.
- [ ] `go test ./...` en verde.
- [ ] `go test -race ./...` ejecutado (o su limitación documentada).
- [ ] Cada cierre deja [`progress.md`](progress.md) actualizado con resultados reales.
- [ ] Cada decisión relevante está en [`decisions.md`](decisions.md).
- [ ] `check` contra OCI real validado por el usuario.
- [ ] Pertenencia de la instancia al Dynamic Group verificada por el usuario.
- [ ] Envío real a Telegram validado por el usuario.
- [ ] Timer systemd ejecutado y verificado en el servidor por el usuario.
- [ ] Binarios `linux/arm64` y `linux/amd64` generados y verificados con `file`.
- [ ] `dist/SHA256SUMS` generado y comprobado.
- [ ] Documentación final en español revisada: `README.md`, `architecture.md`, `oci-permissions.md`, `deployment.md`, `troubleshooting.md`.
- [ ] Entrega final de la sección 11 completa.

## Orden de ejecución

```text
Hito 1 → Hito 2 → Hito 3 → Hito 4 → Hito 5
```

- El Hito 2 necesita el cliente y la configuración del Hito 1.
- El Hito 3 necesita los buckets y el acumulado del Hito 2.
- El Hito 4 necesita los evaluadores del Hito 3 y el estado del Hito 2.
- El Hito 5 no empieza hasta que los cuatro anteriores estén cerrados.

## Documentos de estado

| Documento | Uso |
|---|---|
| [`index.md`](index.md) | Este índice: estado resumido y enlaces. |
| [`progress.md`](progress.md) | Traspaso entre sesiones: qué se hizo, qué falta y evidencia real. |
| [`decisions.md`](decisions.md) | Reglas vigentes y porqués. Al cerrar el Hito 5 queda como lista corta de porqués. |
| [`architecture.md`](architecture.md) | Fuente canónica del ciclo de `run` y de las reglas vigentes al cierre. La investigación del Hito 1A ya está documentada. |
| [`oci-permissions.md`](oci-permissions.md) | Instance Principals, política restrictiva y verificación del Dynamic Group. |
| [`hitos/`](hitos/) | Un archivo autocontenido por hito. |
| [`../Roadmap.md`](../Roadmap.md) | Roadmap original, intacto hasta el cierre; antes de retirarlo se crea `git tag roadmap-final`. |

Documentos que se crearán durante los hitos: `deployment.md` y `troubleshooting.md` (Hito 5); `README.md` (Hito 5).

## Contratos cerrados

Resumen de lo acordado con el autor del roadmap (detalle en [`decisions.md`](decisions.md)):

- **Ciclo de `run`:** lock antes de leer estado; vaciar pendientes antes de evaluar alertas nuevas; persistir `state.json` después de cada envío exitoso; un fallo de OCI no aborta la ejecución. Los 13 pasos viven en [`architecture.md`](architecture.md).
- **Cierre de mes:** cada ejecución consulta desde `min(ahora − requery_window, bucket más antiguo que necesite atención)`. Un mes se cierra en la primera consulta exitosa que cubra hasta el fin de mes y cumpla `ahora >= fin de mes + requery_window`; esa es la consulta de finalización. Después el archivo es inmutable. La gracia es `requery_window` (48 h por defecto), sin constante de 7 días.
- **Estados de bucket:** `complete`, `provisional`, `no_data` e `incomplete`. Una VNIC nunca visible es `incomplete` y cuenta para el error persistente; solo “visible antes y ahora ausente” es `no_data` con warning y `last_visible` persistido.
- **Umbrales:** cuatro niveles fijos (`alert_percentages`), severidad por posición, último valor 100. Comparación con bytes exactos y porcentaje mostrado truncado a un decimal.
- **Unidad:** solo GiB (`monthly_limit_gib`, `high_traffic_6h_gib`).
- **Errores:** 3 fallos consecutivos, enfriamiento 24 h, recuperación según la regla de colapso.
- **Lock y cola:** `run` y `--dry-run` con `flock` exclusivo; `status` atómico sin lock; `check` sin lock. Cola máxima 50, caducidad 7 días, colapso por `(período, tipo)` al nivel más alto.
- **Contrato:** configuración en `/etc/oci-infrastructure-monitor/config.json` con `--config`; variables `TELEGRAM_BOT_TOKEN` y `TELEGRAM_CHAT_ID`; códigos 0/2/3/4/5/6 con prioridad 2 > 4 > 3 > 5 y todos los errores en log. Deadline interno de 4 min y `TimeoutStartSec` de 5 min.
- **`check`:** sin permisos nuevos; usa `ListMetrics` sobre `oci_vcn`. OCID mal formado → `2`; VNIC visible → `0`; VNIC no visible → `0` con warning muy visible (“puede no existir, no tener métricas o no haber tenido tráfico”); consulta fallida → `3`.
- **`status`:** mes actual y anterior; si el anterior no está finalizado, se muestra como “no finalizado (en gracia)” con las correcciones acumuladas.
- **Telegram con `enabled=false`:** evalúa y registra, pero no encola, no envía pendientes ni marca como enviado. La caducidad de 7 días sigue aplicando a la cola existente; al habilitar se colapsa lo vigente y se emite una sola alerta con el nivel actual.
- **Fallo parcial de VNIC:** la afectada queda `incomplete`, se continúa con las demás y se sale `3` con el total incompleto.
- **Entrada y git:** `docs/index.md` hasta el README del Hito 5; `git init` y commit por hito.

## Verificaciones pendientes (Hito 1)

La lista de comprobaciones vive en [`progress.md`](progress.md#validaciones-pendientes-del-hito-1): publicación de ceros, `ListMetrics`, `resourceId`, cobertura de `read metrics` y ventana de actividad.

## Detalles menores por decidir en implementación

- Formato de salida de `status` (texto para humanos; un flag JSON queda para el futuro).
- Cómo se persiste la marca de “mes finalizado” en `state.json` o en el historial.
- Redacción exacta de los mensajes de `check` y de la alerta pendiente del mes anterior.

## Decisión sobre el roadmap original

`Roadmap.md` se conserva intacto durante la implementación. Al cerrar el Hito 5, antes de retirar el roadmap y los hitos, se crea `git tag roadmap-final` para conservarlos. Hasta entonces, la entrada es [`index.md`](index.md).

## Verificación rápida

```bash
go version
go build ./...
go vet ./...
go test ./...
go test -race ./...
```
