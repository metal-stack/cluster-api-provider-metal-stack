/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"log/slog"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	fwv2 "github.com/metal-stack/firewall-controller-manager/api/v2"
	"github.com/metal-stack/metal-go/api/client/firewall"
	"github.com/metal-stack/metal-go/api/models"
	metalgoclient "github.com/metal-stack/metal-go/test/client"
)

var _ = Describe("MetalStackFirewall Controller", func() {
	const resourceName = "test-cluster"

	var (
		ctx                  context.Context
		cancel               func()
		cluster              *clusterv1.Cluster
		infraCluster         *v1alpha1.MetalStackCluster
		fwDeploy             *v1alpha1.MetalStackFirewallDeployment
		fwTemplate           *v1alpha1.MetalStackFirewallTemplate
		controllerReconciler *MetalStackFirewallDeploymentReconciler
		teardown             = func() {
			if fwDeploy == nil || fwTemplate == nil || cluster == nil || infraCluster == nil {
				return
			}
			err := k8sClient.Delete(ctx, cluster)
			Expect(err).To(Or(
				Not(HaveOccurred()),
				Satisfy(apierrors.IsNotFound)))

			err = k8sClient.Delete(ctx, infraCluster)
			Expect(err).To(Or(
				Not(HaveOccurred()),
				Satisfy(apierrors.IsNotFound)))

			err = k8sClient.Delete(ctx, fwDeploy)
			Expect(err).To(Or(
				Not(HaveOccurred()),
				Satisfy(apierrors.IsNotFound)))

			err = k8sClient.Delete(ctx, fwTemplate)
			Expect(err).To(Or(
				Not(HaveOccurred()),
				Satisfy(apierrors.IsNotFound)))
		}
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(suiteCtx)
		fwDeploy = &v1alpha1.MetalStackFirewallDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      resourceName,
			},
			Spec: v1alpha1.MetalStackFirewallDeploymentSpec{
				AutoUpdate: &v1alpha1.MetalStackFirewallAutoUpdate{
					MachineImage: false,
				},
				FirewallTemplateRef: v1alpha1.MetalStackFirewallTemplateRef{
					Name: resourceName,
				},
			},
		}
		fwTemplate = &v1alpha1.MetalStackFirewallTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      resourceName,
			},
			Spec: fwv2.FirewallSpec{
				Size:           "some-size",
				Image:          "some-image",
				Partition:      "some-partition",
				Project:        "some-project",
				Networks:       []string{"interwebs"},
				InitialRuleSet: &fwv2.InitialRuleSet{},
			},
		}
		cluster = &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      resourceName,
			},
			Spec: clusterv1.ClusterSpec{
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					Kind:     v1alpha1.MetalStackClusterKind,
					Name:     resourceName,
					APIGroup: v1alpha1.GroupVersion.Group,
				},
			},
		}
		infraCluster = &v1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      resourceName,
			},
			Spec: v1alpha1.MetalStackClusterSpec{
				FirewallDeploymentRef: &v1alpha1.MetalStackFirewallDeploymentRef{
					Name: resourceName,
				},
				NodeNetworkID: ptr.To("some-network-id"),
				Partition:     "some-partition",
			},
		}

		ctx = ctrllog.IntoContext(ctx, logr.FromSlogHandler(slog.NewTextHandler(GinkgoWriter, nil)))

		controllerReconciler = &MetalStackFirewallDeploymentReconciler{
			Client: k8sClient,
		}
	})

	BeforeEach(func() {
		teardown()
		Expect(k8sClient.Create(ctx, fwTemplate)).To(Succeed())
		Expect(fwDeploy).ToNot(BeNil())
		Expect(fwTemplate).ToNot(BeNil())
	})

	AfterEach(func() {
		teardown()
		cancel()
	})

	Context("with incomplete owner chain", func() {
		When("no owner references are set", func() {
			It("should skip reconciles", func() {
				fwDeploy.OwnerReferences = nil
				Expect(k8sClient.Create(ctx, fwDeploy)).To(Succeed())

				typeNamespacedName := types.NamespacedName{
					Name:      fwDeploy.Name,
					Namespace: fwDeploy.Namespace,
				}
				const firstGen = int64(1)

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
				Expect(fwDeploy.Generation).To(Equal(firstGen))

				By("idempotence", func() {
					_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespacedName,
					})
					Expect(err).NotTo(HaveOccurred())

					Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
					Expect(fwDeploy.Generation).To(Equal(firstGen))
				})
			})
		})

		When("only infra cluster has an owner reference", func() {
			It("should skip reconciles", func() {
				Expect(k8sClient.Create(ctx, fwDeploy)).To(Succeed())

				By("creating the cluster and infra cluster resources")
				Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

				infraCluster.Spec.FirewallDeploymentRef.Name = fwDeploy.Name
				infraCluster.OwnerReferences = []metav1.OwnerReference{
					*metav1.NewControllerRef(cluster, clusterv1.GroupVersion.WithKind(clusterv1.ClusterKind)),
				}
				Expect(k8sClient.Create(ctx, infraCluster)).To(Succeed())

				fwDeploy.OwnerReferences = nil
				Expect(k8sClient.Update(ctx, fwDeploy)).To(Succeed())

				typeNamespacedName := types.NamespacedName{
					Name:      fwDeploy.Name,
					Namespace: fwDeploy.Namespace,
				}
				const firstGen = int64(1)

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
				Expect(fwDeploy.Generation).To(Equal(firstGen))

				By("idempotence", func() {
					_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespacedName,
					})
					Expect(err).NotTo(HaveOccurred())

					Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
					Expect(fwDeploy.Generation).To(Equal(firstGen))
				})
			})
		})

		When("only firewall deployment has an owner reference", func() {
			It("should skip reconciles", func() {
				Expect(k8sClient.Create(ctx, fwDeploy)).To(Succeed())

				By("creating the cluster and infra cluster resources")
				Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

				infraCluster.Spec.FirewallDeploymentRef.Name = fwDeploy.Name
				infraCluster.OwnerReferences = nil
				Expect(k8sClient.Create(ctx, infraCluster)).To(Succeed())

				fwDeploy.OwnerReferences = []metav1.OwnerReference{
					*metav1.NewControllerRef(infraCluster, v1alpha1.GroupVersion.WithKind(v1alpha1.MetalStackClusterKind)),
				}
				Expect(k8sClient.Update(ctx, fwDeploy)).To(Succeed())

				typeNamespacedName := types.NamespacedName{
					Name:      fwDeploy.Name,
					Namespace: fwDeploy.Namespace,
				}
				const firstGen = int64(1)

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
				Expect(fwDeploy.Generation).To(Equal(firstGen))

				By("idempotence", func() {
					_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespacedName,
					})
					Expect(err).NotTo(HaveOccurred())

					Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
					Expect(fwDeploy.Generation).To(Equal(firstGen))
				})
			})
		})
	})

	Context("when reconcile is paused", func() {
		It("should skip reconciles due to cluster.spec.paused", func() {
			Expect(k8sClient.Create(ctx, fwDeploy)).To(Succeed())

			By("creating the cluster and infra cluster resources")
			cluster.Spec.Paused = ptr.To(true)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			infraCluster.Spec.FirewallDeploymentRef.Name = fwDeploy.Name
			infraCluster.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(cluster, clusterv1.GroupVersion.WithKind(clusterv1.ClusterKind)),
			}
			Expect(k8sClient.Create(ctx, infraCluster)).To(Succeed())

			fwDeploy.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(infraCluster, v1alpha1.GroupVersion.WithKind(v1alpha1.MetalStackClusterKind)),
			}
			Expect(k8sClient.Update(ctx, fwDeploy)).To(Succeed())

			typeNamespacedName := types.NamespacedName{
				Name:      fwDeploy.Name,
				Namespace: fwDeploy.Namespace,
			}
			var firstGen = fwDeploy.Generation

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred(), "reconcile should not error even if paused")

			Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
			Expect(fwDeploy.Generation).To(Equal(firstGen))

			By("idempotence", func() {
				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
				Expect(fwDeploy.Generation).To(Equal(firstGen))
			})
		})

		It("should skip reconciles due to infra cluster pause annotation", func() {
			Expect(k8sClient.Create(ctx, fwDeploy)).To(Succeed())

			By("creating the cluster and infra cluster resources")
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			infraCluster.Spec.FirewallDeploymentRef.Name = fwDeploy.Name
			infraCluster.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(cluster, clusterv1.GroupVersion.WithKind(clusterv1.ClusterKind)),
			}
			infraCluster.Annotations = map[string]string{
				clusterv1.PausedAnnotation: "true",
			}
			Expect(k8sClient.Create(ctx, infraCluster)).To(Succeed())

			fwDeploy.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(infraCluster, v1alpha1.GroupVersion.WithKind(v1alpha1.MetalStackClusterKind)),
			}
			Expect(k8sClient.Update(ctx, fwDeploy)).To(Succeed())

			typeNamespacedName := types.NamespacedName{
				Name:      fwDeploy.Name,
				Namespace: fwDeploy.Namespace,
			}
			var firstGen = fwDeploy.Generation

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
			Expect(fwDeploy.Generation).To(Equal(firstGen))

			By("idempotence", func() {
				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
				Expect(fwDeploy.Generation).To(Equal(firstGen))
			})
		})

		It("should skip reconciles due to firewall deployment pause annotation", func() {
			fwDeploy.Annotations = map[string]string{
				clusterv1.PausedAnnotation: "true",
			}
			Expect(k8sClient.Create(ctx, fwDeploy)).To(Succeed())

			By("creating the cluster and infra cluster resources")
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			infraCluster.Spec.FirewallDeploymentRef.Name = fwDeploy.Name
			infraCluster.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(cluster, clusterv1.GroupVersion.WithKind(clusterv1.ClusterKind)),
			}
			Expect(k8sClient.Create(ctx, infraCluster)).To(Succeed())

			fwDeploy.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(infraCluster, v1alpha1.GroupVersion.WithKind(v1alpha1.MetalStackClusterKind)),
			}
			Expect(k8sClient.Update(ctx, fwDeploy)).To(Succeed())

			typeNamespacedName := types.NamespacedName{
				Name:      fwDeploy.Name,
				Namespace: fwDeploy.Namespace,
			}
			var firstGen = fwDeploy.Generation

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
			Expect(fwDeploy.Generation).To(Equal(firstGen))

			By("idempotence", func() {
				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
				Expect(fwDeploy.Generation).To(Equal(firstGen))
			})
		})
	})

	Context("reconciliation", func() {
		It("should create managed resource ref", func() {
			Expect(k8sClient.Create(ctx, fwDeploy)).To(Succeed())

			By("creating the cluster and infra cluster resources")
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			infraCluster.Spec.FirewallDeploymentRef.Name = fwDeploy.Name
			infraCluster.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(cluster, clusterv1.GroupVersion.WithKind(clusterv1.ClusterKind)),
			}
			Expect(k8sClient.Create(ctx, infraCluster)).To(Succeed())

			fwDeploy.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(infraCluster, v1alpha1.GroupVersion.WithKind(v1alpha1.MetalStackClusterKind)),
			}
			Expect(k8sClient.Update(ctx, fwDeploy)).To(Succeed())

			typeNamespacedName := types.NamespacedName{
				Name:      fwDeploy.Name,
				Namespace: fwDeploy.Namespace,
			}

			// TODO: this will change once firewall deployments are created
			controllerReconciler.MetalClient, _ = metalgoclient.NewMetalMockClient(testingT, &metalgoclient.MetalMockFns{
				Firewall: func(m *mock.Mock) {
					m.On("AllocateFirewall", mock.Anything, nil).Return(&firewall.AllocateFirewallOK{
						Payload: &models.V1FirewallResponse{
							ID: ptr.To("firewall-id"),
						},
					}, nil)
				},
			})

			By("first reconcile should create managed resource ref")
			Eventually(func(o Gomega) {
				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				o.Expect(err).NotTo(HaveOccurred())

				o.Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())

				// TODO: this will change once firewall deployments are created
				o.Expect(fwDeploy.Spec.ManagedResourceRef).NotTo(BeNil())

			}).Should(Succeed(), "timed out waiting for managed resource ref to be set")
			Expect(fwDeploy.Spec.ManagedResourceRef.Name).To(Equal("metal://some-partition/firewall-id"))

			finishedGen := fwDeploy.Generation

			By("idempotence", func() {
				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, fwDeploy)).To(Succeed())
				Expect(fwDeploy.Generation).To(Equal(finishedGen))
			})
		})
	})
})
