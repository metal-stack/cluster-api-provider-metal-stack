package frmwrk

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	// . "github.com/onsi/gomega"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
)

var _ = Describe("Basic Cluster Creation", Ordered, func() {
	BeforeAll(func() {
		e2eCtx = NewE2EContext()
		e2eCtx.ProvideBootstrapCluster()
		e2eCtx.CreateClusterctlConfig(context.TODO())
		e2eCtx.InitManagementCluster(context.TODO())
	})

	It("create", func() {
		// TODO:
		// - access kind cluster
		// - install capms
		// - create a namespace
		// - create node network
		// - create firewall
		// - create control plane IP
		// - create cluster

		ctx := context.Background()

		ec := e2eCtx.NewE2ECluster(ClusterConfig{
			SpecName:                 "basic-cluster-creation",
			NamespaceName:            "random",
			ClusterName:              "random",
			KubernetesVersion:        "v1.34.1",
			ControlPlaneIP:           "203.0.113.130",
			ControlPlaneMachineCount: 1,
			WorkerMachineCount:       1,
		})
		defer ec.Teardown(ctx)

		ec.SetupMetalStackPreconditions(ctx)
		ec.SetupNamespace(ctx)
		ec.GenerateAndApplyClusterTemplate(ctx)

		By("Wait for cluster")
		framework.DiscoveryAndWaitForCluster(ctx, framework.DiscoveryAndWaitForClusterInput{
			Namespace: ec.NamespaceName,
			Name:      ec.ClusterName,
			Getter:    e2eCtx.Environment.Bootstrap.GetClient(),
		})

		framework.WaitForControlPlaneToBeReady(ctx, framework.WaitForControlPlaneToBeReadyInput{
			Getter: e2eCtx.Environment.Bootstrap.GetClient(),
			ControlPlane: &controlplanev1.KubeadmControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name:      ec.ClusterName,
					Namespace: ec.NamespaceName,
				},
			},
		})

	})

	AfterAll(func() {
		if e2eCtx.Environment.ClusterProvider != nil {
			e2eCtx.Environment.ClusterProvider.Dispose(context.TODO())
		}
	})
})
