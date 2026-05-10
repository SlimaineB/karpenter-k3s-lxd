# Architecture du cluster K3s + Karpenter + Provider LXD

## Schéma

```
┌─────────────────────────────────────────────────────────────────┐
│                        SERVEUR PHYSIQUE                         │
│                        192.168.1.59                             │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  K3s SERVER (control-plane)                             │   │
│  │  - API Server :6443                                     │   │
│  │  - Scheduler, Controller Manager                        │   │
│  │  - Nœud réel : sliman-system-product-name               │   │
│  │  - $KUBECONFIG = /etc/rancher/k3s/k3s.yaml             │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  KARPENTER LXD PROVIDER (namespace: karpenter)          │   │
│  │  - Pod: k3s-lxd-provider-xxx (1/1 Ready)                │   │
│  │  - NodePool lxd → NodeClaim                             │   │
│  │  - LXDNodeClass default (image, cpu, memory)            │   │
│  │  - Accès LXD socket → /var/snap/lxd/common/lxd/...     │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  LXD (snap)                                              │   │
│  │  - Socket Unix /var/snap/lxd/common/lxd/unix.socket      │   │
│  │  - Bridge lxdbr0 (DHCP)                                  │   │
│  │  - Image locale ubuntu:24.04                             │   │
│  │                                                          │   │
│  │  ┌─────────────────────────────────────────────────┐     │   │
│  │  │  Workers LXD (créés/supprimés par Karpenter)     │     │   │
│  │  │                                                  │     │   │
│  │  │  ┌──────────────────┐ ┌──────────────────┐       │     │   │
│  │  │  │ k3s-lxd-xxx-123  │ │ k3s-lxd-yyy-456  │  ...  │     │   │
│  │  │  │ 1 CPU / 2GiB     │ │ 1 CPU / 2GiB     │       │     │   │
│  │  │  │ k3s-agent v1.35  │ │ k3s-agent v1.35  │       │     │   │
│  │  │  │ 10.103.76.xxx    │ │ 10.103.76.xxx    │       │     │   │
│  │  │  └──────────────────┘ └──────────────────┘       │     │   │
│  │  └─────────────────────────────────────────────────┘     │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Flux de scaling (Karpenter + Provider LXD)

```
Scale-up :
1. User scale deployment → N replicas
2. kube-scheduler ne trouve pas de place → pods Pending
3. Karpenter provisioner détecte les pods Pending
4. Karpenter crée un NodeClaim (ex: lxd-abcde)
5. Provider LXD reçoit NodeClaim.Create()
6. Provider lance un container LXD (ubuntu:24.04)
7. Provider installe k3s-agent en async (goroutine)
8. k3s-agent rejoint le cluster → node Ready
9. kube-scheduler bind les pods sur le nouveau nœud

Scale-down (consolidation) :
10. User scale → 3 replicas (ou delete deployment)
11. Karpenter disruption détecte nœuds vides/sous-utilisés
12. Après consolidateAfter: 30s → évince les pods
13. Provider LXD Stop + Delete le container
14. NodeClaim supprimé → nœud retiré du cluster
```

## Flux détaillé du provider

```
NodeClaim.Create(name="lxd-abcde")
  │
  ├─ Lire LXDNodeClass (image, cpu, memory)
  ├─ Lire NodeClaim.Spec.Requirements (capacity-type, instance-type)
  ├─ lxd.Launch("k3s-lxd-lxd-abcde-<hash>", image, cpu, memory)
  │    ├─ POST /1.0/containers  (création)
  │    ├─ Attente 2s
  │    └─ PUT /1.0/containers/<name>/state  (start)
  ├─ Appliquer labels (capacity-type, etc.)
  ├─ Démarrer goroutine:
  │    ├─ Attendre nodeRegistrationDelay (5s)
  │    └─ curl https://get.k3s.io | sh - (k3s-agent)
  └─ Retourner NodeClaim (provider-id: k3s-lxd://<name>)

NodeClaim.Delete(name="lxd-abcde")
  ├─ lxd.Stop(name)
  │    └─ PUT /1.0/containers/<name>/state  (action:stop, force:true)
  ├─ lxd.Delete(name)
  │    └─ DELETE /1.0/containers/<name>
  └─ Retourner NodeClaimNotFound
        (si container déjà supprimé: silencieux, pas une erreur)
```

## Composants du provider

| Fichier | Rôle |
|---------|------|
| `main.go` | Entry point — operator Karpenter + controllers |
| `cloudprovider/cloudprovider.go` | Interface CloudProvider (Create, Delete, Get, List, GetInstanceTypes) |
| `cloudprovider/helpers.go` | Conversion instance types, helpers (toLXDMemory) |
| `lxd/client.go` | Client REST LXD via socket Unix (Launch, Start, Stop, Delete, Exec) |
| `apis/v1alpha1/` | CRD LXDNodeClass (image, defaultCPU, defaultMemory) |
| `controllers/nodeclass/controller.go` | Controller LXDNodeClass (set condition Ready) |
| `charts/` | Helm chart (deployment, RBAC, service account) |

## Informations utiles

| Élément | Valeur |
|---------|--------|
| Provider ID format | `k3s-lxd://<container-name>` |
| Image LXD | `ubuntu:24.04` (alias local) |
| Socket LXD | `/var/snap/lxd/common/lxd/unix.socket` (hostPath dans le pod) |
| Mode container | `privileged: true` + `security.nesting=true` |
| Snapshotter k3s-agent | `native` (évite overlayfs denied) |
| Leader election | Désactivée (`DISABLE_LEADER_ELECTION=true`) — 1 replica |
| Instance type par défaut | `c-4x-amd64-linux` (4 CPU, 8GB) |
| kubeconfig | `/etc/rancher/k3s/k3s.yaml` |
| containerd k3s | `/run/k3s/containerd/containerd.sock` |

## Limites observées

| Métrique | Valeur | Goulot |
|----------|--------|--------|
| Création worker LXD | ~21s | installation k3s-agent |
| Suppression worker LXD (Karpenter) | ~5s par nœud + 30s consolidateAfter | consolidation |
| Scale-up 3 pods → 3 workers | ~60s | bootstrap séquentiel |
| Scale-down 3 workers → 0 | ~85s | consolidation |
| Workers LXD max testé | 28 | RAM/CPU machine hôte |
| Nœuds max dans le cluster | ~30 (1 CP + 29 LXD) | testé |
