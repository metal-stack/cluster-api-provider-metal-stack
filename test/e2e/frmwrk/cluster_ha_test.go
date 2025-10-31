package frmwrk

import (
	"context"
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("High Availability Cluster", Ordered, Label("ha"), func() {

	kubernetesVersions := strings.Split(os.Getenv("E2E_KUBERNETES_VERSIONS"), ",")
	Expect(kubernetesVersions).ToNot(BeEmpty(), "E2E_KUBERNETES_VERSIONS must be set")

	for i, v := range kubernetesVersions {
		Context(fmt.Sprintf("with kubernetes %s", v), Ordered, func() {
			var (
				ec   *E2ECluster
				ctx  context.Context
				done func()
			)

			BeforeEach(func() {
				ctx, done = context.WithCancel(context.Background())
			})

			AfterEach(func() {
				done()
			})

			It("create new cluster", Label("ha", "create"), func() {
				ec = createE2ECluster(ctx, e2eCtx, ClusterConfig{
					SpecName:                 "ha-cluster-creation-" + v,
					NamespaceName:            fmt.Sprintf("ha-%d", i),
					ClusterName:              "ha-cluster",
					KubernetesVersion:        v,
					ControlPlaneMachineImage: os.Getenv("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					ControlPlaneMachineCount: 3,
					WorkerMachineImage:       os.Getenv("E2E_WORKER_MACHINE_IMAGE_PREFIX") + strings.TrimPrefix(v, "v"),
					WorkerMachineCount:       0,
				})
				Expect(ec).ToNot(BeNil())
			})

			It("delete cluster", Label("ha", "teardown"), func() {
				if ec == nil {
					Skip("E2ECluster not initialized, skipping teardown")
				}
				ec.Teardown(ctx)
			})
		})
	}
})
