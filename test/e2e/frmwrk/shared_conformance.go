package frmwrk

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mholt/archiver/v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (ec *E2ECluster) ValidateConformance() {
	ec.E2EContext.Environment.Bootstrap.GetClientSet().CoreV1().Nodes()

	// if tm.Config().OutputDirectoryPath() == "" {
	// 	t.Skip(color.BlueString("output directory path is not set"))
	// }

	var (
		cluster = ec.Refs.Workload
		c       = cluster.GetClientSet()

		archiveName = fmt.Sprintf("conformance-%s-%s.tar.gz", ec.NamespaceName, ec.ClusterName)
		archivePath = path.Join(ec.E2EContext.Environment.artifactsPath, archiveName)
		fileList    = []string{
			"plugins/e2e/results/global/e2e.log",
			"plugins/e2e/results/global/junit_01.xml",
		}
		sonobuoy = ec.E2EContext.envOrVar("SONOBUOY_PATH")
	)

	nodes, err := c.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred(), "listing nodes for conformance test prerequisites failed")

	if len(nodes.Items) <= 1 {
		Skip("skipping conformance tests, cluster has less than 2 nodes")
	}

	os.Setenv("KUBECONFIG", cluster.GetKubeconfigPath())

	defer func() {
		os.Unsetenv("KUBECONFIG")
	}()

	cleanup := func() {
		_, err := c.CoreV1().Namespaces().Get(context.Background(), "sonobuoy", metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return
			}
			Expect(err).NotTo(HaveOccurred(), "checking for existing sonobuoy namespace failed")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, sonobuoy, "delete", "--wait")
		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECONFIG=%q", cluster.GetKubeconfigPath()))

		out, err := cmd.CombinedOutput()
		GinkgoWriter.Printf("output of command %q: %s", cmd.String(), out)
		Expect(err).NotTo(HaveOccurred(), "cleanup did not succeed, may cause unwanted aftereffects on other tests")
	}
	cleanup()
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Minute)
	defer cancel()

	By("start running conformance tests, execution will take a while...")

	cmd := exec.CommandContext(ctx, sonobuoy, "run", "--mode=certified-conformance", "--wait")
	out, err := cmd.CombinedOutput()
	GinkgoWriter.Printf("output of command %q: %s", cmd.String(), out)
	Expect(err).NotTo(HaveOccurred(), "running conformance tests did not succeed")

	cmd = exec.Command(sonobuoy, "status")
	out, err = cmd.CombinedOutput()
	GinkgoWriter.Printf("output of command %q: %s", cmd.String(), out)
	Expect(err).NotTo(HaveOccurred(), "checking conformance test status did not succeed")

	// wait for env to calm down and results are getting ready
	// or this will happen: output of command "/usr/local/bin/sonobuoy retrieve -f results.tar.gz": error retrieving results: unexpected EOF
	time.Sleep(1 * time.Minute)

	// the retrieve command is quite tricky as sonobuoy thinks that it is executed
	// from this file's directory and it can only handle relative paths for the -f parameter
	// therefore, we chdir before
	err = os.Chdir(ec.E2EContext.Environment.artifactsPath)
	Expect(err).NotTo(HaveOccurred())
	cmd = exec.Command(sonobuoy, "retrieve", "-f", archiveName) //nolint:gosec
	out, err = cmd.CombinedOutput()
	GinkgoWriter.Printf("output of command %q: %s", cmd.String(), out)
	Expect(err).NotTo(HaveOccurred())

	cmd = exec.Command(sonobuoy, "results", archivePath) //nolint:gosec
	out, err = cmd.CombinedOutput()
	GinkgoWriter.Printf("output of command %q: %s", cmd.String(), out)
	Expect(err).NotTo(HaveOccurred())

	Expect(out).NotTo(ContainElement("failed"), "conformance-tests did not succeed")

	tarball, err := os.OpenFile(archivePath, os.O_RDWR, 0644)
	Expect(err).NotTo(HaveOccurred())
	defer tarball.Close()

	gzReader, err := archiver.Gz{}.OpenReader(tarball)
	Expect(err).NotTo(HaveOccurred())

	err = archiver.Tar{}.Extract(context.Background(), gzReader, fileList, func(ctx context.Context, f archiver.File) error {
		in, err := f.Open()
		if err != nil {
			return err
		}
		defer in.Close()

		dst := path.Join(ec.E2EContext.Environment.artifactsPath, f.Name())
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = io.Copy(out, in)
		if err != nil {
			return err
		}
		return out.Close()
	})
	Expect(err).NotTo(HaveOccurred())
}
