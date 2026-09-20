# Installation

Replicove is a Go operator installed on your host cluster. It watches one destination namespace and keeps encrypted capture and access state in a separate, administrator-only namespace.

The alpha is distributed as source. The chart's default image name is a release placeholder; **build and load or push your own image** before installing. Neither walkthrough assumes that placeholder is available in a registry.

## Choose a path

| Path | Use it when | Tools |
| --- | --- | --- |
| [YAML quickstart](yaml.md) | You want native manifests, GitOps, or no Replicove CLI dependency | Docker, Git, kubectl; kind for the local demo |
| [CLI quickstart](../../QUICKSTART.md) | You want plan/approval commands and a managed local tunnel | Above plus Go and make to build the CLI |
| Helm chart | You already manage infrastructure with Helm | Built operator image and Helm compatible with the chart |

The YAML installer is generated from the **same chart** as the CLI, keeping the Deployment and permissions aligned. The operator uses the pinned vCluster chart internally; end users do not need a Helm CLI or preinstalled vCluster.

## Host prerequisites

- Administrator permission to install three CRDs, protected/destination namespaces, and their RBAC.
- A separate source namespace with explicit read permissions for selected resources.
- For the default persistent profile: a working default StorageClass with dynamic provisioning and `Delete` reclaim policy. The control plane requests 1 GiB. Application volumes are additional.
- A working container runtime, cluster DNS, pod networking, and outbound access to the pinned chart and workload images.
- Enough worker CPU and memory for both control planes and selected workloads. The functional CI fixtures are small; there is no published sizing guarantee.

The live host tests cover Kubernetes 1.35.8 and 1.36.4, with vCluster 0.37.1 and guest Kubernetes 1.36.0. Vendor-specific identity, security admission, and CSI behavior require separate qualification. Read [compatibility](../reference/compatibility.md) before adapting to EKS, AKS, GKE, OpenShift, or RKE2.

## Install on your own disposable cluster

Build and push a tag your nodes can pull. Supply your own registry and choose an explicit test-cluster context:

```bash
docker build --tag YOUR_REGISTRY/replicove:dev .
docker push YOUR_REGISTRY/replicove:dev
```

For native YAML, copy `examples/yaml/install/kustomization.yaml` to a local overlay, keep its base pointing at `config/install`, and set `newName` and `newTag` to your image. Apply the CRDs, wait for them to be established, apply the overlay, then wait for the bootstrap Job and operator Deployment as shown in the [walkthrough](yaml.md). Keep the system and destination namespace names consistent in all resources and references if you customize them.

For Helm, create the destination and source namespaces first and provide values:

```yaml
image:
  repository: YOUR_REGISTRY/replicove
  tag: dev
destinationNamespace: replica-lab
sources:
  - namespace: source-dev
    rules:
      - apiGroups: [""]
        resources: [configmaps, serviceaccounts, services]
        verbs: [get, list]
      - apiGroups: [apps]
        resources: [deployments]
        verbs: [get, list]
```

```bash
kubectl --context YOUR_TEST_CONTEXT create namespace replica-lab
helm --kube-context YOUR_TEST_CONTEXT install replicove ./charts/replicove \
  --namespace replicove-system --create-namespace --values operator-values.yaml
```

These source permissions do not include Secrets or Helm release storage. Add those only when needed, together with corresponding `ReplicaGrant` entries. See [operator values](../reference/operator.md).

## Key initialization and persistence

The Helm installation generates an immutable 32-byte encryption key. The native YAML installation runs a bounded bootstrap Job that creates the same key in the protected namespace. No key is embedded in the checked-in manifests.

Re-running the Job validates and reuses an existing key. An invalid key, or a missing key with encrypted state still present, fails initialization instead of replacing it. Back up the key **with** its encrypted state. A missing key cannot be recovered from ciphertext.

## Upgrades and removal

Use the same installation method for an existing operator. Switching a live installation between Helm and standalone manifests needs an explicit ownership migration and is not a supported automatic path.

For YAML upgrades, review the generated diff, preserve the state key and state Secrets, apply CRD updates, remove only the **completed** bootstrap Job if its image/template changed, then apply your updated overlay. Kubernetes Job pod templates are immutable. Wait for the new Job and Deployment and check existing replica conditions. Do not run upgrades while a bootstrap Job is still active.

For Helm upgrades, apply CRD updates separately (Helm does not automatically upgrade files in `crds/`) and use `helm upgrade` with the existing release name, protected namespace, and reviewed values. The chart reuses the existing key.

Before uninstalling, delete replica requests and wait for their finalizers and all access requests to finish. See the [cleanup guide](../guides/cleanup.md). **Do not delete the installation namespaces or CRDs to bypass cleanup.** They can contain unrelated resources and the ownership records needed for recovery.
