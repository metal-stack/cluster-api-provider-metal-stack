package frmwrk

import (
	"context"
	"testing"

	"github.com/onsi/ginkgo/v2"
	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	e2eCtx *E2EContext
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)

	ctrl.SetLogger(klog.Background())
	ginkgo.RunSpecs(t, "capms-e2e")
}

var _ = ginkgo.BeforeSuite(func() {
	e2eCtx = NewE2EContext()
	e2eCtx.ProvideBootstrapCluster()
	e2eCtx.CreateClusterctlConfig(context.TODO())
	e2eCtx.InitManagementCluster(context.TODO())
	e2eCtx.TeardownMetalStackProject(context.TODO())
})

var _ = ginkgo.AfterSuite(func() {
	if ginkgo.CurrentSpecReport().Failed() {
		// on failure, we skip cleanup to investigate
		return
	}
	if e2eCtx != nil {
		filter, err := ginkgotypes.ParseLabelFilter(ginkgo.GinkgoLabelFilter())
		Expect(err).ToNot(HaveOccurred(), "failed to parse ginkgo label filter")

		if !filter([]string{"teardown"}) {
			return
		}
		e2eCtx.Teardown(context.TODO())
	}
})
