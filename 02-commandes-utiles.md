# Commandes utiles pour le développement du provider Karpenter

## Cluster

```bash
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

# Voir les nœuds
kubectl get nodes -o wide

# Voir les Nœuds KWOK uniquement
kubectl get nodes --no-headers | grep kwok

# Voir les NodeClaims (créés par Karpenter)
kubectl get nodeclaims

# Voir les NodePool
kubectl get nodepool

# Voir les KWOKNodeClass
kubectl get kwoknodeclass

# Voir les logs Karpenter
kubectl -n karpenter logs deployment/karpenter -f

# Voir les logs KWOK
kubectl -n kube-system logs deployment/kwok-controller -f
```

## Travail avec le provider Karpenter

```bash
# Aller dans le repo
cd /tmp/karpenter

# Builder le binaire uniquement
CGO_ENABLED=0 go build -o /tmp/karpenter-kwok ./kwok/

# Builder + rebuild l'image Docker + déployer
make_docker() {
  CGO_ENABLED=0 go build -o /tmp/karpenter-kwok-binary ./kwok/
  cp /tmp/karpenter-kwok-binary /tmp/karpenter-kwok-img/karpenter-kwok
  docker build -t karpenter-kwok:latest /tmp/karpenter-kwok-img
  docker save karpenter-kwok:latest > /tmp/karpenter-kwok.tar
  ctr -n k8s.io image import /tmp/karpenter-kwok.tar
  kubectl -n karpenter delete pod -l app.kubernetes.io/instance=karpenter --force --grace-period=0
}

# Rebuild + redeploy rapide
karpenter_rebuild() {
  cd /tmp/karpenter && make_docker
}
```

## Test de scaling

```bash
# Lancer un déploiement qui scale
kubectl apply -f - << 'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inflate
spec:
  replicas: 100
  selector:
    matchLabels:
      app: inflate
  template:
    metadata:
      labels:
        app: inflate
    spec:
      containers:
      - image: nginx:latest
        name: nginx
        resources:
          requests:
            cpu: 500m
            memory: 512Mi
EOF

# Watcher en temps réel
watch -n 1 'echo "Nœuds: $(kubectl get nodes --no-headers | grep -c kwok) KWOK + $(kubectl get nodes --no-headers | grep -v kwok | grep -v NAME | wc -l) real" && echo "Pods: $(kubectl get pods -A | grep -c Running) Running / $(kubectl get pods -A | grep -c Pending) Pending"'

# Voir les types d'instance KWOK disponibles
kubectl get nodeclaims -o jsonpath='{range .items[*]}{.spec.nodeClassRef.name} {.status.instanceType}{"\n"}{end}'

# Vider le cluster
kubectl delete deployment inflate
kubectl delete pods --all -n default
```

## Workers LXD

```bash
# Lister
lxc list

# Shell dans un worker
lxc exec k3s-worker-1 bash

# Logs k3s agent
lxc exec k3s-worker-1 -- journalctl -xeu k3s-agent.service -f

# Redémarrer un worker
lxc restart k3s-worker-1

# Arrêter les workers (libère les ressources)
lxc stop k3s-worker-1 k3s-worker-2

# Supprimer un worker
lxc delete k3s-worker-1 --force
```

## Visualisation

```bash
# eks-node-viewer (TUI interactif)
~/go/bin/eks-node-viewer

# Avec style personnalisé
~/go/bin/eks-node-viewer -resources cpu,memory
```

## Dépannage LXD

```bash
# Configurer /dev/kmsg (contournement LXD)
lxc exec k3s-worker-1 -- ln -sf /dev/null /dev/kmsg

# Vérifier la connectivité au serveur K3s
lxc exec k3s-worker-1 -- curl -sk https://192.168.1.59:6443/ping

# Voir les logs k3s-agent
lxc exec k3s-worker-1 -- journalctl -xeu k3s-agent.service -f
```

## Développer un provider Karpenter custom

1. Fork/clone : `https://github.com/kubernetes-sigs/karpenter`
2. Étudier `kwok/` — c'est un provider minimal et fonctionnel
3. Créer ton provider dans `providers/<ton-provider>/`
4. Points à implémenter :
   - `cloudprovider.CreateNode(ctx, nodeClaim)` → créer le nœud
   - `cloudprovider.DeleteNode(ctx, nodeClaim)` → supprimer le nœud
   - `cloudprovider.GetInstanceTypes(ctx, nodePool)` → types d'instance
   - CRD spécifique (comme KWOKNodeClass)
   - Helm chart pour le déploiement

Ressources :
- Code KWOK provider : `/tmp/karpenter/kwok/`
- Doc officielle : https://karpenter.sh/docs/contributing/development-guide/
- Slack Karpenter : https://karpenter.slack.com
