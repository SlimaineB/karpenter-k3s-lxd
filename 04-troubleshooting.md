# Problèmes courants et solutions

## 1. LXD : overlayfs permission denied

**Erreur** : `failed to mount overlay: permission denied`

**Cause** : AppArmor dans LXD bloque les mounts overlay même en mode privileged.

**Solution** : Utiliser le snapshotter `native` dans k3s.

- **Via `add-node`** : automatique (`INSTALL_K3S_EXEC='--snapshotter native'`)
- **Manuellement sur un worker existant** :

```bash
cat > /etc/rancher/k3s/config.yaml << 'EOF'
snapshotter: native
EOF
systemctl restart k3s-agent
```

## 2. LXD : /dev/kmsg not found

**Erreur** : `failed to create kubelet: open /dev/kmsg: no such file or directory`

**Cause** : LXD (même privileged) n'a pas /dev/kmsg.

**Solution** : Créer un lien symbolique.

```bash
lxc exec k3s-worker-1 -- ln -sf /dev/null /dev/kmsg
```

## 3. LXD : modprobe br_netfilter / overlay failed

**Erreur** : `ExecStartPre=/sbin/modprobe br_netfilter (code=exited, status=1/FAILURE)`

**Cause** : On ne peut pas charger de modules kernel depuis un container LXD.

**Solution** : Override systemd pour skipper modprobe.

```bash
lxc exec k3s-worker-1 -- bash -c "
mkdir -p /etc/systemd/system/k3s-agent.service.d
cat > /etc/systemd/system/k3s-agent.service.d/override.conf << 'EOF'
[Service]
ExecStartPre=
ExecStartPre=/bin/true
EOF
systemctl daemon-reload && systemctl restart k3s-agent
"
```

## 4. Karpenter : "all available instance types exceed limits"

**Cause** : Le NodePool a atteint sa limite CPU.

**Solution** : Augmenter la limite.

```bash
kubectl patch nodepool default --type merge -p '{"spec":{"limits":{"cpu":10000}}}'
```

## 5. Container LXD sans IPv4

**Cause** : LXD init --auto configure parfois un bridge IPv6-only.

**Solution** : Reconfigurer le bridge.

```bash
lxc network set lxdbr0 ipv4.address 10.103.76.1/24
lxc network set lxdbr0 ipv4.nat true
lxc network set lxdbr0 ipv6.address none
# Puis redémarrer les containers
```

## 6. DNS ne marche pas dans le container LXD

**Cause** : Pas de résolveur DNS configuré.

**Solution** :
```bash
lxc exec k3s-worker-1 -- echo 'nameserver 8.8.8.8' > /etc/resolv.conf
```

## 7. Rebuild du provider Karpenter KWOK

```bash
cd /tmp/karpenter
CGO_ENABLED=0 go build -o /tmp/karpenter-kwok ./kwok/
cp /tmp/karpenter-kwok /tmp/karpenter-kwok-img/karpenter-kwok
docker build -t karpenter-kwok:latest /tmp/karpenter-kwok-img
docker save karpenter-kwok:latest > /tmp/karpenter-kwok.tar
ctr -n k8s.io image import /tmp/karpenter-kwok.tar
kubectl -n karpenter delete pod -l app.kubernetes.io/instance=karpenter --force --grace-period=0
```

## 8. ImagePullBackOff — image importée dans le mauvais containerd

**Erreur** : `ImagePullBackOff` / `ErrImagePull` après `helm install`.

**Cause** : k3s a son propre containerd (`/run/k3s/containerd/containerd.sock`). `ctr -n k8s.io image import` sans `CONTAINERD_ADDRESS` importe dans le containerd host, invisible pour k3s.

**Solution** :
```bash
CONTAINERD_ADDRESS=/run/k3s/containerd/containerd.sock ctr -n k8s.io image import /tmp/k3s-lxd-provider.tar
```

**Vérification** :
```bash
CONTAINERD_ADDRESS=/run/k3s/containerd/containerd.sock ctr -n k8s.io image ls | grep k3s-lxd
```

## 9. CRD Karpenter manquant — "no matches for kind"

**Erreur** : Les logs du provider montrent :
```
"error":"no matches for kind \"NodePool\" in version \"karpenter.sh/v1\""
"error":"no matches for kind \"NodeClaim\" in version \"karpenter.sh/v1\""
```

**Cause** : Les CRDs `NodePool` et `NodeClaim` ne sont pas installés sur le cluster.

**Solution** :
```bash
kubectl apply -f /tmp/karpenter/pkg/apis/crds/
```

## 10. LXDNodeClass invalide — "spec.defaultCPU in body must be of type string"

**Erreur** :
```
The LXDNodeClass "default" is invalid: spec.defaultCPU: Invalid value: "integer": spec.defaultCPU in body must be of type string
```

**Cause** : Le champ `defaultCPU` est défini comme `string` dans le CRD. En YAML, `defaultCPU: 1` est un entier.

**Solution** : Toujours mettre la valeur entre guillemets :
```yaml
spec:
  defaultCPU: "1"   # string, pas un entier
```
