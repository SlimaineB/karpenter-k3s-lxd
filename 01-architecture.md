# Architecture du cluster de simulation K3s + Karpenter + KWOK

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
│  ┌──────────────────────────┐  ┌──────────────────────────┐    │
│  │  KARPENTER (KWOK Prov.)  │  │  KWOK Controller         │    │
│  │  - NodePool → NodeClaim  │  │  - Simule nœuds/pods     │    │
│  │  - Crée des nœuds KWOK   │  │  - stage-fast.yaml       │    │
│  │  - namespace: karpenter  │  │  - namespace: kube-system│    │
│  └──────────────────────────┘  └──────────────────────────┘    │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  LXD Bridge (lxdbr0) 10.103.76.1/24                    │   │
│  │                                                        │   │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐    │   │
│  │  │ k3s-worker-1 │ │ k3s-worker-2 │ │ k3s-worker-3 │    │   │
│  │  │ 10.103.76.110│ │ 10.103.76.211│ │ 10.103.76.107│    │   │
│  │  │ 2 CPU/4GiB   │ │ 2 CPU/4GiB   │ │ 1 CPU/2GiB   │    │   │
│  │  └──────────────┘ └──────────────┘ └──────────────┘    │   │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐    │   │
│  │  │ k3s-worker-4 │ │ k3s-worker-5 │ │ ...worker-16 │    │   │
│  │  │ 10.103.76.105│ │ 10.103.76.99 │ │ (1 CPU/2GiB) │    │   │
│  │  └──────────────┘ └──────────────┘ └──────────────┘    │   │
│  │  (N workers LXD, ajout/suppression avec add-node/rm-node)│   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  NŒUDS KWOK (créés par Karpenter)                       │   │
│  │  - kwok-default-xxx-1  (c-16x-amd64-linux)              │   │
│  │  - kwok-default-xxx-2  (c-128x-amd64-linux)             │   │
│  │  - ... jusqu'à la limite NodePool                       │   │
│  │  - Simulés, 0 ressource réelle                          │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Flux de scaling (Karpenter + KWOK)

```
1. User scale deployment → 50 replicas
2. kube-scheduler ne trouve pas de place → pods Pending
3. Karpenter détecte les pods Pending
4. Karpenter crée un NodeClaim (ex: default-fbg7q)
5. KWOK provider reçoit le NodeClaim
6. KWOK provider crée un nœud simulé (ex: kwok-default-xxx-1)
7. Nœud s'enregistre dans l'API (Ready instantané)
8. kube-scheduler bind les pods sur le nouveau nœud
9. KWOK controller transitionne les pods en Running

Scale down (consolidation) :
10. User scale → 3 replicas
11. Karpenter détecte nœud sous-utilisé
12. Après consolidateAfter: 10s → supprime le NodeClaim
13. Nœud KWOK est retiré
```

## Limites observées

| Métrique | Valeur | Goulot |
|----------|--------|--------|
| Pods/s (via Karpenter) | ~40/s | kube-scheduler + Karpenter batching |
| Pods/s (nodeName direct) | ~100/s | API server k3s + etcd |
| Nœuds KWOK max testé | 29 | limite Karpenter (pas KWOK) |
| Workers LXD max testé | 16 | RAM/CPU machine hôte |
| Pods max testé | 10 000 | API server k3s |
| RAM utilisée (10K pods) | ~6 Go / 62 Go | OK |
| CPU utilisé (10K pods) | ~5% | OK |
| Création worker LXD | ~21s | installation k3s-agent |
| Suppression worker LXD | ~5s | lxc delete --force |

## Pour créer ton propre provider Karpenter

Le Karpenter KWOK provider est dans :
```
/tmp/karpenter/kwok/
├── main.go         # Point d'entrée (remplace le cloud provider)
├── cloudprovider/  # Implémentation du provider KWOK
├── apis/           # CRD KWOKNodeClass
├── charts/         # Helm chart
├── options/        # Options du controller
├── utils/          # Utilitaires
└── examples/       # Instance types par défaut
```

Structure clé :
- `main.go` : initialise l'operator avec le KWOK cloud provider
- `cloudprovider/` : contient la logique de création de nœuds simulés
- `apis/` : définit les CRD spécifiques au provider (KWOKNodeClass)
- Le pattern est le même pour créer un provider AWS, Azure, ou autre
