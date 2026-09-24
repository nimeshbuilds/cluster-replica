package main

import "testing"

func TestExperimentManifestDoesNotImportAuthority(t *testing.T) {
	data := []byte(`apiVersion: replica.nimeshbuilds.dev/v1alpha1
kind: ReplicaExperiment
metadata:
  name: resilience
  namespace: lab
  uid: forged
  finalizers: [foreign]
status:
  phase: Active
spec:
  replicaRef: {name: demo, uid: real-guest-parent}
  durationSeconds: 30
  faults:
    - {kind: NetworkIsolation, namespace: app-copy}
`)
	x, err := experimentManifest(data, "resilience", "lab")
	if err != nil {
		t.Fatal(err)
	}
	if x.UID != "" || len(x.Finalizers) > 0 || x.Status.Phase != "" {
		t.Fatal("manifest imported lifecycle authority")
	}
	if _, err := experimentManifest(data, "different", "lab"); err == nil {
		t.Fatal("cross-name accepted")
	}
	if _, err := experimentManifest(data, "resilience", "other"); err == nil {
		t.Fatal("cross-namespace accepted")
	}
}
