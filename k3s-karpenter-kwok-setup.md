# K3s + Karpenter + KWOK — Guide d'installation et de test

## Prérequis

```bash
# K3s
curl -sfL https://get.k3s.io | sh -
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

# Helm
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# Dépendances (Go, Git, Python, Docker)
apt-get install -y golang git docker.io
```

## 1. Installer KWOK

```bash
# Binaires
KWOOK_VERSION=$(curl -s "https://api.github.com/repos/kubernetes-sigs/kwok/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
curl -Lo /usr/local/bin/kwok "https://github.com/kubernetes-sigs/kwok/releases/download/$KWOOK_VERSION/kwok-linux-amd64"
curl -Lo /usr/local/bin/kwokctl "https://github.com/kubernetes-sigs/kwok/releases/download/$KWOOK_VERSION/kwokctl-linux-amd64"
chmod +x /usr/local/bin/kwok /usr/local/bin/kwokctl

# Déployer KWOK dans le cluster k3s
kubectl apply -f https://github.com/kubernetes-sigs/kwok/releases/download/$KWOOK_VERSION/kwok.yaml
kubectl apply -f https://github.com/kubernetes-sigs/kwok/releases/download/$KWOOK_VERSION/stage-fast.yaml
kubectl -n kube-system wait --for=condition=Available deployment/kwok-controller --timeout=60s
```

## 2. Installer le Karpenter KWOK Provider

```bash
# Cloner le repo
git clone --depth 1 https://github.com/kubernetes-sigs/karpenter.git /tmp/karpenter

# Builder le binaire statique
cd /tmp/karpenter
CGO_ENABLED=0 go build -o /tmp/karpenter-kwok ./kwok/

# Builder l'image Docker
mkdir -p /tmp/karpenter-kwok-img
cp /tmp/karpenter-kwok /tmp/karpenter-kwok-img/
cat > /tmp/karpenter-kwok-img/Dockerfile << 'DOCKER'
FROM alpine:3.19
COPY karpenter-kwok /usr/bin/karpenter-kwok
ENTRYPOINT ["/usr/bin/karpenter-kwok"]
DOCKER

docker build -t karpenter-kwok:latest /tmp/karpenter-kwok-img
docker save karpenter-kwok:latest > /tmp/karpenter-kwok.tar

# Importer dans containerd de k3s
ctr -n k8s.io image import /tmp/karpenter-kwok.tar

# Installer les CRDs
kubectl apply -f /tmp/karpenter/kwok/charts/crds

# Installer le chart Helm
helm upgrade --install karpenter /tmp/karpenter/kwok/charts \
  --namespace karpenter --create-namespace \
  --set controller.image.repository="docker.io/library/karpenter-kwok" \
  --set controller.image.tag="latest" \
  --set settings.clusterName="k3s-kwok" \
  --set settings.featureGates.staticCapacity=false

# Vérifier que le pod Karpenter tourne
kubectl -n karpenter get pods
```

## 3. Configurer le NodePool et KWOKNodeClass

```bash
kubectl apply -f - << 'EOF'
apiVersion: karpenter.kwok.sh/v1alpha1
kind: KWOKNodeClass
metadata:
  name: default
---
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  template:
    spec:
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["spot"]
      nodeClassRef:
        name: default
        kind: KWOKNodeClass
        group: karpenter.kwok.sh
      expireAfter: 720h
  limits:
    cpu: 1000
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 10s
EOF
```

## 4. Tester le scaling

```bash
# Tainter le nœud réel (optionnel — force les pods vers les nœuds KWOK)
kubectl taint node $(hostname) CriticalAddonsOnly=true:NoSchedule --overwrite

# Déployer une app qui va déclencher Karpenter
kubectl apply -f - << 'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inflate
spec:
  replicas: 5
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

# Observer
watch kubectl get pods -o wide
kubectl get nodeclaims
kubectl get nodes
```

## 5. Nettoyage

```bash
# Supprimer le déploiement de test
kubectl delete deployment inflate

# Voir Karpenter consolider les nœuds (consolidateAfter: 10s)
watch kubectl get nodes

# Désinstaller Karpenter
helm uninstall karpenter -n karpenter
kubectl delete nodepool default
kubectl delete kwoknodeclass default

# Désinstaller KWOK
kubectl delete -f https://github.com/kubernetes-sigs/kwok/releases/download/<version>/kwok.yaml
kubectl delete -f https://github.com/kubernetes-sigs/kwok/releases/download/<version>/stage-fast.yaml
```

## Notes

- **Karpenter KWOK provider** crée des nœuds simulés avec des types d'instance fictifs (`c-2x-amd64-linux`)
- Les nœuds KWOK ne font pas tourner de vrais kubelets/containers → zéro overhead
- Le taint `CriticalAddonsOnly=true:NoSchedule` sur le nœud réel force Karpenter à provisionner des nœuds KWOK pour les workloads
- Pour des types d'instance personnalisés, utiliser `--instance-types-file-path` ou la variable `INSTANCE_TYPES_FILE_PATH`
