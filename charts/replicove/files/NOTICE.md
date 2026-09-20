# Vendored snapshot API definitions

The three YAML files in `snapshot-crds/` are unmodified CustomResourceDefinitions
from kubernetes-csi/external-snapshotter v8.6.0, commit
`78e32cd84e0abec2621924a30e38c755f93e180a`.

Source: https://github.com/kubernetes-csi/external-snapshotter/tree/v8.6.0/client/config/crd
Copyright: The Kubernetes Authors. Licensed under Apache License 2.0; see the
bundled LICENSE in this directory (same license terms).

The commit and file SHA-256 values are checked by the mirror dependency contract.
Upgrade these APIs together with the snapshot-controller image only after
passing the real CSI mirror lifecycle suite. Snapshot controller RBAC is adapted
from the same release's deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml.
