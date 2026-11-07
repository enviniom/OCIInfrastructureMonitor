# Proyecto: OCI Infrastructure Monitor

## 0. Rol y contexto de trabajo

Actúa como ingeniero de software senior especializado en Go, Oracle Cloud Infrastructure (OCI), Linux, systemd, observabilidad y seguridad.

**Nombre del proyecto:** `oci-infrastructure-monitor`

**Objetivo de la v1:** monitorear el tráfico de red saliente de una instancia OCI, acumular su consumo mensual y enviar alertas preventivas por Telegram.

**Objetivo a largo plazo:** poder añadir monitoreo de CPU, RAM, disco, servicios Linux, respaldos y almacenamiento OCI sin reestructurar el proyecto. No implementes esos módulos ahora ni crees paquetes vacíos para ellos.

### 0.1. Punto de partida

- **El proyecto ya está inicializado por mí** (repositorio y `go.mod`). No ejecutes `go mod init`.
- **Usa la versión de Go instalada en esta máquina.** Ejecuta `go version` al comenzar. No cambies la directiva `go` ni `toolchain` de `go.mod`, ni instales otra versión. Si alguna dependencia exige una versión superior, detente y avísame.
- Antes de escribir código, revisa el estado actual del repositorio (`ls`, `git log`, `go.mod`, `docs/progress.md` si existe) y continúa desde ahí.

### 0.2. Qué puedes validar y qué no

No tienes acceso a OCI, a Telegram ni al servidor de producción, y no debes conectarte por SSH ni modificarlo. La instalación la haré yo manualmente.

| Lo valida el agente | Lo valido yo después del despliegue |
|---|---|
| Compilación, `go vet`, `go test`, `-race` | `check` contra OCI real |
| Lógica con mocks y servidores HTTP de prueba | Pertenencia de la instancia al Dynamic Group |
| Formato de mensajes, persistencia, recuperación | Envío real a Telegram |
| Sintaxis de unidades systemd (si hay herramienta) | Ejecución real del timer |

Nunca afirmes que una integración real funciona. Distingue siempre entre "probado con mocks" y "pendiente de validación real".

### 0.3. Reglas de trabajo

1. **No inventes** capacidades de APIs, unidades de métricas ni resultados de pruebas. Verifica en la documentación oficial.
2. **Detente y pregúntame** únicamente si: no puedes determinar la semántica o unidad de una métrica, una decisión cambiaría el diseño de forma significativa, o hay un conflicto entre requisitos. Las decisiones técnicas menores las tomas tú.
3. **Registra cada decisión relevante** en `docs/decisions.md` (fecha, decisión, motivo, alternativas descartadas).
4. **Cada hito termina** con `go build ./...`, `go vet ./...` y `go test ./...` en verde, y con `docs/progress.md` actualizado (ver sección 9).
5. No avances de hito con errores críticos sin resolver.
6. Dependencias: SDK oficial de OCI para Go (justificado) y librería estándar para todo lo demás. Cualquier otra dependencia requiere justificación en `decisions.md`.

---

## 1. Infraestructura objetivo

| Parámetro | Valor |
|---|---|
| Proveedor | Oracle Cloud Infrastructure (Pay As You Go con recursos Always Free) |
| Región | `us-ashburn-1` |
| Instancia | `instance-20260506-1811` |
| Shape | `VM.Standard.A1.Flex` (4 OCPU ARM, 24 GB RAM) |
| SO | AlmaLinux 10.2, `aarch64` |
| VCN | `vcn-20210127-1644` |
| Compartment | `enviniom (root)` |
| Namespace de métricas | `oci_vcn` |
| Métrica de salida | `VnicToNetworkBytes` |
| Métrica de entrada | `VnicFromNetworkBytes` |

El servidor no tiene Go, OCI CLI ni Oracle Cloud Agent. Tiene Python 3.12, curl, jq y systemd, pero **ninguno debe ser dependencia de ejecución**. SELinux está activo en AlmaLinux: tenlo en cuenta en la documentación de despliegue (contextos de `/var/lib`, `/etc` y del binario).

### 1.1. Autenticación

- Exclusivamente **OCI Instance Principals**. El SDK obtiene y renueva credenciales.
- Sin API Keys, sin archivos de credenciales personales, sin claves privadas.
- Existen: Dynamic Group `Default/BackupInstances` y la política `OCIInfrastructureMonitoringPolicy` (compartimento raíz) con la sentencia:

```text
Allow dynamic-group 'Default'/'BackupInstances' to read metrics in tenancy
```

- No asumas que la instancia pertenece al grupo. Documenta cómo verificarlo manualmente.
- En `docs/oci-permissions.md` propón una política más restrictiva (por compartimento o con condición sobre el namespace `oci_vcn`, verificando la sintaxis exacta en la documentación oficial) y explica cuándo conviene.
- Sin acceso de escritura a recursos OCI. No crear recursos OCI.

---

## 2. Arquitectura

Un único ejecutable, modular pero sin sobreingeniería. Estructura sugerida (puedes simplificarla):

```text
oci-infrastructure-monitor/
├── cmd/monitor/main.go
├── internal/
│   ├── app/            # orquestación de una ejecución
│   ├── config/
│   ├── collectors/network/
│   ├── oci/            # cliente de Monitoring tras una interfaz
│   ├── alerts/
│   ├── telegram/
│   └── persistence/
├── configs/config.example.json
├── systemd/
├── docs/
├── dist/
└── README.md
```

- Interfaces mínimas para añadir luego nuevos recolectores y evaluadores de alertas.
- Toda dependencia externa (OCI, Telegram, reloj, sistema de archivos) detrás de una interfaz para poder usar mocks.
- No hay daemon: el proceso es `oneshot`, lo lanza un `systemd timer` cada 6 horas, hace su trabajo y termina. No asumas que se ejecuta exactamente a las 00:00, 06:00, 12:00 y 18:00.

---

## 3. Especificación: métricas de red

### 3.1. Investigación obligatoria (antes de implementar la recolección)

Verifica en la documentación oficial del SDK y de OCI Monitoring y documenta en `docs/architecture.md`:

1. Unidad exacta de cada métrica.
2. Semántica de los puntos publicados.
3. Agregación correcta para obtener bytes transferidos (no asumas que `sum` es correcto sin comprobarlo).
4. Resoluciones disponibles y la mínima utilizable.
5. Retención histórica.
6. Latencia de publicación.
7. Dimensiones para identificar VNIC e instancia.
8. Límites de consultas (rango, número de datapoints) y cómo paginar o trocear.
9. Límites gratuitos y costo de las solicitudes de Monitoring.

Si algo no coincide con lo esperado, busca una alternativa oficial y documenta la limitación.

### 3.2. Qué se contabiliza

- **Tráfico saliente:** todo lo que registre `VnicToNetworkBytes` en las VNIC configuradas, sin filtrar tráfico privado, interno, hacia servicios OCI ni hacia Internet. Estrategia conservadora: preferimos alertar de más que ignorar tráfico relevante.
- **Tráfico entrante (opcional):** `VnicFromNetworkBytes`, siempre separado. Nunca se suma al contador principal.
- Identificación por **OCID** (instancia y VNIC), configurables. No uses IPs como identificador principal.
- Con varias VNIC, evita duplicaciones por consultas repetidas o dimensiones superpuestas.

### 3.3. Tráfico registrado no es tráfico facturable

Documenta expresamente, en el README y en los mensajes de alerta:

- El tráfico de una VNIC no equivale necesariamente al facturable.
- El contador de una instancia no representa el total de la tenancy.
- Otros servicios pueden generar transferencias no visibles en la VNIC.
- Los reportes oficiales de facturación pueden tener retraso.
- El límite mensual configurable es una **referencia preventiva**, no un consumo facturable confirmado.
- La aplicación debe indicar qué recursos están incluidos en la medición y cuáles no.

---

## 4. Especificación: persistencia por buckets horarios

No uses SQLite, MariaDB, PostgreSQL, Redis ni otro motor. Solo archivos JSON.

### 4.1. Modelo

- La unidad de almacenamiento es el **bucket horario UTC**, identificado por la clave `(recurso OCID, dirección, hora UTC)`.
- Cada ejecución consulta una **ventana de reconsulta** (por defecto las últimas 48 h, configurable) y **sobrescribe por clave** los buckets recibidos. Así la deduplicación es automática y las correcciones tardías de OCI se absorben solas.
- Cada bucket guarda: bytes, marca de tiempo de captura, y un indicador `complete` / `incomplete` / `provisional`. Los buckets más recientes que la latencia de publicación se marcan `provisional`.
- **El acumulado mensual se calcula sumando los buckets del mes**; no se mantiene como contador incremental.
- Si tras un apagado prolongado hay horas anteriores a la ventana que aún están dentro de la retención de OCI, amplía la consulta para recuperarlas. Si ya no es posible recuperarlas, márcalas `incomplete` y el acumulado debe presentarse como **incompleto**, nunca como exacto.
- Los buckets horarios no cruzan el cambio de mes, así que el cambio de mes se resuelve por construcción.

### 4.2. Archivos

```text
/var/lib/oci-infrastructure-monitor/
├── state.json          # alertas enviadas, notificaciones pendientes, última ejecución/consulta exitosa, versión de esquema
├── lock                # bloqueo contra ejecuciones simultáneas
└── history/
    ├── 2026-11.json    # buckets del mes
    └── 2026-12.json
```

- Los meses anteriores son inmutables y **no se eliminan automáticamente**.
- Un archivo de historial corrupto solo afecta a ese mes.
- Todos los archivos incluyen versión de esquema.

### 4.3. Escritura segura

Archivo temporal en el mismo directorio, `fsync`, renombrado atómico, permisos restrictivos (0600 archivos, 0700 directorio), bloqueo exclusivo, detección de corrupción. **Nunca sobrescribas en silencio un archivo corrupto**: muévelo a `*.corrupt-<timestamp>`, registra el error y falla con código de salida explícito.

---

## 5. Especificación: alertas

### 5.1. Umbrales mensuales

| Consumo | Severidad |
|---|---|
| 50% | Informativa |
| 75% | Advertencia |
| 90% | Crítica |
| 100% | Límite alcanzado |

- Porcentaje sobre un límite mensual **configurable** (nunca codificado). La configuración debe indicar si se usan GB decimales o GiB binarios; por defecto GiB.
- Si se cruzan varios umbrales entre ejecuciones, envía **una** alerta con el nivel máximo y registra todos los umbrales cruzados.
- No repitas la misma alerta dentro de un período mensual. Los estados de alerta se reinician al comenzar el mes.
- Si el acumulado es incompleto, la alerta lo indica explícitamente.

### 5.2. Consumo anormal

- Alerta independiente sobre la suma de bytes salientes de las **últimas 6 horas completas** (usando los buckets), contra un umbral configurable en GiB.
- Enfriamiento configurable para no repetir notificaciones.
- Si la ventana contiene buckets `incomplete` o `provisional`, indícalo y no saques conclusiones indebidas (no dispares una alerta de "normalidad" ni de "anomalía" con datos insuficientes sin advertirlo).
- No asumas un valor universal de consumo normal. Dejar el diseño preparado para comparar luego contra una línea base histórica, sin implementarlo todavía.

---

## 6. Especificación: Telegram

- API oficial por HTTPS con `net/http`. Sin frameworks de bots.
- Token y Chat ID en `/etc/oci-infrastructure-monitor/telegram.env` (permisos restrictivos). Nunca en el repositorio, en argumentos CLI, en logs ni en mensajes de error.
- Mensajes en **español**. Tipos: informativa, advertencia, crítica, límite alcanzado, consumo anormal, error persistente de recopilación, recuperación del funcionamiento normal, y mensaje de prueba.
- Todo mensaje incluye: instancia, región, período, consumo acumulado, límite configurado, porcentaje, fecha de evaluación, tipo de medición, severidad, y la nota de que es tráfico registrado, no facturable.
- Fiabilidad: timeouts, reintentos con backoff, tratamiento de HTTP 429 (`retry_after`), errores de red, **cola de notificaciones pendientes en `state.json`**.
- **Una alerta solo se marca como enviada tras respuesta exitosa de Telegram.**
- Documenta que Telegram no garantiza entrega exactamente una vez; puede haber duplicados raros tras un fallo entre el envío y la persistencia.

---

## 7. Configuración, CLI y systemd

### 7.1. Configuración JSON

Ejemplo conceptual (valores de ejemplo, no límites confirmados de la tenancy):

```json
{
  "oci": { "region": "us-ashburn-1", "auth": "instance_principal" },
  "network": {
    "enabled": true,
    "instance_ocid": "",
    "vnic_ocids": [],
    "monthly_limit_gib": 10240,
    "warning_thresholds": [50, 75, 90, 100],
    "high_traffic_6h_gib": 100,
    "requery_window_hours": 48,
    "collect_inbound": false
  },
  "telegram": { "enabled": true },
  "storage": { "state_dir": "/var/lib/oci-infrastructure-monitor" },
  "logging": { "level": "info" }
}
```

Validación estricta: campos desconocidos rechazados, rangos válidos, umbrales ordenados y únicos, OCIDs con formato correcto, rutas absolutas.

### 7.2. CLI

```text
oci-infrastructure-monitor run [--dry-run]
oci-infrastructure-monitor status
oci-infrastructure-monitor check
oci-infrastructure-monitor test-telegram
oci-infrastructure-monitor version
oci-infrastructure-monitor --help
```

- `run`: consulta OCI, actualiza estado, evalúa alertas.
- `status`: muestra el estado persistido sin consultar OCI, indicando si el acumulado es incompleto.
- `check`: verifica autenticación, permisos, conectividad y disponibilidad de métricas **sin modificar el estado**.
- `test-telegram`: envía un mensaje de prueba explícito.
- `version`: versión, arquitectura, SO y fecha de compilación (inyectados con `-ldflags`).
- `--dry-run`: simula sin modificar estado ni enviar notificaciones.
- Códigos de salida distintos y documentados para: éxito, error de configuración, error de OCI, error de persistencia, error de Telegram, ejecución concurrente.

### 7.3. systemd

- `systemd/oci-infrastructure-monitor.service`: `Type=oneshot`, usuario dedicado sin privilegios, `EnvironmentFile` protegido, `StateDirectory`, journald, `TimeoutStartSec` adecuado, endurecimiento (`NoNewPrivileges`, `ProtectSystem`, `ProtectHome`, `PrivateTmp`, etc.).
- **Cuidado:** el endurecimiento no debe bloquear el acceso al servicio de metadatos de OCI (`169.254.169.254`), imprescindible para Instance Principals. No uses `PrivateNetwork` ni `IPAddressDeny` sin una excepción documentada para esa IP y para los endpoints de OCI y Telegram.
- `systemd/oci-infrastructure-monitor.timer`: cada 6 horas, `Persistent=true`, `RandomizedDelaySec` razonable.
- No instales ni habilites nada automáticamente. Documenta cómo comprobar timer y logs (`systemctl list-timers`, `journalctl -u ...`).

---

## 8. Hitos

Cada hito es autocontenido: puede ejecutarse en una sesión nueva leyendo solo `README.md`, `docs/progress.md`, `docs/decisions.md` y las secciones indicadas de este documento.

### Hito 1. Investigación, configuración y cliente OCI

**Lee:** secciones 0, 1, 2, 3, 7.1.
**Entregables:**
- Investigación de la sección 3.1 documentada en `docs/architecture.md` y `docs/oci-permissions.md`.
- Paquete `config` con validación estricta y tests.
- Interfaz y cliente de OCI Monitoring con Instance Principals (con mock para tests); consultas con troceo/paginación según los límites investigados.
- Comando `check` (sin modificar estado) y `version`.
**Criterio de cierre:** tests de configuración y del cliente (con mocks) en verde; límites, unidades y agregación documentados con enlaces a la fuente oficial.

### Hito 2. Persistencia y acumulación

**Lee:** secciones 0, 3.2, 4.
**Entregables:**
- Paquete `persistence`: `state.json`, historial mensual, escritura atómica, bloqueo, detección de corrupción.
- Lógica de buckets: sobrescritura por clave, ventana de reconsulta, recuperación tras apagado, marcado de `incomplete`/`provisional`, cálculo del acumulado, cambio de mes.
- Comando `status`.
**Criterio de cierre:** tests de duplicados, cambio de mes, recuperación, huecos irrecuperables, corrupción de archivos y concurrencia; `go test -race` ejecutado (o su limitación documentada).

### Hito 3. Alertas

**Lee:** secciones 0, 5, 4.1.
**Entregables:**
- Evaluador de umbrales mensuales y de consumo anormal en 6 h, con deduplicación, enfriamiento y manejo de datos incompletos.
- Interfaz de evaluadores extensible.
**Criterio de cierre:** tests de umbrales, múltiples umbrales cruzados, reinicio mensual, consumo anormal, datos incompletos y enfriamiento.

### Hito 4. Telegram y orquestación

**Lee:** secciones 0, 6, 7.2.
**Entregables:**
- Paquete `telegram` con reintentos, backoff, HTTP 429 y protección del token.
- Cola de notificaciones pendientes y reglas de "marcar enviada solo tras éxito".
- Orquestación completa del comando `run` (los 13 pasos del ciclo), `--dry-run` y `test-telegram`.
- Alerta de error persistente y de recuperación.
**Criterio de cierre:** tests con servidor HTTP simulado (errores, 429, timeouts, token ausente en logs); `run` de extremo a extremo con mocks.

### Hito 5. systemd, compilación y documentación

**Lee:** secciones 0, 7.3, 9, 10.
**Entregables:**
- Service y timer.
- Binarios y checksums.
- `README.md`, `docs/architecture.md`, `docs/oci-permissions.md`, `docs/deployment.md`, `docs/troubleshooting.md` en español, con comandos exactos para AlmaLinux 10.2 ARM64 (usuario dedicado, directorios, permisos, SELinux, instalación, actualización, desinstalación, verificación manual del Dynamic Group).
- Entrega final (sección 11).
**Criterio de cierre:** `go test ./...`, `go test -race ./...` y `go vet ./...` ejecutados con resultado real reportado; binarios verificados con `file`.

---

## 9. Protocolo de traspaso entre sesiones

Al **iniciar** una sesión: lee `README.md`, `docs/progress.md`, `docs/decisions.md` y solo las secciones del hito asignado. Ejecuta `go version`, `go build ./...` y `go test ./...` para confirmar el estado real antes de tocar nada.

Al **cerrar** un hito, actualiza `docs/progress.md` con:
- Qué se completó (y qué quedó parcial).
- Resultado real de los comandos de verificación.
- Decisiones nuevas (también en `decisions.md`).
- Supuestos pendientes de validar en producción.
- Qué debe leer y hacer la siguiente sesión.

---

## 10. Pruebas y compilación

- Pruebas unitarias sin acceso real a OCI ni Telegram, usando interfaces y mocks.
- Ejecutar `go test ./...`, `go test -race ./...`, `go vet ./...`. Si `-race` requiere CGO o una arquitectura distinta, documentarlo.
- No declares exitosa ninguna prueba que no hayas ejecutado.
- Compilación cruzada sin CGO con `-ldflags` para versión y fecha:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/oci-infrastructure-monitor-linux-arm64 ./cmd/monitor
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/oci-infrastructure-monitor-linux-amd64 ./cmd/monitor
```

- Genera `dist/SHA256SUMS` y verifica las arquitecturas con `file`.

---

## 11. Entrega final (al cerrar el Hito 5)

1. Resumen de la arquitectura implementada.
2. Estructura definitiva del proyecto.
3. Funcionalidades completadas.
4. Resultados reales de las pruebas, separando mocks de validaciones reales pendientes.
5. Binarios y checksums SHA256.
6. Dependencias utilizadas y su justificación.
7. Limitaciones técnicas encontradas.
8. Permisos IAM necesarios.
9. Pasos manuales pendientes antes del despliegue.
10. Resumen de `docs/decisions.md`.

**No instales, ejecutes ni despliegues la aplicación en el servidor OCI.** Prioriza fiabilidad, seguridad, simplicidad y bajo consumo de recursos. El resultado debe ser una aplicación de producción, no un prototipo.
