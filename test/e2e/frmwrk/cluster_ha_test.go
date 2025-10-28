package frmwrk

import (
	"context"
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("High Availability Cluster", Ordered, func() {

	BeforeAll(func() {
		e2eCtx = NewE2EContext()
		e2eCtx.ProvideBootstrapCluster()
		e2eCtx.CreateClusterctlConfig(context.TODO())
		e2eCtx.InitManagementCluster(context.TODO())
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
					SpecName:                 "ha-cluster-creation-" + v,
					NamespaceName:            fmt.Sprintf("e2e-ha-cluster-creation-%d", i),
					ClusterName:              fmt.Sprintf("ha-%d", i),
					KubernetesVersion:        v,
					ControlPlaneMachineImage: os.Getenv("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					ControlPlaneMachineCount: 3,
					WorkerMachineImage:       os.Getenv("E2E_WORKER_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					WorkerMachineCount:       0,
				})
				Expect(ec).ToNot(BeNil())
			})

			It("delete cluster", func() {
				ec.Teardown(ctx)
			})
		})
	}

	It("teardown management cluster", func() {
		e2eCtx.Teardown(context.Background())
	})
})
