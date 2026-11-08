# Auditoría de la v1 — validación y plan de remediación

- **Fecha:** 2026-11-07
- **Base:** `HEAD 6a077c0`, Go 1.27.3, OCI SDK `v65.129.0`
- **Alcance:** los 25 hallazgos de la auditoría (3 medios, 10 bajos, 7 informativos y 5 de repositorio/entrega).
- **Requisito añadido:** trazabilidad del binario desplegado (§10): `Makefile` + `version`, sin Git ni Go en el servidor.
- **Método:** lectura del código de `HEAD`, ejecución de la CLI con un binario recién compilado, `go test ./...` y `go vet ./...` (ambos en verde), inspección de los binarios de `dist/`, de la documentación y del SDK de OCI. Las líneas citadas corresponden a `HEAD 6a077c0`; después de aplicar el plan pueden desplazarse.

> **Conclusión corta.** Los tres hallazgos medios son reales. **M3 es el más grave y está mejor sustentado de lo que la auditoría supone:** no es solo un supuesto sin validar, sino una contradicción con el modelo que el propio proyecto documenta; bajo ese modelo la última hora de cada mes se pierde siempre y el mes se puede finalizar como `completo`. **M2 y M1** también se confirman. De los bajos, la mayoría aplica; **B4 (redacción del token) no aplica**. De los informativos, unos cuantos son mejoras reales y otros son decisiones documentadas o comportamiento de la stdlib. En repositorio/entrega, **R1 es cierto solo en parte** (los binarios sí están sellados) y **R2 es falso** (el enlace no está roto).

---

## 1. Tabla de veredictos

| ID | Hallazgo | Veredicto | ¿Aplica? | Prioridad |
|---|---|---|---|---|
| M1 | Carrera de cuarentena entre `status` y `run` | **Confirmado** | Sí | P1 |
| M2 | Argumentos posicionales extra ignorados | **Confirmado** | Sí | P1 |
| M3 | Pérdida de la última hora del mes | **Confirmado como contradicción documental** (resultado exacto pendiente de 1 consulta real) | Sí | **P0** |
| B1 | `fsync` del directorio best-effort en `writeFileAtomic`/`quarantine` | **Confirmado** | Sí | P2 |
| B2 | Un 200 truncado se clasifica como error no reintentable | **Confirmado** | Sí | P2 |
| B3 | El 429 ignora el header `Retry-After` | **Confirmado** | Sí (mejora menor) | P3 |
| B4 | Redacción del token y re-codificación de URL | **No aplica** con tokens reales | No | — |
| B5 | Cliente OCI sin reintentos ni clasificación por código | **Confirmado** | Sí | P2 |
| B6 | `int64(value)` sin cota superior en `normalize` | **Confirmado** (y peor en arm64) | Sí | P2 |
| B7 | `test-telegram` descarta los errores de persistencia | **Confirmado**; viola `architecture.md` | Sí | P2 |
| B8 | `drainPending` pierde el error de Telegram si falla el guardado | **Confirmado**; viola `README:189` | Sí | P2 |
| B9 | Credenciales cargadas antes del lock y de la recopilación | **Confirmado**, pero es una decisión de diseño | Solo documentación | P3 |
| B10 | `run --help` sale con 2 | **Confirmado** | Sí | P2 |
| I1 | `peekSchema` clasifica `null`/`{}` como versión desconocida | **Confirmado**; el archivo no se toca | Opcional | P3 |
| I2 | `encoding/json` no rechaza `null` en escalares | **Confirmado**; comportamiento de la stdlib | No | — |
| I3 | `outbox` solo acota si el llamador lo aplica; `Validate` no exige unicidad/período | **Confirmado**; librería interna | Endurecimiento opcional | P3 |
| I4 | `network.enabled=false` imprime `pendientes=0` sin leer el estado | **Confirmado**; cosmético | Sí | P3 |
| I5 | Doble renderizador y anomalía reconstruida como mensaje mensual | **Confirmado**; el segundo es un bug real | Sí | P2 |
| I6 | `MonthlyThresholds` no evalúa meses pasados | **Confirmado**; diseño documentado | No (documentar) | — |
| I7 | Ergonomía: `--version`, extras en `version`/`help`, comentario 7/8 | **Confirmado** | Sí | P3 |
| R1 | Binarios de `dist/` desactualizados y sin sellar | **Parcial**: sí están sellados; la procedencia es un árbol *dirty* sobre `8b0fce2` | Solo reproducibilidad | P3 |
| R2 | Enlace roto `README:7 → progress.md#pendientes-de-validación-real` | **Falso**: el encabezado existe en `docs/progress.md:461` | No | — |
| R3 | `internal/oci` sin tests; `cmd/monitor` con cobertura baja | **Confirmado en lo esencial** | Sí | P2/P3 |
| R4 | Sin `git remote` | **Confirmado**; operativo | No | — |
| R5 | `.tmp/` con fixtures y tokens ficticios, ignorado por git | **Confirmado**; sin acción | No | — |

---

## 2. Hallazgos medios

### M3 — La última hora del mes se pierde de forma permanente (P0)

**Lo que hace el código.** El proyecto asume dos cosas a la vez:

1. El timestamp de un punto agregado marca el **final** de la ventana (`normalize.go:20-34`, `docs/architecture.md:63`, `docs/decisions.md:13`).
2. El `endTime` de la consulta es **exclusivo** (`docs/architecture.md:63`), algo que el SDK confirma: *“The response excludes metric data points for the endTime”* (`monitoring/summarize_metrics_data_details.go`).

La consulta de finalización termina exactamente en `monthEnd` (`plan.go:139-144`), y la de gracia también (`plan.go:149`). Bajo los dos supuestos anteriores, el punto de la hora 23:00 del último día tiene timestamp `monthEnd` y el servidor lo excluye. **Ninguna otra consulta de ese mes termina después de `monthEnd`**, así que esa hora no se recupera nunca.

Al reconciliar, `Reconcile` no ve muestra para las 23:00, la VNIC ya era visible y la hora no es reciente: la guarda como `no_data` con 0 bytes (`reconcile.go:112-120`). La consulta de gracia posterior al cierre ya crea ese bucket; cuando llega la finalización, `oldestAttention` no lo considera atención (solo mira missing/provisional/incomplete) y ni siquiera lo reconsulta. Como `PublishZeros` es `false`, `no_data` no invalida el acumulado; el mes puede quedar marcado como **`completo`** y finalizarse inmutable con una hora menos. La pérdida es de 1/672 a 1/744 del mes (≈0,13–0,15 %), silenciosa y permanente.

**Por qué los tests no lo detectan.** El stub de OCI de `cmd/monitor/run_test.go:33` genera un punto en `hour+1h` para cada hora del rango, **incluido `query.EndTime`**, que es justo el punto que la API real excluye. El fake es más permisivo que el servidor y tapa el defecto.

**Veredicto.** Confirmado. La auditoría lo dejó como “sin confirmar” porque falta la consulta real, pero el código no puede ser correcto tal como está bajo el modelo que el propio proyecto documenta. Si la consulta real mostrara que el timestamp marca el **inicio** de la ventana, el error sería peor: `BucketHour` restaría una hora a todos los puntos y desplazaría el mes completo. En cualquiera de los dos casos hay que corregir; la consulta real solo decide **cuál** de las dos correcciones aplicar.

**Remediación propuesta (bajo el modelo documentado).**

- Mantener `Job.End` como frontera **de datos** (sigue siendo el fin exclusivo del mes) y añadir una frontera **de consulta**:

```go
// QueryEnd returns the endTime to send to OCI. Aggregated points are
// timestamped at the end of their window and the API excludes endTime,
// so the query must reach one resolution past the last bucket we want.
func (j Job) QueryEnd() time.Time { return j.End.Add(time.Hour) }
```

- Usar `job.QueryEnd()` en `collectInternal` (`collect.go:190-197`) y `end.Add(time.Hour)` en `check.go:57-64`.
- No tocar `NormalizeSeries`/`Reconcile`: siguen filtrando contra `job.End`, así que un punto hipotético en `monthEnd+1h` se descarta.
- Hacer que el stub respete el contrato real: no emitir puntos con `timestamp >= query.EndTime`.
- Añadir un test de extremo a extremo de finalización: un mes de octubre completo, `now` posterior a la gracia, y verificar que el bucket de las 23:00 del 31 tiene los bytes esperados (no `no_data`/0).
- Si la consulta real muestra que el timestamp es el inicio de ventana, en su lugar: `BucketHour` devuelve `truncated` sin restar la resolución, y **no** se extiende `QueryEnd`. Añadir el test correspondiente.

**Validación empírica barata (ver §7, Fase 0).**

---

### M1 — Carrera de cuarentena entre `status` y `run` (P1)

**Lo que hace el código.** `status` llama a `app.BuildStatus` (`main.go:181-182`), que lee con `LoadOptions{Quarantine:true}` sin tomar el lock (`status.go:49-75`). `run` sí toma el lock exclusivo (`run.go:186-198`) y escribe con `rename` atómico (`store.go:450`). `quarantine` renombra **lo que haya en la ruta en ese instante** (`store.go:360-369`) sin volver a leer ni comprobar el contenido.

La ventana es la que va entre el `os.ReadFile` de `status` (`store.go:171`) y el `os.Rename` de `quarantine` (`store.go:365`). Si el `rename` de `run` aterriza ahí, el archivo nuevo y válido va a `*.corrupt-*`.

**Impacto.** El siguiente `run` arranca sin `state.json`: se pierden la cola de pendientes, los cooldowns y el registro de alertas notificadas. Los totales del mes no se corrompen (el historial es otro archivo) y los meses finalizados se re-adoptan desde el historial, pero puede haber reenvíos de alertas y notificaciones perdidas. La probabilidad es minúscula; el patrón de pérdida es real.

**Veredicto.** Confirmado.

**Remediación recomendada: `status` deja de escribir.** Es la opción más simple y elimina la clase de bug por construcción:

```go
func BuildStatus(store *persistence.Store, cfg config.Config, now time.Time) StatusReport {
    // status es de solo lectura: informa de la corrupción pero no mueve archivos.
    return BuildStatusWithOptions(store, cfg, now, persistence.LoadOptions{Quarantine: false})
}
```

`solo run` (que ya tiene el lock) cuarentena; `test-telegram` y `--dry-run` ya lo hacen sin mover nada. Esto es coherente con la decisión del proyecto de que “un diagnóstico no debe alterar el estado que intenta diagnosticar” (`docs/decisions.md:43`); `status` era la excepción. Hay que actualizar `README.md` (tabla de CLI), `docs/architecture.md:189-198`, `docs/decisions.md`, `docs/progress.md` y los tests que hoy afirman que `BuildStatus` mueve el archivo (`status_test.go:170-199`).

**Alternativa si se quiere conservar la cuarentena en `status`:** añadir un lock compartido (`flock LOCK_SH`) para la lectura y la cuarentena; si el lock está ocupado por un `run`, leer con `Quarantine:false` y avisar “run en curso; no se mueve nada”. Es más código, añade contención con el lock exclusivo del `run` y no elimina la necesidad de probar el caso. La opción de “releer y re-verificar” reduce la ventana pero no la cierra: entre la relectura y el `rename` sigue habiendo un intervalo.

**Test de regresión.** Estado corrupto + `executeStatus` → sale 4, `state.json` sigue en su sitio y no hay `*.corrupt-*`; después, `run` con el lock → sí cuarentena.

---

### M2 — La CLI ignora argumentos posicionales extra (P1)

**Lo que hace el código.** Los cuatro subcomandos hacen `fs.Parse(args)` pero nunca consultan `fs.Args()` (`main.go:75-77`, `130-132`, `171-173`, `196-198`). Además, `flag` deja de parsear en el primer argumento posicional, así que los flags que vengan **después** tampoco se aplican.

**Evidencia reproducida.**

| Comando | Resultado observado |
|---|---|
| `status --config .tmp/cli/config.json extra` | exit 0; usa el config indicado e ignora `extra` |
| `status --config .tmp/cli/config.json /ruta/config.json` | exit 0; usa el primer config, ignora la ruta |
| `run --dry-run --config … extra` | exit 0; ejecuta la simulación e ignora `extra` |
| `run --dry-run extra --config …` | exit 2; `flag` se detiene en `extra` y `--config` no se aplica |

**Impacto.** El caso que describe la auditoría es real: `monitor status /ruta/config.json` (olvidar `--config`) usa el config por defecto sin avisar; si el config por defecto existe, un `run` mal invocado puede escribir en el directorio de estado de producción o enviar alertas reales. Contradice la filosofía estricta que el README proclama para la configuración (`README.md:90`).

**Veredicto.** Confirmado.

**Remediación.** Un helper único para los cuatro subcomandos:

```go
// parseFlags parsea y exige que no queden argumentos posicionales.
// Devuelve (exitCode, ok). En ErrHelp devuelve 0: la ayuda ya la imprime
// flag (por stderr; si se quiere stdout, ajustar fs.SetOutput/fs.Usage).
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
    if err := fs.Parse(args); err != nil {
        if errors.Is(err, flag.ErrHelp) { return 0, false }
        return 2, false
    }
    if fs.NArg() > 0 {
        fmt.Fprintf(os.Stderr, "%s: argumentos no admitidos: %v\n", fs.Name(), fs.Args())
        fs.Usage()
        return 2, false
    }
    return 0, true
}
```

- Llamarlo **antes** de cargar la configuración, para que el test no necesite un config válido.
- Test: `execute([]string{"status", "x"})` → 2; `execute([]string{"run", "--help"})` → 0; `execute([]string{"version", "x"})` → 2.
- Añadir `--version` como alias de `version` (hoy cae en “unknown command”, exit 2).
- `version`/`help` con argumentos extra deben rechazarse (hoy los aceptan y salen 0).

---

## 3. Hallazgos bajos

### B1 — `fsync` del directorio best-effort

`writeFileAtomic` hace bien el orden crítico (fsync del archivo antes del `rename`), pero después ignora cualquier fallo al abrir o sincronizar el directorio (`store.go:454-457`). `quarantine` renombra sin `fsync` del directorio (`store.go:365`). Tras un corte de energía, el `rename` podría no ser durable.

**Remediación.** Helper `syncDir(dir) error` que abra el directorio, haga `Sync()` e ignore solo `EINVAL`/`ENOTSUP`; devolver el error en `writeFileAtomic` después del `rename` y adjuntarlo al `CorruptionError` en `quarantine`. Test: guardado normal sigue funcionando; un test con un hook que fuerza el fallo verifica que se propaga.

### B2 — Un 200 truncado pierde el reintento

En `client.go` la rama `case response.StatusCode == http.StatusOK && !decoded.OK` (`telegram/client.go:259-265`) precede a `case readErr != nil` (`:264`), así que un 200 con cuerpo truncado o vacío entra en la primera rama y se devuelve como `APIError` **no reintentable**. El mensaje no se pierde (queda en cola), pero se pierde el reintento.

**Remediación.** Leer el cuerpo y comprobar `readErr`/`decodeErr` antes de interpretar `ok`:

```go
body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
if readErr != nil {
    return attemptResult{err: &TransportError{...}, retry: true}
}
var decoded apiResponse
decodeErr := json.Unmarshal(body, &decoded)
switch {
case response.StatusCode == http.StatusOK && decodeErr != nil:
    return attemptResult{err: &TransportError{message: "respuesta de Telegram ilegible"}, retry: true}
case response.StatusCode == http.StatusOK && decoded.OK:
    return attemptResult{}
case response.StatusCode == http.StatusOK && !decoded.OK:
    return attemptResult{err: &APIError{...}} // ok:false con cuerpo válido no se reintenta
...
}
```

Tests: 200 truncado → se reintenta; 200 `{"ok":false}` → una sola llamada y `APIError`; 200 `{"ok":true}` → éxito.

### B3 — `429` sin `Retry-After` de cabecera

El código usa solo `decoded.Parameters.RetryAfter` (`telegram/client.go:243-253`). Telegram normalmente lo manda en el cuerpo; el header `Retry-After` es un fallback razonable. **Remediación:** si el cuerpo no trae un valor > 0, parsear `Retry-After` (segundos o fecha HTTP) antes de caer al segundo por defecto. Test con un 429 que solo trae header.

### B4 — Redacción del token: no aplica

La sustitución es literal (`telegram/client.go:280-285`) y no cubre re-codificaciones de URL. Con el formato real de Telegram (`digits:[A-Za-z0-9_-]`) esos caracteres no se re-codifican, así que no hay vía de escape práctica. `TransportError` sin `Unwrap` es la decisión correcta: evita que un `*url.Error` con el token en la URL se imprima al desenvolver (`:57-64`). **No se propone cambio.**

### B5 — Cliente OCI sin reintentos

Los métodos del cliente usan `NoRetryPolicy()` cuando el cliente no tiene política configurada (`monitoring_client.go`: `policy := common.NoRetryPolicy(); if client.RetryPolicy() != nil { ... }`), y este código no configura ninguna. Un 429/5xx transitorio marca las horas como `incomplete` y el `run` sale 3. El requery de 48 h lo mitiga, pero un reintento corto es barato.

**Remediación.** Envolver `SummarizeMetricsData` y `ListMetrics` en un bucle de hasta 2–3 intentos, solo para errores reintentables (`common.IsErrorRetryableByDefault`), con backoff corto y respeto del `ctx` (el `queryTimeout` de 30 s ya acota el total). No usar `OCI_SDK_DEFAULT_RETRY_ENABLED=true`: la política por defecto del SDK son 8 intentos / ~1,5 min, demasiado larga para el ciclo. Tests con un `HTTPRequestDispatcher` falso: 500 y luego 200 → 2 llamadas; 401 → 1 llamada.

### B6 — `int64(value)` sin cota superior

`normalize.go:64-68` redondea y clampea negativos sobre el `float`, pero convierte a `int64` sin comprobar el rango. El resultado depende de la arquitectura: en amd64 `CVTTSD2SQ` produce `MinInt64`; en **arm64** (el objetivo documentado) `FCVTZSD` satura a `MaxInt64`. Lo comprobamos con el compilador:

```
arm64:  FCVTZSD  F0, R0
amd64:  CVTTSD2SQ X0, AX
```

En amd64 un `1e30` se convierte en un bucket negativo y `History.Validate` hace fallar el guardado (exit 4); en arm64 se convierte en `MaxInt64`, pasa la validación y contamina la suma (que además puede desbordar). Es peor que lo que describe la auditoría.

**Remediación.** Descartar el punto si no cabe:

```go
const maxInt64AsFloat = float64(1 << 63) // 2^63
value := math.Round(point.Value)
if value >= maxInt64AsFloat {
    continue // dato imposible; la hora se resolverá como no_data/incomplete
}
if value < 0 {
    value = 0
}
hours[hour] = int64(value)
```

Tests: `1e30` se descarta; `-1` → 0; un valor normal se conserva. Idealmente, contar los descartes y exponerlos en `CollectReport` para que el `run` los registre.

**Hallazgo adicional relacionado.** Hay dos `bytesFromGiB`: el de `internal/alerts/format.go:17-25` sí satura; el de `internal/app/messages.go:270-275` no. Un `monthly_limit_gib` absurdo (la validación solo exige finito y > 0, `config.go:178-182`) desborda en el segundo y hace que el mensaje diga “límite no configurado”. Conviene unificar en un solo helper saturado.

### B7 — `test-telegram` descarta la corrupción que reporta el store

`main.go:152-155` toma `.Months[0].Summary` y no mira `report.Errors` ni `StateErr`; el mensaje de prueba se envía igual. El README se cumple (“lee el estado sin cuarentena”), pero `docs/architecture.md:198` promete que “informa de un archivo corrupto pero no lo mueve”. Hoy no informa.

**Remediación.** Imprimir `report.Errors` en stderr con prefijo `test-telegram:` y decidir el código: recomiendo mantener 0 si el envío fue bien (el contrato del comando es probar Telegram) y dejar que `status`/`run` señalen la persistencia; si se prefiere estricto, devolver 4 tras el envío. Documentar la decisión.

### B8 — `drainPending` pierde el error de Telegram si falla el guardado

En `run.go:493-500`, si el envío falla y luego `SaveState` falla, se devuelve `saveErr` **antes** de setear `result.Failed`/`result.Err`; `Run` (`run.go:331-337`) solo registra el error de persistencia. `README:189` promete “deja todos en el log”. La notificación no se pierde (queda en cola), pero se pierde el diagnóstico.

**Remediación.** Setear `result.Failed = true` y `result.Err = err` antes de intentar guardar; en `Run`, si `drain.Err != nil`, setear también `report.TelegramError` y loguearlo, aunque el código de salida sea 4 por prioridad. Test: inyectar un `save` que falle (o un store en un directorio no escribible, con skip si el test corre como root) y comprobar que ambos errores aparecen en el log.

### B9 — Credenciales antes del lock y de la recopilación

`main.go:92-104` carga `telegram.env` y construye el sender antes del lock (`run.go:188`) y de la recopilación. Un `telegram.env` roto aborta con 2 sin consultar OCI; los huecos solo se recuperan dentro de la ventana de 48 h. El orden es correcto para la prioridad `2 > 3`, pero el coste no está documentado.

**Veredicto.** No es un bug; es una decisión. **Remediación:** documentarlo en `README.md`/`troubleshooting.md` (“si `telegram.env` está roto, corrige y relanza dentro de la ventana de 48 h”). Mejora opcional: no abortar en `main`, pasar `sender=nil` y dejar que `Run` recolecte y guarde antes de devolver el `ConfigError` (2); así no se pierden horas. Es un cambio de comportamiento y merece una decisión explícita.

### B10 — `run --help` sale con 2

`fs.Parse` devuelve `flag.ErrHelp` y los cuatro subcomandos lo tratan como error (`main.go:75-77`, etc.). `--help` a nivel raíz sí sale 0 (`main.go:59-61`). **Remediación:** el helper de M2 intercepta `flag.ErrHelp` y devuelve 0 (la ayuda ya la imprime `flag`; si se quiere en stdout, ajustar `fs.SetOutput`/`fs.Usage`); los errores de parseo de verdad siguen saliendo 2 por stderr. Tests para los cuatro subcomandos.

---

## 4. Informativos

### I1 — `peekSchema` y los JSON válidos que no son objetos

`peekSchema` (`store.go:394-402`) deserializa sobre un struct; `null` y `{}` dejan `SchemaVersion=0` y se reportan como “versión desconocida” (`ErrSchemaVersion`) en vez de “corrupto”. El archivo no se toca en ninguno de los dos casos, así que el impacto es informativo. **Remediación opcional:** exigir que el primer byte no blanco sea `{` y que `schema_version` sea > 0; entonces `{}`/`null` se clasifican como corruptos y siguen la cuarentena normal. Cambia el tipo de error, no el código de salida.

### I2 — `encoding/json` y los `null` en escalares

Es comportamiento de la stdlib: un `null` en un campo escalar deja el valor cero sin error. Los casos que importan fallan por validación posterior (p. ej. `alert_percentages: null` → 0 porcentajes → error de configuración). **No se propone cambio**; si se quisiera una lectura estricta de verdad habría que decodificar sobre `json.RawMessage` o usar un decoder con tipos explícitos, y no compensa.

### I3 — El outbox solo acota si el llamador lo aplica

`outbox.Expire`/`Enqueue` son funciones puras; el binario las aplica en `run.go:292-297` y `:391`, pero un consumidor de la librería podría saltárselas. `State.Validate` (`model.go:475-505`) no exige unicidad de `(kind, period)` ni `Period` no vacío para todos los kinds. Al ser `internal/outbox`, el riesgo es teórico. **Remediación opcional:** en `State.Validate`, exigir `Period` no vacío y unicidad por `(kind, period)`; y en `outbox.Enqueue`, aplicar `Expire` al principio (o documentar que es responsabilidad del llamador).

### I4 — `network.enabled=false` imprime `pendientes=0`

`run.go:205-211` retorna antes de leer el estado; `report.Pending` queda 0 y `writeRunReport` lo imprime como si se hubiera leído. **Remediación:** añadir `PendingKnown bool` al `RunReport` (true tras cargar el estado) y imprimir `pendientes=n/d` o `pendientes=no consultado` cuando sea false. Test del caso `network.enabled=false`.

### I5 — Doble renderizador y anomalía reconstruida como mensaje mensual

Dos problemas con la misma raíz:

1. Los tests validan `alerts.Alert.Text()` (`alerts_test.go`, `monthly_test.go:88`, `anomaly_test.go:70`), pero lo que se envía es `app.RenderAlert` (`messages.go:29-38`). Los formatos difieren: el test espera “Alerta mensual — informativa”; producción envía “OCI Infrastructure Monitor — alerta mensual (informativa)”. Una regresión en el renderizador real no la detecta ningún test.
2. Al encolar, `Alert.Notification()` no rellena `Text` (`alerts.go:140-152`); `Run` guarda la notificación sin texto y solo usa `candidate.Text` para el envío inmediato. Si el envío falla o el proceso cae, `RenderPending` reconstruye la anomalía con `renderPendingGeneric`, que usa `measurementMonthly` y pierde la ventana de 6 h original (`messages.go:145-182`).

**Remediación.**

- Persistir el texto al encolar: `notification.Text = candidate.Text` antes de `outbox.Enqueue`. Así `RenderPending` devuelve el texto original; la reconstrucción queda solo para estados migrados sin texto. Test: envío fallido de una anomalía → siguiente `run` reenvía el mismo texto con la ventana de 6 h.
- Mover las aserciones de formato a `internal/app/messages_test.go` contra `RenderAlert`; borrar `Alert.Text()`/`MonthlyAlert.Text()`/`AnomalyAlert.Text()` (no los usa producción) o documentarlos como no canónicos. Preferible borrarlos para que no haya dos fuentes de verdad.

### I6 — `MonthlyThresholds` no evalúa meses pasados

Solo mira `FormatMonth(now)` (`evaluate.go:76-131`). Si el proceso está caído más de un mes, los umbrales del mes intermedio no se alertan nunca. Está documentado en el comentario del evaluador y en `decisions.md`; es una limitación de producto, no un bug. **Remediación:** añadirlo a la sección de limitaciones del README; un evaluador de “catch-up” sería otro diseño (y podría generar alertas fuera de tiempo).

### I7 — Ergonomía y comentario de los pasos 7/8

- `--version` no se reconoce (exit 2); solo `version`.
- `version extra` y `help extra` se aceptan y salen 0.
- El comentario de `Run` dice “7. persistir historiales; 8. sumar el mes”, pero `collectInternal` suma en memoria antes de que `Run` persista los historiales (`run.go:124-140`, `collect.go:282-287`, `run.go:258-265`). Es un desfase de documentación, no de comportamiento.

**Remediación:** incluir `--version`/`-version` en el `switch`; rechazar extras en `version`/`help`; corregir el comentario (o renumerar) para que describa el orden real.

---

## 5. Repositorio y entregables

### R1 — Binarios de `dist/`: sellados, pero con procedencia *dirty*

La auditoría afirma que no se usó `-ldflags -X` y que imprimirían `dev/unknown/unknown`. **No es así.** El binario de `dist/` imprime:

```
$ ./dist/oci-infrastructure-monitor-linux-amd64 version
oci-infrastructure-monitor 1.0.0
commit: 8b0fce2
built: 2026-11-07T22:15:20Z
```

Lo que sí es cierto: `go version -m` muestra `vcs.revision=8b0fce209a59…` y `vcs.modified=true`, es decir, se compiló desde un **árbol de trabajo sucio** sobre `8b0fce2`, no desde `HEAD` limpio. `docs/delivery.md:6` lo documenta expresamente (“compilada desde el árbol de trabajo sobre el commit `8b0fce2`; incluye las correcciones… `test-telegram` sin cuarentena”). La confusión de la auditoría es comprensible: **Go 1.27.3 no registra `-ldflags` en `go version -m`**; lo verificamos compilando un binario sellado y comprobando que el buildinfo tampoco muestra `-ldflags`, aunque `version` imprime los valores. El sello se comprueba con `version`, no con `go version -m`.

`SHA256SUMS` coincide con los binarios presentes; `dist/` está en `.gitignore` y no se versiona.

**Remediación (opcional, P3):** recompilar desde `HEAD` limpio con `make dist-monitor` (§10) para que `vcs.revision=6a077c0` y `vcs.modified=false`, regenerar `SHA256SUMS` y actualizar `docs/progress.md:327` con el nuevo commit/fecha y la salida de `version`. El target verifica que el `vcs.revision` del binario coincide con el commit compilado. No hay que “sellar” nada: ya lo está. Si se recompila, recordar que `go version -m` no mostrará las `-X`; verificar con `version`.

### R2 — El enlace no está roto

`README:7` apunta a `docs/progress.md#pendientes-de-validación-real`. El encabezado existe:

```
docs/progress.md:461: ## Pendientes de validación real
```

y el ancla de GitHub para ese texto es exactamente `#pendientes-de-validación-real`. El encabezado está en el archivo desde el commit `4c6b06b` (`git log -S`). **No se propone cambio.** Si el revisor abrió el enlace en un visor que no soporta anclas Unicode, podría fallar ahí, pero no en GitHub/GitLab.

### R3 — Cobertura de tests

`internal/oci` no tiene ningún archivo de test: confirmado (`go test ./...` lo reporta como `[no test files]`). Es el único paquete que toca el SDK real, así que es la laguna más importante. `cmd/monitor` tiene pocos tests de CLI (los de `run` usan stubs, pero no hay tests de argumentos/exit codes).

No pude reproducir el 70 % exacto de `cmd/monitor`: en este entorno `go test -cover` falla con `go: no such tool "covdata"` (toolchain incompleto), aunque `go test ./...` y `go vet ./...` pasan. No es un problema del repositorio; en un toolchain completo se mide con `go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out`.

**Remediación.** Añadir tests de `internal/oci` con un `HTTPRequestDispatcher` falso (mapeo de series/puntos, omisión de nulos, paginación de `ListMetrics`, errores y reintentos) y tests de `cmd/monitor` para `execute` (M2/B10, `--version`, extras). Objetivo razonable: cubrir las ramas de error y el contrato de la CLI, no perseguir un porcentaje.

### R4 — Sin `git remote`

Confirmado (`git remote -v` vacío). Es una decisión operativa, no un defecto del código. **No se propone cambio** salvo que se quiera publicar; en ese caso, configurar el remoto y decidir qué hacer con `dist/` (hoy ignorado) y `.tmp/`.

### R5 — `.tmp/` con fixtures y tokens ficticios

Confirmado: `.tmp/audit1/telegram.env` y `.tmp/audit2/telegram.env` contienen `123456789:AAinvalidtoken`; `.gitignore` cubre `.tmp/`. No hay fuga. **No se propone cambio**; opcionalmente borrar los fixtures de auditorías viejas para no confundir.

---

## 6. Lo que no se remedia con código (resumen)

| ID | Por qué |
|---|---|
| B4 | El charset real del token (`digits:[A-Za-z0-9_-]`) no se re-codifica; `TransportError` sin `Unwrap` es la decisión correcta. |
| I2 | Comportamiento de `encoding/json`; los casos relevantes fallan por validación posterior. Endurecerlo no compensa. |
| I6 | Limitación de producto documentada; un “catch-up” de meses pasados es otro diseño. |
| R2 | El encabezado y el ancla existen; el enlace no está roto. |
| R4 | Operativo; no hay código que arreglar. |
| R5 | Los fixtures son ficticios y están ignorados por git. |
| B9 | No es un bug: es la prioridad `2 > 3`. Solo falta documentar el coste (o decidir recolectar antes de fallar). |
| R1 | Los binarios sí están sellados; la procedencia *dirty* está documentada. Solo es una mejora de reproducibilidad. |

---

## 7. Plan de remediación

### Fase 0 — Validación empírica de M3 (bloqueante solo para elegir la variante del fix)

1. En el host con Instance Principals, ejecutar `check` sobre una VNIC con tráfico conocido en la última hora. `check` consulta `[end-1h, end)` con `end = now.Truncate(hour)` (`check.go:38-40`).
   - **`DataPoints == 0`** → el punto de la última hora está en `end` y la API lo excluye → timestamp al final + `endTime` exclusivo → **M3 confirmado**; aplicar `QueryEnd = End + 1h`.
   - **`DataPoints > 0`** → hay que mirar el timestamp del punto: si es `start`, el timestamp marca el inicio y `BucketHour` está desplazando **todas** las horas (aplicar la otra variante, quitar el `-1h` de `BucketHour`, y revisar los tests); si es `end`, la API estaría incluyendo el `endTime` pese a la documentación del SDK y el código actual sería correcto (verificarlo con una segunda hora).
   - Repetir en 2–3 horas distintas para descartar una hora sin tráfico. Para ver el timestamp, añadir temporalmente un `log` en `check` o un `--verbose` que imprima `Point.Time`.
2. Consulta puntual de la última hora de un mes pasado con `endTime = monthEnd` y con `endTime = monthEnd + 1h`; comparar si aparece el punto de las 23:00.
3. Registrar el resultado en `docs/progress.md` (sustituir el pendiente “formato real de los timestamps”).

### Fase 1 — P0/P1 (datos y corrección silenciosa)

| # | Tarea | Archivos | Test | Esfuerzo |
|---|---|---|---|---|
| 1 | M3: `Job.QueryEnd()` + uso en `collect`/`check`; stub fiel; test de finalización | `accumulate/plan.go`, `app/collect.go`, `app/check.go`, `cmd/monitor/run_test.go` | bucket 23:00 con bytes tras finalizar | 3–4 h |
| 2 | M2: helper de parseo, rechazo de `NArg > 0`, `--version`, extras en `version`/`help` | `cmd/monitor/main.go`, `main_test.go`/`run_test.go` | tabla de exit codes | 1–2 h |
| 3 | M1: `status` de solo lectura; actualizar docs/tests | `app/status.go`, `cmd/monitor/main.go`, tests, docs | corrupto + status → no mueve; run → mueve | 2–3 h |

### Fase 2 — P2 (defectos funcionales acotados)

| # | Tarea | Archivos | Test | Esfuerzo |
|---|---|---|---|---|
| 4 | B2: 200 truncado reintentable; orden del `switch` | `telegram/client.go` | 200 truncado / `ok:false` / éxito | 1 h |
| 5 | B8: conservar el error de Telegram si falla el guardado | `app/run.go` | fallo de envío + fallo de save | 1–2 h |
| 6 | I5: persistir `Text` al encolar; test de reenvío de anomalía | `app/run.go`, `alerts/alerts.go` | anomalía fallida → mismo texto | 1 h |
| 7 | B7: `test-telegram` informa de la corrupción | `cmd/monitor/main.go` | corrupto + test-telegram → warning, no mueve | 1 h |
| 8 | B6: cota superior en `normalize`; unificar `bytesFromGiB` | `accumulate/normalize.go`, `app/messages.go` | `1e30` se descarta; `-1` → 0 | 1–2 h |
| 9 | B1: `syncDir` y propagación de errores | `persistence/store.go` | guardado normal + hook de fallo | 1–2 h |
| 10 | B5: reintentos cortos y clasificación en OCI | `oci/client.go` | 500→200; 401 no reintenta | 3–4 h |
| 11 | B10: `--help` en 0 y en stdout | `cmd/monitor/main.go` | 4 subcomandos | 1 h |

### Fase 3 — P3 (robustez, tests y entrega)

| # | Tarea | Archivos | Esfuerzo |
|---|---|---|---|
| 12 | B3: fallback `Retry-After` | `telegram/client.go` | 1 h |
| 13 | I1: `peekSchema` exige objeto y versión > 0 | `persistence/store.go` | 30 min |
| 14 | I3: `Validate` exige período/unicidad; `Enqueue` expira | `persistence/model.go`, `outbox/outbox.go` | 1–2 h |
| 15 | I4: `pendientes=n/d` con red deshabilitada | `app/run.go`, `cmd/monitor/main.go` | 30 min |
| 16 | I5: mover aserciones a `messages_test.go` y borrar `Alert.Text()` | `alerts/*`, `app/messages_test.go` | 2–3 h |
| 17 | I7: `--version`, extras, comentario 7/8 | `cmd/monitor/main.go`, `app/run.go` | 30 min |
| 18 | R3: tests de `internal/oci` y de `execute` | `oci/client_test.go`, `cmd/monitor/*_test.go` | 1 día |
| 19 | R1: recompilar desde `HEAD` limpio con `make dist-monitor` (§10) y regenerar sums | `dist/`, `docs/progress.md` | 30 min |
| 20 | Docs: M3, M1, B9, I6, `test-telegram` | `README.md`, `docs/architecture.md`, `decisions.md`, `progress.md` | 2–3 h |

### Criterios de aceptación globales

- `go test ./...`, `go vet ./...` y `go test -race ./...` en verde.
- Tests nuevos que fallen antes del cambio y pasen después (especialmente M3, M1, M2, B2, B8).
- La CLI: `status --config X extra` → 2; `run --help` → 0; `--version` → 0; `version extra` → 2.
- `status` con estado corrupto: sale 4, informa y **no** mueve el archivo; `run` sí lo mueve (si se adopta la opción recomendada de M1; con la alternativa del lock compartido, `status` solo mueve cuando el lock está libre).
- `sha256sum -c dist/SHA256SUMS` en verde; en el host de build, `go version -m` muestra `vcs.revision` = `HEAD`; en el servidor, `version` muestra ese mismo commit (no se puede ejecutar el binario ARM64 en un host amd64).
- Documentación actualizada: ninguna promesa que el código no cumpla.

### Decisiones que requieren tu criterio

El enfoque de trazabilidad ya está decidido: **`Makefile`** como interfaz de build (puede invocar un script para empaquetado o comprobaciones auxiliares) y **`version`** como comando de consulta en el servidor. La fila R1/build queda resuelta por esa decisión; el resto sigue abierto.

| Tema | Opción recomendada | Alternativa |
|---|---|---|
| M1 | `status` de solo lectura; `run` es el único que cuarentena. | Lock compartido para `status`, con fallback sin cuarentena si hay un `run`. |
| B7 | `test-telegram` avisa por stderr y mantiene 0 si el envío fue bien. | Devolver 4 tras el envío para señalar también la persistencia. |
| B9 | Documentar el coste de abortar antes de recolectar. | Recolectar y guardar primero; devolver el `ConfigError` (2) al final. |
| I6 | Aceptar la limitación y documentarla. | Evaluador de “catch-up” para el mes intermedio (otro diseño). |
| R1 / build | **(decidido)** `Makefile` `dist-monitor`: `-X`, verificación de `vcs.revision` y `SHA256SUMS`; reconstruir desde `HEAD` limpio. | Dejar los binarios actuales (sellados pero *dirty*) y no añadir Makefile. |

---

## 8. Anexo: evidencia reproducible

```text
$ go test ./...
ok  cmd/monitor · accumulate · alerts · app · config · logging
ok  outbox · persistence · telegram
?   internal/oci [no test files]

$ go vet ./...
(sin salida)

$ .tmp/bin/ocimon status --config .tmp/cli/config.json extra
exit=0
$ .tmp/bin/ocimon run --dry-run --config .tmp/audit-m1/config.json extra
run (simulación): ... pendientes=0 ...
exit=0
$ .tmp/bin/ocimon run --help
exit=2
$ .tmp/bin/ocimon --version
unknown command "--version"
exit=2

$ ./dist/oci-infrastructure-monitor-linux-amd64 version
oci-infrastructure-monitor 1.0.0
commit: 8b0fce2
built: 2026-11-07T22:15:20Z

$ go version -m dist/oci-infrastructure-monitor-linux-amd64 | grep vcs
build vcs.revision=8b0fce209a59381ed3098a1d3ba1009c683c9b21
build vcs.time=2026-11-07T21:19:18Z
build vcs.modified=true

$ GOARCH=arm64 go tool compile -S f.go | grep FCVTZ
FCVTZSD F0, R0
$ GOARCH=amd64 go tool compile -S f.go | grep CVTT
CVTTSD2SQ X0, AX
```

Y la cita del SDK que sostiene M3:

> “The response includes metric data points for the startTime.”
> “The response excludes metric data points for the endTime.”
> — `monitoring/summarize_metrics_data_details.go`, OCI Go SDK v65.129.0.

---

## 9. Orden recomendado de ejecución

1. **M3** (Fase 0 + tarea 1): es la única pérdida permanente y silenciosa.
2. **M2** (tarea 2): evita invocaciones accidentales contra el estado de producción.
3. **M1** (tarea 3): cierra la pérdida de estado por carrera.
4. **B2/B8/I5** (tareas 4–6): afectan a la entrega de alertas y al diagnóstico.
5. **B6/B7/B1/B5/B10** (tareas 7–11).
6. **Trazabilidad (§10):** `Makefile` `dist-monitor`, `version` documentado en el README y recompilación final de `dist/` con ese target.
7. El resto (P3) cuando el árbol esté limpio y los tests en verde.

---

## 10. Trazabilidad del binario desplegado (Makefile + `version`)

### 10.1 Restricción y decisión

- El servidor de despliegue es **ARM64** y **no tiene Git ni Go**. El operador entra por SSH y solo puede ejecutar el binario.
- El build se hace fuera del servidor (máquina de desarrollo o CI), donde sí hay Git y Go.
- **Decisión:** la interfaz de build es un **`Makefile`** (ya se usa en los otros proyectos del responsable). Si una tarea auxiliar se complica (empaquetado, comprobaciones), el target puede invocar un script; lo que se documenta y se usa es `make ...`.
- **Modo estricto:** `dist-monitor` exige un árbol limpio (`git status --porcelain` vacío). No hay target para builds sucios; un release no debe llevar un hash que no identifique el código.
- **Comando de consulta:** `oci-infrastructure-monitor version`, que ya existe. No se añade endpoint HTTP ni comando nuevo; el servidor no necesita Git ni Go para ejecutarlo.
- **Inyección:** `-X main.version`, `-X main.commit`, `-X main.buildDate`. `-buildvcs=true` deja además `vcs.revision`/`vcs.modified` en el binario; el propio binario los imprime (tarea 23, implementada).

### 10.2 Makefile propuesto

```make
.DEFAULT_GOAL := dist-monitor

GO          ?= go
VERSION     := $(shell git describe --tags --always)
GIT_COMMIT  := $(shell git rev-parse --short=12 HEAD)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_LDFLAGS  := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)"

DIST        := dist
BIN_ARM64   := $(DIST)/oci-infrastructure-monitor-linux-arm64

.PHONY: dist-monitor check-clean test vet

check-clean:
	@git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { \
		echo "error: no es un repositorio git; se necesita Git para el commit" >&2; \
		exit 1; \
	}
	@test -z "$$(git status --porcelain)" || { \
		echo "error: árbol sucio; commitea los cambios antes de compilar un release" >&2; \
		git status --short >&2; \
		exit 1; \
	}

dist-monitor: check-clean
	@mkdir -p $(DIST)
	@echo "Compilando $(BIN_ARM64) (commit: $(GIT_COMMIT))..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -buildvcs=true $(GO_LDFLAGS) -o $(BIN_ARM64) ./cmd/monitor
	@file $(BIN_ARM64)
	@$(GO) version -m $(BIN_ARM64) | grep 'vcs\.'
	@sha256sum $(BIN_ARM64) > $(DIST)/SHA256SUMS

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
```

Notas:

- `check-clean` usa `git status --porcelain` (incluye no trackeados). `dist-monitor` depende de él: un árbol sucio no compila. No hay target para builds sucios.
- `-ldflags` debe ir entre comillas; `$(GO_LDFLAGS)` se expande como un solo argumento para `go build`.
- `go version -m` se ejecuta en el host de build y funciona con binarios ARM64 aunque el host sea amd64. `version` se ejecuta en el servidor.
- `VERSION` es `git describe`, que da etiqueta + distancia + hash; el commit sigue siendo el hash.
- Opcional (tarea 24): un target `package` que monte un `.tar.gz` con el binario, `systemd/` y `configs/config.example.json`, al estilo de `dist-api`.

### 10.3 Flujo en el servidor

```bash
# Copiar el binario desde la máquina de desarrollo
scp dist/oci-infrastructure-monitor-linux-arm64 servidor:/tmp/

# En el servidor: instalar y preguntar al binario
sudo install -m 0755 /tmp/oci-infrastructure-monitor-linux-arm64 /usr/local/bin/oci-infrastructure-monitor
oci-infrastructure-monitor version
```

Salida típica:

```text
oci-infrastructure-monitor v1.0.0-5-g6a077c0
commit: 6a077c084793
built: 2026-11-07T23:15:37Z
vcs.revision: 6a077c084793824b18f69392da36bf931969a80b
vcs.time: 2026-11-07T22:27:10Z
vcs.modified: false
go: go1.27.3
os/arch: linux/arm64
```

Qué significa:

- `version`: `git describe --tags --always` en el build; “5 commits después de `v1.0.0`, en `6a077c0`”.
- `commit`: hash corto de 12 caracteres, el identificador exacto para consultar el repositorio.
- `-dirty` en cualquiera de las dos líneas: el binario incluye cambios sin commitear; el hash es solo la base y no es reproducible desde ese commit.
- `dev`/`unknown`: se compiló sin `-X`; recompilar con `make dist-monitor`.

Para saber qué cambios le faltan al binario, la comparación se hace en el repo de desarrollo (el servidor no tiene Git):

```bash
git log --oneline 6a077c084793..HEAD   # commits que le faltan
git diff 6a077c084793..HEAD            # diff exacto
```

### 10.4 README

Añadir en “Operación”:

```markdown
### Saber qué commit está desplegado

El servidor no necesita Git ni Go: el binario lleva el commit embebido.

    sudo -u oci-monitor /usr/local/bin/oci-infrastructure-monitor version

Salida típica:

    oci-infrastructure-monitor v1.0.0-5-g6a077c0
    commit: 6a077c084793
    built: 2026-11-07T23:15:37Z
    vcs.revision: 6a077c084793824b18f69392da36bf931969a80b
    vcs.time: 2026-11-07T22:27:10Z
    vcs.modified: false
    go: go1.27.3
    os/arch: linux/arm64

`version` es el `git describe` del build y `commit` el hash corto. El Makefile
no genera builds sucios; si aparece `-dirty` o `vcs.modified=true`, se compiló a
mano con cambios sin commitear y el hash es solo la base. Si aparece
`dev`/`unknown`, se compiló sin `-X`: recompila con `make dist-monitor`.

Para ver qué cambios le faltan, en el repositorio de desarrollo:

    git log --oneline 6a077c084793..HEAD
    git diff 6a077c084793..HEAD
```

Y en “Desarrollo y compilación”:

- `make dist-monitor` como vía recomendada; el comando manual con `-X` queda como referencia.
- Nota: el servidor no necesita Git ni Go; la comparación con `HEAD` se hace en el repo de desarrollo.
- En la tabla de CLI, `version`: “Versión, commit, fecha, Go y arquitectura; úsalo en el servidor para saber qué código está desplegado”.
- En `docs/troubleshooting.md`: `version` muestra `dev`/`unknown` → el binario se compiló sin `-X`; recompilar con el target.

### 10.5 Criterios de aceptación

- `make dist-monitor` en árbol limpio produce el binario ARM64; `file` dice `ARM aarch64`; `go version -m` muestra `vcs.revision` = `git rev-parse HEAD` y `vcs.modified=false`.
- `make dist-monitor` en árbol sucio falla con un mensaje claro; no hay target para builds sucios. Si se compila a mano sin árbol limpio, `version` muestra `-dirty`/`vcs.modified=true`.
- En el servidor, `oci-infrastructure-monitor version` muestra el mismo `commit` que `git rev-parse --short=12 HEAD` en el build.
- El README documenta el comando, la salida, `-dirty`/`dev` y la comparación local con `git log`/`git diff`.
- El servidor no requiere Git ni Go.

### 10.6 Tareas

| # | Tarea | Archivos | Verificación | Esfuerzo |
|---|---|---|---|---|
| 21 | `Makefile`: `dist-monitor`, `check-clean`, `test`, `vet`, `GO_LDFLAGS` | `Makefile` | build limpio; falla en sucio; `go version -m` | 2–3 h |
| 22 | Sección del README y troubleshooting | `README.md`, `docs/troubleshooting.md` | comando y salida documentados | 1 h |
| 23 | `version` con `vcs.revision`/`vcs.modified` (fallback `debug.ReadBuildInfo`) | `cmd/monitor/main.go` | `version` sin `-X` muestra el commit | 1 h |
| 24 | (Opcional) target `package` con tar.gz + systemd + config | `Makefile` | tarball reproducible | 1–2 h |

**Estado (2026-11-07):** tareas 21, 22 y 23 implementadas en el repositorio; la 24 (empaquetado) no se incluyó.

---

## 11. Estado de implementación (2026-11-07)

| Hallazgo | Estado | Nota |
|---|---|---|
| M3 | **Implementado** | `Job.QueryEnd() = End + 1h`; el stub respeta `endTime` exclusivo; test de finalización con el bucket 23:00. Pendiente la consulta real que confirme la convención de timestamps (Fase 0). |
| M2 | **Implementado** | `parseFlags` rechaza posicionales en los cuatro subcomandos; tests de CLI. |
| M1 | **Implementado** | `status` es de solo lectura; `run` es el único que cuarentena (con el lock). |
| B1 | **Implementado** | `syncDir` propaga errores; `quarantine` también hace fsync del directorio. |
| B2 | **Implementado** | Un 200 con cuerpo truncado es `TransportError` reintentable; `ok:false` sigue siendo permanente. |
| B3 | **Implementado** | Fallback al header `Retry-After` (segundos o fecha HTTP). |
| B5 | **Implementado** | 3 intentos con backoff corto y `common.IsErrorRetryableByDefault`; tests con API falsa. |
| B6 | **Implementado** | Se descartan valores ≥ 2^63; `bytesFromGiB` de `app` satura como el de `alerts`. |
| B7 | **Implementado** | `test-telegram` imprime los errores de estado y mantiene el envío. |
| B8 | **Implementado** | `drainPending` conserva el error de Telegram si falla el guardado; `Run` loguea ambos. |
| B10 | **Implementado** | `--help` en los subcomandos sale 0. |
| I1 | **Implementado** | `peekSchema` exige objeto JSON y `schema_version` > 0; `null`/`{}` son corruptos. |
| I4 | **Implementado** | `pendientes=n/d` cuando el estado no se leyó (p. ej. `network.enabled=false`). |
| I5 | **Implementado (bug)** | El texto de la notificación se persiste al encolar; una anomalía reintentada conserva su ventana de 6 h. Los tests de `RenderAlert` ya existían. |
| I7 | **Implementado** | `--version` como alias, extras rechazados, comentario de los pasos 7/8 corregido. |
| R3 | **Implementado (tests)** | Tests nuevos de `internal/oci` y de la CLI (`execute`, `check`, `status`, `test-telegram`); la cobertura exacta no se pudo medir porque el toolchain de este entorno no incluye `covdata`. |
| §10 | **Implementado** | `Makefile` estricto ARM64, `version` con `vcs.*`, README/architecture/troubleshooting. |
| R1 | **Pendiente de commit** | El Makefile ya está; `make dist-monitor` falla a propósito hasta commitear el árbol. |
| B4, I2, I6, R2, R4, R5 | **Sin acción** | No aplican o son decisiones/operativos, según §6. |
| B9 | **Implementado (documentación)** | Nota en `troubleshooting.md`: un `telegram.env` ilegible aborta antes de consultar OCI; relanzar dentro de la ventana de 48 h. |
| I3 | **Implementado** | `State.Validate` exige `period` y unicidad `(kind, period)`; `outbox.Enqueue` expira la cola antes de insertar. |

**Pendiente real:** la Fase 0 (consulta a OCI con `check` para confirmar si el timestamp agregado es el final de la ventana) y el commit del árbol para poder ejecutar `make dist-monitor`.
