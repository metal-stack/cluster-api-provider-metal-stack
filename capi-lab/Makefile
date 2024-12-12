.DEFAULT_GOAL := up

KUBECONFIG := $(shell pwd)/mini-lab/.kubeconfig
IMG ?= capms-controller:latest

.PHONY: up
up: bake deploy-capi

.PHONY: bake
bake:
	make -C mini-lab up	MINI_LAB_FLAVOR=capms ANSIBLE_EXTRA_VARS_FILE=../mini-lab-overrides/extra-vars.yaml

.PHONY: deploy-capi
deploy-capi:
	make -C mini-lab env
	@[ -f .extra_vars.yaml ] || echo '{}' > .extra_vars.yaml
	docker compose --env-file ./mini-lab/.env up --remove-orphans --force-recreate capi

.PHONY: cleanup
cleanup:
	make -C mini-lab cleanup
	docker compose down

.PHONY: dev-env
dev-env:
	@echo "export METALCTL_API_URL=http://metal.172.17.0.1.nip.io:8080"
	@echo "export METALCTL_HMAC=metal-admin"
	@echo "export KUBECONFIG=$(shell pwd)/mini-lab/.kubeconfig"

.PHONY: controller
controller:
	cd .. && make docker-build && cd -
	kind --name metal-control-plane load docker-image $(IMG)
	kubectl --kubeconfig=$(KUBECONFIG) patch deployments.apps -n cap-metal-stack metal-stack-controller-manager --patch='{"spec":{"template":{"spec":{"containers":[{"name": "manager","imagePullPolicy":"IfNotPresent","image":"$(IMG)"}]}}}}'
	kubectl --kubeconfig=$(KUBECONFIG) delete pod -n cap-metal-stack -l control-plane=metal-stack-controller-manager
