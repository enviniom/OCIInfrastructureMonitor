# Hito 5 — systemd, compilación y documentación

- [x] **Hito 5 completado** — marcar solo cuando todos los entregables y criterios de cierre estén verificados.

> **Estado del cierre:** entregables y criterios verificados. El cierre se registra en el commit del Hito 5; las casillas reflejan la implementación y la verificación.

> **Archivo autocontenido.** Incluye el contexto general y las secciones 0, 7.3, 9, 10 y 11 del roadmap. No necesitas abrir `Roadmap.md` para ejecutar este hito.
> **Depende de:** Hitos [1](hito-1-investigacion-configuracion-cliente-oci.md), [2](hito-2-persistencia-acumulacion.md), [3](hito-3-alertas.md) y [4](hito-4-telegram-orquestacion.md). **Cierra:** el proyecto.
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
- Este hito asume cerrados los cuatro hitos anteriores. Revisa `docs/progress.md` para confirmar el estado real.

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

## 2. systemd (sección 7.3)

### 2.1. Service

`systemd/oci-infrastructure-monitor.service` debe incluir:

- `Type=oneshot`.
- Usuario dedicado sin privilegios.
- `EnvironmentFile` protegido para las credenciales de Telegram.
- `StateDirectory` para `/var/lib/oci-infrastructure-monitor`.
- `ExecStart` con `run --config /etc/oci-infrastructure-monitor/config.json` (o la ruta elegida).
- Salida a journald.
- `TimeoutStartSec=5min` y deadline global interno de 4 min; cada llamada individual con timeout menor y margen para persistir el estado.
- Endurecimiento: `NoNewPrivileges`, `ProtectSystem`, `ProtectHome`, `PrivateTmp`, etc.

**Cuidado con el endurecimiento:** no debe bloquear el acceso al servicio de metadatos de OCI (`169.254.169.254`), imprescindible para Instance Principals. No uses `PrivateNetwork` ni `IPAddressDeny` sin una excepción documentada para esa IP y para los endpoints de OCI y Telegram.

### 2.2. Timer

`systemd/oci-infrastructure-monitor.timer` debe incluir:

- Ejecución cada 6 horas.
- `Persistent=true`.
- `RandomizedDelaySec` razonable.

No instales ni habilites nada automáticamente. Documenta cómo comprobar el timer y los logs:

```bash
systemctl list-timers
journalctl -u oci-infrastructure-monitor
```

## 3. Protocolo de traspaso entre sesiones (sección 9)

Al **iniciar** una sesión: lee `docs/index.md` (o `README.md` cuando exista a partir de este hito), `docs/progress.md`, `docs/decisions.md` y solo las secciones del hito asignado. Ejecuta `go version`, `go build ./...` y `go test ./...` para confirmar el estado real antes de tocar nada.

Al **cerrar** un hito, actualiza `docs/progress.md` con:

- Qué se completó (y qué quedó parcial).
- Resultado real de los comandos de verificación.
- Decisiones nuevas (también en `decisions.md`).
- Supuestos pendientes de validar en producción.
- Qué debe leer y hacer la siguiente sesión.

Además, haz un commit al cerrar cada hito.

### 3.1. Repositorio git

- `git init` antes del Hito 1, si aún no existe.
- Un commit por hito cerrado.
- `.gitignore` debe excluir `dist/` y cualquier `*.env`. La configuración real con OCIDs no debe versionarse; versiona solo `configs/config.example.json`.
- `Roadmap.md` se conserva intacto; la convención de entrada por `docs/index.md` vive en `docs/` y en `docs/decisions.md`.

## 4. Pruebas y compilación (sección 10)

- Pruebas unitarias sin acceso real a OCI ni Telegram, usando interfaces y mocks.
- Ejecutar `go test ./...`, `go test -race ./...`, `go vet ./...`. Si `-race` requiere CGO o una arquitectura distinta, documentarlo.
- No declares exitosa ninguna prueba que no hayas ejecutado.
- Compilación cruzada sin CGO con `-ldflags` para versión y fecha:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/oci-infrastructure-monitor-linux-arm64 ./cmd/monitor
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/oci-infrastructure-monitor-linux-amd64 ./cmd/monitor
```

- Genera `dist/SHA256SUMS` y verifica las arquitecturas con `file`.

## 5. Entrega final (sección 11)

Al cerrar el Hito 5, entrega:

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

## 6. Cierre documental

Al cerrar el proyecto:

1. Mueve a `docs/architecture.md` las reglas vigentes de buckets, cierre de mes, códigos de salida, colapso y operación.
2. Reduce `docs/decisions.md` a una lista corta de porqués; el detalle operativo ya vive en `architecture.md`.
3. Antes de retirar `Roadmap.md` o `docs/hitos/`, crea el tag de recuperación:

```bash
git tag roadmap-final
```

4. Solo después del tag puedes borrar el roadmap y los hitos si el equipo lo decide. El tag permite recuperarlos.

## 7. Entregables

- [x] `systemd/oci-infrastructure-monitor.service` con `Type=oneshot`, usuario dedicado, `EnvironmentFile`, `StateDirectory`, journald, `TimeoutStartSec=5min` y endurecimiento compatible con `169.254.169.254`.
- [x] `systemd/oci-infrastructure-monitor.timer` cada 6 horas, `Persistent=true` y `RandomizedDelaySec` razonable.
- [x] Binarios `linux/arm64` y `linux/amd64` en `dist/`, verificados con `file`.
- [x] `dist/SHA256SUMS` generado y comprobado.
- [x] `README.md` en español con el objetivo, la advertencia de "tráfico registrado no facturable", la estructura, la configuración, el CLI y los códigos de salida.
- [x] `docs/architecture.md` completo y coherente con la implementación final.
- [x] `docs/oci-permissions.md` con la política restrictiva propuesta y la verificación manual del Dynamic Group.
- [x] `docs/deployment.md` en español con comandos exactos para AlmaLinux 10.2 ARM64: usuario dedicado, directorios, permisos (incluido `telegram.env`), SELinux (contextos de `/var/lib`, `/etc` y del binario), instalación, actualización, desinstalación y verificación manual.
- [x] `docs/troubleshooting.md` en español con los fallos esperables y su diagnóstico.
- [x] Cierre documental: reglas vigentes consolidadas en `docs/architecture.md`, `docs/decisions.md` reducido a porqués y `git tag roadmap-final` creado antes de retirar el roadmap o los hitos.
- [x] Entrega final de la sección 5 de este archivo.
- [x] `docs/progress.md` actualizado con los resultados reales del cierre.

## 8. Criterio de cierre

- [x] `go test ./...`, `go test -race ./...` y `go vet ./...` ejecutados con resultado real reportado.
- [x] Binarios generados para `linux/arm64` y `linux/amd64`; `file` confirma la arquitectura de cada uno.
- [x] `dist/SHA256SUMS` verificado.
- [x] Las unidades systemd tienen sintaxis válida y no bloquean el servicio de metadatos de OCI (revisión documentada; si hay herramienta de validación, ejecutada).
- [x] La documentación de despliegue cubre instalación, actualización, desinstalación, permisos, SELinux y verificación del Dynamic Group.
- [x] Las reglas vigentes están en `docs/architecture.md` y `docs/decisions.md` quedó como lista de porqués.
- [x] `git tag roadmap-final` existe antes de retirar `Roadmap.md` o `docs/hitos/`.
- [x] La entrega final de la sección 11 está completa y separa claramente lo probado con mocks de lo pendiente de validación real.
- [x] La casilla superior de este hito marcada solo después de todo lo anterior.

## 9. Evidencia y cierre del proyecto

```bash
go version
go build ./...
go vet ./...
go test ./...
go test -race ./...
file dist/oci-infrastructure-monitor-linux-arm64
file dist/oci-infrastructure-monitor-linux-amd64
sha256sum -c dist/SHA256SUMS
```

Al cerrar, añade la entrada del Hito 5 en [`../progress.md`](../progress.md), marca las casillas de [`../index.md`](../index.md) y deja el proyecto listo para que el responsable del proyecto haga la instalación manual.
