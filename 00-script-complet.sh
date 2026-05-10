#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# Script complet : K3s + KWOK + Karpenter KWOK Provider
# + Workers LXD pour dev d'un provider Karpenter custom
# ============================================================

# --- Couleurs ---
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
err()  { echo -e "${RED}[x]${NC} $1"; exit 1; }
info() { echo -e "${CYAN}[i]${NC} $1"; }

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
SRV_IP="192.168.1.59"

# ============================================================
# CONFIG RÉSEAU LXD
# ============================================================
LXD_BRIDGE="10.103.76"
LXD_GW="${LXD_BRIDGE}.1"

# ============================================================
# 0. INFOS
# ============================================================
infos() {
  echo -e "${CYAN}"
  cat << 'EOF'
╔══════════════════════════════════════════════════════════════╗
║       K3s + KWOK + Karpenter + LXD Workers                 ║
║                                                            ║
║  Usage: ./00-script-complet.sh <commande>                  ║
║                                                            ║
║  INSTALL : prereqs k3s kwok karpenter lxd all              ║
║  NODES   : add-node <name> <cpu> <ram>                     ║
║            rm-node <name>                                  ║
║            list-nodes                                      ║
║  LXD     : start-lxd  stop-lxd                             ║
║  CLEAN   : nuke                                            ║
║  MISC    : test info                                       ║
╚══════════════════════════════════════════════════════════════╝
EOF
  echo -e "${NC}"
}

# ============================================================
# PRÉREQUIS
# ============================================================
install_prereqs() {
  log "Installation des dépendances..."
  apt-get update -qq
  apt-get install -y -qq curl git golang docker.io snapd

  if ! command -v helm &>/dev/null; then
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
  fi
  log "Prérequis OK"
}

# ============================================================
# K3S
# ============================================================
install_k3s() {
  if command -v k3s &>/dev/null; then warn "K3s déjà installé"; return; fi
  log "Installation de K3s..."
  curl -sfL https://get.k3s.io | sh -
  mkdir -p /root/.kube
  ln -sf /etc/rancher/k3s/k3s.yaml /root/.kube/config
  log "K3s OK"
}

# ============================================================
# KWOK
# ============================================================
install_kwok() {
  log "Installation de KWOK..."
  local version
  version=$(curl -s "https://api.github.com/repos/kubernetes-sigs/kwok/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
  curl -Lo /usr/local/bin/kwok "https://github.com/kubernetes-sigs/kwok/releases/download/$version/kwok-linux-amd64"
  curl -Lo /usr/local/bin/kwokctl "https://github.com/kubernetes-sigs/kwok/releases/download/$version/kwokctl-linux-amd64"
  chmod +x /usr/local/bin/kwok /usr/local/bin/kwokctl
  kubectl apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/$version/kwok.yaml"
  kubectl apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/$version/stage-fast.yaml"
  kubectl -n kube-system wait --for=condition=Available deployment/kwok-controller --timeout=60s
  log "KWOK OK"
}

# ============================================================
# KARPENTER KWOK PROVIDER
# ============================================================
install_karpenter_kwok() {
  log "Installation du Karpenter KWOK provider..."
  if [ ! -d /tmp/karpenter ]; then
    git clone --depth 1 https://github.com/kubernetes-sigs/karpenter.git /tmp/karpenter
  fi
  cd /tmp/karpenter
  CGO_ENABLED=0 go build -o /tmp/karpenter-kwok-binary ./kwok/
  mkdir -p /tmp/karpenter-kwok-img
  cp /tmp/karpenter-kwok-binary /tmp/karpenter-kwok-img/karpenter-kwok
  cat > /tmp/karpenter-kwok-img/Dockerfile << 'DOCKER'
FROM alpine:3.19
COPY karpenter-kwok /usr/bin/karpenter-kwok
ENTRYPOINT ["/usr/bin/karpenter-kwok"]
DOCKER
  docker build -t karpenter-kwok:latest /tmp/karpenter-kwok-img
  docker save karpenter-kwok:latest > /tmp/karpenter-kwok.tar
  ctr -n k8s.io image import /tmp/karpenter-kwok.tar
  kubectl apply -f /tmp/karpenter/kwok/charts/crds
  helm upgrade --install karpenter /tmp/karpenter/kwok/charts \
    --namespace karpenter --create-namespace \
    --set controller.image.repository="docker.io/library/karpenter-kwok" \
    --set controller.image.tag="latest" \
    --set settings.clusterName="k3s-kwok" \
    --set settings.featureGates.staticCapacity=false
  log "Karpenter KWOK provider OK"
}

# ============================================================
# NODEPOOL + KWOKNODECLASS
# ============================================================
configure_karpenter() {
  log "Configuration NodePool / KWOKNodeClass..."
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
    cpu: 10000
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 10s
EOF
  log "NodePool OK"
}

# ============================================================
# LXD : INIT
# ============================================================
lxd_init() {
  if ! command -v lxc &>/dev/null; then
    log "Installation LXD..."
    snap install lxd
  fi
  lxd init --auto
  lxc network set lxdbr0 ipv4.address "${LXD_GW}/24" 2>/dev/null || true
  lxc network set lxdbr0 ipv4.nat true 2>/dev/null || true
  lxc network set lxdbr0 ipv6.address none 2>/dev/null || true
}

# ============================================================
# LXD : AJOUTER UN NŒUD (one-click)
# ============================================================
lxd_add_node() {
  local prefix="${1:-k3s-worker}"
  local cpu="${2:-2}"
  local ram="${3:-4GiB}"

  # Trouver le prochain index libre (prefix-N)
  local idx=1 name
  while lxc info "${prefix}-${idx}" &>/dev/null 2>&1; do
    ((idx++))
  done
  name="${prefix}-${idx}"

  log "Création du nœud LXD : $name (${cpu} CPU / ${ram})"
  lxd_init

  lxc launch ubuntu:24.04 "$name" \
    -c limits.cpu="$cpu" \
    -c limits.memory="$ram" \
    -c security.nesting=true \
    -c security.privileged=true

  # Attendre l'IP DHCP
  local ip=""
  for i in $(seq 1 30); do
    ip=$(lxc list "$name" --format json 2>/dev/null | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    for addr in d[0]['state']['network']['eth0']['addresses']:
        if addr['family'] == 'inet':
            print(addr['address'])
except Exception:
    print('')
" 2>/dev/null)
    [ -n "$ip" ] && break
    sleep 2
  done
  [ -z "$ip" ] && ip="unknown"
  log "IP: $ip"

  # /dev/kmsg fix
  lxc exec "$name" -- ln -sf /dev/null /dev/kmsg 2>/dev/null || true

  # Install k3s agent avec snapshotter native (overlayfs cassé dans LXD)
  local token
  token=$(cat /var/lib/rancher/k3s/server/node-token)
  log "Installation k3s-agent sur $name..."
  lxc exec "$name" -- bash -c "
    curl -sfL https://get.k3s.io | \
    K3S_URL=https://$SRV_IP:6443 \
    K3S_TOKEN=$token \
    INSTALL_K3S_EXEC='--snapshotter native' \
    sh -
  "

  # Fix modprobe (k3s-agent plante sinon)
  lxc exec "$name" -- bash -c "
    mkdir -p /etc/systemd/system/k3s-agent.service.d
    cat > /etc/systemd/system/k3s-agent.service.d/override.conf << 'OVERRIDE'
[Service]
ExecStartPre=
ExecStartPre=/bin/true
OVERRIDE
    systemctl daemon-reload && systemctl restart k3s-agent
  "

  log "Nœud ajouté : $name ($ip)"
  kubectl wait --for=condition=Ready node "$name" --timeout=120s 2>/dev/null && \
    log "$name est Ready" || warn "$name pas encore Ready (vérifie les logs)"
}

# ============================================================
# LXD : SUPPRIMER UN NŒUD (one-click)
# ============================================================
lxd_rm_node() {
  local name="$1"
  if [ -z "$name" ]; then
    err "Usage: $0 rm-node <name>"
  fi

  if ! lxc info "$name" &>/dev/null; then
    err "Nœud LXD '$name' introuvable"
  fi

  log "Suppression du nœud : $name"
  info "Cordon + drain Kubernetes..."
  kubectl cordon "$name" 2>/dev/null || true
  kubectl drain "$name" --ignore-daemonsets --delete-emptydir-data --force 2>/dev/null || true
  kubectl delete node "$name" --force --grace-period=0 2>/dev/null || true
  log "Nœud Kubernetes supprimé"

  info "Arrêt + suppression du container LXD..."
  lxc stop "$name" --force 2>/dev/null || true
  lxc delete "$name" --force 2>/dev/null || true
  log "Nœud LXD supprimé"
  log "Terminé"
}

# ============================================================
# LXD : LISTER LES NŒUDS
# ============================================================
lxd_list_nodes() {
  echo -e "\n${CYAN}╔═ NŒUDS KUBERNETES ═══════════════════════════════╗${NC}"
  kubectl get nodes -o wide 2>/dev/null || echo "(cluster indisponible)"
  echo -e "${CYAN}╠═ NŒUDS LXD ═══════════════════════════════════════╣${NC}"
  if command -v lxc &>/dev/null; then
    lxc list 2>/dev/null || echo "(LXD non installé)"
  else
    echo "(LXD non installé)"
  fi
  echo -e "${CYAN}╚══════════════════════════════════════════════════╝${NC}\n"
}

# ============================================================
# LXD : STOP / START
# ============================================================
lxd_stop() {
  for name in $(lxc list -c n --format csv 2>/dev/null | grep -v "^$"); do
    info "Arrêt de $name..."
    lxc stop "$name" --force 2>/dev/null || true
  done
  log "Tous les workers LXD arrêtés"
}

lxd_start() {
  for name in $(lxc list -c n --format csv 2>/dev/null | grep -v "^$"); do
    log "Démarrage de $name..."
    lxc start "$name" 2>/dev/null || true
  done
  sleep 15
  for name in $(lxc list -c n --format csv 2>/dev/null | grep -v "^$"); do
    lxc exec "$name" -- bash -c "
      ln -sf /dev/null /dev/kmsg
      systemctl restart k3s-agent 2>/dev/null || true
    "
    kubectl wait --for=condition=Ready node "$name" --timeout=60s 2>/dev/null && \
      log "$name Ready" || warn "$name pas encore Ready"
  done
}

# ============================================================
# TEST SCALING
# ============================================================
test_scale() {
  log "Test scaling Karpenter..."
  kubectl apply -f - << 'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inflate
spec:
  replicas: 10
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
  sleep 30
  kubectl get pods -o wide
  kubectl scale deployment inflate --replicas=3
  sleep 15
  kubectl delete deployment inflate
  log "Test OK"
}

# ============================================================
# NUKE (tout détruire)
# ============================================================
nuke() {
  echo -e "${RED}╔════════════════════════════════════════════════╗${NC}"
  echo -e "${RED}║        NETTOYAGE COMPLET DU CLUSTER          ║${NC}"
  echo -e "${RED}╚════════════════════════════════════════════════╝${NC}"
  warn "⚠️  Ceci va TOUT supprimer (Karpenter, KWOK, workers LXD)"

  # 1. Supprimer tous les nœuds LXD
  for name in $(lxc list -c n --format csv 2>/dev/null); do
    [ -n "$name" ] && lxd_rm_node "$name" 2>/dev/null || true
  done

  # 2. Désinstaller Karpenter
  helm uninstall karpenter -n karpenter 2>/dev/null || true
  kubectl delete namespace karpenter --force --grace-period=0 2>/dev/null || true
  kubectl delete nodepool default 2>/dev/null || true
  kubectl delete kwoknodeclass default 2>/dev/null || true

  # 3. Désinstaller KWOK
  local version
  version=$(curl -s "https://api.github.com/repos/kubernetes-sigs/kwok/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
  kubectl delete -f "https://github.com/kubernetes-sigs/kwok/releases/download/$version/kwok.yaml" 2>/dev/null || true
  kubectl delete -f "https://github.com/kubernetes-sigs/kwok/releases/download/$version/stage-fast.yaml" 2>/dev/null || true

  # 4. Nettoyer les pods du namespace default
  kubectl delete pods --all -n default --force --grace-period=0 2>/dev/null || true

  log "Nettoyage terminé"
}

# ============================================================
# MAIN
# ============================================================
case "${1:-info}" in
  prereqs)      install_prereqs ;;
  k3s)          install_k3s ;;
  kwok)         install_kwok ;;
  karpenter)    install_karpenter_kwok; configure_karpenter ;;
  lxd)          lxd_init ;;
  add-node)     shift; lxd_add_node "$@" ;;
  rm-node)      shift; lxd_rm_node "$@" ;;
  list-nodes)   lxd_list_nodes ;;
  start-lxd)    lxd_start ;;
  stop-lxd)     lxd_stop ;;
  test)         test_scale ;;
  nuke)         nuke ;;
  all)
    install_prereqs
    install_k3s
    install_kwok
    install_karpenter_kwok
    configure_karpenter
    ;;
  info|--help|-h) infos ;;
  *) err "Usage: $0 {prereqs|k3s|kwok|karpenter|lxd|add-node|rm-node|list-nodes|start-lxd|stop-lxd|test|nuke|all}" ;;
esac
