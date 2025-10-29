package frmwrk

import (
	"context"
	"os"
	"path"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck

	"github.com/drone/envsubst/v2"
	"sigs.k8s.io/cluster-api/test/framework"
)

func createE2ECluster(ctx context.Context, e2eCtx *E2EContext, cfg ClusterConfig) *E2ECluster {
	ec := e2eCtx.NewE2ECluster(cfg)

	ec.SetupMetalStackPreconditions(ctx)
	ec.SetupNamespace(ctx)
	ec.GenerateAndApplyClusterTemplate(ctx)

	By("Wait for cluster")
	controlPlane := framework.GetKubeadmControlPlaneByCluster(ctx, framework.GetKubeadmControlPlaneByClusterInput{
		Lister:      e2eCtx.Environment.Bootstrap.GetClient(),
		ClusterName: ec.Refs.Cluster.Name,
		Namespace:   ec.Refs.Cluster.Namespace,
	})

	framework.DiscoveryAndWaitForCluster(ctx, framework.DiscoveryAndWaitForClusterInput{
		Getter:    e2eCtx.Environment.Bootstrap.GetClient(),
		Namespace: ec.Refs.Cluster.Namespace,
		Name:      ec.Refs.Cluster.Name,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-cluster")...)

	Expect(controlPlane).To(Not(BeNil()))

	By("Wait for CNI and CCM")
	targetTemplate, err := os.ReadFile(path.Join(e2eCtx.Environment.artifactsPath, "config", "target", "base.yaml"))
	Expect(err).ToNot(HaveOccurred())

	vars := ec.Variables()
	targetResources, err := envsubst.Eval(string(targetTemplate), func(varName string) string {
		return vars[varName]
	})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() error {
		return ec.Refs.Workload.CreateOrUpdate(ctx, []byte(targetResources))
	}, "10m", "15s").Should(Succeed()) // currently this long delay is required as machines might not be ready yet

	By("Wait for kubeadm control plane")
	framework.DiscoveryAndWaitForControlPlaneInitialized(ctx, framework.DiscoveryAndWaitForControlPlaneInitializedInput{
		Lister:  e2eCtx.Environment.Bootstrap.GetClient(),
		Cluster: ec.Refs.Cluster,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-control-plane")...)

	framework.WaitForClusterToProvision(ctx, framework.WaitForClusterToProvisionInput{
		Cluster: ec.Refs.Cluster,
		Getter:  e2eCtx.Environment.Bootstrap.GetClient(),
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-cluster-provisioned")...)
	return ec
}
