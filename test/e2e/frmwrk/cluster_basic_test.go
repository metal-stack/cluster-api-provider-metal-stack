package frmwrk

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
)

var _ = Describe("Basic Cluster", Ordered, Label("basic"), func() {
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

			It("create new cluster", Label("create"), func() {
				ec = createE2ECluster(ctx, e2eCtx, ClusterConfig{
					SpecName:                 "basic-cluster-creation-" + v,
					NamespaceName:            fmt.Sprintf("e2e-basic-cluster-creation-%d", i),
					ClusterName:              fmt.Sprintf("basic-%d", i),
					KubernetesVersion:        v,
					ControlPlaneMachineImage: os.Getenv("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					ControlPlaneMachineCount: 1,
					WorkerMachineImage:       os.Getenv("E2E_WORKER_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					WorkerMachineCount:       1,
				})
				Expect(ec).ToNot(BeNil())
			})

			It("move from bootstrap to workload cluster", Label("move"), func() {
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
					FromKubeconfigPath:   ec.E2EContext.Environment.Bootstrap.GetKubeconfigPath(),
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

			It("move from workload to bootstrap cluster", Label("move"), func() {
				Expect(ec).NotTo(BeNil(), "e2e cluster required")

				clusterctl.Move(ctx, clusterctl.MoveInput{
					LogFolder:            path.Join(ec.E2EContext.Environment.artifactsPath, "clusters", ec.ClusterName, "move-back"),
					ClusterctlConfigPath: ec.E2EContext.Environment.ClusterctlConfigPath,
					FromKubeconfigPath:   ec.Refs.Workload.GetKubeconfigPath(),
					ToKubeconfigPath:     ec.E2EContext.Environment.Bootstrap.GetKubeconfigPath(),
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

			It("delete cluster", Label("delete"), func() {
				ec.Teardown(ctx)
			})
		})
	}
})
