package frmwrk

import (
	"context"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck

	framework "sigs.k8s.io/cluster-api/test/framework"
)

func createE2ECluster(ctx context.Context, e2eCtx *E2EContext, cfg ClusterConfig) *E2ECluster {
	ec := e2eCtx.NewE2ECluster(cfg)

	ec.SetupMetalStackPreconditions(ctx)
	ec.SetupNamespace(ctx)
	ec.GenerateAndApplyClusterTemplate(ctx)

	DeferCleanup(func() {
		ec.Dump(context.Background())
	})

	By("Wait for cluster")
	framework.DiscoveryAndWaitForCluster(ctx, framework.DiscoveryAndWaitForClusterInput{
		Getter:    e2eCtx.Environment.Bootstrap.GetClient(),
		Namespace: ec.Refs.Cluster.Namespace,
		Name:      ec.Refs.Cluster.Name,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-cluster")...)

	controlPlane := framework.GetKubeadmControlPlaneByCluster(ctx, framework.GetKubeadmControlPlaneByClusterInput{
		Lister:      e2eCtx.Environment.Bootstrap.GetClient(),
		ClusterName: ec.Refs.Cluster.Name,
		Namespace:   ec.Refs.Cluster.Namespace,
	})
	Expect(controlPlane).To(Not(BeNil()))

	By("Wait for kubeadm control plane")
	framework.DiscoveryAndWaitForControlPlaneInitialized(ctx, framework.DiscoveryAndWaitForControlPlaneInitializedInput{
		Lister:  e2eCtx.Environment.Bootstrap.GetClient(),
		Cluster: ec.Refs.Cluster,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-control-plane")...)

	framework.WaitForClusterToProvision(ctx, framework.WaitForClusterToProvisionInput{
		Cluster: ec.Refs.Cluster,
		Getter:  e2eCtx.Environment.Bootstrap.GetClient(),
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-cluster-provisioned")...)

	framework.WaitForOneKubeadmControlPlaneMachineToExist(ctx, framework.WaitForOneKubeadmControlPlaneMachineToExistInput{
		Lister:       e2eCtx.Environment.Bootstrap.GetClient(),
		Cluster:      ec.Refs.Cluster,
		ControlPlane: controlPlane,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-control-plane-machine")...)

	framework.WaitForControlPlaneAndMachinesReady(ctx, framework.WaitForControlPlaneAndMachinesReadyInput{
		Cluster:      ec.Refs.Cluster,
		GetLister:    e2eCtx.Environment.Bootstrap.GetClient(),
		ControlPlane: controlPlane,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-control-plane-and-machines-ready")...)

	return ec
}
