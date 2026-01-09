# cluster-api-provider-metal-stack

The Cluster API provider for metal-stack (CAPMS) implements the declarative management of Kubernetes cluster infrastructure on top of [metal-stack](https://metal-stack.io/) using [Cluster API (CAPI)](https://cluster-api.sigs.k8s.io/).

> [!WARNING]
> As of now the CAPMS is not yet feature complete and there might be breaking changes in future releases.
> In case you search for a feature stable alternative consider [Gardener on metal-stack](https://docs.metal-stack.io/stable/installation/deployment/#Gardener-with-metal-stack) instead.
> For developing this project head to our [DEVELOPMENT.md](/DEVELOPMENT.md).

Currently, we provide the following custom resources:

- [`MetalStackCluster`](./api/v1alpha1/metalstackcluster_types.go) can be used as [infrastructure cluster](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster) and ensures that there is a control plane IP for the cluster.
- [`MetalStackMachine`](./api/v1alpha1/metalstackmachine_types.go) bridges between [infrastructure machines](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-machine) and metal-stack machines.
- [`MetalStackMachineTemplate`](./api/v1alpha1/metalstackmachinetemplate_types.go) can be used to define reusable machine specifications for `MetalStackMachine` resources.
- [`MetalStackFirewallDeployment`](./api/v1alpha1/metalstackfirewalldeployment_types.go) can be used to define firewall deployments for a cluster.
- [`MetalStackFirewallTemplate`](./api/v1alpha1/metalstackfirewalltemplate_types.go) defines the configuration of deployed firewalls.

We plan to cover more resources in the future:

- Complete Firewall Deployments using the [Firewall Controller Manager](https://github.com/metal-stack/firewall-controller-manager)
- Improved configuration suggestion of CNIs

> [!note]
> Currently our infrastructure provider is only tested against the [Cluster API bootstrap provider Kubeadm (CABPK)](https://cluster-api.sigs.k8s.io/tasks/bootstrap/kubeadm-bootstrap/index.html?highlight=kubeadm#cluster-api-bootstrap-provider-kubeadm).
> While other providers might work, there is no guarantee nor the goal to reach compatibility.

## Getting started

**Prerequisites:**

- Running metal-stack installation. See our [installation](https://docs.metal-stack.io/stable/installation/deployment/) section on how to get started with metal-stack.
- Operating system images available to metal-stack. See [metal-stack/metal-images](https://github.com/metal-stack/metal-images) for pre-built ones.
- Management cluster (with network access to the metal-stack infrastructure).
- CLI metalctl installed for communicating with the metal-api. Installation instructions can be found in the corresponding [repository](https://github.com/metal-stack/metalctl).
- CLI clusterctl

First, add the metal-stack infrastructure provider to your `clusterctl.yaml`:

```yaml
# ~/.config/cluster-api/clusterctl.yaml
providers:
  - name: "metal-stack"
    url: "https://github.com/metal-stack/cluster-api-provider-metal-stack/releases/latest/download/infrastructure-components.yaml"
    type: InfrastructureProvider
```

Now, you are able to install the CAPMS into your management cluster:

```bash
# export the following environment variables
export METAL_API_URL=<url>
export METAL_API_HMAC=<hmac>
export METAL_API_HMAC_AUTH_TYPE=<Metal-Admin or Metal-Edit>
export EXP_KUBEADM_BOOTSTRAP_FORMAT_IGNITION=true

# initialize the management cluster
clusterctl init --infrastructure metal-stack
```

> [!CAUTION]
> **Manual steps needed:**
> Due to the early development stage, manual actions are needed for the cluster to operate. Some metal-stack resources need to be created manually.

Allocate a VIP for the control plane.

```bash
export CLUSTER_NAME=<cluster-name>
export METAL_PARTITION=<partition>
export METAL_PROJECT_ID=<project-id>

export CONTROL_PLANE_IP=$(metalctl network ip create --network internet --project $METAL_PROJECT_ID --name "$CLUSTER_NAME-vip" --type static -o template --template "{{ .ipaddress }}")
```

For your first cluster, it is advised to start with our generated template. Ensure that the namespaced cluster name is unique within the metal stack project.

```bash
# display required environment variables
clusterctl generate cluster $CLUSTER_NAME --infrastructure metal-stack --list-variables --flavor calico

# set additional environment variables
export CONTROL_PLANE_MACHINE_IMAGE=<machine-image>
export CONTROL_PLANE_MACHINE_SIZE=<machine-size>
export WORKER_MACHINE_IMAGE=<machine-image>
export WORKER_MACHINE_SIZE=<machine-size>
export FIREWALL_MACHINE_IMAGE=<machine-image>
export FIREWALL_MACHINE_SIZE=<machine-size>

# generate manifest
clusterctl generate cluster $CLUSTER_NAME --kubernetes-version v1.32.9 --infrastructure metal-stack --flavor calico
```

Apply the generated manifest from the `clusterctl` output.

```bash
kubectl apply -f <manifest>
```

That's it!

## Frequently Asked Questions

### I need to know the Control Plane IP address in advance. Can I provide a static IP address in advance?

Yes, simply create a static IP address and set it to `metalstackcluster/$CLUSTER_NAME.spec.controlPlaneIP`.

```bash
metalctl network ip create --name $CLUSTER_NAME-vip --project $METAL_PROJECT_ID --type static
```

### I'd like to have a specific Pod CIDR. How can I achieve this?

When generating your cluster, set `POD_CIDR` to your desired value.

```bash
export POD_CIDR=["10.240.0.0/12"]
```

## I'd like to update the firewall and / or its rules. How can I achieve this?

Unfortunately there is no automated way to update firewall deployments or rules yet. You have to manually edit the `MetalStackFirewallDeployment` resource to force it to create a new firewall.

1. Save the firewall machine ID stored in `MetalStackFirewallDeployment.spec.managedResourceRef.name`.
2. Update the `MetalStackFirewallTemplate` as desired.
3. Remove `MetalStackFirewallDeployment.spec.managedResourceRef`.
4. Wait for CAPMS to create the new firewall.
5. Wait for the new firewall to be in `Phoned Home` state using `metalctl machine list --id <new-id>`.
6. Delete the old firewall machine using `metalctl machine delete <old-id>` as soon as possible.

This leads to a minimized downtime of the cluster as the firewall is not available during the transition.

## Flavors

You might choose from different cluster template [flavors](https://cluster-api.sigs.k8s.io/clusterctl/commands/generate-cluster.html?highlight=flavor#flavors) to generate manifests with clusterctl. Here is a table describing the available flavors:

| Name      | Description                                                                                                                                                                                                                                                                            | K8s Compatibility |
| --------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------- |
|           | This is the default flavor without providing the `--flavor` flag. This expects the user to deploy a CNI and a CCM.                                                                                                                                                                     | >= v1.33          |
| calico    | Installs [calico](https://docs.tigera.io/calico/latest/about/) CNI along with [metal-ccm](https://github.com/metal-stack/metal-ccm). Depends on `ClusterResourceSet` and the [Add-on Provider for Helm](https://github.com/kubernetes-sigs/cluster-api-addon-provider-helm/tree/main). | >= v1.33          |
| pre-v1.33 | The same as the default flavor but working for K8s versions < v1.33.                                                                                                                                                                                                                   | < v1.33           |
