package frmwrk

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/drone/envsubst/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/cluster-api/test/framework"
)

var _ = Describe("Basic Cluster Creation", Ordered, func() {
	var (
		ec *E2ECluster
	)

	BeforeAll(func() {
		e2eCtx = NewE2EContext()
		e2eCtx.ProvideBootstrapCluster()
		e2eCtx.CreateClusterctlConfig(context.TODO())
		e2eCtx.InitManagementCluster(context.TODO())

		DeferCleanup(e2eCtx.Teardown, context.TODO())
	})

	BeforeEach(func() {
		ec = nil
	})

	kubernetesVersions := strings.Split(os.Getenv("E2E_KUBERNETES_VERSIONS"), ",")
	Expect(kubernetesVersions).ToNot(BeEmpty(), "E2E_KUBERNETES_VERSIONS must be set")

	for i, v := range kubernetesVersions {
		It(fmt.Sprintf("create new cluster with kubernetes %s", v), func() {
			ctx := context.Background()

			ec = createE2ECluster(ctx, e2eCtx, ClusterConfig{
				SpecName:                 "basic-cluster-creation-" + v,
				NamespaceName:            fmt.Sprintf("e2e-basic-cluster-creation-%d", i),
				ClusterName:              "simple",
				KubernetesVersion:        v,
				ControlPlaneMachineImage: os.Getenv("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
				ControlPlaneMachineCount: 1,
				WorkerMachineImage:       os.Getenv("E2E_WORKER_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
				WorkerMachineCount:       1,
			})
			Expect(ec).ToNot(BeNil())
		})
	}
})

func createE2ECluster(ctx context.Context, e2eCtx *E2EContext, cfg ClusterConfig) *E2ECluster {
	ec := e2eCtx.NewE2ECluster(cfg)
	DeferCleanup(ec.Teardown, ctx)

	ec.SetupMetalStackPreconditions(ctx)
	ec.SetupNamespace(ctx)
	ec.GenerateAndApplyClusterTemplate(ctx)

	By("Wait for cluster")
	controlPlane := framework.GetKubeadmControlPlaneByCluster(ctx, framework.GetKubeadmControlPlaneByClusterInput{
		Lister:      e2eCtx.Environment.Bootstrap.GetClient(),
		ClusterName: ec.Refs.Cluster.Name,
		Namespace:   ec.Refs.Cluster.Namespace,
	})

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
	}, "5m", "15s").Should(Succeed())

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
