# cluster-api-provider-metal-stack

The Cluster API provider for metal-stack (CAPMS) implements the declarative management of Kubernetes cluster infrastructure.

> [!CAUTION]
> This project is currently under heavy development and is not advised to be used in production any time soon.
> Please use our stack on top of [Gardener](https://docs.metal-stack.io/stable/installation/deployment/#Gardener-with-metal-stack) instead.
> User documentation will follow as soon. Until then head to our [CONTRIBUTING.md](/CONTRIBUTING.md)

Currently we provide the following custom resources:

- [`MetalStackCluster`](./api/v1alpha1/metalstackcluster_types.go) can be used as [infrastructure cluster](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster) and ensures that there is a control plane ip for the cluster.
- [`MetalStackMachine`](./api/v1alpha1/metalstackmachine_types.go) bridges between [infrastructure machines](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-machine) and metal-stack machines.

> [!note]
> Currently our infrastructure provider is only tested against the [Cluster API bootstrap provider Kubeadm (CABPK)](https://cluster-api.sigs.k8s.io/tasks/bootstrap/kubeadm-bootstrap/index.html?highlight=kubeadm#cluster-api-bootstrap-provider-kubeadm).
> While other providers might work, there is no guarantee nor the goal to reach compatibility.

## Getting started

**Prerequisites:**

- a running metal-stack installation

First add the metal-stack infrastructure provider to your `clusterctl.yaml`:

```yaml
# ~/.config/cluster-api/clusterctl.yaml
providers:
  - name: "metal-stack"
    url: "https://github.com/metal-stack/cluster-api-provider-metal-stack/releases/latest/download/infrastructure-components.yaml"
    type: InfrastructureProvider
```

Now you are able to install the CAPMS into your cluster:

```bash
export METAL_API_URL=http://metal.203.0.113.1.nip.io:8080
export METAL_API_HMAC=metal-admin
export EXP_KUBEADM_BOOTSTRAP_FORMAT_IGNITION=true

clusterctl init --infrastructure metal-stack
```

Now you should be able to create Clusters on top of metal-stack.
For your first cluster it is advised to start with our generated template.

```bash
# to display all env variables that need to be set
clusterctl generate cluster example --kubernetes-version v1.30.6 --infrastructure metal-stack --list-variables
```

> [!CAUTION]
> **Manual steps needed:**
> Due to the early development stage the following manual actions are needed for the cluster to operate.

1. The pod network and firewall need to be created manually.
2. The metal-ccm has to be deployed
3. You need to install your CNI of choice. This is required due to CAPI.
