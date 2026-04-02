.DEFAULT_GOAL := up
.EXPORT_ALL_VARIABLES:

ANSIBLE_EXTRA_VARS_FILE=$(shell pwd)/mini-lab-overrides/extra-vars.yaml
KIND_EXPERIMENTAL_DOCKER_NETWORK=mini_lab_ext
KUBECONFIG := $(shell pwd)/mini-lab/.kubeconfig

MINI_LAB_FLAVOR := $(or $(MINI_LAB_FLAVOR),capms)

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

ifeq ($(MINI_LAB_FLAVOR),capms)
DEPLOY_TARGET=deploy-kubeadm
else ifeq ($(MINI_LAB_FLAVOR),kamaji)
DEPLOY_TARGET=deploy-kamaji
KUBERNETES_VERSION = 1.34.1
WORKER_MACHINE_IMAGE ?= capms-ubuntu-$(KUBERNETES_VERSION)
TENANT_NAMESPACE = kamaji-tenants
CONTROL_PLANE_IP ?=
PODS_CIDR = 192.168.0.0/16
SERVICES_CIDR = 10.96.0.0/12
else
$(error Unknown flavor $(MINI_LAB_FLAVOR))
endif

.PHONY: up
up: bake $(DEPLOY_TARGET)

.PHONY: bake
bake:
	cd mini-lab && make external_network up # change directory required for gen-certs

.PHONY: deploy-kubeadm
deploy-kubeadm:
	docker compose run --rm clusterctl init --addon helm

.PHONY: deploy-kamaji
deploy-kamaji:
	docker compose -f compose.yaml -f compose.kamaji.yaml run --rm clusterctl init --addon helm --control-plane kamaji
	# TODO: remove once merged: https://github.com/clastix/cluster-api-control-plane-provider-kamaji/pull/323
	# - Grant the Kamaji controller manager access to MetalStackCluster resources.
	kubectl --kubeconfig=$(KUBECONFIG) apply -f kamaji/kamaji-rbac.yaml
	# - Register MetalStackCluster as a supported dynamic infrastructure cluster in the Kamaji controller manager.
	# - The following patch is quite dirty as we double the args, but works as the last value in the args list wins for duplicate keys.
	kubectl --kubeconfig=$(KUBECONFIG) patch deployments.apps -n kamaji-system capi-kamaji-controller-manager --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--feature-gates=DynamicInfrastructureClusterPatch=true,ExternalClusterReference=false,ExternalClusterReferenceCrossNamespace=false,SkipInfraClusterPatch=false"},{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--dynamic-infrastructure-clusters=MetalStackCluster"}]'
	kubectl --kubeconfig=$(KUBECONFIG) rollout status deployment -n kamaji-system capi-kamaji-controller-manager
	
	# workaround for a route not being applied successfully in the exit container.
	docker exec -it exit ip route add 203.0.113.128/25 via 203.0.113.128 dev mini_lab_ext

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

.PHONY: control-plane-ip
control-plane-ip:
	metalctl network ip create --network internet-mini-lab --project $(METAL_PROJECT_ID) --name "$(CLUSTER_NAME)-vip" --type static -o template --template "{{ .ipaddress }}"

.PHONY: apply-sample-cluster
apply-sample-cluster:
	$(eval CONTROL_PLANE_IP = $(shell metalctl network ip list --name "$(CLUSTER_NAME)-vip" -o template --template '{{ .ipaddress }}'))
	echo  $(CLUSTER_NAME)
	clusterctl generate cluster $(CLUSTER_NAME) \
		--kubeconfig=$(KUBECONFIG) \
		--worker-machine-count 1 \
		--control-plane-machine-count 1 \
		--kubernetes-version $(KUBERNETES_VERSION) \
		--from ../config/clusterctl-templates/cluster-template-calico-lab.yaml \
		| kubectl --kubeconfig=$(KUBECONFIG) apply -f -

.PHONY: delete-sample-cluster
delete-sample-cluster:
	$(eval CONTROL_PLANE_IP = $(shell metalctl network ip list --name "$(CLUSTER_NAME)-vip" -o template --template '{{ .ipaddress }}'))
	clusterctl generate cluster $(CLUSTER_NAME) \
		--kubeconfig=$(KUBECONFIG) \
		--worker-machine-count 1 \
		--control-plane-machine-count 1 \
		--kubernetes-version $(KUBERNETES_VERSION) \
		--from ../config/clusterctl-templates/cluster-template-calico-lab.yaml \
		| kubectl --kubeconfig=$(KUBECONFIG) delete -f -

.PHONY: mtu-fix
mtu-fix:
	cd mini-lab && ssh -F files/ssh/config leaf01 'ip link set dev vtep-1001 mtu 9100 && echo done'
	cd mini-lab && ssh -F files/ssh/config leaf02 'ip link set dev vtep-1001 mtu 9100 && echo done'

.PHONY: create-kamaji-tenant
create-kamaji-tenant:
	$(eval CONTROL_PLANE_IP = $(shell metalctl network ip list --name "$(CLUSTER_NAME)-vip" -o template --template '{{ .ipaddress }}'))
	$(eval METAL_NODE_NETWORK_ID = $(shell metalctl network list --project $(METAL_PROJECT_ID) -o template --template '{{ (index . 0).id }}'))
	# let MetalLB assign the IP to the tenant cluster control plane service
	envsubst < kamaji/metallb-tenant-pool.yaml | kubectl --kubeconfig=$(KUBECONFIG) apply -f -
	docker compose -f compose.yaml -f compose.kamaji.yaml run --rm \
		clusterctl generate cluster $(CLUSTER_NAME) \
		--target-namespace $(TENANT_NAMESPACE) \
		--worker-machine-count 1 \
		--kubernetes-version $(KUBERNETES_VERSION) \
		--from /templates/cluster-template-kamaji-tenant.yaml \
		| kubectl --kubeconfig=$(KUBECONFIG) apply -f -

.PHONY: delete-kamaji-tenant
delete-kamaji-tenant:
	docker compose -f compose.yaml -f compose.kamaji.yaml run --rm \
		clusterctl generate cluster $(CLUSTER_NAME) \
		--target-namespace $(TENANT_NAMESPACE) \
		--worker-machine-count 1 \
		--kubernetes-version $(KUBERNETES_VERSION) \
		--from /templates/cluster-template-kamaji-tenant.yaml \
		| kubectl --kubeconfig=$(KUBECONFIG) delete -f -

.PHONY: kamaji-tenant-kubeconfig
kamaji-tenant-kubeconfig:
	kubectl --kubeconfig=$(KUBECONFIG) -n $(TENANT_NAMESPACE) get secret $(CLUSTER_NAME)-kubeconfig -o jsonpath='{.data.value}' | base64 -d > ../kamaji-tenant.kubeconfig
	@echo "Tenant kubeconfig written to kamaji-tenant.kubeconfig"

.PHONY: kamaji-tenant-deploy-calico
kamaji-tenant-deploy-calico:
	kubectl --kubeconfig=../kamaji-tenant.kubeconfig create -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/tigera-operator.yaml || true
	kubectl --kubeconfig=../kamaji-tenant.kubeconfig apply -f kamaji/calico.yaml

.PHONY: kamaji-tenant-deploy-metal-ccm
kamaji-tenant-deploy-metal-ccm:
	kustomize build ../config/target-cluster/overlays/kamaji | envsubst | kubectl --kubeconfig=../kamaji-tenant.kubeconfig apply -f -
