#!/usr/bin/env bash
# Source ce fichier dans ton shell : source 03-env-vars.sh

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
alias k=kubectl
alias kk='kubectl -n karpenter'
alias kw='kubectl -n kube-system'
alias kgn='kubectl get nodes -o wide'
alias kgp='kubectl get pods -o wide'
alias kgpc='kubectl get nodeclaims'
alias kgnp='kubectl get nodepool'
alias l='lxc list'

karpenter_logs() { kubectl -n karpenter logs deployment/karpenter -f "$@"; }
kwok_logs()     { kubectl -n kube-system logs deployment/kwok-controller -f "$@"; }
inflate()       { kubectl scale deployment inflate --replicas="$1"; }
