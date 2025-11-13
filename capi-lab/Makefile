.DEFAULT_GOAL := up
.EXPORT_ALL_VARIABLES:

ANSIBLE_EXTRA_VARS_FILE=$(shell pwd)/mini-lab-overrides/extra-vars.yaml
KIND_EXPERIMENTAL_DOCKER_NETWORK=mini_lab_ext
KUBECONFIG := $(shell pwd)/mini-lab/.kubeconfig
MINI_LAB_FLAVOR=capms

CLUSTER_NAME ?= metal-test
KUBERNETES_VERSION ?= 1.33.5

METAL_API_URL=http://metal.203.0.113.1.nip.io:8080
METAL_API_HMAC=metal-edit
METAL_API_HMAC_AUTH_TYPE=Metal-Edit
METALCTL_API_URL=http://metal.203.0.113.1.nip.io:8080
METALCTL_HMAC=metal-edit
METALCTL_HMAC_AUTH_TYPE=Metal-Edit

METAL_PARTITION ?= mini-lab
METAL_PROJECT_ID ?= 00000000-0000-0000-0000-000000000001

CONTROL_PLANE_MACHINE_IMAGE ?= capms-ubuntu-$(KUBERNETES_VERSION)
CONTROL_PLANE_MACHINE_SIZE ?= v1-small-x86
WORKER_MACHINE_IMAGE ?= capms-ubuntu-$(KUBERNETES_VERSION)
WORKER_MACHINE_SIZE ?= v1-small-x86
FIREWALL_MACHINE_IMAGE ?= firewall-ubuntu-3.0
FIREWALL_MACHINE_SIZE ?= v1-small-x86
FIREWALL_EXTERNAL_NETWORKS ?= ["internet-mini-lab"]

IMG ?= ghcr.io/metal-stack/cluster-api-metal-stack-controller:latest

.PHONY: up
up: bake deploy-capi

.PHONY: bake
bake:
	cd mini-lab && make external_network up # change directory required for gen-certs

.PHONY: deploy-capi
deploy-capi:
	docker compose run --rm clusterctl init --addon helm

.PHONY: cleanup
cleanup:
	make -C mini-lab cleanup
	docker compose down

.PHONY: dev-env
dev-env:
	@echo "export METALCTL_API_URL=${METALCTL_API_URL}"
	@echo "export METALCTL_HMAC=${METALCTL_HMAC}"
	@echo "export METALCTL_HMAC_AUTH_TYPE=${METALCTL_HMAC_AUTH_TYPE}"
	@echo "export KUBECONFIG=${KUBECONFIG}"

.PHONY: controller
controller:
	cd .. && make docker-build && cd -
	kind --name metal-control-plane load docker-image $(IMG)
	kubectl --kubeconfig=$(KUBECONFIG) patch deployments.apps -n cap-metal-stack metal-stack-controller-manager --patch='{"spec":{"template":{"spec":{"containers":[{"name": "manager","imagePullPolicy":"IfNotPresent","image":"$(IMG)"}]}}}}'
	kubectl --kubeconfig=$(KUBECONFIG) delete pod -n cap-metal-stack -l control-plane=metal-stack-controller-manager

.PHONY: firewall
firewall:
	metalctl firewall create --description fw --name fw --hostname fw --project 00000000-0000-0000-0000-000000000001 --partition mini-lab --image firewall-ubuntu-3.0 --size v1-small-x86 --firewall-rules-file=firewall-rules.yaml --networks internet-mini-lab,$(shell metalctl network list --name $(CLUSTER_NAME) -o template --template '{{ .id }}')

.PHONY: node-network
node-network:
	metalctl network allocate --description "node network for $(CLUSTER_NAME) cluster" --name $(CLUSTER_NAME) --project 00000000-0000-0000-0000-000000000001 --partition mini-lab

.PHONY: control-plane-ip
control-plane-ip:
	metalctl network ip create --network internet-mini-lab --project $(METAL_PROJECT_ID) --name "$(CLUSTER_NAME)-vip" --type static -o template --template "{{ .ipaddress }}"

.PHONY: apply-sample-cluster
apply-sample-cluster:
	$(eval METAL_NODE_NETWORK_ID = $(shell metalctl network list --name $(CLUSTER_NAME) -o template --template '{{ .id }}'))
	$(eval CONTROL_PLANE_IP = $(shell metalctl network ip list --name "$(CLUSTER_NAME)-vip" -o template --template '{{ .ipaddress }}'))
	echo  $(CLUSTER_NAME)
	clusterctl generate cluster $(CLUSTER_NAME) \
		--kubeconfig=$(KUBECONFIG) \
		--worker-machine-count 1 \
		--control-plane-machine-count 1 \
		--kubernetes-version $(KUBERNETES_VERSION) \
		--from ../config/clusterctl-templates/cluster-template-calico.yaml \
		| kubectl --kubeconfig=$(KUBECONFIG) apply -f -

.PHONY: delete-sample-cluster
delete-sample-cluster:
	$(eval METAL_NODE_NETWORK_ID = $(shell metalctl network list --name $(CLUSTER_NAME) -o template --template '{{ .id }}'))
	$(eval CONTROL_PLANE_IP = $(shell metalctl network ip list --name "$(CLUSTER_NAME)-vip" -o template --template '{{ .ipaddress }}'))
	clusterctl generate cluster $(CLUSTER_NAME) \
		--kubeconfig=$(KUBECONFIG) \
		--worker-machine-count 1 \
		--control-plane-machine-count 1 \
		--kubernetes-version $(KUBERNETES_VERSION) \
		--from ../config/clusterctl-templates/cluster-template-calico.yaml \
		| kubectl --kubeconfig=$(KUBECONFIG) delete -f -

.PHONY: mtu-fix
mtu-fix:
	cd mini-lab && ssh -F files/ssh/config leaf01 'ip link set dev vtep-1001 mtu 9100 && echo done'
	cd mini-lab && ssh -F files/ssh/config leaf02 'ip link set dev vtep-1001 mtu 9100 && echo done'
