# Karpenter LXD Provider for k3s

Custom Karpenter cloud provider that provisions real LXD containers as k3s worker nodes.

## Requirements

- LXD (snap) with `lxdbr0` bridge
- iptables rules for LXD bridge NAT (auto-configured by the provider on startup)
- `net.ipv4.ip_forward = 1` (also auto-configured)

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
| `main.go` | Entry point — wires operator, controllers, cloud provider, iptables setup |

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

> **Note**: Requires the Karpenter source at `/tmp/karpenter` (set in `go.mod` replace directive). Build and test with Go 1.22+.

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

# 4. Deploy the provider (iptables init container runs automatically)
helm upgrade --install k3s-lxd-provider charts/ \
  --namespace karpenter --create-namespace \
  --set k3s.serverURL=https://<server>:6443 \
  --set k3s.token=$(cat /var/lib/rancher/k3s/server/node-token)

# 5. Create LXDNodeClass and NodePool
kubectl apply -f - <<'EOF'
apiVersion: karpenter.k3s.sh/v1alpha1
kind: LXDNodeClass
metadata:
  name: default
spec:
  image: "ubuntu:24.04"
  defaultCPU: "1"
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

## Configuration

The `LXDNodeClass` CRD configures:

| Field | Default | Description |
|-------|---------|-------------|
| `image` | `ubuntu:24.04` | LXD image alias |
| `defaultCPU` | `"2"` | CPU limit per container (string, must be quoted in YAML) |
| `defaultMemory` | `4Gi` | Memory limit (Kubernetes format, auto-converted to MiB for LXD) |
| `nodeRegistrationDelay` | `5s` | Delay before k3s-agent install |

The `NodePool` selects the `LXDNodeClass` and defines requirements (arch, os, capacity-type, etc.).

## Network requirements

The LXD bridge (`lxdbr0`) needs NAT/masquerade for internet access from containers. The provider:

1. **Init container** (`iptables-setup`): runs at pod startup with `hostNetwork: true` and configures:
   - `iptables -I FORWARD -i lxdbr0 -j ACCEPT`
   - `iptables -I FORWARD -o lxdbr0 -j ACCEPT`  
   - `iptables -t nat -A POSTROUTING -s 10.103.76.0/24 ! -o lxdbr0 -j MASQUERADE`
   - `net.ipv4.ip_forward = 1`
2. **main.go**: re-applies rules on provider restart (in case kube-router/flannel reset them)

Without these rules, LXD containers cannot reach the internet and k3s-agent install will fail.

## Key Decisions

- Uses LXD Unix socket API directly (`/var/snap/lxd/common/lxd/unix.socket`), no `lxc` binary in container
- Privileged container + `security.nesting=true` for k3s (Flannel VXLAN)
- Asynchronous bootstrap: `Create()` returns immediately, k3s-agent installs in goroutine with 5min timeout
- Provider ID format: `k3s-lxd://<container-name>`
- Exec commands check exit codes via LXD operation metadata (no more silent failures)
