.DEFAULT_GOAL := up
.EXPORT_ALL_VARIABLES:

ANSIBLE_EXTRA_VARS_FILE=$(shell pwd)/mini-lab-overrides/extra-vars.yaml
KIND_EXPERIMENTAL_DOCKER_NETWORK=mini_lab_ext
KUBECONFIG := $(shell pwd)/mini-lab/.kubeconfig
MINI_LAB_FLAVOR=capms
METALCTL_API_URL=http://metal.203.0.113.1.nip.io:8080
METALCTL_HMAC=metal-admin

IMG ?= ghcr.io/metal-stack/cluster-api-metal-stack-controller:latest

.PHONY: up
up: bake deploy-capi

.PHONY: bake
bake:
	cd mini-lab && make external_network up # change directory required for gen-certs

.PHONY: deploy-capi
deploy-capi:
	docker compose run --rm clusterctl init

.PHONY: cleanup
cleanup:
	make -C mini-lab cleanup
	docker compose down

.PHONY: dev-env
dev-env:
	@echo "export METALCTL_API_URL=${METALCTL_API_URL}"
	@echo "export METALCTL_HMAC=${METALCTL_HMAC}"
	@echo "export KUBECONFIG=${KUBECONFIG}"

.PHONY: controller
controller:
	cd .. && make docker-build && cd -
	kind --name metal-control-plane load docker-image $(IMG)
	kubectl --kubeconfig=$(KUBECONFIG) patch deployments.apps -n cap-metal-stack metal-stack-controller-manager --patch='{"spec":{"template":{"spec":{"containers":[{"name": "manager","imagePullPolicy":"IfNotPresent","image":"$(IMG)"}]}}}}'
	kubectl --kubeconfig=$(KUBECONFIG) delete pod -n cap-metal-stack -l control-plane=metal-stack-controller-manager

.PHONY: firewall
firewall:
	metalctl firewall create --description fw --name fw --hostname fw --project 00000000-0000-0000-0000-000000000001 --partition mini-lab --image firewall-ubuntu-3.0 --size v1-small-x86 --firewall-rules-file=firewall-rules.yaml --networks internet-mini-lab,$(shell metalctl network list --name metal-test -o template --template '{{ .id }}')

.PHONY: node-network
node-network:
	metalctl network allocate --description "node network for metal-test cluster" --name metal-test --project 00000000-0000-0000-0000-000000000001 --partition mini-lab

.PHONY: mtu-fix
mtu-fix:
	cd mini-lab && ssh -F files/ssh/config leaf01 'ip link set dev vtep-1001 mtu 9100 && echo done'
	cd mini-lab && ssh -F files/ssh/config leaf02 'ip link set dev vtep-1001 mtu 9100 && echo done'
