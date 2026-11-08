# Despliegue en AlmaLinux 10.2 ARM64

Guía de instalación, verificación, actualización y desinstalación de `oci-infrastructure-monitor` en un servidor AlmaLinux 10.2 ARM64 con systemd y SELinux.

La guía está separada por entornos:

1. **En desarrollo:** compilar y preparar los archivos que se suben al servidor.
2. **En el servidor:** instalar cada archivo y crear la configuración.
3. **Verificación:** comprobar el binario, OCI, Telegram y el estado antes de habilitar el timer.

> **Alcance.** Esta guía no se ha ejecutado contra un servidor real: el agente validó la compilación, los tests, la sintaxis de las unidades systemd y los checksums. Las verificaciones manuales debe ejecutarlas el responsable del proyecto en la instancia.

---

## Mapa rápido de archivos

| Archivo en desarrollo | Se sube a | Destino final | Permisos |
|---|---|---|---|
| `dist/oci-infrastructure-monitor-linux-arm64` | `/home/opc/deploys/oci-infrastructure-monitor-linux-arm64` | `/usr/local/bin/oci-infrastructure-monitor` | `0755 root:root` |
| `dist/SHA256SUMS.deploy` | `/home/opc/deploys/SHA256SUMS` | Se usa para verificar; no se instala | — |
| `configs/config.example.json` | `/home/opc/deploys/config.example.json` | `/etc/oci-infrastructure-monitor/config.json` | `0640 root:oci-monitor` |
| `systemd/oci-infrastructure-monitor.service` | `/home/opc/deploys/oci-infrastructure-monitor.service` | `/etc/systemd/system/oci-infrastructure-monitor.service` | `0644 root:root` |
| `systemd/oci-infrastructure-monitor.timer` | `/home/opc/deploys/oci-infrastructure-monitor.timer` | `/etc/systemd/system/oci-infrastructure-monitor.timer` | `0644 root:root` |
| `README.md` y `docs/*.md` (opcional) | `/home/opc/deploys/` (planos) | `/usr/local/share/doc/oci-infrastructure-monitor/` | `0644 root:root` |
| — (se crea en el servidor) | — | `/etc/oci-infrastructure-monitor/telegram.env` | `0600 oci-monitor:oci-monitor` |
| — (se crea con systemd) | — | `/var/lib/oci-infrastructure-monitor/` | `0700 oci-monitor:oci-monitor` |

El servidor **no necesita Git, Go ni compilador**. Solo recibe estos archivos y ejecuta el binario. El binario es autocontenido: no se suben `go.mod`, `go.sum`, `.git` ni el resto de `dist/`.

---

## Parte 1 — En desarrollo: compilar y preparar la entrega

### 1.1 Requisitos

En la máquina de desarrollo:

- Git, `make` y Go 1.27.3 (la versión de `go.mod`).
- `scp` para copiar la entrega al servidor.
- El árbol de trabajo **commiteado**: `make dist-monitor` es estricto y no compila con cambios sin commitear.

No necesitas credenciales de OCI ni de Telegram para compilar; eso se configura en el servidor.

### 1.2 Compilar

```bash
git status --porcelain    # debe estar vacío
make dist-monitor
```

`make dist-monitor`:

- compila `linux/arm64` con `CGO_ENABLED=0`;
- inyecta `main.version` (`git describe`), `main.commit` (hash corto) y `main.buildDate`;
- usa `-buildvcs=true`, así que el binario también lleva `vcs.revision`, `vcs.time` y `vcs.modified`;
- verifica con `file` que es ARM64 y con `go version -m` el `vcs.revision`;
- genera `dist/SHA256SUMS`.

Salida esperada (los valores cambian en cada build):

```text
Compilando dist/oci-infrastructure-monitor-linux-arm64 (commit: fad1b9c7470c)...
dist/oci-infrastructure-monitor-linux-arm64: ELF 64-bit LSB executable, ARM aarch64, ...
	build	vcs.revision=fad1b9c7470cd0e801bdce4c06a6ce3b0529115e
	build	vcs.time=2026-11-08T00:40:30Z
	build	vcs.modified=false
```

`vcs.modified=false` es obligatorio para un release. Si es `true`, el árbol tenía cambios y el binario no es reproducible desde ese commit.

### 1.3 Verificar el artefacto y generar el checksum de la carga

```bash
file dist/oci-infrastructure-monitor-linux-arm64
go version -m dist/oci-infrastructure-monitor-linux-arm64 | grep 'vcs\.'
(cd dist && sha256sum oci-infrastructure-monitor-linux-arm64) > dist/SHA256SUMS.deploy
cat dist/SHA256SUMS.deploy
```

El binario es estático y ARM64; no depende de glibc ni de Go en el servidor. `dist/SHA256SUMS` es el que genera el Makefile con rutas `dist/...`; `dist/SHA256SUMS.deploy` es el que se sube a `/home/opc/deploys` y verifica el archivo plano. Ambos contienen el mismo hash.

### 1.4 Subir los archivos a `/home/opc/deploys`

El servidor no usa carpetas temporales: la carpeta de carga es `/home/opc/deploys`; desde ahí se instala en los destinos de la Parte 2.

```bash
ssh opc@servidor 'mkdir -p /home/opc/deploys'

scp dist/oci-infrastructure-monitor-linux-arm64 \
    configs/config.example.json \
    systemd/oci-infrastructure-monitor.service \
    systemd/oci-infrastructure-monitor.timer \
    opc@servidor:/home/opc/deploys/

scp dist/SHA256SUMS.deploy opc@servidor:/home/opc/deploys/SHA256SUMS

# Opcional: documentación para el Documentation= del service
scp README.md docs/*.md opc@servidor:/home/opc/deploys/
```

En `/home/opc/deploys` quedan los archivos planos:

```text
oci-infrastructure-monitor-linux-arm64
SHA256SUMS
config.example.json
oci-infrastructure-monitor.service
oci-infrastructure-monitor.timer
README.md y *.md   (opcional)
```

No subas `state.json`, `history/` ni el resto de `dist/`. El binario es autocontenido.

---

## Parte 2 — En el servidor: instalar

Todos los comandos de esta parte se ejecutan por SSH en el servidor. No hace falta Go ni Git.

### 2.1 Comprobar el entorno

```bash
uname -m                  # debe ser aarch64
cat /etc/os-release       # AlmaLinux 10.2
systemctl --version
getenforce                # Enforcing o Permissive
timedatectl               # reloj y zona horaria
df -h /var/lib /usr/local
```

Si SELinux está en `Enforcing`, aplica también el paso 2.9. Si el reloj no está sincronizado, corrígelo antes de habilitar el timer: los buckets y los enfriamientos dependen de la hora.

### 2.2 Crear el usuario y los directorios

El servicio **no corre como root**. Corre como `oci-monitor`, una cuenta de sistema sin shell ni contraseña:

```bash
sudo groupadd --system oci-monitor
sudo useradd --system \
  --gid oci-monitor \
  --home-dir /var/lib/oci-infrastructure-monitor \
  --no-create-home \
  --shell /sbin/nologin \
  --comment "OCI Infrastructure Monitor" \
  oci-monitor

sudo install -d -m 0700 -o oci-monitor -g oci-monitor /var/lib/oci-infrastructure-monitor
sudo install -d -m 0750 -o root       -g oci-monitor /etc/oci-infrastructure-monitor
```

Comprobación:

```bash
id oci-monitor
ls -ld /var/lib/oci-infrastructure-monitor /etc/oci-infrastructure-monitor
# esperado: 0700 oci-monitor:oci-monitor y 0750 root:oci-monitor
```

Por qué este usuario:

| Recurso | Propietario/modo | Qué puede hacer `oci-monitor` |
|---|---|---|
| `/usr/local/bin/oci-infrastructure-monitor` | `root:root 0755` | Ejecutarlo, no modificarlo |
| `/etc/oci-infrastructure-monitor/config.json` | `root:oci-monitor 0640` | Leerlo por grupo |
| `/etc/oci-infrastructure-monitor/telegram.env` | `oci-monitor:oci-monitor 0600` | Leerlo como dueño |
| `/var/lib/oci-infrastructure-monitor` | `oci-monitor:oci-monitor 0700` | Leer/escribir estado, historial y lock |

No ejecutes `run` como root: los archivos quedarían `root:root` y el servicio no podría escribir el estado. Usa siempre `sudo -u oci-monitor` o el servicio de systemd.

### 2.3 Verificar la carga

```bash
cd /home/opc/deploys
sha256sum -c SHA256SUMS
# esperado: oci-infrastructure-monitor-linux-arm64: OK
```

Si la suma no coincide, no instales nada: vuelve a subir el binario.

Los comandos siguientes usan `/home/opc/deploys` como origen. No se crean carpetas temporales: `install` copia directamente a los destinos. Si abres otra shell, vuelve a entrar en `/home/opc/deploys` o usa rutas absolutas.

### 2.4 Instalar el binario

```bash
sudo install -m 0755 -o root -g root \
  oci-infrastructure-monitor-linux-arm64 \
  /usr/local/bin/oci-infrastructure-monitor
sudo restorecon -v /usr/local/bin/oci-infrastructure-monitor
/usr/local/bin/oci-infrastructure-monitor version
```

Salida esperada (los valores son los de tu build):

```text
oci-infrastructure-monitor roadmap-final-5-gfad1b9c
commit: fad1b9c7470c
built: 2026-11-08T00:40:36Z
vcs.revision: fad1b9c7470cd0e801bdce4c06a6ce3b0529115e
vcs.time: 2026-11-08T00:40:30Z
vcs.modified: false
go: go1.27.3
os/arch: linux/arm64
```

Comprueba que `commit`/`vcs.revision` es el commit que compilaste y que `vcs.modified=false`. Si `version` muestra `dev`/`unknown`, el binario se compiló sin `-X`: no lo despliegues.

### 2.5 Instalar la documentación (opcional)

La unidad referencia `README.md` con `Documentation=`. Si quieres que esa ruta exista:

```bash
sudo install -d -m 0755 /usr/local/share/doc/oci-infrastructure-monitor
sudo install -m 0644 /home/opc/deploys/*.md \
  /usr/local/share/doc/oci-infrastructure-monitor/
sudo restorecon -Rv /usr/local/share/doc/oci-infrastructure-monitor
```

Si no subiste la documentación, omite este paso.

### 2.6 Crear el `config.json`

Copia la plantilla y edítala:

```bash
sudo install -m 0640 -o root -g oci-monitor \
  config.example.json \
  /etc/oci-infrastructure-monitor/config.json
sudo vi /etc/oci-infrastructure-monitor/config.json
sudo chown root:oci-monitor /etc/oci-infrastructure-monitor/config.json
sudo chmod 0640 /etc/oci-infrastructure-monitor/config.json
```

Cambia como mínimo:

- `oci.region`;
- `network.compartment_ocid` (el compartimento de la **subred** de la VNIC);
- `network.instance_ocid`;
- `network.vnic_ocids`;
- `network.monthly_limit_gib` y `network.high_traffic_6h_gib` según tu plan;
- `network.alert_percentages` si quieres otros cuatro niveles (deben ser cuatro, crecientes y el último `100`).

Mantén `storage.state_dir` en `/var/lib/oci-infrastructure-monitor` y `logging.level` en `info` salvo que estés diagnosticando.

Valida el JSON y los permisos (si no hay `python3`, instálalo con `sudo dnf install -y python3`):

```bash
python3 -m json.tool /etc/oci-infrastructure-monitor/config.json > /dev/null
sudo -u oci-monitor test -r /etc/oci-infrastructure-monitor/config.json && echo "config legible"
```

La lectura es estricta: un campo desconocido, un OCID mal formado, un porcentaje fuera de orden o una ruta relativa se rechazan con código `2`. El ejemplo contiene OCIDs ficticios; no funcionará contra OCI hasta que los sustituyas.

> `network.enabled=false` es válido y hace que `run` salga `0` sin consultar OCI; `test-telegram` sigue funcionando para probar solo el canal.

### 2.7 Crear el `telegram.env`

Crea el archivo con el propietario y modo correctos:

```bash
sudo install -m 0600 -o oci-monitor -g oci-monitor /dev/null \
  /etc/oci-infrastructure-monitor/telegram.env
sudo vi /etc/oci-infrastructure-monitor/telegram.env
```

Contenido (sin comillas, sin espacios alrededor del `=`):

```dotenv
TELEGRAM_BOT_TOKEN=123456789:AA...
TELEGRAM_CHAT_ID=123456789
```

`vi` puede reemplazar el archivo y dejar el propietario en `root`; vuelve a aplicarlo y comprueba:

```bash
sudo chown oci-monitor:oci-monitor /etc/oci-infrastructure-monitor/telegram.env
sudo chmod 0600 /etc/oci-infrastructure-monitor/telegram.env
sudo stat -c '%a %U:%G %n' /etc/oci-infrastructure-monitor/telegram.env
# esperado: 600 oci-monitor:oci-monitor
```

Reglas que aplica la aplicación:

- Modo `0600`; cualquier bit de grupo u otros se rechaza con código `2`.
- El usuario `oci-monitor` debe poder leer el archivo, porque la aplicación lo abre con `--telegram-env` además de recibirlo por `EnvironmentFile`.
- Las variables de entorno no vacías ganan al archivo; systemd las inyecta desde este mismo archivo.
- El token nunca se pasa por argumentos ni se escribe en el log. No lo pongas en `ExecStart`.

No hace falta el archivo para `run` si `telegram.enabled=false`; el `EnvironmentFile` es opcional (prefijo `-`). Pero `test-telegram` siempre lee las credenciales, así que necesita el archivo aunque el envío automático esté deshabilitado.

### 2.8 Instalar las unidades systemd

```bash
sudo install -m 0644 oci-infrastructure-monitor.service /etc/systemd/system/
sudo install -m 0644 oci-infrastructure-monitor.timer   /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemd-analyze verify \
  /etc/systemd/system/oci-infrastructure-monitor.service \
  /etc/systemd/system/oci-infrastructure-monitor.timer
```

`systemd-analyze verify` no debe imprimir nada sobre estas dos unidades (puede avisar de otras unidades del sistema). `ExecStart` debe existir; por eso este paso va después de instalar el binario.

El `service` no tiene `[Install]`: **solo se habilita el timer**.

Propiedades que conviene entender:

| Directiva | Valor | Porqué |
|---|---|---|
| `Type` | `oneshot` | Una ejecución y termina; el timer la dispara cada 6 h |
| `User`/`Group` | `oci-monitor` | Sin privilegios |
| `EnvironmentFile` | `-/etc/…/telegram.env` | Credenciales sin exponerlas en `ExecStart`; opcional |
| `StateDirectory` | `oci-infrastructure-monitor` | `/var/lib/oci-infrastructure-monitor` con `0700`, única ruta escribible |
| `TimeoutStartSec` | `5min` | Mayor que el deadline interno de 4 min |
| `ProtectSystem=strict` | — | Solo `StateDirectory` (y el `/tmp` privado) es escribible |
| `NoNewPrivileges`, `PrivateTmp`, `PrivateDevices`, `ProtectHome` | — | Endurecimiento estándar |
| `SystemCallFilter=@system-service` | — | Conjunto de syscalls de servicio |
| Ausencia de `PrivateNetwork`/`IPAddressDeny` | — | No se bloquea `169.254.169.254`, OCI ni Telegram |

### 2.9 SELinux

Con SELinux en `Enforcing`, verifica los contextos antes de arrancar:

```bash
ls -Z /usr/local/bin/oci-infrastructure-monitor
ls -Zd /etc/oci-infrastructure-monitor /var/lib/oci-infrastructure-monitor
```

Esperado en AlmaLinux 10.2:

| Ruta | Contexto esperado |
|---|---|
| `/usr/local/bin/oci-infrastructure-monitor` | `system_u:object_r:bin_t:s0` |
| `/etc/oci-infrastructure-monitor` | `system_u:object_r:etc_t:s0` |
| `/var/lib/oci-infrastructure-monitor` | `system_u:object_r:var_lib_t:s0` |

Aplica las etiquetas por defecto:

```bash
sudo restorecon -Rv /usr/local/bin/oci-infrastructure-monitor \
  /etc/oci-infrastructure-monitor /var/lib/oci-infrastructure-monitor
```

Si el binario vive en una ruta no estándar o `restorecon` no corrige el contexto, añade una regla persistente. `semanage` viene en `policycoreutils-python-utils`:

```bash
sudo dnf install -y policycoreutils-python-utils

sudo semanage fcontext -a -t bin_t '/usr/local/bin/oci-infrastructure-monitor'
sudo semanage fcontext -a -t etc_t '/etc/oci-infrastructure-monitor(/.*)?'
sudo semanage fcontext -a -t var_lib_t '/var/lib/oci-infrastructure-monitor(/.*)?'
sudo restorecon -Rv /usr/local/bin/oci-infrastructure-monitor \
  /etc/oci-infrastructure-monitor /var/lib/oci-infrastructure-monitor
```

Si una regla ya existe, usa `semanage fcontext -m` en lugar de `-a`.

Diagnóstico de una denegación:

```bash
sudo ausearch -m avc -ts recent
sudo ausearch -m avc -ts today | audit2why
```

No desactives SELinux para que la aplicación funcione. Si aparece un AVC, corrige el contexto o el `fcontext`; el estado y la configuración deben vivir en `/var/lib` y `/etc`, que es lo que la política espera.

---

## Parte 3 — Verificar antes de habilitar el timer

Ejecuta los comandos que leen estado o consultan OCI como el usuario del servicio, no como root:

```bash
# 1. Identidad del binario
/usr/local/bin/oci-infrastructure-monitor version

# 2. Configuración, autenticación y visibilidad de las VNIC
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor check
```

Salida esperada de `check`:

```text
check: OK
region: us-ashburn-1
compartment: ocid1.compartment.…
instance: ocid1.instance.…
vnic ocid1.vnic.…: visible (N definiciones, M datapoints en la última hora)
```

Si la VNIC no aparece, el mensaje dirá que puede no existir, no tener métricas o no haber tenido tráfico; no es una afirmación de inexistencia. Revisa [`troubleshooting.md`](troubleshooting.md).

```bash
# 3. Telegram (funciona aunque telegram.enabled=false)
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor test-telegram

# 4. Simulación: no escribe estado, no encola, no envía
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run --dry-run
```

Si quieres evitar envíos en la primera ejecución real, pon `telegram.enabled=false` en el `config.json`, ejecuta `run` una vez para poblar el estado, y luego cámbialo a `true`. `test-telegram` funciona igualmente con `false`.

```bash
# 5. Primera ejecución real
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run

# 6. Estado persistido
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor status
```

Comprueba los archivos y permisos:

```bash
sudo find /var/lib/oci-infrastructure-monitor -maxdepth 2 \
  -printf '%m %u:%g %p\n' | sort
```

Esperado: directorios `0700 oci-monitor:oci-monitor`, `state.json`, `lock` e `history/*.json` con `0600`.

Si la primera ejecución encuentra datos de las últimas 48 h en OCI Monitoring, creará el historial del mes y el acumulado; las horas anteriores a la primera visibilidad quedan `incomplete` y se muestran así en `status`.

### Verificación del Dynamic Group

Desde una máquina con OCI CLI y permisos de Identity (no desde la instancia, que no tiene OCI CLI):

```bash
oci iam dynamic-group list --compartment-id <tenancy_ocid>
oci iam dynamic-group get --dynamic-group-id <dynamic_group_ocid>
oci iam policy list --compartment-id <tenancy_ocid> --all
```

Comprueba que la regla de coincidencia incluye la instancia. El OCID real se puede confirmar desde la propia instancia:

```bash
curl -s -H "Authorization: Bearer Oracle" http://169.254.169.254/opc/v2/instance/ \
  | jq -r '.id'
```

Ese OCID debe cumplir la regla del Dynamic Group. La pertenencia no se deduce de que la instancia exista. Detalle y ejemplos de reglas: [`oci-permissions.md`](oci-permissions.md).

---

## Parte 4 — Operación: timer y logs

### 4.1 Habilitar el timer

```bash
sudo systemctl enable --now oci-infrastructure-monitor.timer
systemctl list-timers oci-infrastructure-monitor.timer
systemctl status oci-infrastructure-monitor.timer --no-pager
```

Comprueba la expresión y el próximo disparo:

```bash
systemd-analyze calendar '*-*-* 00/6:00:00'
```

El timer dispara a las 00:00, 06:00, 12:00 y 18:00 (hora local del servidor; los buckets se guardan en UTC), con hasta 15 min de retardo aleatorio. `Persistent=true` recupera la ejecución si el servidor estaba apagado.

### 4.2 Ejecución manual

```bash
sudo systemctl start oci-infrastructure-monitor.service
sudo systemctl status oci-infrastructure-monitor.service --no-pager
journalctl -u oci-infrastructure-monitor.service -n 80 --no-pager
```

Sigue los logs en vivo:

```bash
journalctl -u oci-infrastructure-monitor.service -f
```

Una ejecución correcta termina con `code=exited, status=0/SUCCESS`. Un código distinto de `0` deja la unidad en `failed`, pero el timer seguirá disparando en la siguiente ventana.

### 4.3 Red y firewall

- **Entrante:** ningún puerto. La aplicación no escucha.
- **Saliente:** HTTPS 443 a los endpoints de OCI Monitoring de la región y a `api.telegram.org`; HTTP 80 a `169.254.169.254` (metadatos de instancia); DNS 53.
- No configures `PrivateNetwork`, `IPAddressDeny` ni un proxy transparente que bloquee el servicio de metadatos.
- Si usas un proxy de salida, verifica que el SDK de OCI y el cliente de Telegram lo respeten antes de confiar en él; esta guía no lo cubre.

---

## Parte 5 — Actualización

En desarrollo, compila y prepara la entrega como en la Parte 1:

```bash
git status --porcelain    # debe estar vacío
make dist-monitor
(cd dist && sha256sum oci-infrastructure-monitor-linux-arm64) > dist/SHA256SUMS.deploy
scp dist/oci-infrastructure-monitor-linux-arm64 opc@servidor:/home/opc/deploys/
scp dist/SHA256SUMS.deploy opc@servidor:/home/opc/deploys/SHA256SUMS
```

En el servidor:

```bash
# 1. Conserva el binario anterior para revertir
sudo cp -a /usr/local/bin/oci-infrastructure-monitor /usr/local/bin/oci-infrastructure-monitor.previous

# 2. Detén el timer para evitar solapamiento
sudo systemctl stop oci-infrastructure-monitor.timer

# 3. Instala el binario nuevo de forma atómica
cd /home/opc/deploys
sha256sum -c SHA256SUMS
sudo install -m 0755 -o root -g root oci-infrastructure-monitor-linux-arm64 \
  /usr/local/bin/oci-infrastructure-monitor.new
sudo mv /usr/local/bin/oci-infrastructure-monitor.new /usr/local/bin/oci-infrastructure-monitor
sudo restorecon -v /usr/local/bin/oci-infrastructure-monitor
/usr/local/bin/oci-infrastructure-monitor version

# 4. Verifica antes de reactivar
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor check
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run --dry-run
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor status

# 5. Reactiva el timer
sudo systemctl start oci-infrastructure-monitor.timer
```

Notas:

- El esquema de estado es **v3**. Un binario nuevo migra en memoria un `state.json` o un historial v1/v2 y lo reescribe como v3 en el siguiente guardado. Un historial finalizado antiguo se lee como v3 y no se reescribe.
- Un binario antiguo que encuentre un estado v3 lo rechaza con código `4`; no es un fallo de datos, es una incompatibilidad de versión. Para revertir, restaura también el estado anterior o usa el binario nuevo.
- No copies `state.json` a mano entre servidores: el historial y el cierre del mes son la fuente de verdad.

---

## Parte 6 — Desinstalación

```bash
sudo systemctl disable --now oci-infrastructure-monitor.timer
sudo systemctl stop oci-infrastructure-monitor.service
sudo rm -f /etc/systemd/system/oci-infrastructure-monitor.service \
           /etc/systemd/system/oci-infrastructure-monitor.timer
sudo systemctl daemon-reload
sudo systemctl reset-failed
sudo rm -f /usr/local/bin/oci-infrastructure-monitor \
           /usr/local/bin/oci-infrastructure-monitor.previous
```

Los datos y la configuración se conservan por defecto:

```text
/var/lib/oci-infrastructure-monitor/   historial, estado y lock
/etc/oci-infrastructure-monitor/       config.json y telegram.env
```

Bórralos solo si no vas a reinstalar:

```bash
sudo rm -rf /var/lib/oci-infrastructure-monitor /etc/oci-infrastructure-monitor
sudo userdel oci-monitor
sudo groupdel oci-monitor
```

> Borrar `/var/lib/oci-infrastructure-monitor` elimina el historial mensual. Un mes finalizado no se puede reconstruir si los datos ya salieron de la retención de 90 días de OCI. Haz una copia antes si necesitas conservarlo.

---

## Apéndice — Checklist de puesta en marcha

### Desarrollo

- [ ] `git status --porcelain` vacío antes de compilar.
- [ ] `make dist-monitor` sale `0` y genera el binario ARM64.
- [ ] `go version -m` muestra `vcs.revision` = commit y `vcs.modified=false`.
- [ ] `dist/SHA256SUMS` coincide con el binario.
- [ ] `/home/opc/deploys` contiene el binario, `SHA256SUMS`, `config.example.json`, las dos unidades y, si se quiere, la documentación.

### Servidor

- [ ] `uname -m` es `aarch64`; `systemctl --version` funciona.
- [ ] `sha256sum -c SHA256SUMS` coincide en `/home/opc/deploys`.
- [ ] `oci-monitor` existe, sin shell y sin privilegios.
- [ ] `/var/lib/oci-infrastructure-monitor` es `0700 oci-monitor:oci-monitor`.
- [ ] `/etc/oci-infrastructure-monitor` es `0750 root:oci-monitor`.
- [ ] `config.json` es `0640 root:oci-monitor` y tiene OCIDs reales.
- [ ] `telegram.env` es `0600 oci-monitor:oci-monitor` y la app puede leerlo.
- [ ] `systemd-analyze verify` no reporta errores en las dos unidades.
- [ ] Contextos SELinux: `bin_t`, `etc_t`, `var_lib_t`; sin AVC recientes.

### Verificación

- [ ] `version` muestra el commit esperado y `vcs.modified=false`.
- [ ] `check` devuelve `0` y la VNIC es visible (o el warning está justificado).
- [ ] `test-telegram` llega al chat correcto.
- [ ] `run --dry-run` no escribe estado ni envía.
- [ ] `run` real sale `0`; `status` muestra el mes y los buckets.
- [ ] `state.json`, `lock` e `history/*.json` son `0600`.
- [ ] El timer está activo y `systemctl list-timers` muestra la próxima ejecución.
- [ ] `journalctl -u oci-infrastructure-monitor.service` no muestra el token.
- [ ] El Dynamic Group y la política restrictiva están verificados (ver [`oci-permissions.md`](oci-permissions.md)).
