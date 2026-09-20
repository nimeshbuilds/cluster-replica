GO ?= go
CONTROLLER_GEN_VERSION := v0.22.0
CHART := $(CURDIR)/.cache/vcluster-0.37.1.tgz

.PHONY: build generate manifests test test-contract test-integration test-e2e vet check docs docs-serve

build:
	mkdir -p bin
	$(GO) build -trimpath -o bin/cluster-replica ./cmd/operator
	$(GO) build -trimpath -o bin/replicove ./cmd/replicove

generate:
	$(GO) run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) object paths=./api/... crd output:crd:artifacts:config=config/crd
	gofmt -w api cmd internal test
	cp config/crd/*.yaml charts/replicove/crds/
	$(MAKE) manifests

manifests:
	$(GO) run ./hack/render-install

test:
	$(GO) test -race ./...

test-contract:
	./hack/fetch-chart.sh
	VCLUSTER_CHART="$(CHART)" $(GO) test -count=1 -run TestPinnedChartContract -v ./internal/runtime/helm

test-integration:
	./hack/fetch-chart.sh
	./hack/fetch-envtest.sh
	VCLUSTER_CHART="$(CHART)" KUBEBUILDER_ASSETS="$(CURDIR)/.cache/envtest/controller-tools/envtest" $(GO) test -count=1 -tags=integration -v ./test/integration

vet:
	$(GO) vet ./...

# Requires Docker. Creates/deletes only its own fresh kind cluster.
test-e2e:
	./hack/fetch-e2e-tools.sh
	./hack/e2e.sh

check: test test-contract test-integration vet build

DOCS_PYTHON ?= $(CURDIR)/.cache/docs-venv/bin/python

docs: build
	$(DOCS_PYTHON) hack/docs.py prepare
	$(DOCS_PYTHON) -m mkdocs build --strict
	$(DOCS_PYTHON) hack/docs.py check

docs-serve: docs
	$(DOCS_PYTHON) -m mkdocs serve --dev-addr 127.0.0.1:8000
