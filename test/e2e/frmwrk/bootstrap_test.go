package frmwrk

import (
	"context"
	"fmt"
	"os"
	"path"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"strings"

	"github.com/drone/envsubst/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
)

var _ = Describe("Basic Cluster Creation", Ordered, func() {

	BeforeAll(func() {
		e2eCtx = NewE2EContext()
		e2eCtx.ProvideBootstrapCluster()
		e2eCtx.CreateClusterctlConfig(context.TODO())
		e2eCtx.InitManagementCluster(context.TODO())

		DeferCleanup(e2eCtx.Teardown, context.TODO())
	})

	kubernetesVersions := strings.Split(os.Getenv("E2E_KUBERNETES_VERSIONS"), ",")
	Expect(kubernetesVersions).ToNot(BeEmpty(), "E2E_KUBERNETES_VERSIONS must be set")

	for i, v := range kubernetesVersions {
		Context(fmt.Sprintf("with kubernetes %s", v), Ordered, func() {
			var (
				ec  *E2ECluster
				ctx context.Context
			)

			BeforeEach(func() {
				ctx = context.Background()
			})

			It("create new cluster", func() {
				ec = createE2ECluster(ctx, e2eCtx, ClusterConfig{
					SpecName:                 "basic-cluster-creation-" + v,
					NamespaceName:            fmt.Sprintf("e2e-basic-cluster-creation-%d", i),
					ClusterName:              fmt.Sprintf("simple-%d", i),
					KubernetesVersion:        v,
					ControlPlaneMachineImage: os.Getenv("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					ControlPlaneMachineCount: 1,
					WorkerMachineImage:       os.Getenv("E2E_WORKER_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					WorkerMachineCount:       1,
				})
				Expect(ec).ToNot(BeNil())
			})

			It("move from bootstrap to workload cluster", func() {
				Expect(ec).NotTo(BeNil(), "e2e cluster required")

				clusterctl.InitManagementClusterAndWatchControllerLogs(ctx, clusterctl.InitManagementClusterAndWatchControllerLogsInput{
					ClusterProxy:            ec.Refs.Workload,
					ClusterctlConfigPath:    e2eCtx.Environment.ClusterctlConfigPath,
					InfrastructureProviders: e2eCtx.E2EConfig.InfrastructureProviders(),
					LogFolder:               path.Join(e2eCtx.Environment.artifactsPath, "clusters", ec.ClusterName, "init"),
				})

				clusterctl.Move(ctx, clusterctl.MoveInput{
					LogFolder:            path.Join(ec.E2EContext.Environment.artifactsPath, "clusters", ec.ClusterName, "move-to"),
					ClusterctlConfigPath: ec.E2EContext.Environment.ClusterctlConfigPath,
					FromKubeconfigPath:   ec.E2EContext.Environment.kubeconfigPath,
					ToKubeconfigPath:     ec.Refs.Workload.GetKubeconfigPath(),
					Namespace:            ec.NamespaceName,
				})

				cluster := &clusterv1.Cluster{}
				err := e2eCtx.Environment.Bootstrap.GetClient().Get(ctx, client.ObjectKey{
					Namespace: ec.NamespaceName,
					Name:      ec.ClusterName,
				}, cluster)
				Expect(err).To(Satisfy(apierrors.IsNotFound), "cluster should have been moved")

				cluster = &clusterv1.Cluster{}
				err = ec.Refs.Workload.GetClient().Get(ctx, client.ObjectKey{
					Namespace: ec.NamespaceName,
					Name:      ec.ClusterName,
				}, cluster)
				Expect(err).ToNot(HaveOccurred(), "cluster should be present")
			})

			It("move from workload to bootstrap cluster", func() {
				Expect(ec).NotTo(BeNil(), "e2e cluster required")

				clusterctl.Move(ctx, clusterctl.MoveInput{
					LogFolder:            path.Join(ec.E2EContext.Environment.artifactsPath, "clusters", ec.ClusterName, "move-back"),
					ClusterctlConfigPath: ec.E2EContext.Environment.ClusterctlConfigPath,
					FromKubeconfigPath:   ec.Refs.Workload.GetKubeconfigPath(),
					ToKubeconfigPath:     ec.E2EContext.Environment.kubeconfigPath,
					Namespace:            ec.NamespaceName,
				})

				cluster := &clusterv1.Cluster{}
				err := ec.Refs.Workload.GetClient().Get(ctx, client.ObjectKey{
					Namespace: ec.NamespaceName,
					Name:      ec.ClusterName,
				}, cluster)
				Expect(err).To(Satisfy(apierrors.IsNotFound), "cluster should have been moved")

				cluster = &clusterv1.Cluster{}
				err = e2eCtx.Environment.Bootstrap.GetClient().Get(ctx, client.ObjectKey{
					Namespace: ec.NamespaceName,
					Name:      ec.ClusterName,
				}, cluster)
				Expect(err).ToNot(HaveOccurred(), "cluster should be present")
			})

			It("delete cluster", func() {
				ec.Teardown(ctx)
				// TODO: expect resources to be gone
			})
		})
	}

})

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
