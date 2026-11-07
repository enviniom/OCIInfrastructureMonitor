# OCI Infrastructure Monitor — Índice del roadmap

Aplicación `oneshot` en Go para medir el tráfico de red saliente de una instancia de Oracle Cloud Infrastructure (OCI), acumular el consumo mensual y enviar alertas preventivas por Telegram. La v1 cubre solo red; CPU, RAM, disco, servicios Linux, respaldos y almacenamiento OCI quedan para fases posteriores.

Este índice es el mapa de hitos y estado del proyecto. Para instalar u operar la aplicación, empieza por [`README.md`](../README.md). El roadmap original se conserva intacto en [`Roadmap.md`](../Roadmap.md) como referencia; si hay contradicción, prevalece el roadmap y hay que corregir estos archivos.

## Estado

- **Implementación:** Hito 5 cerrado; v1 completa. `internal/config`, cliente OCI, `check`, `version`, persistencia, buckets horarios, `status`, evaluadores de alertas, `internal/telegram`, cola de pendientes, `run`, `--dry-run`, `test-telegram`, esquema `state.json` v3, unidades systemd, binarios y documentación de despliegue.
- **Repositorio:** `go.mod` (`go 1.27.3`), `Roadmap.md`, `README.md`, `systemd/`, `dist/` (no versionado) y git inicializado. Hito 5 cerrado y commiteado; validaciones reales pendientes.
- [x] Roadmap dividido en 5 hitos autocontenidos (2026-11-07).
- [x] Índice de hitos con casillas de seguimiento.
- [x] Plantillas [`progress.md`](progress.md) y [`decisions.md`](decisions.md) creadas.
- [x] Contratos iniciales de diseño cerrados con el autor del roadmap (2026-11-07).
- [x] Hito 1A: investigación de OCI Monitoring y permisos documentada (2026-11-07). Ver [`architecture.md`](architecture.md) y [`oci-permissions.md`](oci-permissions.md).
- [x] Hito 1B: configuración estricta, ejemplo y tests de `internal/config` (2026-11-07).
- [x] Hito 1 cerrado: cliente OCI, `check`, `version`, tests con mocks y `go test -race` (2026-11-07).
- [x] Hito 2 cerrado: `internal/persistence`, `internal/accumulate`, `app.Collect`, comando `status`, tests de duplicados, cambio de mes, finalización, correcciones, huecos, `no_data`, corrupción y concurrencia (2026-11-07).
- [x] Hito 3 cerrado: `internal/alerts`, evaluadores de umbrales mensuales y de consumo anormal de 6 h, deduplicación, enfriamiento, avisos de datos incompletos/provisionales y esquema `state.json` v2 con migración desde v1 (2026-11-07).
- [x] Hito 4 cerrado: `internal/telegram`, `internal/outbox`, `internal/logging`, ciclo completo de `run` con lock, cola, persistencia tras cada envío, error persistente y recuperación, `--dry-run`, `test-telegram`, mensajes en español y esquema v3 con migración desde v1/v2 (2026-11-07).
- [x] Hito 5 cerrado: unidades systemd con timer de 6 h, binarios `linux/arm64` y `linux/amd64` verificados con `file`, `dist/SHA256SUMS`, `README.md`, `docs/deployment.md`, `docs/troubleshooting.md`, `docs/delivery.md`, reglas consolidadas en `architecture.md`, `decisions.md` reducido a porqués y `git tag roadmap-final` (2026-11-07).

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

- [x] **[Hito 1 — Investigación, configuración y cliente OCI](hitos/hito-1-investigacion-configuracion-cliente-oci.md).** Documenta unidades, semántica, agregación y límites de OCI Monitoring; implementa `config`, el cliente de Monitoring con Instance Principals, `check` y `version`.
- [x] **[Hito 2 — Persistencia y acumulación](hitos/hito-2-persistencia-acumulacion.md).** Implementa `state.json`, historial mensual, buckets horarios, escritura atómica, bloqueo, corrupción y el comando `status`.
- [x] **[Hito 3 — Alertas](hitos/hito-3-alertas.md).** Implementa umbrales mensuales y consumo anormal de 6 h, con deduplicación, enfriamiento y manejo de datos incompletos.
- [x] **[Hito 4 — Telegram y orquestación](hitos/hito-4-telegram-orquestacion.md).** Implementa el cliente de Telegram, la cola de pendientes, el ciclo completo de `run`, `--dry-run` y `test-telegram`.
- [x] **[Hito 5 — systemd, compilación y documentación](hitos/hito-5-systemd-compilacion-documentacion.md).** Cierra con service/timer, binarios, checksums, documentación de despliegue y entrega final.

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

- [x] `go build ./...` sin errores en cada cierre de hito.
- [x] `go vet ./...` sin advertencias.
- [x] `go test ./...` en verde.
- [x] `go test -race ./...` ejecutado (o su limitación documentada).
- [x] Cada cierre deja [`progress.md`](progress.md) actualizado con resultados reales.
- [x] Cada decisión relevante está en [`decisions.md`](decisions.md).
- [ ] `check` contra OCI real validado por el usuario.
- [ ] Pertenencia de la instancia al Dynamic Group verificada por el usuario.
- [ ] Envío real a Telegram validado por el usuario.
- [ ] Timer systemd ejecutado y verificado en el servidor por el usuario.
- [x] Binarios `linux/arm64` y `linux/amd64` generados y verificados con `file`.
- [x] `dist/SHA256SUMS` generado y comprobado.
- [x] Documentación final en español escrita y revisada por el agente: `README.md`, `architecture.md`, `oci-permissions.md`, `deployment.md`, `troubleshooting.md`, `delivery.md`.
- [x] Entrega final de la sección 11 completa.
- [x] Commit del Hito 5 realizado.

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
| [`index.md`](index.md) | Este índice: mapa de hitos, estado resumido y enlaces. |
| [`../README.md`](../README.md) | Entrada para instalar, configurar y operar la aplicación. |
| [`progress.md`](progress.md) | Traspaso entre sesiones: qué se hizo, qué falta y evidencia real. |
| [`decisions.md`](decisions.md) | Lista corta de porqués; el detalle operativo vive en `architecture.md`. |
| [`architecture.md`](architecture.md) | Fuente canónica del ciclo de `run`, las reglas vigentes y la operación systemd. |
| [`oci-permissions.md`](oci-permissions.md) | Instance Principals, política restrictiva y verificación del Dynamic Group. |
| [`deployment.md`](deployment.md) | Instalación, actualización, desinstalación, permisos y SELinux en AlmaLinux 10.2 ARM64. |
| [`troubleshooting.md`](troubleshooting.md) | Fallos esperables, códigos de salida y diagnóstico. |
| [`delivery.md`](delivery.md) | Entrega final de la sección 11: artefactos, evidencia y pendientes reales. |
| [`hitos/`](hitos/) | Un archivo autocontenido por hito (histórico). |
| [`../Roadmap.md`](../Roadmap.md) | Roadmap original, intacto; el tag `roadmap-final` permite recuperarlo. |

Documentos creados en el Hito 5: [`../README.md`](../README.md), [`deployment.md`](deployment.md), [`troubleshooting.md`](troubleshooting.md) y [`delivery.md`](delivery.md).

## Contratos cerrados

Resumen de lo acordado con el autor del roadmap (detalle en [`decisions.md`](decisions.md)):

- **Ciclo de `run`:** lock antes de leer estado; vaciar pendientes antes de evaluar alertas nuevas; persistir `state.json` después de cada envío exitoso; un fallo de OCI no aborta la ejecución. Los 13 pasos viven en [`architecture.md`](architecture.md).
- **Cierre de mes:** cada ejecución consulta desde `min(ahora − requery_window, bucket más antiguo que necesite atención)`. Un mes se cierra en la primera consulta exitosa que cubra hasta el fin de mes y cumpla `ahora >= fin de mes + requery_window`; esa es la consulta de finalización. Después el archivo es inmutable. La gracia es `requery_window` (48 h por defecto), sin constante de 7 días.
- **Estados de bucket:** `complete`, `provisional`, `no_data` e `incomplete`. Una VNIC nunca visible es `incomplete` y cuenta para el error persistente; solo “visible antes y ahora ausente” es `no_data` con warning y `last_visible` persistido.
- **Umbrales:** cuatro niveles fijos (`alert_percentages`), severidad por posición, último valor 100. Comparación con bytes exactos y porcentaje mostrado truncado a un decimal.
- **Alertas:** evaluadores puros sobre los buckets persistidos; solo se evalúa el mes en curso; se registran los umbrales nuevos al confirmar el envío; la anomalía de 6 h usa enfriamiento configurable y advierte de ventanas provisionales/incompletas.
- **Unidad:** solo GiB (`monthly_limit_gib`, `high_traffic_6h_gib`).
- **Errores:** 3 fallos consecutivos, enfriamiento 24 h, recuperación según la regla de colapso.
- **Lock y cola:** `run` y `--dry-run` con `flock` exclusivo; `status` atómico sin lock; `check` sin lock. Cola máxima 50, caducidad 7 días, colapso por `(período, tipo)` al nivel más alto, con el texto renderizado persistido para una entrega posterior.
- **Contrato:** configuración en `/etc/oci-infrastructure-monitor/config.json` con `--config`; variables `TELEGRAM_BOT_TOKEN` y `TELEGRAM_CHAT_ID`; códigos 0/2/3/4/5/6 con prioridad 2 > 4 > 3 > 5 y todos los errores en log. Deadline interno de 4 min y `TimeoutStartSec` de 5 min.
- **`check`:** sin permisos nuevos; usa `ListMetrics` sobre `oci_vcn`. OCID mal formado → `2`; VNIC visible → `0`; VNIC no visible → `0` con warning muy visible (“puede no existir, no tener métricas o no haber tenido tráfico”); consulta fallida → `3`.
- **`status`:** mes actual y anterior; si el anterior no está finalizado, se muestra como “no finalizado (en gracia)” con las correcciones acumuladas.
- **Telegram con `enabled=false`:** evalúa y registra, pero no encola, no envía pendientes ni marca como enviado. La caducidad de 7 días sigue aplicando a la cola existente; al habilitar se colapsa lo vigente y se emite una sola alerta con el nivel actual.
- **Fallo parcial de VNIC:** la afectada queda `incomplete`, se continúa con las demás y se sale `3` con el total incompleto.
- **Telegram (transporte):** API oficial con `net/http`; credenciales en `telegram.env` con permisos `0600`; timeout por intento, 3 intentos con backoff, `429` con `retry_after` y redacción del token en errores y logs. Un `429` que no cabe en el deadline deja la cola persistida y sale `5`.
- **Entrada y git:** `README.md` es la entrada desde el Hito 5; `docs/index.md` queda como mapa de hitos. `git init` y un commit por hito; el Hito 5 se commitea al cerrar.

## Validación real pendiente

Los hitos 1–5 cerraron con tests, mocks y un servidor HTTP simulado para Telegram; el Hito 5 añadió la validación estática de systemd y los binarios. No se ha ejecutado nada contra OCI, Telegram ni el servidor real. Quedan para después del despliegue: `check` contra OCI, pertenencia al Dynamic Group, política restrictiva, publicación de ceros, semántica delta/`sum()`, latencia real, formato real de los timestamps agregados, envío real a Telegram, `429` real, permisos reales de `telegram.env`, SELinux, ejecución del binario ARM64, endurecimiento systemd y el timer. La lista viva está en [`progress.md`](progress.md).

## Detalles menores por decidir en implementación

- La redacción de la notificación pendiente del mes anterior quedó resuelta en el Hito 4: conserva su período y lleva un `AVISO` con la fecha de generación.
- Flag JSON de `status`: queda para el futuro; la v1 es texto para humanos.

## Decisión sobre el roadmap original

`Roadmap.md` se conserva intacto. En el cierre del Hito 5 se creó `git tag roadmap-final` antes de que el equipo decida retirar el roadmap o los hitos; retirarlos no forma parte de este cierre. El tag `roadmap-final` se recrea sobre el commit del cierre (`git tag -f -a roadmap-final -m "Roadmap y docs/hitos del cierre de la v1"`) antes de retirar cualquier archivo. La entrada operativa es [`../README.md`](../README.md) y el mapa de hitos sigue en este índice.

## Verificación rápida

```bash
go version
go build ./...
go vet ./...
go test ./...
go test -race ./...

sha256sum -c dist/SHA256SUMS
file dist/oci-infrastructure-monitor-linux-*
systemd-analyze verify systemd/oci-infrastructure-monitor.timer
```
