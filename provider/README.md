# Karpenter LXD Provider for k3s

Custom Karpenter cloud provider that provisions real LXD containers as k3s worker nodes.

## Architecture

```
Pending Pod → Karpenter → cloudprovider.Create()
                              ↓
                        LXD API (Unix socket)
                              ↓
                    LXD container (ubuntu:24.04)
                              ↓
                    k3s-agent bootstrap (curl install)
                              ↓
                    Node joins cluster → Pods schedule
```

## Components

| Directory | Purpose |
|-----------|---------|
| `apis/v1alpha1/` | `LXDNodeClass` CRD (image, cpu, memory, delay) |
| `cloudprovider/` | Karpenter `CloudProvider` interface (`Create`, `Delete`, `Get`, `List`, `GetInstanceTypes`) |
| `controllers/nodeclass/` | Controller that sets `Ready` condition on `LXDNodeClass` |
| `lxd/` | LXD REST API client over Unix socket |
| `charts/` | Helm chart (deployment, RBAC, service account) |
| `main.go` | Entry point — wires operator, controllers, cloud provider |

## How it works

1. Karpenter detects pending pods and calls `Create()` with a `NodeClaim`
2. `Create()` looks up the `LXDNodeClass` for image/resources, selects an instance type
3. Launches a LXD container via REST API (`/1.0/containers`)
4. Starts the container (`PUT /1.0/containers/<name>/state`)
5. In a goroutine after a delay: installs k3s-agent via `curl https://get.k3s.io`
6. Returns the `NodeClaim` with provider ID `k3s-lxd://<container-name>`
7. k3s-agent registers the node, Karpenter lifecycle controller labels it, pods schedule

## Instance Types

Defined in `cloudprovider/instance_types.json`. Each entry specifies CPU, memory, architecture, OS, and offerings with pricing and zones. The `c-4x-amd64-linux` type is the default.

## Building

```bash
CGO_ENABLED=0 go build -o k3s-lxd-provider .
docker build -t k3s-lxd-provider:latest .
```

## Deploying

```bash
# 1. Import image into k3s containerd (NOT host containerd)
docker save k3s-lxd-provider:latest -o /tmp/k3s-lxd-provider.tar
CONTAINERD_ADDRESS=/run/k3s/containerd/containerd.sock \
  ctr -n k8s.io image import /tmp/k3s-lxd-provider.tar

# 2. Install Karpenter CRDs (NodePool, NodeClaim)
kubectl apply -f /tmp/karpenter/pkg/apis/crds/

# 3. Install provider CRD
kubectl apply -f apis/crds/

# 4. Deploy the provider
helm upgrade --install k3s-lxd-provider charts/ \
  --namespace karpenter --create-namespace \
  --set k3s.serverURL=https://<server>:6443 \
  --set k3s.token=<node-token>

# 5. Create LXDNodeClass and NodePool
kubectl apply -f - <<'EOF'
apiVersion: karpenter.k3s.sh/v1alpha1
kind: LXDNodeClass
metadata:
  name: default
spec:
  image: "ubuntu:24.04"
  defaultCPU: "1"          # must be a string
  defaultMemory: 2Gi
  nodeRegistrationDelay: 5s
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: lxd
spec:
  template:
    spec:
      nodeClassRef:
        group: karpenter.k3s.sh
        kind: LXDNodeClass
        name: default
      requirements:
        - key: karpenter.sh/capacity-type
          operator: In
          values: [spot]
  limits:
    cpu: 100
    memory: 100Gi
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 30s
EOF
```

> **Note** : k3s a son propre containerd. Si vous utilisez `ctr` sans `CONTAINERD_ADDRESS`, l'image est importée dans le containerd host, pas dans celui de k3s → `ImagePullBackOff`.

## Configuration

The `LXDNodeClass` CRD configures:

| Field | Default | Description |
|-------|---------|-------------|
| `image` | `ubuntu:24.04` | LXD image alias |
| `defaultCPU` | `"2"` | CPU limit per container (string, must be quoted in YAML) |
| `defaultMemory` | `4Gi` | Memory limit (Kubernetes format, auto-converted to MiB for LXD) |
| `nodeRegistrationDelay` | `5s` | Delay before k3s-agent install |

The `NodePool` selects the `LXDNodeClass` and defines requirements (arch, os, capacity-type, etc.).

## Key Decisions

- Uses LXD Unix socket API directly (`/var/snap/lxd/common/lxd/unix.socket`), no `lxc` binary in container
- Privileged container + `security.nesting=true` for k3s (Flannel VXLAN)
- Asynchronous bootstrap: `Create()` returns immediately, k3s-agent installs in goroutine
- Provider ID format: `k3s-lxd://<container-name>`
