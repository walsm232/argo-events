# Template Defaults

Cluster operators can set defaults for the `spec.template` of every EventSource
and Sensor in the controller ConfigMap `argo-events-controller-config`, instead
of repeating them in each object. This is useful for settings that every pod
must have, such as a security context required by pod security standards.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argo-events-controller-config
data:
  controller-config.yaml: |
    eventBus:
      ...
    eventSource:
      template:
        securityContext:
          runAsNonRoot: true
          runAsUser: 1000
        container:
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
    sensor:
      template:
        securityContext:
          runAsNonRoot: true
          runAsUser: 1000
```

Both `eventSource.template` and `sensor.template` accept the same fields as
`spec.template` on the objects.

## How defaults are merged

The object's own `spec.template` is applied on top of the defaults as a
[strategic merge patch](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/update-api-object-kubectl-patch/#use-a-strategic-merge-patch-to-update-a-deployment), the same way Argo Workflows applies `workflowDefaults`:

- Fields set on the object take precedence over the defaults.
- Nested fields such as `securityContext` are merged field by field, so a
  default `runAsNonRoot` is kept when an object only sets `fsGroup`.
- Maps such as `metadata.labels` and `nodeSelector` are merged.
- `volumes` and `imagePullSecrets` are merged by `name`. Other lists, such as
  `tolerations` and `container.env`, are replaced by the object's list when it
  sets one.

## Changing the defaults

The controller watches the ConfigMap. When the EventSource or Sensor defaults
change, it reconciles every EventSource or Sensor straight away, and each one
whose resulting deployment changed is rolled out. Kubernetes can take up to a
minute or so to update a mounted ConfigMap, so allow for that delay.

Because a change can restart every EventSource or Sensor pod in the cluster,
make changes to the defaults deliberately.
