# Diagnóstico y resolución de problemas

Guía de los fallos esperables de `oci-infrastructure-monitor` y cómo diagnosticarlos. Está escrita para la instalación de [`deployment.md`](deployment.md). Los mensajes de la aplicación van en español al journal; los comandos y el estado también.

> **Regla de oro:** empieza por `status`, sigue con `run --dry-run` y termina con el journal. No borres `state.json` ni el historial hasta haber copiado el directorio de estado.
>
> Si el `PATH` de `sudo` no incluye `/usr/local/bin`, sustituye `oci-infrastructure-monitor` por `/usr/local/bin/oci-infrastructure-monitor` en los comandos de esta guía.

## 1. Comandos base

```bash
# Estado persistido (no consulta OCI, no escribe)
sudo -u oci-monitor oci-infrastructure-monitor status

# Simulación: lee, calcula y dice qué habría enviado, sin escribir ni enviar
sudo -u oci-monitor oci-infrastructure-monitor run --dry-run

# Diagnóstico de OCI y permisos (no escribe estado)
sudo -u oci-monitor oci-infrastructure-monitor check

# Unidad y logs
systemctl status oci-infrastructure-monitor.service --no-pager
journalctl -u oci-infrastructure-monitor.service -n 100 --no-pager
journalctl -u oci-infrastructure-monitor.service -p warning --since today
journalctl -u oci-infrastructure-monitor.service -f

# Timer
systemctl list-timers --all | grep oci
systemctl status oci-infrastructure-monitor.timer --no-pager
systemd-analyze calendar '*-*-* 00/6:00:00'
```

Cada ejecución real imprime una línea de resumen:

```text
run: consultas_oci=4 fallos_oci=0 enviadas=0 encoladas=0 pendientes=0 caducadas=0 descartadas=0 código=0
```

`consultas_oci` cuenta las consultas ejecutadas, `fallos_oci` las que fallaron, `enviadas` los mensajes aceptados por Telegram, `encoladas` las nuevas pendientes, `pendientes` las que quedan en la cola, `caducadas` las retiradas por los 7 días y `descartadas` las retiradas por desbordamiento de las 50 entradas.

### Saber qué commit está desplegado

El servidor no necesita Git ni Go:

```bash
oci-infrastructure-monitor version
```

Si muestra `dev`/`unknown` en `version`/`commit`, el binario se compiló sin `-X`; recompílalo con `make dist-monitor` en el repositorio de desarrollo. Si `vcs.modified` es `true` o el commit lleva `-dirty`, el binario incluye cambios sin commitear y el hash es solo la base. Para ver qué cambios le faltan, en el repositorio de desarrollo: `git log --oneline <commit>..HEAD` y `git diff <commit>..HEAD`.

## 2. La unidad está en `failed`

```bash
systemctl status oci-infrastructure-monitor.service --no-pager
journalctl -u oci-infrastructure-monitor.service --since "1 hour ago" --no-pager
```

El código de salida de la última ejecución está en `status=...` y en la línea `código=N` del resumen. Busca la tabla de la sección 3.

Si el servicio no arranca y el journal dice `Failed to resolve user 'oci-monitor'` o `Command ... is not executable`, faltan el usuario o el binario: repite las secciones 3 y 4 de [`deployment.md`](deployment.md).

Si systemd lo marca como `failed` pero la ejecución fue correcta, revisa que `ExecStart` apunte a `/usr/local/bin/oci-infrastructure-monitor` y que el archivo sea ejecutable (`ls -l`).

## 3. Códigos de salida

| Código | Significado | Dónde mirar |
|---|---|---|
| `0` | Éxito, posiblemente con warnings | Nada; revisa `status` si hay avisos |
| `2` | Configuración o credenciales | Sección 4 |
| `3` | OCI (autenticación, permisos, red, consulta) | Sección 5 |
| `4` | Persistencia (permisos, corrupción, esquema) | Sección 6 |
| `5` | Telegram | Sección 7 |
| `6` | Ejecución concurrente (`flock`) | Sección 8 |
| `1` | Reservado; no debería aparecer | Reporta el caso con el journal |

Con varios errores, `run` devuelve el de mayor prioridad: `2` > `4` > `3` > `5`. Todos quedan en el journal, así que no te quedes solo con el código.

## 4. Código 2: configuración

Mensajes típicos:

```text
configuration error: open configuration "/etc/oci-infrastructure-monitor/config.json": no such file or directory
configuration error: configuration "...": decode configuration: json: unknown field "foo"
configuration error: invalid configuration: network.alert_percentages must contain exactly 4 values
configuración de Telegram: telegram credentials "...": ... must not be readable by group or others (mode 0644, want 0600)
```

Checklist:

- La ruta existe y el usuario del servicio puede leerla: `sudo -u oci-monitor test -r /etc/oci-infrastructure-monitor/config.json`.
- El JSON es válido: `python3 -m json.tool ...`.
- No hay campos desconocidos ni tipos incorrectos; la lectura es estricta.
- `oci.region` no está vacío y `oci.auth` es `instance_principal`.
- Los OCIDs tienen la forma `ocid1.<tipo>.<región>.<algo>.<algo>`; la validación local es sintáctica.

Un `telegram.env` ilegible aborta el ciclo antes de consultar OCI, porque el código de configuración (`2`) tiene prioridad sobre el de OCI (`3`). Corrige el archivo y relanza dentro de la ventana de reconsulta de 48 h para no perder horas.
- `alert_percentages` tiene exactamente 4 enteros crecientes, el último `100`.
- `monthly_limit_gib` y `high_traffic_6h_gib` son mayores que 0 si `network.enabled=true`.
- `storage.state_dir` es una ruta absoluta.
- `logging.level` es `debug`, `info`, `warn` o `error`.
- `telegram.env` tiene modo `0600` y es legible por `oci-monitor`; el token no tiene espacios.
- Si `telegram.enabled=true`, el token y el Chat ID están presentes.

Recuerda que los booleanos ausentes toman el valor por defecto (`true`); para deshabilitar hay que escribir `"enabled": false`.

## 5. Código 3: OCI

Mensajes típicos:

```text
OCI: no se pudo resolver la identidad con Instance Principals: ...
OCI: vnic ocid1.vnic... outbound ... a ...: summarize metrics: ...
```

Causas y comprobaciones:

1. **Instance Principals no resuelve.** La instancia debe poder leer `169.254.169.254` y pertenecer al Dynamic Group. Comprueba desde la instancia:

   ```bash
   curl -s -H "Authorization: Bearer Oracle" http://169.254.169.254/opc/v2/instance/ | head
   ```

   Si no responde, revisa que no haya un firewall/proxy bloqueando el enlace local y que el servicio de metadatos esté disponible. La unidad systemd no lo bloquea a propósito.

2. **El Dynamic Group no incluye la instancia.** Verifica la regla de coincidencia con el OCID real. Procedimiento en [`oci-permissions.md`](oci-permissions.md).

3. **Permisos insuficientes.** La política debe conceder `read metrics` sobre `oci_vcn` en el compartimento de la **subred**. Un `404 NotAuthorizedOrNotFound` suele ser una política que no cubre ese compartimento, no un OCID inexistente.

4. **Compartimento equivocado.** `network.compartment_ocid` es el de la subred, no el de la instancia. Si está mal, las consultas devuelven series vacías o fallan.

5. **Región equivocada.** `oci.region` debe ser la región donde viven las VNIC.

6. **Timeout o red.** El presupuesto es de 30 s por consulta y 4 min por ejecución. Un fallo de red deja las horas afectadas `incomplete`, envía lo que haya en la cola y sale `3`. Revisa DNS, salida HTTPS y latencia.

7. **Una VNIC falla y otras no.** La afectada queda `incomplete`, se continúa con las demás y se sale `3`. El detalle por VNIC está en el journal.

8. **VNIC nunca visible.** Si las consultas tienen éxito pero la VNIC nunca aparece, sus horas quedan `incomplete` y cuenta como fallo de recopilación; a los 3 fallos se genera la alerta persistente. El código de salida sigue siendo `0` si las consultas no fallaron. Causas: OCID mal escrito, compartimento equivocado, VNIC sin tráfico en la retención o métrica aún no publicada.

`check` distingue tres estados: OCID mal formado (`2`), VNIC visible (`0`), VNIC no visible (`0` con warning muy visible). Un `3` en `check` es un fallo de consulta, no una VNIC ausente.

## 6. Código 4: persistencia

Mensajes típicos:

```text
persistencia: create state directory "...": permission denied
persistencia: decode state: ...
persistencia: schema version 4 is not supported (supported: 1..3)
persistencia: history file is finalized
status: error de persistencia; revisa los archivos indicados
```

Comprobaciones:

1. **Permisos.** El usuario `oci-monitor` debe ser dueño de `/var/lib/oci-infrastructure-monitor` (`0700`). Un `permission denied` al crear el temporal o al renombrar apunta a propietario o SELinux.

   ```bash
   sudo find /var/lib/oci-infrastructure-monitor -maxdepth 2 -printf '%m %u:%g %p\n'
   sudo -u oci-monitor test -w /var/lib/oci-infrastructure-monitor && echo "escribible"
   ```

2. **Esquema desconocido.** Un `state.json` con `schema_version` mayor que el soportado (hoy `3`) se rechaza sin moverlo ni sobrescribirlo. Es lo esperado al volver a un binario antiguo: usa el binario nuevo o restaura una copia.

3. **Archivo corrupto.** La aplicación mueve el archivo a `<nombre>.corrupt-<timestamp UTC>` y sale `4`; nunca lo sobrescribe. Ejemplo: `state.json.corrupt-20261107T214213Z`. Inspecciona el original y el cuarentenado, y decide si restaurar una copia o continuar con el historial. **No borres el archivo corrupto**: es la evidencia.

   ```bash
   ls -la /var/lib/oci-infrastructure-monitor
   head -c 500 /var/lib/oci-infrastructure-monitor/state.json.corrupt-*
   ```

   `status` informa del corrupto y sale `4`, pero no lo mueve; solo `run` lo mueve. Sigue mostrando lo legible.

4. **Historial finalizado.** `SaveHistory` rechaza sobrescribir un mes finalizado (`ErrFinalizedHistory`). Es una protección, no un fallo: un mes cerrado es inmutable. Si necesitas corregirlo, restaura una copia editada y hazlo con el timer detenido y bajo tu responsabilidad.

5. **Disco lleno.** Un `no space left on device` deja el estado anterior intacto (la escritura es temporal + `rename`), pero la ejecución falla. Libera espacio; el estado no queda a medias.

6. **Cierre entre dos escrituras.** Si el proceso muere entre el guardado del historial y el de `state.json`, la ejecución siguiente adopta el cierre desde el historial (es la fuente de verdad). No hagas nada.

7. **Corrección tardía.** Una corrección de un mes ya finalizado se descarta y se registra en el journal (`mes ...: N buckets corregidos tras el cierre; se descartan`). Es lo esperado.

## 7. Código 5: Telegram

Mensajes típicos:

```text
telegram: 401 Unauthorized: Unauthorized
telegram: 400 Bad Request: chat not found
telegram: 429 Too Many Requests: Too Many Requests: retry after 30
telegram: límite de velocidad con retry_after 30s; la notificación queda en cola y la ejecución termina
```

| Síntoma | Causa probable | Solución |
|---|---|---|
| `401 Unauthorized` | Token inválido o revocado | Genera uno nuevo con @BotFather y actualiza `telegram.env` |
| `403 Forbidden` | El bot fue bloqueado o expulsado del chat | Desbloquea el bot o corrige el Chat ID |
| `400 chat not found` | Chat ID incorrecto o el bot nunca habló con ese chat | Escribe al bot primero; usa el ID numérico correcto |
| `429` corto | Límite de velocidad | El cliente espera `retry_after` y reintenta si cabe en el deadline |
| `429` largo | La espera no cabe en el deadline | El mensaje queda en la cola y se reintenta en la siguiente ejecución; sale `5` |
| `5xx` o timeout | Telegram o la red | Se reintenta 3 veces con backoff; si falla, queda en cola |
| No llega nada y el código es `0` | `telegram.enabled=false` | Habilítalo; la app evalúa y registra pero no envía |
| No llega nada tras habilitar | Cola colapsada por la transición | Al reactivar se descarta lo mensual pendiente y se emite una sola alerta con el nivel actual |

Comprobaciones:

```bash
sudo stat -c '%a %U:%G %n' /etc/oci-infrastructure-monitor/telegram.env
sudo -u oci-monitor test -r /etc/oci-infrastructure-monitor/telegram.env && echo "legible"
sudo -u oci-monitor oci-infrastructure-monitor test-telegram
```

`test-telegram` funciona aunque `telegram.enabled=false`. Un fallo de Telegram **no** cuenta para el error persistente de recopilación: va al código de salida y al journal.

Verifica que el token no aparece en los logs:

```bash
TOKEN=$(sudo sed -n 's/^TELEGRAM_BOT_TOKEN=//p' /etc/oci-infrastructure-monitor/telegram.env)
if [ -z "$TOKEN" ]; then
  echo "no se pudo leer TELEGRAM_BOT_TOKEN; revisa el archivo"
elif sudo journalctl -u oci-infrastructure-monitor.service --since today | grep -qF -- "$TOKEN"; then
  echo "FUGA: rota el token"
else
  echo "sin token en el journal"
fi
unset TOKEN
```

Si el token aparece, considéralo una fuga: rota el token con @BotFather, actualiza `telegram.env` y revisa los permisos del archivo y del journal.

## 8. Código 6: ejecución concurrente

El `flock` es exclusivo y no bloqueante. Otra ejecución está en curso (por ejemplo, arrancaste el servicio a mano mientras el timer lo disparaba) o el lock está retenido por un proceso colgado.

```bash
systemctl status oci-infrastructure-monitor.service --no-pager
sudo fuser -v /var/lib/oci-infrastructure-monitor/lock
ps -ef | grep -v grep | grep oci-infrastructure-monitor
```

No borres el archivo `lock`: `flock` se libera al cerrar el descriptor y un archivo huérfano no bloquea. Si hay un proceso realmente colgado, espera al `TimeoutStartSec` (5 min) o termínalo con `systemctl stop`. La siguiente ejecución del timer volverá a intentarlo.

## 9. Warnings y calidad de datos

| Salida de `status` | Significado | Acción |
|---|---|---|
| `acumulado: completo` | Todos los buckets están confirmados | Ninguna |
| `acumulado: provisional` | Hay horas recientes dentro de `publication_latency_hours` | Esperar la reconsulta de 48 h |
| `acumulado: incompleto` | Consulta fallida, hora fuera de retención o VNIC nunca visible | Revisar OCI y el journal; el total es un mínimo |
| `acumulado: sin datos` | No hay buckets todavía | Primera ejecución o red deshabilitada |
| `aviso: vnic ... dejó de aparecer` | Tenía datos y ahora no devuelve ninguno | El tráfico puede ser cero; conserva `last_visible`. No es un fallo de consulta |
| `vnic ... nunca fue visible` en el journal | Nunca devolvió datapoints | OCID, compartimento o ausencia total de tráfico |
| `no finalizado (en gracia)` | El mes anterior aún está dentro de `requery_window` | Normal hasta fin de mes + 48 h |
| `finalizado el ...` | Mes cerrado e inmutable | Las correcciones posteriores se descartan |

Una VNIC con 48 h de tráfico cero puede parecer “desaparecida”. El aviso lo dice y `last_visible` da contexto; no borra buckets confirmados.

## 10. El timer no se dispara

```bash
systemctl list-timers --all | grep oci
systemctl is-enabled oci-infrastructure-monitor.timer
systemctl is-active oci-infrastructure-monitor.timer
journalctl -u oci-infrastructure-monitor.timer --since today
systemd-analyze calendar '*-*-* 00/6:00:00'
```

Checklist:

- Solo se habilita el timer, no el service: `systemctl enable --now oci-infrastructure-monitor.timer`.
- `systemctl list-timers` debe mostrar `NEXT` y `LEFT`; si `NEXT` es `n/a`, el timer está mal formado.
- `Persistent=true`: si el servidor estuvo apagado, la ejecución perdida se recupera al arrancar.
- Un servicio `failed` no deshabilita el timer; la siguiente ventana lo vuelve a intentar.
- Si cambiaste las unidades, `systemctl daemon-reload`.
- Comprueba la hora del sistema (`timedatectl`); el timer usa hora local, los datos UTC.
- `RandomizedDelaySec=15min` puede retrasar hasta 15 min; no es un fallo.

Prueba inmediata:

```bash
sudo systemctl start oci-infrastructure-monitor.service
journalctl -u oci-infrastructure-monitor.service -n 100 --no-pager
```

## 11. SELinux

```bash
getenforce
sudo ausearch -m avc -ts recent
sudo ausearch -m avc -ts today | audit2why
ls -Z /usr/local/bin/oci-infrastructure-monitor
ls -Zd /etc/oci-infrastructure-monitor /var/lib/oci-infrastructure-monitor
```

Esperado: `bin_t`, `etc_t`, `var_lib_t`. Corrige con `restorecon` o `semanage fcontext` (sección 8 de [`deployment.md`](deployment.md)). No pongas SELinux en `Permissive` como solución permanente: el problema volverá y oculta el riesgo.

## 12. Endurecimiento de systemd y `operation not permitted`

La unidad aplica `ProtectSystem=strict`, `PrivateDevices`, `SystemCallFilter=@system-service`, `MemoryDenyWriteExecute` y un conjunto vacío de capacidades. El binario no necesita JIT, dispositivos ni escritura fuera del directorio de estado. Si una versión futura o una dependencia introduce una syscall o memoria ejecutable, el síntoma será un error `operation not permitted` dentro de la aplicación, no un fallo de configuración.

Diagnóstico:

```bash
journalctl -u oci-infrastructure-monitor.service -n 100 --no-pager | grep -i 'not permitted\|operation'
sudo systemd-analyze security oci-infrastructure-monitor.service
```

Como último recurso, y solo tras confirmar la causa, relaja una directiva con un override documentado:

```bash
sudo systemctl edit oci-infrastructure-monitor.service
```

```ini
[Service]
# Solo si se confirmó que la syscall bloqueada es imprescindible.
SystemCallFilter=
```

o

```ini
[Service]
MemoryDenyWriteExecute=no
```

Vuelve a aplicar `sudo systemctl daemon-reload` y documenta el cambio. No relajes `ProtectSystem`, `NoNewPrivileges` ni el usuario dedicado.

## 13. Mensajes duplicados o pérdida de mensajes

- **Duplicado tras un corte:** si el proceso muere entre el envío aceptado por Telegram y el guardado de `state.json`, el reintento puede duplicar el mensaje. Es una limitación conocida de Telegram (no hay entrega exactamente una vez) y está documentada en [`architecture.md`](architecture.md).
- **Pérdida por cola llena o caducidad:** la cola tiene 50 entradas y 7 días. Al desbordarse o caducar se descarta la más antigua y se registra en el journal. Revisa `caducadas` y `descartadas` en el resumen.
- **Colapso:** una alerta mensual superior sustituye a la pendiente; una igual o inferior se suprime. Al enviar el 90 % se registran 50, 75 y 90, de modo que el 75 % no se reenvía.
- **`telegram.enabled=false`:** no se encola nada nuevo y la cola existente caduca a los 7 días; al reactivar se colapsa lo mensual y se emite una sola alerta con el nivel actual.

## 14. Totales sospechosos

- **El saliente crece muy rápido o no cuadra con la factura.** Es el tráfico registrado por la VNIC, no el facturable: incluye tráfico privado e interno y se mide antes de los descartes. Úsalo como tendencia.
- **Las dos últimas horas son `provisional`.** OCI no publica una latencia oficial; 2 h es un margen conservador. La reconsulta de 48 h los sobrescribe.
- **El mes anterior sigue “en gracia”.** Se finaliza a partir de fin de mes + `requery_window_hours` (48 h). Es normal.
- **Hay un hueco marcado `incomplete`.** Una hora anterior a la retención de 90 días, una consulta fallida o una VNIC nunca visible no se puede recuperar; el acumulado se marca incompleto.
- **La anomalía de 6 h se disparó con datos provisionales.** El mensaje incluye un `AVISO` y la alerta queda marcada como no fiable; el consumo real puede ser mayor.
- **Un umbral no se repite.** Es la deduplicación: `alerts.monthly_alerts` y la cola evitan reenviar el mismo nivel.

## 15. Reinicio limpio del estado

Úsalo solo si el estado es irrecuperable o si quieres empezar de cero en un servidor de pruebas. **Perderás el historial mensual:**

```bash
sudo systemctl stop oci-infrastructure-monitor.timer
sudo systemctl stop oci-infrastructure-monitor.service

# Copia de seguridad antes de tocar nada
sudo cp -a /var/lib/oci-infrastructure-monitor /root/oci-monitor-state-backup-$(date -u +%Y%m%dT%H%M%SZ)

# Aparta el estado; no lo borres hasta confirmar que la app arranca
sudo mv /var/lib/oci-infrastructure-monitor /var/lib/oci-infrastructure-monitor.old
sudo install -d -m 0700 -o oci-monitor -g oci-monitor /var/lib/oci-infrastructure-monitor

sudo -u oci-monitor oci-infrastructure-monitor run --dry-run
sudo -u oci-monitor oci-infrastructure-monitor run
sudo -u oci-monitor oci-infrastructure-monitor status

sudo systemctl start oci-infrastructure-monitor.timer
```

Sin estado, la aplicación examina solo el mes en curso y no “recupera” meses anteriores que nunca vigiló. Si el mes anterior te importa, restaura la copia en lugar de empezar de cero.

## 16. Antes de pedir ayuda

Adjunta:

```bash
uname -a
getenforce
systemctl status oci-infrastructure-monitor.timer --no-pager
systemctl status oci-infrastructure-monitor.service --no-pager
journalctl -u oci-infrastructure-monitor.service -n 200 --no-pager
sudo -u oci-monitor oci-infrastructure-monitor status
sudo -u oci-monitor oci-infrastructure-monitor version
```

Oculta los OCIDs, el Chat ID y cualquier token antes de compartir la salida.
