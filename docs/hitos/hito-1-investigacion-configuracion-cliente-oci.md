# Hito 1 — Investigación, configuración y cliente OCI

- [x] **Hito 1 completado** — marcar solo cuando todos los entregables y criterios de cierre estén verificados.

> **Archivo autocontenido.** Incluye el contexto general y las secciones 0, 1, 2, 3 y 7.1 del roadmap. No necesitas abrir `Roadmap.md` para ejecutar este hito.
> **Depende de:** nada. **Habilita:** [Hito 2](hito-2-persistencia-acumulacion.md).
> Índice: [`../index.md`](../index.md) · Progreso: [`../progress.md`](../progress.md) · Decisiones: [`../decisions.md`](../decisions.md).

## 1. Rol y contexto de trabajo

Actúa como ingeniero de software senior especializado en Go, Oracle Cloud Infrastructure (OCI), Linux, systemd, observabilidad y seguridad.

**Nombre del proyecto:** `oci-infrastructure-monitor`

**Objetivo de la v1:** monitorear el tráfico de red saliente de una instancia OCI, acumular su consumo mensual y enviar alertas preventivas por Telegram.

**Objetivo a largo plazo:** poder añadir monitoreo de CPU, RAM, disco, servicios Linux, respaldos y almacenamiento OCI sin reestructurar el proyecto. No implementes esos módulos ahora ni crees paquetes vacíos para ellos.

### 1.1. Punto de partida

- **El proyecto ya está inicializado por mí** (repositorio y `go.mod`). No ejecutes `go mod init`.
- **Usa la versión de Go instalada en esta máquina.** Ejecuta `go version` al comenzar. No cambies la directiva `go` ni `toolchain` de `go.mod`, ni instales otra versión. Si alguna dependencia exige una versión superior, detente y avísame.
- Antes de escribir código, revisa el estado actual del repositorio (`ls`, `git log`, `go.mod`, `docs/progress.md` si existe) y continúa desde ahí.

### 1.2. Qué puedes validar y qué no

No tienes acceso a OCI, a Telegram ni al servidor de producción, y no debes conectarte por SSH ni modificarlo. La instalación la hará el responsable del proyecto manualmente.

| Lo valida el agente | Lo valida el usuario después del despliegue |
|---|---|
| Compilación, `go vet`, `go test`, `-race` | `check` contra OCI real |
| Lógica con mocks y servidores HTTP de prueba | Pertenencia de la instancia al Dynamic Group |
| Formato de mensajes, persistencia, recuperación | Envío real a Telegram |
| Sintaxis de unidades systemd (si hay herramienta) | Ejecución real del timer |

Nunca afirmes que una integración real funciona. Distingue siempre entre "probado con mocks" y "pendiente de validación real".

### 1.3. Reglas de trabajo

1. **No inventes** capacidades de APIs, unidades de métricas ni resultados de pruebas. Verifica en la documentación oficial.
2. **Detente y pregunta** únicamente si: no puedes determinar la semántica o unidad de una métrica, una decisión cambiaría el diseño de forma significativa, o hay un conflicto entre requisitos. Las decisiones técnicas menores las tomas tú.
3. **Registra cada decisión relevante** en `docs/decisions.md` (fecha, decisión, motivo, alternativas descartadas).
4. **Cada hito termina** con `go build ./...`, `go vet ./...` y `go test ./...` en verde, y con `docs/progress.md` actualizado.
5. No avances de hito con errores críticos sin resolver.
6. Dependencias: SDK oficial de OCI para Go (justificado) y librería estándar para todo lo demás. Cualquier otra dependencia requiere justificación en `decisions.md`.

## 2. Infraestructura objetivo

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

### 2.1. Autenticación

- Exclusivamente **OCI Instance Principals**. El SDK obtiene y renueva credenciales.
- Sin API Keys, sin archivos de credenciales personales, sin claves privadas.
- Existen: Dynamic Group `Default/BackupInstances` y la política `OCIInfrastructureMonitoringPolicy` (compartimento raíz) con la sentencia:

```text
Allow dynamic-group 'Default'/'BackupInstances' to read metrics in tenancy
```

- No asumas que la instancia pertenece al grupo. Documenta cómo verificarlo manualmente.
- En `docs/oci-permissions.md` propón una política más restrictiva (por compartimento o con condición sobre el namespace `oci_vcn`, verificando la sintaxis exacta en la documentación oficial) y explica cuándo conviene.
- Documenta en ese archivo la limitación de `check`: con solo `read metrics` no se distingue una VNIC inexistente de una sin actividad; la verificación de existencia real sería opt-in y requeriría permisos de red.
- Sin acceso de escritura a recursos OCI. No crear recursos OCI.

## 3. Arquitectura

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

## 4. Métricas de red

### 4.1. Investigación obligatoria (antes de implementar la recolección)

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
10. Si OCI publica puntos con valor 0 o los omite, porque de eso depende la distinción `no_data`/`incomplete`.
11. Si `ListMetrics` sobre `oci_vcn` está cubierto por `read metrics` y si la dimensión `resourceId` identifica la VNIC en este namespace.
12. Qué ventana de actividad usa `ListMetrics`: cuánto tiempo una VNIC inactiva sigue apareciendo, para persistir `last_visible`.

Si algo no coincide con lo esperado, busca una alternativa oficial y documenta la limitación.

### 4.2. Qué se contabiliza

- **Tráfico saliente:** todo lo que registre `VnicToNetworkBytes` en las VNIC configuradas, sin filtrar tráfico privado, interno, hacia servicios OCI ni hacia Internet. Estrategia conservadora: preferimos alertar de más que ignorar tráfico relevante.
- **Tráfico entrante (opcional):** `VnicFromNetworkBytes`, siempre separado. Nunca se suma al contador principal.
- Identificación por **OCID** (instancia y VNIC), configurables. No uses IPs como identificador principal.
- Con varias VNIC, evita duplicaciones por consultas repetidas o dimensiones superpuestas.

### 4.3. Tráfico registrado no es tráfico facturable

Documenta expresamente, en el README y en los mensajes de alerta:

- El tráfico de una VNIC no equivale necesariamente al facturable.
- El contador de una instancia no representa el total de la tenancy.
- Otros servicios pueden generar transferencias no visibles en la VNIC.
- Los reportes oficiales de facturación pueden tener retraso.
- El límite mensual configurable es una **referencia preventiva**, no un consumo facturable confirmado.
- La aplicación debe indicar qué recursos están incluidos en la medición y cuáles no.

Estos textos se materializan en el `README.md` (Hito 5) y en la plantilla de mensajes (Hito 4); en este hito solo se dejan como requisito documentado.

## 5. Configuración (sección 7.1)

Ejemplo conceptual (valores de ejemplo, no límites confirmados de la tenancy):

```json
{
  "oci": { "region": "us-ashburn-1", "auth": "instance_principal" },
  "network": {
    "enabled": true,
    "compartment_ocid": "",
    "instance_ocid": "",
    "vnic_ocids": [],
    "monthly_limit_gib": 10240,
    "alert_percentages": [50, 75, 90, 100],
    "high_traffic_6h_gib": 100,
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

Validación estricta: campos desconocidos rechazados, rangos válidos, OCIDs con formato correcto, rutas absolutas. `alert_percentages` debe tener exactamente 4 valores enteros, estrictamente crecientes, el primero mayor que 0 y el último igual a 100; la severidad se asigna por posición. `monthly_limit_gib` y `high_traffic_6h_gib` deben ser mayores que 0; `requery_window_hours`, `publication_latency_hours`, `no_data_streak_hours`, `collection_failure_threshold` y `collection_failure_cooldown_hours` deben ser mayores o iguales que 1. `compartment_ocid` es el compartimento de la subred donde viven las métricas de la VNIC. La unidad es siempre GiB (1 GiB = 2^30 bytes); no se admite GB decimal. La ruta por defecto del archivo es `/etc/oci-infrastructure-monitor/config.json` y se puede cambiar con `--config`.

## 6. Entregables

- [x] Investigación de la sección 4.1 documentada en `docs/architecture.md`, con los 12 puntos y enlaces a la fuente oficial.
- [x] `docs/oci-permissions.md` con la política restrictiva propuesta (por compartimento o con condición sobre `oci_vcn`), su sintaxis verificada, cuándo conviene y la limitación de `check`: sin permisos de red no se distingue una VNIC inexistente de una sin actividad; la verificación de existencia real queda como opt-in futuro.
- [x] Paquete `internal/config` con validación estricta y tests, incluida la ruta por defecto `/etc/oci-infrastructure-monitor/config.json` y el flag `--config`.
- [x] `configs/config.example.json` con valores de ejemplo, sin secretos, que valide con el paquete `config`.
- [x] Interfaz del cliente de OCI Monitoring y cliente con Instance Principals, con mock para tests.
- [x] Consultas con paginación (`ListMetrics`) y sin troceo en las ventanas previstas: 48 h y un mes a 1 h quedan muy por debajo de 100.000 datapoints.
- [x] Comando `check` que valida formato y visibilidad en métricas: OCID mal formado (sale `2`, configuración), VNIC visible en la retención o VNIC no visible (puede no existir, no tener métricas o no haber tenido tráfico; sale `0` con warning). Solo una consulta fallida sale `3`. Usa `ListMetrics` sin permisos nuevos; **no escribe configuración ni estado**.
- [x] Comando `version` con versión, arquitectura, SO y fecha de compilación inyectados con `-ldflags`.
- [x] `docs/progress.md` actualizado con resultados reales y supuestos pendientes.

## 7. Criterio de cierre

- [x] Tests de configuración y del cliente (con mocks) en verde.
- [x] El ejemplo de configuración valida sin errores y no contiene secretos.
- [x] La validación rechaza `alert_percentages` con distinto de 4 valores, valores no crecientes, primero 0 o último distinto de 100.
- [x] Límites, unidades, agregación y comportamiento de OCI con los ceros documentados con enlaces a la fuente oficial.
- [x] Documentado si `ListMetrics` sobre `oci_vcn` está cubierto por `read metrics` y si `resourceId` identifica la VNIC.
- [x] Documentada la ventana de actividad de `ListMetrics` (no hay una publicada; se documenta la limitación y el uso de `last_visible`).
- [x] `check` distingue OCID mal formado, VNIC visible en métricas y VNIC no visible, con el mensaje exacto de los tres casos.
- [x] Una VNIC no visible sale `0` con warning; un OCID mal formado sale `2`; solo una consulta fallida sale `3`.
- [x] El comando `check` no crea ni modifica archivos de estado ni de configuración (verificado por revisión: no importa persistencia).
- [x] No se añadieron dependencias fuera del SDK oficial de OCI y la librería estándar sin justificación en `decisions.md`.
- [x] `go build ./...`, `go vet ./...` y `go test ./...` ejecutados con resultado real reportado.
- [x] `go test -race ./...` ejecutado (config, app y cmd, 2026-11-07).
- [x] La casilla superior de este hito marcada solo después de todo lo anterior.

## 8. Evidencia y traspaso

Ejecuta y registra el resultado real:

```bash
go version
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Al cerrar, añade la entrada del Hito 1 en [`../progress.md`](../progress.md) con: qué se completó, qué quedó parcial, resultados de los comandos, decisiones nuevas, supuestos pendientes de validación real y qué debe hacer la siguiente sesión.

## 9. Fuera de alcance de este hito

- Persistencia, buckets, acumulación y comando `status` (Hito 2).
- Evaluadores de alertas (Hito 3).
- Envío por Telegram y ciclo completo de `run` (Hito 4).
- Service/timer systemd, binarios y documentación de despliegue (Hito 5).
- Cualquier operación de escritura sobre OCI o creación de recursos.

## 10. Siguiente hito

[Hito 2 — Persistencia y acumulación](hito-2-persistencia-acumulacion.md).
