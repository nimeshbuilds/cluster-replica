#!/usr/bin/env bash
set -euo pipefail
# vcluster connect supplies a temporary guest KUBECONFIG to this child process.
# Never print/export that file in CI artifacts.
[[ -n "${KUBECONFIG:-}" ]] || { echo 'Missing guest kubeconfig' >&2; exit 1; }
kubectl get --raw /readyz
kubectl version --output=json > "$E2E_RESULTS/guest-version.json"
if [[ "${1:-create}" == create ]]; then
  kubectl create namespace smoke
  kubectl -n smoke create deployment smoke --image=registry.k8s.io/e2e-test-images/agnhost:2.63.0 -- netexec --http-port=8080
  kubectl -n smoke expose deployment smoke --port=8080
fi
kubectl -n smoke rollout status deployment/smoke --timeout=180s
kubectl -n smoke delete job http-probe --ignore-not-found --wait=true
kubectl -n smoke create job http-probe --image=registry.k8s.io/e2e-test-images/busybox:1.37.0-1 -- \
  sh -ec 'wget -qO- http://smoke.smoke.svc.cluster.local:8080/hostname | grep -q "smoke-"'
kubectl -n smoke wait job/http-probe --for=condition=complete --timeout=120s
kubectl -n smoke get pods,services,jobs,deployments -o json | python3 "$E2E_REPO/test/e2e/inventory.py" > "$E2E_RESULTS/guest-workload.json"
echo 'Guest DNS, Service routing, scheduling and HTTP probe passed.'
