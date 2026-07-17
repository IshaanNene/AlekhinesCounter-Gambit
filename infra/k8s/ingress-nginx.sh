#!/usr/bin/env bash
#
# Install the ingress-nginx controller on the kind cluster.
#
# ingress-nginx is a cluster add-on, not part of the app's Helm chart — the chart
# ships the Ingress *resource* (templates/ingress.yaml); this installs the
# *controller* that serves it. Kept separate so the chart stays portable to any
# cluster that already has an ingress controller (EKS/GKE/etc.).
#
# The kind provider manifest binds the controller to the host via hostPort 80/443
# on a node labelled ingress-ready=true. Terraform maps that node's :80 to the
# host (see infra/terraform), so the ingress ends up at http://localhost:8888.
#
# The manifest's own nodeSelector is only "linux", so it can land on a worker
# whose :80 is *not* the mapped one; we pin it to the ingress-ready node (the
# control-plane, which Terraform maps) so the host port actually reaches it.

set -euo pipefail

VERSION="${INGRESS_NGINX_VERSION:-controller-v1.12.1}"
MANIFEST="https://raw.githubusercontent.com/kubernetes/ingress-nginx/${VERSION}/deploy/static/provider/kind/deploy.yaml"

echo ">> installing ingress-nginx ${VERSION}"
kubectl apply -f "$MANIFEST"

echo ">> pinning the controller to the ingress-ready node"
kubectl patch deployment ingress-nginx-controller -n ingress-nginx --type=strategic -p '{
  "spec":{"template":{"spec":{
    "nodeSelector":{"ingress-ready":"true","kubernetes.io/os":"linux"},
    "tolerations":[{"key":"node-role.kubernetes.io/control-plane","operator":"Exists","effect":"NoSchedule"}]
  }}}}'

echo ">> waiting for the controller to be ready"
kubectl rollout status deployment/ingress-nginx-controller -n ingress-nginx --timeout=150s

echo ">> ingress-nginx ready — the app ingress will be reachable on the mapped host port"
