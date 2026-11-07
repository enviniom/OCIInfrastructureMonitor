# Despliegue en AlmaLinux 10.2 ARM64

Guía de instalación, actualización y desinstalación de `oci-infrastructure-monitor` en un servidor AlmaLinux 10.2 ARM64 con systemd y SELinux. Los comandos están pensados para ejecutarse uno a uno, revisando la salida.

> **Alcance.** Esta guía no se ha ejecutado contra un servidor real: el agente solo validó la compilación, los tests, la sintaxis de las unidades systemd y los checksums. Las verificaciones manuales debe ejecutarlas el responsable del proyecto en la instancia.

## 1. Requisitos previos

En la instancia de OCI:

- AlmaLinux 10.2 ARM64 (`uname -m` debe devolver `aarch64`), o cualquier Linux con systemd para el binario `amd64`.
- Reloj sincronizado (`chronyc tracking`): los buckets y el enfriamiento dependen de la hora.
- Salida HTTPS a los endpoints de OCI Monitoring y a `api.telegram.org`; acceso a `169.254.169.254`.
- La instancia pertenece al Dynamic Group y la política concede `read metrics` sobre `oci_vcn`. Verificación: [`oci-permissions.md`](oci-permissions.md).
- Para las verificaciones manuales: `curl` y `jq`; si faltan, `sudo dnf install -y curl jq`. La aplicación no los necesita en ejecución.

En la instancia **no** hace falta OCI CLI, Go ni compilador. El binario es estático (`CGO_ENABLED=0`).

Antes de empezar, comprueba el entorno:

```bash
uname -m                  # aarch64
getenforce                # Enforcing o Permissive
systemctl --version
timedatectl
df -h /var/lib /usr/local
```

Si SELinux está en `Enforcing`, lee también la sección 8.

## 2. Variables de esta guía

Sustituye estos valores por los reales. No los pegues en el historial del shell si contienen secretos.

| Variable | Ejemplo | Dónde se obtiene |
|---|---|---|
| Región | `us-ashburn-1` | Consola de OCI |
| Compartimento de la subred | `ocid1.compartment.oc1..…` | Consola de OCI, subred de la VNIC |
| Instancia | `ocid1.instance.oc1.iad.…` | Consola de OCI o metadatos |
| VNIC | `ocid1.vnic.oc1.iad.…` | Consola de OCI, detalles de la instancia |
| Límite mensual | `10240` (GiB) | Tu plan o tu propio umbral |
| Bot de Telegram | `123456789:AA…` | @BotFather |
| Chat ID | `123456789` | `@userinfobot` o la API de Telegram |

## 3. Usuario dedicado y directorios

El servicio se ejecuta sin privilegios. No uses `root` ni tu usuario personal.

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

`/var/lib/oci-infrastructure-monitor` es también el `StateDirectory` de systemd, declarado con `StateDirectoryMode=0700`. Si ya existe, comprueba que mantiene ese modo y propietario.

## 4. Binario

Copia a la instancia el directorio `dist/` completo (binario y `SHA256SUMS`), o al menos el binario y el archivo de sumas, y verifica desde el directorio que contiene `dist/`:

```bash
sha256sum -c dist/SHA256SUMS
# esperado: dist/oci-infrastructure-monitor-linux-arm64: OK (o «La suma coincide» según el idioma)
```

Instala el binario de ARM64:

```bash
sudo install -m 0755 -o root -g root \
  dist/oci-infrastructure-monitor-linux-arm64 \
  /usr/local/bin/oci-infrastructure-monitor
sudo restorecon -v /usr/local/bin/oci-infrastructure-monitor
/usr/local/bin/oci-infrastructure-monitor version
```

Salida esperada (los valores de commit y fecha son los de tu compilación):

```text
oci-infrastructure-monitor 1.0.0
commit: 8b0fce2
built: 2026-11-07T22:15:20Z
go: go1.27.3
os/arch: linux/arm64
```

Copia también la documentación que referencia la unidad (`Documentation=file:/usr/local/share/doc/oci-infrastructure-monitor/README.md`):

```bash
sudo install -d -m 0755 /usr/local/share/doc/oci-infrastructure-monitor
sudo install -m 0644 README.md docs/architecture.md docs/deployment.md \
  docs/troubleshooting.md docs/decisions.md docs/oci-permissions.md docs/delivery.md \
  /usr/local/share/doc/oci-infrastructure-monitor/
sudo restorecon -Rv /usr/local/share/doc/oci-infrastructure-monitor
```

## 5. Configuración

Copia el ejemplo y edítalo. El archivo queda legible por el grupo del servicio, no por otros:

```bash
sudo install -m 0640 -o root -g oci-monitor \
  configs/config.example.json \
  /etc/oci-infrastructure-monitor/config.json
sudo vi /etc/oci-infrastructure-monitor/config.json
```

Cambia como mínimo:

- `oci.region`;
- `network.compartment_ocid`, `network.instance_ocid`, `network.vnic_ocids`;
- `network.monthly_limit_gib` y `network.high_traffic_6h_gib` según tu plan;
- `network.alert_percentages` si quieres otros cuatro niveles (deben ser cuatro, crecientes y el último `100`).

Mantén `storage.state_dir` en `/var/lib/oci-infrastructure-monitor` y `logging.level` en `info` salvo que estés diagnosticando.

Valida el JSON y los permisos (si no hay `python3`, instálalo antes con `sudo dnf install -y python3`):

```bash
python3 -m json.tool /etc/oci-infrastructure-monitor/config.json > /dev/null
sudo -u oci-monitor test -r /etc/oci-infrastructure-monitor/config.json && echo "config legible"
```

La lectura es estricta: un campo desconocido, un OCID mal formado, un porcentaje fuera de orden o una ruta relativa se rechazan con código `2`. El ejemplo contiene OCIDs ficticios; no funcionará contra OCI hasta que los sustituyas.

> `network.enabled=false` es válido y hace que `run` salga `0` sin consultar OCI; `test-telegram` sigue funcionando para probar solo el canal.

## 6. Credenciales de Telegram

Crea el archivo vacío con el propietario y modo correctos:

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

No hace falta el archivo si `telegram.enabled=false`; el `EnvironmentFile` es opcional (prefijo `-`) y la aplicación solo falla si el canal está habilitado y faltan las credenciales.

## 7. Unidades systemd

Instala las dos unidades y recarga:

```bash
sudo install -m 0644 systemd/oci-infrastructure-monitor.service /etc/systemd/system/
sudo install -m 0644 systemd/oci-infrastructure-monitor.timer   /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemd-analyze verify \
  /etc/systemd/system/oci-infrastructure-monitor.service \
  /etc/systemd/system/oci-infrastructure-monitor.timer
```

`systemd-analyze verify` no debe imprimir nada sobre estas dos unidades (puede avisar de otras unidades del sistema). Comprueba que `ExecStart` existe; por eso este paso va después de instalar el binario (sección 4). En un checkout sin instalar, el `service` falla con `Command … is not executable`; el `timer` se puede validar solo. El `service` no tiene `[Install]`: **solo se habilita el timer**.

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

## 8. SELinux

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

Si el binario vive en una ruta no estándar (`/opt`, `/srv`, etc.) o `restorecon` no corrige el contexto, añade una regla persistente. `semanage` viene en `policycoreutils-python-utils`:

```bash
sudo dnf install -y policycoreutils-python-utils

# bin_t para el ejecutable
sudo semanage fcontext -a -t bin_t '/usr/local/bin/oci-infrastructure-monitor'
# si la regla ya existe, usa -m en lugar de -a

# etc_t para la configuración
sudo semanage fcontext -a -t etc_t '/etc/oci-infrastructure-monitor(/.*)?'

# var_lib_t para el estado
sudo semanage fcontext -a -t var_lib_t '/var/lib/oci-infrastructure-monitor(/.*)?'

sudo restorecon -Rv /usr/local/bin/oci-infrastructure-monitor \
  /etc/oci-infrastructure-monitor /var/lib/oci-infrastructure-monitor
```

Diagnóstico de una denegación:

```bash
sudo ausearch -m avc -ts recent
sudo ausearch -m avc -ts today | audit2why
```

No desactives SELinux para que la aplicación funcione. Si aparece un AVC, corrige el contexto o el `fcontext`; el estado y la configuración deben vivir en `/var/lib` y `/etc`, que es lo que la política espera.

## 9. Verificación manual antes de habilitar el timer

Ejecuta siempre como el usuario del servicio, no como root:

```bash
# 1. Configuración, autenticación y visibilidad de las VNIC
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor check
```

Esperado:

```text
check: OK
region: us-ashburn-1
compartment: ocid1.compartment.…
instance: ocid1.instance.…
vnic ocid1.vnic.…: visible (N definiciones, M datapoints en la última hora)
```

Si la VNIC no aparece, el mensaje dirá que puede no existir, no tener métricas o no haber tenido tráfico; no es una afirmación de inexistencia. Revisa [`troubleshooting.md`](troubleshooting.md).

```bash
# 2. Telegram (funciona aunque telegram.enabled=false)
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor test-telegram

# 3. Simulación: no escribe estado, no encola, no envía
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run --dry-run

# 4. Primera ejecución real
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run

# 5. Estado persistido
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

## 10. Timer

Habilita solo el timer:

```bash
sudo systemctl enable --now oci-infrastructure-monitor.timer
systemctl list-timers oci-infrastructure-monitor.timer
systemctl status oci-infrastructure-monitor.timer --no-pager
```

Comprueba la expresión y el próximo disparo:

```bash
systemd-analyze calendar '*-*-* 00/6:00:00'
```

El timer dispara a las 00:00, 06:00, 12:00 y 18:00 (hora local), con hasta 15 min de retardo aleatorio. `Persistent=true` recupera la ejecución si el servidor estaba apagado.

Prueba inmediata sin esperar:

```bash
sudo systemctl start oci-infrastructure-monitor.service
sudo systemctl status oci-infrastructure-monitor.service --no-pager
journalctl -u oci-infrastructure-monitor.service -n 80 --no-pager
```

Sigue los logs en vivo durante una ejecución:

```bash
journalctl -u oci-infrastructure-monitor.service -f
```

Una ejecución correcta termina con `code=exited, status=0/SUCCESS`. Un código distinto de `0` deja la unidad en `failed`, pero el timer seguirá disparando en la siguiente ventana.

## 11. Red y firewall

- **Entrante:** ningún puerto. La aplicación no escucha.
- **Saliente:** HTTPS 443 a los endpoints de OCI Monitoring de la región y a `api.telegram.org`; HTTP 80 a `169.254.169.254` (metadatos de instancia); DNS 53.
- No configures `PrivateNetwork`, `IPAddressDeny` ni un proxy transparente que bloquee el servicio de metadatos.
- Si usas un proxy de salida, verifica que el SDK de OCI y el cliente de Telegram lo respeten antes de confiar en él; esta guía no lo cubre.

## 12. Actualización

1. Conserva el binario anterior para poder revertir:

```bash
sudo cp -a /usr/local/bin/oci-infrastructure-monitor /usr/local/bin/oci-infrastructure-monitor.previous
```

2. Detén el timer para evitar un solapamiento durante el cambio:

```bash
sudo systemctl stop oci-infrastructure-monitor.timer
```

3. Instala el binario nuevo de forma atómica y reetiqueta:

```bash
sudo install -m 0755 -o root -g root \
  dist/oci-infrastructure-monitor-linux-arm64 \
  /usr/local/bin/oci-infrastructure-monitor.new
sudo mv /usr/local/bin/oci-infrastructure-monitor.new /usr/local/bin/oci-infrastructure-monitor
sudo restorecon -v /usr/local/bin/oci-infrastructure-monitor
/usr/local/bin/oci-infrastructure-monitor version
```

4. Verifica antes de reactivar:

```bash
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor check
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor run --dry-run
sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor status
```

5. Reactiva el timer:

```bash
sudo systemctl start oci-infrastructure-monitor.timer
```

Notas:

- El esquema de estado es **v3**. Un binario nuevo migra en memoria un `state.json` o un historial v1/v2 y lo reescribe como v3 en el siguiente guardado. Un historial finalizado antiguo se lee como v3 y no se reescribe.
- Un binario antiguo que encuentre un estado v3 lo rechaza con código `4`; no es un fallo de datos, es una incompatibilidad de versión. Para revertir, restaura también el estado anterior o usa el binario nuevo.
- No copies `state.json` a mano entre servidores: el historial y el cierre del mes son la fuente de verdad.

## 13. Desinstalación

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

## 14. Checklist de puesta en marcha

- [ ] `uname -m` es `aarch64` (o se usa el binario `amd64`).
- [ ] `sha256sum -c dist/SHA256SUMS` coincide.
- [ ] `oci-monitor` existe, sin shell y sin privilegios.
- [ ] `/var/lib/oci-infrastructure-monitor` es `0700 oci-monitor:oci-monitor`.
- [ ] `/etc/oci-infrastructure-monitor` es `0750 root:oci-monitor`.
- [ ] `config.json` es `0640 root:oci-monitor` y tiene OCIDs reales.
- [ ] `telegram.env` es `0600 oci-monitor:oci-monitor` y la app puede leerlo.
- [ ] `systemd-analyze verify` no reporta errores en las dos unidades.
- [ ] Contextos SELinux: `bin_t`, `etc_t`, `var_lib_t`; sin AVC recientes.
- [ ] `check` devuelve `0` y la VNIC es visible (o el warning está justificado).
- [ ] `test-telegram` llega al chat correcto.
- [ ] `run --dry-run` no escribe estado ni envía.
- [ ] `run` real sale `0`; `status` muestra el mes y los buckets.
- [ ] `state.json`, `lock` e `history/*.json` son `0600`.
- [ ] El timer está activo y `systemctl list-timers` muestra la próxima ejecución.
- [ ] `journalctl -u oci-infrastructure-monitor.service` no muestra el token.
- [ ] El Dynamic Group y la política restrictiva están verificados (ver [`oci-permissions.md`](oci-permissions.md)).
