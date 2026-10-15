# AKS Upgrade Gate examples

Examples for the AKS **Upgrade Gate** (preview). During a node pool upgrade, AKS pauses after
each upgraded node and creates a `HealthCheckRequest`. Every provider registered by an
`UpgradeGatePolicy` answers with a `HealthSignal`: `True` lets the upgrade continue, `False`
aborts it, and no answer before `nodeTimeout` also aborts it.

| Folder | What |
|---|---|
| [`mockHealthMonitor`](mockHealthMonitor) | A mock provider that answers every `HealthCheckRequest`, with `False` / `Unknown` on the ones you name. Runs in the cluster (`xinywadocker/healthmonitor:v0.1.0`) or locally. |
