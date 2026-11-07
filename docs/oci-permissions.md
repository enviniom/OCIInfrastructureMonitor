# Permisos OCI

> Documento del Hito 1. Explica la autenticación con Instance Principals, la política actual, una alternativa de mínimo privilegio y cómo verificar el Dynamic Group. No se crean ni modifican recursos OCI.

## Autenticación

La aplicación usa exclusivamente **Instance Principals**. El SDK de OCI obtiene y renueva las credenciales desde el servicio de metadatos de la instancia (`169.254.169.254`). No se usan API keys, archivos de usuario ni claves privadas.

La instancia debe pertenecer al Dynamic Group `Default/BackupInstances`. Si no pertenece, las llamadas fallan con error de autorización; la aplicación no puede resolverlo por sí sola.

## Política actual

La política existente es:

```text
Allow dynamic-group 'Default'/'BackupInstances' to read metrics in tenancy
```

Es correcta para empezar, pero `in tenancy` concede lectura de métricas de todos los namespaces y compartimentos de la tenancy. Para producción conviene acotarla.

## Política de mínimo privilegio

Las métricas de una VNIC (`oci_vcn`) residen en el **compartimento de la subred**, no en el de la instancia. La política restrictiva recomendada es:

```text
Allow dynamic-group 'Default'/'BackupInstances' to read metrics in compartment <subnet_compartment> where target.metrics.namespace='oci_vcn'
```

- Si las VNIC están en varios compartimentos de subred, añade una sentencia por compartimento.
- La v1 consulta un solo compartimento (`network.compartment_ocid`). Si las VNIC configuradas están en compartimentos distintos, la consulta no las verá; la política puede cubrir varios, pero la configuración actual no. Es una limitación documentada de la v1.
- Si la subred está en el compartimento raíz, usa `in tenancy` con la misma condición de namespace.
- `read metrics` incluye `inspect` en la jerarquía de verbos de OCI (`inspect` < `read` < `use` < `manage`), así que cubre tanto `SummarizeMetricsData` como `ListMetrics`. La documentación de Monitoring pide `inspect metrics` para listar definiciones y `read metrics` para consultar datos; `read` satisface ambos.
- La condición `where target.metrics.namespace='oci_vcn'` es la sintaxis oficial que aparece en la documentación de VNIC Metrics.
- No se concede `read virtual-network-family` ni `read instance-family`; por defecto la aplicación no lee recursos de red ni instancias.

## Qué puede y qué no puede hacer `check`

`check` usa `ListMetrics` sobre `oci_vcn` y reporta:

1. **OCID mal formado:** validación local, sin llamada a OCI. Código de salida `2`.
2. **VNIC visible en métricas:** aparece una definición con `resourceId` igual al OCID durante la retención. Código `0`.
3. **VNIC no visible:** puede no existir, no tener métricas o no haber tenido tráfico. Código `0` con warning muy visible; no es una afirmación de inexistencia.

Una consulta fallida (permisos, red, autenticación) sale con código `3`. `check` no escribe configuración ni estado.

La documentación de `ListMetrics` no publica una ventana de actividad: lista definiciones de métricas, no recursos vivos. Por eso no se usa como prueba de existencia. `resourceId` sí está confirmado como el OCID de la VNIC.

## Verificación manual del Dynamic Group

Desde una máquina con OCI CLI y permisos de Identity (no desde la instancia, que no tiene OCI CLI):

```bash
oci iam dynamic-group list --compartment-id <tenancy_ocid>
oci iam dynamic-group get --dynamic-group-id <dynamic_group_ocid>
oci iam policy list --compartment-id <tenancy_ocid> --all
```

Comprueba que la regla de coincidencia del Dynamic Group incluye la instancia. Ejemplos típicos de regla:

```text
instance.compartment.id = 'ocid1.compartment.oc1.....'
instance.id = 'ocid1.instance.oc1.....'
```

Desde la instancia, el OCID se puede confirmar con el servicio de metadatos:

```bash
curl -s -H "Authorization: Bearer Oracle" http://169.254.169.254/opc/v2/instance/ \
  | jq -r '.id'
```

Ese OCID debe cumplir la regla del Dynamic Group. La pertenencia no se puede deducir solo de que la instancia exista.

## Verificación desde la aplicación

```bash
oci-infrastructure-monitor check --config /etc/oci-infrastructure-monitor/config.json
```

- Si falla la autenticación o los permisos, revisa el Dynamic Group, la política y el compartimento de la subred.
- Si la VNIC no aparece, el mensaje debe indicar las tres causas posibles; no asumas que el OCID está mal.
- `run` no debe usarse para diagnosticar permisos: `check` es el comando de diagnóstico y no modifica estado.

## Verificación de existencia opt-in (futura)

Si más adelante se quiere distinguir “VNIC inexistente” de “VNIC sin actividad”, hace falta leer recursos de red e instancias, por ejemplo:

```text
Allow dynamic-group 'Default'/'BackupInstances' to read virtual-network-family in compartment <compartment>
Allow dynamic-group 'Default'/'BackupInstances' to read instance-family in compartment <compartment>
```

Eso amplía la superficie IAM y **no** es el comportamiento por defecto. Cualquier implementación futura debe documentarse aquí y justificarse en `decisions.md`.

## Referencias

- [VNIC Metrics — Required IAM Policy](https://docs.oracle.com/en-us/iaas/Content/Network/Reference/vnicmetrics.htm#policy)
- [Securing Monitoring — Metric Access for Groups](https://docs.oracle.com/en-us/iaas/Content/Security/Reference/monitoring_security.htm#metric-groups)
- [IAM Verbs](https://docs.oracle.com/en-us/iaas/Content/Identity/policieshow/Verbs.htm)
- [Managing Dynamic Groups](https://docs.oracle.com/en-us/iaas/Content/Identity/dynamicgroups/managingdynamicgroups.htm)
- [Instance Principals](https://docs.oracle.com/en-us/iaas/Content/Identity/Tasks/callingservicesfrominstances.htm)
