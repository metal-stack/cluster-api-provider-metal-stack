//go:build integration
// +build integration

package frmwrk

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
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
