SHELL := /bin/bash

CLUSTER_NAME ?= evo-sched
SCHEDULER_IMAGE ?= evok8s-scheduler:0.1.0
KIND_NODE_IMAGE ?= kindest/node:v1.35.0

.PHONY: verify verify-v2 test vet paper killer-figure release-package release-package-v3 cluster-up cluster-stop scheduler-image scheduler-deploy live-smoke

verify:
	./scripts/verify-official-v3.sh

verify-v2:
	./scripts/verify-official-v2.sh

test:
	cd scheduler && go test -count=1 ./...

vet:
	cd scheduler && go vet ./...

paper:
	cd paper && latexmk -pdf -interaction=nonstopmode -halt-on-error main.tex

killer-figure:
	cd presentation/kubecon-europe-2027 && latexmk -pdf -interaction=nonstopmode -halt-on-error killer-figure.tex

release-package:
	./scripts/package-v2-release.sh $(RELEASE_OUTPUT)

release-package-v3:
	./scripts/package-v3-release.sh $(RELEASE_OUTPUT)

cluster-up:
	@if docker inspect $(CLUSTER_NAME)-control-plane >/dev/null 2>&1; then \
		docker start \
			$(CLUSTER_NAME)-control-plane \
			$(CLUSTER_NAME)-worker \
			$(CLUSTER_NAME)-worker2 \
			$(CLUSTER_NAME)-worker3 \
			$(CLUSTER_NAME)-worker4 >/dev/null; \
	else \
		kind create cluster \
			--name $(CLUSTER_NAME) \
			--config kind-config-4workers.yaml \
			--image $(KIND_NODE_IMAGE) \
			--wait 180s; \
	fi
	kubectl wait --for=condition=Ready nodes --all --timeout=180s

cluster-stop:
	@docker stop \
		$(CLUSTER_NAME)-control-plane \
		$(CLUSTER_NAME)-worker \
		$(CLUSTER_NAME)-worker2 \
		$(CLUSTER_NAME)-worker3 \
		$(CLUSTER_NAME)-worker4 >/dev/null 2>&1 || true

scheduler-image:
	docker build -t $(SCHEDULER_IMAGE) ./scheduler
	kind load docker-image --name $(CLUSTER_NAME) $(SCHEDULER_IMAGE)

scheduler-deploy:
	kubectl create namespace evo-sched --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f rbac.yaml
	kubectl apply -f deploy.yaml
	kubectl -n evo-sched rollout status deployment/evok8s-scheduler --timeout=180s

live-smoke:
	$(MAKE) cluster-up
	$(MAKE) scheduler-image
	$(MAKE) scheduler-deploy
	cd scheduler && go run ./cmd/evaluator \
		--workload ../workloads/uniform-small.json \
		--weights-file ../experiments/weights-v2.json \
		--poll-interval 250ms \
		--timeout 60s \
		--output /tmp/evo-sched-v2-smoke.json
