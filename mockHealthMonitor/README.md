# mockHealthMonitor — upgrade gate demo provider

Watches `HealthCheckRequest`s (HCRs) and answers each one with a `HealthSignal` that AKS accepts:
the `health.aks.io/provider` label, an ownerReference to the HCR, and a `Ready` condition.

| HCR | Signal written after `--wait-time` |
|---|---|
| named in `--failed-target` | `Ready=False` (Unhealthy). The upgrade aborts. |
| named in `--timeout-target` | `Ready=Unknown` (InProgress, never finishes). The upgrade aborts at `nodeTimeout`. |
| any other HCR | `Ready=True` (Healthy) |

With neither target flag set, every signal is Healthy.

The monitor only answers. AKS ignores its signals unless an `UpgradeGatePolicy` registers the
same provider name for the pool being upgraded. Apply the policy **before** the upgrade starts:
AKS reads policies when the upgrade begins.

## Deploy on the cluster

Image: `xinywadocker/healthmonitor:v0.1.0` (public, linux/amd64 + linux/arm64).

```sh
kubectl apply -f deploy/upgradegatepolicy.yaml   # register provider "healthmonitor" for nodepool1 (edit the pool first)
kubectl apply -f deploy/healthmonitor.yaml       # namespace, RBAC, Deployment
kubectl -n healthmonitor logs deploy/healthmonitor -f
```

To change its behavior, edit the args marked `EDIT ME` in `deploy/healthmonitor.yaml` and
re-apply, or edit them live with `kubectl -n healthmonitor edit deploy healthmonitor`. Args are
read at startup, and an edit rolls out a new pod. The pod is stateless, so it can restart at any
time, even mid-upgrade. Signals it already wrote are kept, and new HCRs are answered. Remove it
with `kubectl delete -f deploy/`.

Rebuild and push the image:

```sh
docker buildx build --platform linux/amd64,linux/arm64 \
  -t xinywadocker/healthmonitor:v0.1.0 -t xinywadocker/healthmonitor:latest --push .
```

## Run locally

Start the monitor **before** the upgrade:

```sh
# all healthy
go run . --provider-name healthmonitor --wait-time 20s

# one node unhealthy, one node silent; everything else healthy
go run . --provider-name healthmonitor --wait-time 20s \
  --failed-target aks-nodepool1-12345678-vmss000001 \
  --timeout-target aks-nodepool1-12345678-vmss000002
```

| Flag | Default | Meaning |
|---|---|---|
| `--provider-name` | required | Must equal `spec.provider` of your `UpgradeGatePolicy` |
| `--wait-time` | `30s` | Delay between an HCR appearing and its signal being created. Keep it below `nodeTimeout`. |
| `--failed-target` | none | Comma-separated HCR names to report Unhealthy |
| `--timeout-target` | none | Comma-separated HCR names to leave Unknown |
| `--kubeconfig` | `$KUBECONFIG`, then `~/.kube/config` | Customer cluster kubeconfig. In a pod, the service account is used. |
| `--health-probe-bind-address` | `0` (off) | `/healthz` and `/readyz` address. The Deployment uses `:8081`. |

**Picking target names.** A node HCR is named after the node. Reimaged nodes keep their names, so
use one from `kubectl get nodes`. A surge node gets a new, higher VMSS index, and its name only
shows up once it exists. Pool and cluster HCRs
are `nodepool-<pool>` and `cluster-<cluster>`. Passing one of these to `--failed-target` trips the
circuit breaker. That only matters while the same provider is also answering node HCRs.

**Two providers.** Run one monitor per provider, in separate terminals, each with its own
`--provider-name` and its own policy. Metrics are disabled, so they don't fight over a port.

## Test

```sh
go test ./...
```
