# OCI Infrastructure Monitor

Aplicación `oneshot` en Go que mide el tráfico de red **saliente** de una o varias VNIC de Oracle Cloud Infrastructure (OCI), acumula el consumo por hora y por mes, y envía alertas preventivas por Telegram.

La v1 cubre solo red. CPU, RAM, disco, servicios Linux, respaldos y almacenamiento OCI quedan para fases posteriores.

> **Estado:** implementación de la v1 cerrada (hitos 1–5). Las pruebas se ejecutaron con mocks y un servidor HTTP simulado; las validaciones contra OCI, Telegram y el servidor real siguen pendientes y se listan en [`docs/progress.md`](docs/progress.md#pendientes-de-validación-real). No se ha desplegado nada en producción desde este repositorio.

## Advertencia: tráfico registrado no es tráfico facturable

La aplicación suma `VnicToNetworkBytes`, la métrica de la VNIC. Ese valor:

- incluye tráfico privado, interno, hacia servicios de OCI y hacia Internet;
- se mide **antes** de los descartes por security list;
- **no** coincide necesariamente con el consumo que OCI factura.

Úsala como alerta preventiva y como tendencia, no como la única fuente de facturación. Los totales pueden presentarse como `completo`, `provisional`, `incompleto` o `sin datos`; solo `completo` permite conclusiones firmes, `provisional` es una estimación y `incompleto` es un mínimo.

## Qué hace

- Consulta OCI Monitoring (`oci_vcn`, `VnicToNetworkBytes`, agregación `sum()` por hora UTC) con Instance Principals.
- Persiste un bucket por VNIC, dirección y hora en `history/YYYY-MM.json`, con escritura atómica y `flock`.
- Reconsulta 48 h para corregir datos recientes; cierra el mes cuando pasa la gracia y lo deja inmutable.
- Evalúa cuatro umbrales mensuales (`alert_percentages`) y un consumo anormal en 6 h, con deduplicación y enfriamiento.
- Encola las notificaciones pendientes (máximo 50, caducidad 7 días), las colapsa por período y tipo, y las reintenta.
- Alerta de error persistente de recopilación a los 3 fallos consecutivos y avisa de la recuperación.
- Ofrece `check`, `status`, `run`, `run --dry-run`, `test-telegram` y `version`.
- Se ejecuta desde systemd con un timer cada 6 horas.

## Requisitos

| Componente | Requisito |
|---|---|
| Servidor | Linux con systemd; AlmaLinux 10.2 ARM64 es el objetivo documentado (también hay binario `amd64`) |
| Go (solo para compilar) | `go 1.27.3`, tal como fija `go.mod`; no se cambia la directiva |
| OCI | Instancia con Instance Principals, Dynamic Group con la instancia y `read metrics` sobre `oci_vcn` |
| Telegram | Bot y Chat ID; el token se guarda en `telegram.env` con modo `0600` |
| Red | Salida HTTPS a los endpoints de OCI y `api.telegram.org`; acceso a `169.254.169.254` |

Guía completa, comandos exactos y SELinux: [`docs/deployment.md`](docs/deployment.md).
Permisos IAM y verificación del Dynamic Group: [`docs/oci-permissions.md`](docs/oci-permissions.md).

## Instalación rápida

Los pasos completos están en [`docs/deployment.md`](docs/deployment.md). Resumen para AlmaLinux 10.2 ARM64:

```bash
# 1. Usuario y directorios
sudo groupadd --system oci-monitor
sudo useradd --system --gid oci-monitor \
  --home-dir /var/lib/oci-infrastructure-monitor --shell /sbin/nologin oci-monitor
sudo install -d -m 0700 -o oci-monitor -g oci-monitor /var/lib/oci-infrastructure-monitor
sudo install -d -m 0750 -o root -g oci-monitor /etc/oci-infrastructure-monitor

# 2. Binario
sudo install -m 0755 -o root -g root \
  dist/oci-infrastructure-monitor-linux-arm64 /usr/local/bin/oci-infrastructure-monitor
sudo restorecon -v /usr/local/bin/oci-infrastructure-monitor

# 3. Configuración y credenciales
sudo install -m 0640 -o root -g oci-monitor \
  configs/config.example.json /etc/oci-infrastructure-monitor/config.json
sudo vi /etc/oci-infrastructure-monitor/config.json        # OCIDs y límites reales
sudo install -m 0600 -o oci-monitor -g oci-monitor /dev/null \
  /etc/oci-infrastructure-monitor/telegram.env
sudo vi /etc/oci-infrastructure-monitor/telegram.env       # TELEGRAM_BOT_TOKEN y TELEGRAM_CHAT_ID
sudo chown oci-monitor:oci-monitor /etc/oci-infrastructure-monitor/telegram.env
sudo chmod 0600 /etc/oci-infrastructure-monitor/telegram.env

# 4. systemd
sudo install -m 0644 systemd/oci-infrastructure-monitor.service /etc/systemd/system/
sudo install -m 0644 systemd/oci-infrastructure-monitor.timer   /etc/systemd/system/
sudo systemctl daemon-reload

# 5. Verificación manual antes de habilitar el timer
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor check
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor test-telegram
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run --dry-run
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run

# 6. Timer
sudo systemctl enable --now oci-infrastructure-monitor.timer
systemctl list-timers oci-infrastructure-monitor.timer
```

No se habilita el `service`: solo el timer. El `service` no tiene sección `[Install]` a propósito.

## Configuración

Archivo por defecto: `/etc/oci-infrastructure-monitor/config.json` (`--config` permite otra ruta). La lectura es estricta: un campo desconocido o un tipo incorrecto es un error de configuración (código `2`); no se ignoran campos en silencio.

```json
{
  "oci": { "region": "us-ashburn-1", "auth": "instance_principal" },
  "network": {
    "enabled": true,
    "compartment_ocid": "ocid1.compartment.oc1..…",
    "instance_ocid": "ocid1.instance.oc1.iad.…",
    "vnic_ocids": ["ocid1.vnic.oc1.iad.…"],
    "monthly_limit_gib": 10240,
    "alert_percentages": [50, 75, 90, 100],
    "high_traffic_6h_gib": 100,
    "high_traffic_cooldown_hours": 24,
    "requery_window_hours": 48,
    "publication_latency_hours": 2,
    "no_data_streak_hours": 6,
    "collection_failure_threshold": 3,
    "collection_failure_cooldown_hours": 24,
    "collect_inbound": false
  },
  "telegram": { "enabled": true },
  "storage": { "state_dir": "/var/lib/oci-infrastructure-monitor" },
  "logging": { "level": "info" }
}
```

Copia lista para editar: [`configs/config.example.json`](configs/config.example.json). El ejemplo usa OCIDs ficticios.

| Campo | Defecto | Regla |
|---|---|---|
| `oci.region` | — | Obligatorio; región de OCI (por ejemplo `us-ashburn-1`) |
| `oci.auth` | `instance_principal` | Único valor aceptado; no se admiten API keys |
| `network.enabled` | `true` | `false` no consulta OCI y `run` sale `0` |
| `network.compartment_ocid` | — | Compartimento de la **subred** de las VNIC; obligatorio si la red está habilitada |
| `network.instance_ocid` | — | OCID de la instancia; se usa para identificar el recurso en estado y mensajes |
| `network.vnic_ocids` | — | Al menos uno, sin duplicados, OCIDs válidos |
| `network.monthly_limit_gib` | — | Límite mensual en GiB (2^30); mayor que 0 si la red está habilitada |
| `network.alert_percentages` | `[50,75,90,100]` | Exactamente 4 enteros crecientes; el último debe ser 100; severidad por posición |
| `network.high_traffic_6h_gib` | — | Umbral de anomalía de 6 h en GiB; mayor que 0 si la red está habilitada |
| `network.high_traffic_cooldown_hours` | `24` | Enfriamiento de la anomalía, mínimo 1 |
| `network.requery_window_hours` | `48` | Ventana de reconsulta y gracia de cierre, mínimo 1 |
| `network.publication_latency_hours` | `2` | Margen para marcar buckets recientes como `provisional`, mínimo 1 |
| `network.no_data_streak_hours` | `6` | Racha de `no_data` que invalida el acumulado **solo** si OCI publica ceros (hoy `false`; ver `decisions.md`) |
| `network.collection_failure_threshold` | `3` | Fallos consecutivos antes de la alerta de recopilación, mínimo 1 |
| `network.collection_failure_cooldown_hours` | `24` | Enfriamiento de esa alerta, mínimo 1 |
| `network.collect_inbound` | `false` | Si es `true`, guarda también `VnicFromNetworkBytes` como informativo; nunca se suma al saliente |
| `telegram.enabled` | `true` | `false` evalúa y registra, pero no encola, no envía ni marca como enviado |
| `storage.state_dir` | `/var/lib/oci-infrastructure-monitor` | Ruta absoluta; debe ser escribible por el usuario del servicio |
| `logging.level` | `info` | `debug`, `info`, `warn` o `error` |

Los valores booleanos no distinguen “ausente” de `false`: `enabled` vale `true` por defecto, así que para deshabilitar la red o Telegram hay que escribir explícitamente `"enabled": false`.

### Credenciales de Telegram

`/etc/oci-infrastructure-monitor/telegram.env` (o `--telegram-env`) con:

```dotenv
TELEGRAM_BOT_TOKEN=123456789:AA...
TELEGRAM_CHAT_ID=123456789
```

- Modo `0600` y propietario del usuario del servicio (`oci-monitor`). La aplicación rechaza cualquier bit de grupo u otros, y además el usuario del servicio debe poder leerlo.
- Las variables de entorno no vacías tienen prioridad sobre el archivo; `systemd` las inyecta con `EnvironmentFile`.
- El token nunca se acepta por argumentos, no se escribe en log y se redacta de los errores de transporte.
- `test-telegram` funciona aunque `telegram.enabled` sea `false`.

## CLI

```text
oci-infrastructure-monitor run [--dry-run] [--config PATH] [--telegram-env PATH]
oci-infrastructure-monitor check [--config PATH]
oci-infrastructure-monitor status [--config PATH]
oci-infrastructure-monitor test-telegram [--config PATH] [--telegram-env PATH]
oci-infrastructure-monitor version | --version
oci-infrastructure-monitor --help
```

| Comando | Qué hace | Escribe estado |
|---|---|---|
| `run` | Ciclo completo de 13 pasos: lock, recopilación, historial, cola, alertas, envío | Sí, tras cada envío y al final |
| `run --dry-run` | Lee y calcula; informa de lo que se habría enviado | No escribe `state.json` ni historiales; puede crear el directorio de estado, `history/` y `lock` |
| `check` | Valida configuración, autenticación y visibilidad de las VNIC; no consulta estado | No |
| `status` | Muestra el estado persistido en texto, sin consultar OCI | No; informa de un archivo corrupto sin moverlo |
| `test-telegram` | Envía un mensaje de prueba explícito | No; lee el estado sin cuarentena |
| `version` | Versión, commit, fecha, Go y arquitectura; úsalo en el servidor para saber qué código está desplegado | No |

### Códigos de salida

| Código | Significado |
|---|---|
| `0` | Éxito (puede incluir warnings, por ejemplo un acumulado incompleto) |
| `2` | Configuración inválida o credenciales de Telegram ausentes/ilegibles |
| `3` | Fallo de OCI (autenticación, permisos, red o consulta) |
| `4` | Fallo de persistencia (permisos, corrupción, esquema desconocido) |
| `5` | Fallo de Telegram (transporte, `4xx`, `429` que no cabe en el deadline) |
| `6` | Ejecución concurrente: el `flock` está tomado |
| `1` | Reservado; hoy no se usa |

Con varios errores a la vez, `run` aplica la prioridad `2` > `4` > `3` > `5` y deja todos en el log. Un fallo de OCI no aborta: se envían los pendientes, el acumulado queda incompleto y se sale `3`. Cualquier código distinto de `0` deja la unidad systemd en `failed`.

## Operación

```bash
# Estado persistido (texto para humanos)
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor status

# Simulación sin escribir ni enviar
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run --dry-run

# Una ejecución manual (respeta el lock; no solapar con el timer)
sudo systemctl start oci-infrastructure-monitor.service
sudo systemctl status oci-infrastructure-monitor.service

# Timer y próxima ejecución
systemctl list-timers oci-infrastructure-monitor.timer
systemd-analyze calendar '*-*-* 00/6:00:00'

# Logs
journalctl -u oci-infrastructure-monitor.service -n 100 --no-pager
journalctl -u oci-infrastructure-monitor.service -p warning --since today
```

`status` muestra el directorio, el esquema, la última ejecución, la última consulta exitosa, los fallos consecutivos, las pendientes y, para el mes actual y el anterior, el saliente en GiB y bytes, el estado del acumulado, el conteo de buckets, el entrante informativo y las correcciones de la gracia.

Los buckets y los timestamps son UTC; el timer usa la hora local del servidor. Un mes finalizado no se modifica: si el proceso cae entre el cierre del historial y el guardado del estado, la ejecución siguiente adopta el cierre desde el historial.

### Saber qué commit está desplegado

El servidor no necesita Git ni Go: el binario lleva el commit embebido.

```bash
oci-infrastructure-monitor version
```

Salida típica:

```text
oci-infrastructure-monitor roadmap-final
commit: 6a077c084793
built: 2026-11-07T23:59:59Z
vcs.revision: 6a077c084793824b18f69392da36bf931969a80b
vcs.time: 2026-11-07T22:27:10Z
vcs.modified: false
go: go1.27.3
os/arch: linux/arm64
```

`version` es el `git describe` del build y `commit` el hash corto. El `Makefile` exige un árbol limpio, así que un release no debería mostrar `-dirty` ni `vcs.modified=true`. Si aparece `dev`/`unknown`, el binario se compiló sin `-X`: recompílalo con `make dist-monitor`.

Para ver qué cambios le faltan al binario, en el repositorio de desarrollo:

```bash
git log --oneline 6a077c084793..HEAD
git diff 6a077c084793..HEAD
```

## Estructura del proyecto

```text
.
├── cmd/monitor/              CLI: run, check, status, test-telegram, version
├── configs/
│   └── config.example.json   Ejemplo sin secretos ni OCIDs reales
├── dist/                     Binarios y SHA256SUMS (no versionado)
├── docs/
│   ├── architecture.md       Fuente canónica del ciclo y las reglas
│   ├── decisions.md          Porqués de las decisiones
│   ├── delivery.md           Entrega final (sección 11 del roadmap)
│   ├── deployment.md         Instalación, actualización y desinstalación
│   ├── oci-permissions.md    Instance Principals, IAM y Dynamic Group
│   ├── progress.md           Traspaso y evidencia real
│   ├── troubleshooting.md    Diagnóstico de fallos
│   └── hitos/                Un archivo por hito (histórico)
├── internal/
│   ├── accumulate/           Normalización a buckets, ventana, resumen mensual
│   ├── alerts/               Evaluadores puros (umbrales y anomalía)
│   ├── app/                  check, collect, run, status y mensajes
│   ├── config/               Carga estricta y validación
│   ├── logging/              Logger con niveles
│   ├── oci/                  Cliente de Monitoring con Instance Principals
│   ├── outbox/               Cola de pendientes, TTL y colapso
│   ├── persistence/          state.json, historial, esquema, escritura atómica y flock
│   └── telegram/             Credenciales y transporte HTTP
├── systemd/                  Unidades service y timer
├── Makefile                  Build ARM64 con commit embebido y verificación
├── Roadmap.md                Roadmap original (se conserva; ver tag roadmap-final)
├── go.mod / go.sum
└── README.md
```

## Desarrollo y compilación

```bash
go version          # debe ser go1.27.3
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Si `GOCACHE` apunta a una ruta sin escritura, Go falla al inicializar la caché (`failed to initialize build cache …: permission denied`) o, con una caché existente en un sistema de archivos de solo lectura, con el mensaje engañoso `runtime/race: package …: cannot find package`. Exporta una caché escribible:

```bash
export GOCACHE="$PWD/.tmp/gocache"
go test -race ./...
```

Compilación de release (ARM64, con commit embebido):

```bash
make dist-monitor              # release ARM64; exige árbol limpio
```

`make dist-monitor` compila `dist/oci-infrastructure-monitor-linux-arm64`, comprueba la arquitectura con `file`, imprime el `vcs.revision` del binario y genera `dist/SHA256SUMS`. Si hay cambios sin commitear, **falla**: un release no debe llevar un hash que no identifique el código. El Makefile inyecta `main.version`, `main.commit` y `main.buildDate` con `-X` y usa `-buildvcs=true`.

El equivalente manual es:

```bash
VERSION=$(git describe --tags --always)
COMMIT=$(git rev-parse --short=12 HEAD)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=true \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$DATE" \
  -o dist/oci-infrastructure-monitor-linux-arm64 ./cmd/monitor

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=true \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$DATE" \
  -o dist/oci-infrastructure-monitor-linux-amd64 ./cmd/monitor

file dist/oci-infrastructure-monitor-linux-*
sha256sum dist/oci-infrastructure-monitor-linux-* > dist/SHA256SUMS
sha256sum -c dist/SHA256SUMS
```

`go version -m` muestra el `vcs.revision` y `vcs.modified` del binario; `version` muestra el commit (en el servidor, que no tiene Go, es la vía). El Makefile es la vía recomendada porque verifica el commit y exige un árbol limpio.

`dist/` está en `.gitignore`: los binarios son artefactos de entrega, no código versionado.

### Dependencias

- **Directa:** `github.com/oracle/oci-go-sdk/v65` v65.129.0. Es el SDK oficial y el único modo razonable de usar Monitoring con Instance Principals; el roadmap lo permite explícitamente.
- **Indirectas del SDK:** `gofrs/flock`, `sony/gobreaker/v2`, `youmark/pkcs8`, `golang.org/x/crypto`, `golang.org/x/sys`. No se usan directamente; el `flock` del proyecto es `syscall.Flock` de la biblioteca estándar.
- Todo lo demás (HTTP, JSON, logging, tests, systemd) es biblioteca estándar. No se añadieron frameworks de bots, ORMs ni utilidades de CLI.

## Seguridad

- Instance Principals: no hay API keys ni archivos de usuario en el servidor.
- El token de Telegram vive en `telegram.env` con `0600`; no se acepta por argumentos ni se registra.
- La unidad systemd usa usuario dedicado, `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`, `StateDirectory`, `UMask=0077`, filtro de syscalls y un conjunto vacío de capacidades.
- **No** se usan `PrivateNetwork` ni `IPAddressDeny`: bloquearían `169.254.169.254` (Instance Principals), los endpoints de OCI y `api.telegram.org`.
- La configuración y el estado no se versionan. Solo se versiona `configs/config.example.json`.

## Limitaciones y pendientes de validación real

- La semántica exacta del punto de 1 minuto (delta frente a acumulado) no está confirmada empíricamente; el `sum()` por hora es una hipótesis de trabajo.
- La latencia de publicación es desconocida; 2 h es un margen conservador.
- No está confirmado si OCI publica ceros; por eso `PublishZeros` vale `false` y la regla de rachas de `no_data` no invalida el acumulado.
- `ListMetrics` no prueba existencia: `check` avisa, no afirma que una VNIC no exista.
- La v1 asume un único compartimento de subred (`network.compartment_ocid`) para todas las VNIC.
- El tráfico registrado no es el facturable.
- Si el proceso está caído más de un mes, `MonthlyThresholds` no alerta los umbrales del mes intermedio: solo evalúa el mes en curso. El total se reconstruye desde el historial al volver.
- Sin validar en producción: `check` contra OCI, Dynamic Group, envío real a Telegram, `429` real, timer real, SELinux y permisos del usuario del servicio.

Lista viva y evidencia: [`docs/progress.md`](docs/progress.md).

## Documentación

| Documento | Contenido |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | Ciclo de `run`, buckets, cierre, alertas, Telegram, systemd y reglas vigentes |
| [`docs/decisions.md`](docs/decisions.md) | Lista corta de decisiones y porqués |
| [`docs/deployment.md`](docs/deployment.md) | Instalación, actualización, desinstalación, permisos y SELinux |
| [`docs/troubleshooting.md`](docs/troubleshooting.md) | Fallos esperables y diagnóstico |
| [`docs/oci-permissions.md`](docs/oci-permissions.md) | IAM, Instance Principals y Dynamic Group |
| [`docs/delivery.md`](docs/delivery.md) | Entrega final de la v1 |
| [`docs/progress.md`](docs/progress.md) | Estado real, evidencia y pendientes |
| [`docs/index.md`](docs/index.md) | Índice del roadmap |
