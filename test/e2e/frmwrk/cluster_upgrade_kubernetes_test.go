package frmwrk

import (
	"context"
	"fmt"
	"k8s.io/utils/ptr"
	"os"
	"strings"

	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
)

var _ = Describe("Upgrade Kubernetes Cluster Version", Label("upgrade"), func() {
	ctx := context.TODO()
	fromKubernetesVersion := strings.Split(os.Getenv("KUBERNETES_VERSION_UPGRADE_FROM"), ",")
	toKubernetesVersion := strings.Split(os.Getenv("KUBERNETES_VERSION_UPGRADE_TO"), ",")

	It(fmt.Sprintf("from %d to %d", fromKubernetesVersion, toKubernetesVersion), func() {
		cfg := e2eCtx.E2EConfig.DeepCopy()
		cfg.Variables["KUBERNETES_VERSION_UPGRADE_FROM"] = e2eCtx.envOrVar("KUBERNETES_VERSION_UPGRADE_FROM")
		cfg.Variables["KUBERNETES_VERSION_UPGRADE_TO"] = e2eCtx.envOrVar("KUBERNETES_VERSION_UPGRADE_TO")
		cfg.Variables["KUBETEST_CONFIGURATION"] = e2eCtx.envOrVar("KUBETEST_CONFIGURATION")

		clusterUpgrade(ctx, e2eCtx, func() ClusterUpgradeSpecInput {
			return ClusterUpgradeSpecInput{
				E2EConfig:                cfg,
				ClusterctlConfigPath:     e2eCtx.Environment.ClusterctlConfigPath,
				BootstrapClusterProxy:    e2eCtx.Environment.Bootstrap,
				ArtifactFolder:           e2eCtx.Environment.artifactsPath,
				SkipCleanup:              true,
				SkipConformanceTests:     true,
				ControlPlaneWaiters:      clusterctl.ControlPlaneWaiters{},
				ControlPlaneMachineCount: ptr.To[int64](1),
				WorkerMachineCount:       ptr.To[int64](1),
			}
		})
	})
})
