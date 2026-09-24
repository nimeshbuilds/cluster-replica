package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/nimeshbuilds/cluster-replica/internal/testrun"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/yaml"
)

func poolFixture() poolFile {
	members := []poolMember{}
	for _, name := range []string{"a", "b"} {
		members = append(members, poolMember{Namespace: "lab-" + name, SystemNamespace: "replicove-" + name, Values: map[string]any{"capacity": map[string]any{"enabled": true, "hard": map[string]any{"requests.cpu": "4", "requests.memory": "8Gi", "requests.storage": "20Gi", "pods": "30", "persistentvolumeclaims": "10"}}}})
	}
	return poolFile{APIVersion: testrun.RecipeVersion, Kind: "ReplicaPool", Members: members}
}

func TestPoolRendersSeparateNativeInstallationsWithoutKeys(t *testing.T) {
	pool := poolFixture()
	output, err := renderPool(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(output)), 4096)
	identities := map[string]bool{}
	namespaces, operators, quotas, bootstrap, crds := 0, 0, 0, 0, 0
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if len(obj.Object) == 0 {
			continue
		}
		identity := obj.GetAPIVersion() + "/" + obj.GetKind() + "/" + obj.GetNamespace() + "/" + obj.GetName()
		if identities[identity] {
			t.Fatalf("duplicate pool object: %s", identity)
		}
		identities[identity] = true
		switch obj.GetKind() {
		case "Namespace":
			namespaces++
		case "CustomResourceDefinition":
			crds++
		case "Secret":
			t.Fatal("rendered an encryption key")
		case "ReplicaGrant":
			t.Fatal("renderer auto-granted source access")
		case "ResourceQuota":
			quotas++
			if !strings.HasPrefix(obj.GetNamespace(), "lab-") {
				t.Fatal("quota applied to system namespace")
			}
		case "Job":
			bootstrap++
		case "Deployment":
			operators++
			if !strings.HasPrefix(obj.GetNamespace(), "replicove-") {
				t.Fatal("operator in tenant namespace")
			}
		}
	}
	if namespaces != 4 || operators != 2 || quotas != 2 || bootstrap != 2 || crds < 5 {
		t.Fatalf("namespaces=%d operators=%d quotas=%d bootstrap=%d crds=%d", namespaces, operators, quotas, bootstrap, crds)
	}
}

func TestPoolRejectsUnsafeOrAmbiguousMembers(t *testing.T) {
	cases := map[string]func(*poolFile){
		"empty":        func(p *poolFile) { p.Members = nil },
		"system same":  func(p *poolFile) { p.Members[0].Namespace = p.Members[0].SystemNamespace },
		"duplicate":    func(p *poolFile) { p.Members[1].Namespace = p.Members[0].Namespace },
		"cross system": func(p *poolFile) { p.Members[1].Namespace = p.Members[0].SystemNamespace },
		"host system":  func(p *poolFile) { p.Members[0].Namespace = "kube-system" },
		"no quota":     func(p *poolFile) { p.Members[0].Values = nil },
		"partial quota": func(p *poolFile) {
			delete(p.Members[0].Values["capacity"].(map[string]any)["hard"].(map[string]any), "pods")
		},
		"auto snapshots": func(p *poolFile) {
			p.Members[0].Values["mirrors"] = map[string]any{"enabled": true, "snapshotController": map[string]any{"mode": "auto"}}
		},
		"duplicate controllers": func(p *poolFile) {
			for i := range p.Members {
				p.Members[i].Values["mirrors"] = map[string]any{"enabled": true, "snapshotController": map[string]any{"mode": "managed"}}
			}
		},
		"oversized snapshot label": func(p *poolFile) {
			p.Members[0].ReleaseName = strings.Repeat("a", 44)
			p.Members[0].Values["mirrors"] = map[string]any{"enabled": true, "snapshotController": map[string]any{"mode": "managed"}}
		},
		"protected sources": func(p *poolFile) {
			p.Members[0].Values["sources"] = []any{map[string]any{"namespace": p.Members[1].SystemNamespace}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := poolFixture()
			mutate(&p)
			if err := validatePool(&p); err == nil {
				t.Fatal("accepted unsafe pool")
			}
		})
	}
}

func TestPoolSnapshotControllerIsSharedOnce(t *testing.T) {
	p := poolFixture()
	for i := range p.Members {
		mode := "existing"
		if i == 0 {
			mode = "managed"
		}
		p.Members[i].Values["mirrors"] = map[string]any{"enabled": true, "networkPolicyEnforced": true, "snapshotController": map[string]any{"mode": mode}}
	}
	output, err := renderPool(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(output)), 4096)
	controllers, crds := 0, 0
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if obj.GetKind() == "Deployment" && strings.HasSuffix(obj.GetName(), "snapshot-controller") {
			controllers++
		}
		if obj.GetKind() == "CustomResourceDefinition" && strings.HasSuffix(obj.GetName(), "snapshot.storage.k8s.io") {
			crds++
		}
	}
	if controllers != 1 || crds != 3 {
		t.Fatalf("controllers=%d snapshotCRDs=%d", controllers, crds)
	}
}

func TestPoolStrictParser(t *testing.T) {
	data, _ := yaml.Marshal(poolFixture())
	if _, err := parsePool(data); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"unknown: value\n", "---\nkind: Other\n", "kind: Other\n"} {
		if _, err := parsePool(append(append([]byte(nil), data...), []byte(suffix)...)); err == nil {
			t.Fatal("accepted malformed pool")
		}
	}
}

// Kubernetes permits identifiers which YAML 1.1 otherwise interprets as
// numbers, booleans or null. Decode every rendered object into its actual API
// type so metadata, RBAC subject names and Pod selectors cannot silently coerce.
func TestPoolPreservesYAMLScalarIdentifiersAsStrings(t *testing.T) {
	pool := poolFixture()
	pool.Members[0].Namespace = "123"
	pool.Members[0].SystemNamespace = "null"
	pool.Members[0].ReleaseName = "true"
	pool.Members[1].Namespace = "yes"
	pool.Members[1].SystemNamespace = "on"
	pool.Members[1].ReleaseName = "false"
	for i := range pool.Members {
		pool.Members[i].Values["sources"] = []any{map[string]any{"namespace": "456", "rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps"}, "verbs": []any{"get", "list"}}}}}
		mode := "existing"
		if i == 0 {
			mode = "managed"
		}
		pool.Members[i].Values["mirrors"] = map[string]any{"enabled": true, "networkPolicyEnforced": true, "sources": []any{"no"}, "snapshotController": map[string]any{"mode": mode}}
	}
	output, err := renderPool(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)
	typed := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	decoder := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(output)), 4096)
	names := map[string]bool{}
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if len(obj.Object) == 0 {
			continue
		}
		data, err := json.Marshal(obj.Object)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := typed.Decode(data, nil, nil); err != nil {
			t.Fatalf("invalid typed %s: %v", obj.GetKind(), err)
		}
		if obj.GetKind() == "Namespace" {
			names[obj.GetName()] = true
		}
	}
	for _, name := range []string{"123", "null", "yes", "on"} {
		if !names[name] {
			t.Fatalf("namespace %q lost its string identity", name)
		}
	}
}
