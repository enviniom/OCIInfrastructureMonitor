# Entrega final de la v1

Documento de cierre del Hito 5 y de la sección 11 del roadmap. Describe lo implementado, la evidencia real de las pruebas, los artefactos y lo que queda por validar en producción.

> **Fecha de cierre:** 2026-11-07.
> **Versión:** `1.0.0` (compilada desde el árbol de trabajo sobre el commit `8b0fce2`; incluye las correcciones de auditoría previas al commit del cierre: `test-telegram` sin cuarentena y sus tests).
> **Estado:** implementación y documentación cerradas y commiteadas. Nada se ha instalado ni desplegado en un servidor real desde este entorno.

## 1. Arquitectura implementada

Aplicación `oneshot` en Go, sin daemon ni base de datos. Un único binario ejecuta el ciclo de 13 pasos bajo `flock`, consulta OCI Monitoring con Instance Principals, normaliza los datapoints a buckets horarios UTC, persiste `state.json` e `history/YYYY-MM.json`, evalúa alertas y entrega por Telegram.

```text
systemd timer (cada 6 h)
        │
        ▼
oci-infrastructure-monitor run
        │  flock exclusivo
        ├─ config + logger + identidad OCI (169.254.169.254)
        ├─ leer estado e historial
        ├─ planificar ventana de 48 h / finalización de mes
        ├─ SummarizeMetricsData (sum por hora, oci_vcn)
        ├─ normalizar → buckets (provisional/complete/no_data/incomplete)
        ├─ persistir historial (atómico)
        ├─ acumular el mes
        ├─ vaciar cola de pendientes (persistir tras cada envío)
        ├─ evaluar umbrales mensuales + anomalía 6 h + salud de recopilación
        ├─ encolar, enviar y marcar solo tras éxito
        └─ guardar estado final
```

Componentes y responsabilidades:

| Paquete | Responsabilidad |
|---|---|
| `internal/config` | Carga JSON estricta, defaults y validación de OCIDs, umbrales, rutas y límites |
| `internal/oci` | Interfaz `Client` y cliente real de Monitoring con Instance Principals |
| `internal/persistence` | `state.json`, historial mensual, esquema v3, escritura atómica, `flock`, cuarentena de corruptos |
| `internal/accumulate` | Normalización a buckets, planificación de la ventana, resumen mensual y correcciones |
| `internal/alerts` | Evaluadores puros de umbrales y anomalía; nunca envían ni escriben |
| `internal/telegram` | Credenciales `0600`, transporte HTTP, reintentos, `429` y redacción del token |
| `internal/outbox` | Cola de 50 entradas, TTL de 7 días y colapso por `(kind, period)` |
| `internal/logging` | Logger con niveles y timestamps UTC |
| `internal/app` | `Check`, `Collect`, `Run` de 13 pasos, `BuildStatus` y mensajes en español |
| `cmd/monitor` | CLI: `run`, `check`, `status`, `test-telegram`, `version`, `--help` |

Reglas vigentes y su detalle: [`architecture.md`](architecture.md). Porqués: [`decisions.md`](decisions.md).

## 2. Estructura definitiva del proyecto

```text
.
├── cmd/
│   └── monitor/                  main.go, status.go y tests del CLI
├── configs/
│   └── config.example.json
├── dist/                         binarios + SHA256SUMS (no versionado)
├── docs/
│   ├── architecture.md
│   ├── decisions.md
│   ├── delivery.md
│   ├── deployment.md
│   ├── index.md
│   ├── oci-permissions.md
│   ├── progress.md
│   ├── troubleshooting.md
│   └── hitos/                    hito-1 … hito-5 (histórico)
├── internal/
│   ├── accumulate/
│   ├── alerts/
│   ├── app/
│   ├── config/
│   ├── logging/
│   ├── oci/
│   ├── outbox/
│   ├── persistence/
│   └── telegram/
├── systemd/
│   ├── oci-infrastructure-monitor.service
│   └── oci-infrastructure-monitor.timer
├── go.mod
├── go.sum
├── Roadmap.md
└── README.md
```

Métricas del árbol: 10 paquetes propios (`cmd/monitor` más 9 en `internal/`), 12 672 líneas de Go contando tests, 228 tests de nivel superior y 272 casos con subtests.

## 3. Funcionalidades completadas

- **Configuración estricta** con `DisallowUnknownFields`, defaults documentados, validación de OCIDs, cuatro umbrales crecientes, rutas absolutas y niveles de log.
- **OCI Monitoring** con Instance Principals, consultas agregadas `sum()` a 1 h, paginación de `ListMetrics`, timeout por consulta y compartimento de subred.
- **`check`** de solo lectura con tres estados: OCID mal formado (`2`), VNIC visible (`0`) y VNIC no visible (`0` con warning explícito).
- **Persistencia** atómica con `fsync` + `rename`, `0600`/`0700`, `flock` exclusivo no bloqueante, cuarentena de corruptos y esquema v3 con migración en memoria desde v1/v2.
- **Buckets horarios UTC** con ventana de reconsulta de 48 h, finalización de mes, meses inmutables, huecos irrecuperables, correcciones durante la gracia y detección de VNIC desaparecida/nunca visible.
- **`status`** en español, sin OCI ni escritura, con mes actual y anterior, estado del acumulado, buckets, correcciones y avisos.
- **Alertas** de cuatro umbrales mensuales (severidad por posición, porcentaje truncado), anomalía de 6 h con enfriamiento, error persistente de recopilación y recuperación en sus tres ramas.
- **Telegram** con API oficial, credenciales `0600`, 3 intentos con backoff, `429` con `retry_after`, `RetryAfterError` y redacción del token.
- **Cola de pendientes** de 50 entradas y 7 días, colapso por período y tipo, texto renderizado y reintento en la siguiente ejecución.
- **`run` de 13 pasos** con orden garantizado, persistencia tras cada envío, `--dry-run` sin escrituras, transición deshabilitado→habilitado y códigos de salida con prioridad.
- **systemd** con usuario dedicado, `StateDirectory`, journald, `TimeoutStartSec=5min`, endurecimiento compatible con el servicio de metadatos y timer de 6 h con `Persistent=true`.
- **Distribución** con binarios estáticos `linux/arm64` y `linux/amd64`, `version` con commit y fecha, y `SHA256SUMS`.

## 4. Resultados reales de las pruebas

Ejecutadas el 2026-11-07 en `linux/amd64` con `go1.27.3`:

| Comando | Resultado real |
|---|---|
| `go version` | `go version go1.27.3 linux/amd64` |
| `go build ./...` | Exit `0`, sin salida |
| `go vet ./...` | Exit `0`, sin advertencias |
| `go test ./...` | Exit `0`; 9 paquetes OK y `internal/oci` sin tests propios (se prueba con mock desde `app`) |
| `go test -race ./...` | Exit `0` en los 9 paquetes con tests (`internal/oci` no tiene tests propios) |
| `systemd-analyze verify` (service) | Exit `0` sobre una copia con `ExecStart` apuntando a `dist/oci-infrastructure-monitor-linux-amd64`; en un checkout limpio falla porque `/usr/local/bin/oci-infrastructure-monitor` no existe |
| `systemd-analyze verify` (timer) | Exit `0` |
| `systemd-analyze calendar '*-*-* 00/6:00:00'` | Normaliza a `*-*-* 00/6:00:00`; dispara a las 00/06/12/18, no cada 6 min |
| `systemd-analyze security --offline` | Exposición `1.6 OK` |
| `file` (arm64) | `ELF 64-bit LSB executable, ARM aarch64, statically linked, stripped` |
| `file` (amd64) | `ELF 64-bit LSB executable, x86-64, statically linked, stripped` |
| `sha256sum -c dist/SHA256SUMS` | Ambas sumas coinciden |
| `./dist/…-amd64 version` | `1.0.0`, commit `8b0fce2`, built `2026-11-07T22:15:20Z` |

Detalle de tests: 228 funciones `Test…` de nivel superior y 272 casos contando subtests. Cubren, entre otros:

- configuración (ejemplo, defaults, campos desconocidos, JSON inválido, umbrales, OCIDs, duplicados, rutas, niveles, red deshabilitada);
- persistencia (escritura atómica, `flock`, corrupción, esquema, migración, historial finalizado, concurrencia) y regresión de que `test-telegram` y `--dry-run` no mueven un corrupto;
- acumulación (duplicados, cambio de mes, huecos, `no_data`, finalización, correcciones, visibilidad);
- alertas (umbrales, múltiples cruces, reinicio mensual, anomalía, datos incompletos, enfriamiento);
- `run` de extremo a extremo con mocks (éxito, fallo de OCI, fallo parcial de VNIC, lock ocupado, cola llena/caducada, reintento, transición de Telegram, `--dry-run`, persistencia tras cada envío);
- Telegram con servidor HTTP simulado (`4xx`, `5xx`, `429` corto y largo, timeout, error de red, token ausente en logs);
- CLI (`check`, `status`, `version`, códigos de salida).

**Matiz de entorno:** el `GOCACHE` por defecto de esta máquina está en una ruta de solo lectura para el sandbox. `go test -race` falla al inicializar la caché (`failed to initialize build cache …: permission denied` en un directorio sin escritura) o, con una caché existente en un sistema de archivos de solo lectura, con el mensaje engañoso `runtime/race: package …: cannot find package`. Con `GOCACHE="$PWD/.tmp/gocache"` los cuatro comandos pasan desde caché vacía. Es una limitación del entorno de desarrollo, no del código; está documentada en el README.

### Probado con mocks frente a pendiente de validación real

| Probado con mocks / simulación | Pendiente de validación real |
|---|---|
| Cliente de Monitoring con `oci.Client` falso | `check` contra OCI y publicación real de métricas |
| Servidor HTTP simulado de Telegram | Envío real, `429` real y latencia real |
| `run` de extremo a extremo con reloj y store controlados | Timer systemd en el servidor |
| Escritura atómica, `flock` y cuarentena en el sistema de archivos local | Permisos y SELinux con el usuario `oci-monitor` |
| Compilación cruzada y checksums | Ejecución del binario ARM64 en la instancia |
| Sintaxis y endurecimiento de systemd con `systemd-analyze` | Arranque real bajo systemd con el endurecimiento |

## 5. Binarios y checksums SHA256

```text
dist/oci-infrastructure-monitor-linux-arm64   7,4 MiB   ELF aarch64
dist/oci-infrastructure-monitor-linux-amd64   8,0 MiB   ELF x86-64

4c101e640b661715e0c72d561252ee31189d0390ab1dcced3a81c227ed769260  dist/oci-infrastructure-monitor-linux-arm64
4dfef6b99ba503de2dac623b8cd5cdbc3d8d5b407d459100c1bb28b253e06ac9  dist/oci-infrastructure-monitor-linux-amd64
```

`dist/SHA256SUMS` contiene exactamente esas dos líneas y se verifica desde la raíz del repositorio:

```bash
sha256sum -c dist/SHA256SUMS
```

Los binarios se generaron con `CGO_ENABLED=0`, `-trimpath` y `-ldflags "-s -w -X main.version=1.0.0 -X main.commit=8b0fce2 -X main.buildDate=2026-11-07T22:15:20Z"`. `dist/` no se versiona.

## 6. Dependencias y justificación

| Dependencia | Tipo | Justificación |
|---|---|---|
| `github.com/oracle/oci-go-sdk/v65` v65.129.0 | Directa | SDK oficial; único modo razonable de usar Monitoring con Instance Principals y el permitido por el roadmap |
| `github.com/gofrs/flock` v0.10.0 | Indirecta | Arrastrada por el SDK; el proyecto usa `syscall.Flock` |
| `github.com/sony/gobreaker/v2` v2.4.0 | Indirecta | Arrastrada por el SDK |
| `github.com/youmark/pkcs8` | Indirecta | Arrastrada por el SDK |
| `golang.org/x/crypto` | Indirecta | Arrastrada por el SDK |
| `golang.org/x/sys` | Indirecta | Arrastrada por el SDK |

Todo lo demás es biblioteca estándar: `net/http`, `encoding/json`, `flag`, `context`, `os`, `syscall`, `testing` y `log`-propio. No se añadieron frameworks de bots, ORMs, utilidades de CLI ni dependencias de test.

## 7. Limitaciones técnicas

1. La semántica exacta del punto de 1 minuto (delta frente a acumulado) no está confirmada empíricamente; `sum()` por hora es una hipótesis de trabajo.
2. La latencia de publicación de OCI no está documentada; 2 h es un margen conservador que puede ajustarse.
3. No se sabe si `oci_vcn` publica ceros; `PublishZeros=false` y la invalidación por racha de `no_data` no está activa.
4. `ListMetrics` no prueba existencia: `check` avisa, no afirma que una VNIC no exista.
5. La v1 asume un único compartimento de subred para todas las VNIC.
6. El tráfico registrado no es el facturable; la app lo advierte en README y en las alertas.
7. Una VNIC con tráfico cero durante la retención puede parecer desaparecida; el aviso y `last_visible` dan contexto.
8. Si el proceso muere entre un envío aceptado y el guardado del estado, el reintento puede duplicar el mensaje.
9. El texto de una notificación está limitado a 4096 caracteres (los mensajes generados quedan muy por debajo).
10. El endurecimiento de systemd se validó con `systemd-analyze`, no en el servidor; una syscall no cubierta por `@system-service` daría `operation not permitted`.
11. El binario ARM64 se verificó con `file` y SHA256, pero no se ejecutó en un host ARM64 desde este entorno.
12. No se documenta soporte de proxy de salida; si se necesita, hay que verificarlo con el SDK y el cliente HTTP.

## 8. Permisos IAM necesarios

- **Autenticación:** Instance Principals; la instancia debe pertenecer al Dynamic Group `Default/BackupInstances` (o el que se configure).
- **Política actual (amplia):**

```text
Allow dynamic-group 'Default'/'BackupInstances' to read metrics in tenancy
```

- **Política restrictiva recomendada:**

```text
Allow dynamic-group 'Default'/'BackupInstances' to read metrics in compartment <subnet_compartment> where target.metrics.namespace='oci_vcn'
```

- `read metrics` incluye `inspect`, suficiente para `SummarizeMetricsData` y `ListMetrics`.
- No se conceden permisos de red ni de instancias. La verificación de existencia de una VNIC es opt-in y no forma parte del comportamiento por defecto.
- Procedimiento de verificación del Dynamic Group y de la política: [`oci-permissions.md`](oci-permissions.md).

## 9. Pasos manuales pendientes antes del despliegue

1. Verificar que la instancia pertenece al Dynamic Group y que el OCID coincide con su regla.
2. Aplicar la política restrictiva (o confirmar que la actual cubre el compartimento de la subred).
3. Compilar o descargar los binarios, verificar `SHA256SUMS` y copiar el de ARM64 si el servidor es `aarch64`.
4. Crear el usuario `oci-monitor` y los directorios con los permisos de [`deployment.md`](deployment.md).
5. Instalar el binario y las unidades systemd.
6. Copiar y editar `config.json` con OCIDs y límites reales; validar el JSON.
7. Crear `telegram.env` con `0600 oci-monitor:oci-monitor` y el token real.
8. Revisar los contextos SELinux (`bin_t`, `etc_t`, `var_lib_t`) y los AVC recientes.
9. Ejecutar `check` y confirmar que la VNIC es visible.
10. Ejecutar `test-telegram` y confirmar la recepción en el chat.
11. Ejecutar `run --dry-run` y revisar la simulación.
12. Ejecutar `run` real y revisar `status` y los permisos de `state.json`/`history`.
13. Habilitar solo el timer y comprobar `systemctl list-timers`.
14. Ajustar `publication_latency_hours`, umbrales y enfriamientos con el tráfico real.
15. Confirmar empíricamente la semántica de `sum()` y la publicación de ceros; si OCI publica ceros, activar `PublishZeros` y su exigencia de datos antes/después.
16. Revisar esta entrega y, si se decide retirar `Roadmap.md` o `docs/hitos/`, comprobar antes que `roadmap-final` apunta al commit del cierre.

## 10. Resumen de `docs/decisions.md`

La lista completa, con porqués y alternativas descartadas, está en [`decisions.md`](decisions.md). En una línea cada una:

1. Solo red y un binario `oneshot`: es el alcance del roadmap y evita daemon, base de datos e interfaz web.
2. Instance Principals: no hay API keys que rotar ni filtrar.
3. Un compartimento de subred: es donde viven las métricas y no se conceden permisos de red.
4. Bucket horario UTC con timestamp de fin de ventana: evita desfases y duplicados.
5. Una sola gracia de 48 h: la ventana de reconsulta y la gracia de cierre son la misma cosa.
6. Cierre explícito e historial inmutable: sobrevive a un corte entre escrituras y no reabre meses.
7. Clasificación por evidencia y visibilidad inferida de datapoints: no inventa ceros ni afirma existencia.
8. `PublishZeros=false`: no se asume una semántica no documentada.
9. Cuatro umbrales, severidad por posición, GiB y truncado: severidad inequívoca y mensajes no engañosos.
10. Solo el mes en curso, deduplicación doble y marca tras envío: ni duplicados ni alertas perdidas.
11. Anomalía de 6 h con enfriamiento y avisos: no silencia lo reciente ni lo presenta como firme.
12. Telegram oficial, token `0600` y redacción: el token en la URL es el riesgo principal.
13. Cola 50/7 días con colapso y texto persistido: entrega acotada y ventana original conservada.
14. JSON atómico y `flock` de la stdlib: sin dependencias ni archivos a medias.
15. Error persistente a los 3 fallos con recuperación: distingue inactividad de fallo sin permisos de red.
16. Códigos con prioridad y deadline de 4 min: un código único y sin morir a mitad de escritura.
17. Esquema v3 con migración y rechazo de versiones futuras: lectura estricta sin perder estado.
18. systemd con usuario dedicado y sin bloquear la red de OCI: mínimo privilegio compatible con los metadatos.
19. Compilación estática y artefactos fuera de git: reproducible, auditable y sin runtime en el servidor.
20. Documentación en español y tag `roadmap-final`: entrega al responsable y recuperación del roadmap.
