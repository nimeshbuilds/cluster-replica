# Shared by the real local-chart and published-OCI installation tests.
replicove_helm_install() {
 local image="${REPLICOVE_IMAGE:-cluster-replica:e2e}"
 local args=()
 if [[ -n "${REPLICOVE_DESTINATION_NAMESPACE:-}" ]]; then args+=(--set-string "destinationNamespace=$REPLICOVE_DESTINATION_NAMESPACE"); fi
 if [[ "$image" == *@sha256:* ]]; then
  args+=(--set-string "image.repository=${image%@*}" --set-string "image.digest=${image#*@}")
 else
  args+=(--set-string "image.repository=${image%:*}" --set-string "image.tag=${image##*:}" --set-string 'image.digest=')
 fi
 if [[ -n "${REPLICOVE_CHART_VERSION:-}" ]]; then args+=(--version "$REPLICOVE_CHART_VERSION"); fi
 helm upgrade --install "${REPLICOVE_HELM_RELEASE:-replicove}" "${REPLICOVE_CHART:-./charts/replicove}" \
  --kubeconfig "$KUBECONFIG" --namespace "${REPLICOVE_HELM_NAMESPACE:-replicove-system}" --create-namespace \
  --wait --timeout 3m --values "$1" "${args[@]}"
}
