# cluster-api-provider-metal-stack

The Cluster API provider for metal-stack (CAPMS) implements the declarative management of Kubernetes cluster infrastructure.

> [!caution] Early development
> This project is currently under heavy development and is not advised to be used in production any time soon.
> Please use or stack on top of [Gardener](https://docs.metal-stack.io/stable/installation/deployment/#Gardener-with-metal-stack) instead.
> User documentation will follow as soon. Until then head to our [CONTRIBUTING.md](/CONTRIBUTING.md)

Currently we provide the following custom resources:

- [`MetalStackCluster`](./api/v1alpha1/metalstackcluster_types.go) can be used as [infrastructure cluster](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster) and ensures that the metal-stack network and firewall are being prepared.
- [`MetalStackMachine`](./api/v1alpha1/metalstackmachine_types.go) bridges between [infrastructure machines](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-machine) and metal-stack machines.

> [!note] Compatibility with bootstrap providers
> Currently our infrastructure provider is only tested against the [Cluster API bootstrap provider Kubeadm (CABPK)](https://cluster-api.sigs.k8s.io/tasks/bootstrap/kubeadm-bootstrap/index.html?highlight=kubeadm#cluster-api-bootstrap-provider-kubeadm).
> While other providers might work, there is no guarantee nor to goal to reach compatability.
