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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	metalip "github.com/metal-stack/metal-go/api/client/ip"
	metalnetwork "github.com/metal-stack/metal-go/api/client/network"
	"github.com/metal-stack/metal-go/api/models"
	metalgoclient "github.com/metal-stack/metal-go/test/client"
	"github.com/metal-stack/metal-lib/pkg/testcommon"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("MetalStackCluster Controller", func() {
	const resourcePrefix = "test-resource-"

	var (
		ctx                  context.Context
		cancel               func()
		resource             *v1alpha1.MetalStackCluster
		controllerReconciler *MetalStackClusterReconciler
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(suiteCtx)
		resource = &v1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    "default",
				GenerateName: resourcePrefix,
			},
		}

		controllerReconciler = &MetalStackClusterReconciler{
			Client: k8sClient,
		}
	})

	AfterEach(func() {
		cancel()
	})

	Context("without owning cluster resource", func() {
		BeforeEach(func() {
			resource.ObjectMeta.OwnerReferences = nil
		})

		It("should skip reconciles", func() {
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			typeNamespacedName := types.NamespacedName{
				Name:      resource.Name,
				Namespace: "default",
			}
			const firstGen = int64(1)

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Generation).To(Equal(firstGen))

			By("idempotence", func() {
				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
				Expect(resource.Generation).To(Equal(firstGen))
			})
		})
	})

	Context("when cluster is paused", func() {
		BeforeEach(func() {
			resource.Spec = v1alpha1.MetalStackClusterSpec{
				ControlPlaneEndpoint: v1alpha1.APIEndpoint{},
				ProjectID:            "test-project",
				NodeNetworkID:        "node-network-id",
				ControlPlaneIP:       nil,
				Partition:            "test-partition",
			}
		})

		It("should skip reconciles", func() {
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("creating the cluster resource and setting the owner reference")
			owner := &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "owner-",
					Namespace:    "default",
				},
				Spec: clusterv1beta1.ClusterSpec{
					Paused: true,
				},
			}
			Expect(k8sClient.Create(ctx, owner)).To(Succeed())

			resource.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(owner, clusterv1beta1.GroupVersion.WithKind("Cluster")),
			}
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())

			typeNamespacedName := types.NamespacedName{
				Name:      resource.Name,
				Namespace: "default",
			}
			const firstGen = int64(1)

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Generation).To(Equal(firstGen))

			Expect(resource.Status.Conditions).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Type":   Equal(clusterv1.PausedV1Beta2Condition),
				"Status": Equal(corev1.ConditionTrue),
			})))

			By("idempotence", func() {
				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
				Expect(resource.Generation).To(Equal(firstGen))

				Expect(resource.Status.Conditions).To(ContainElement(MatchFields(IgnoreExtras, Fields{
					"Type":   Equal(clusterv1.PausedV1Beta2Condition),
					"Status": Equal(corev1.ConditionTrue),
				})))
			})
		})
	})

	Context("reconciliation with auto-acquiring dependent resources", func() {
		BeforeEach(func() {
			resource.Spec = v1alpha1.MetalStackClusterSpec{
				ControlPlaneEndpoint: v1alpha1.APIEndpoint{},
				ProjectID:            "test-project",
				NodeNetworkID:        "node-network-id",
				ControlPlaneIP:       nil,
				Partition:            "test-partition",
			}
		})

		It("should successfully reconcile", func() {
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			By("creating the cluster resource and setting the owner reference")
			owner := &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "owner-",
					Namespace:    "default",
				},
			}
			Expect(k8sClient.Create(ctx, owner)).To(Succeed())

			resource.OwnerReferences = []metav1.OwnerReference{
				*metav1.NewControllerRef(owner, clusterv1beta1.GroupVersion.WithKind("Cluster")),
			}
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())

			By("reconciling the resource")

			typeNamespacedName := types.NamespacedName{
				Name:      resource.Name,
				Namespace: "default",
			}

			controllerReconciler.MetalClient, _ = metalgoclient.NewMetalMockClient(testingT, &metalgoclient.MetalMockFns{
				IP: func(m *mock.Mock) {
					m.On("AllocateIP", testcommon.MatchIgnoreContext(testingT, metalip.NewAllocateIPParams().WithBody(&models.V1IPAllocateRequest{
						Tags: []string{
							"cluster.metal-stack.io/id=" + string(resource.UID),
							"metal-stack.infrastructure.cluster.x-k8s.io/purpose=control-plane",
						},
						Name:        resource.Name + "-control-plane",
						Description: resource.Namespace + "/" + resource.Name + " control plane ip",
						Networkid:   ptr.To("internet"),
						Projectid:   ptr.To("test-project"),
						Type:        ptr.To("ephemeral"),
					})), nil).Return(&metalip.AllocateIPCreated{
						Payload: &models.V1IPResponse{
							Ipaddress: ptr.To("192.168.42.1"),
						},
					}, nil)
				},
				Network: func(m *mock.Mock) {
					m.On("FindNetworks", testcommon.MatchIgnoreContext(testingT, metalnetwork.NewFindNetworksParams().WithBody(&models.V1NetworkFindRequest{
						Labels: map[string]string{
							"network.metal-stack.io/default": "",
						},
					})), nil).Return(&metalnetwork.FindNetworksOK{
						Payload: []*models.V1NetworkResponse{
							{
								ID: ptr.To("internet"),
							},
						},
					}, nil)
				},
			})

			// during first reconcile the finalizer gets added only
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Finalizers).To(ContainElement(v1alpha1.ClusterFinalizer))

			// second reconcile

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// third reconcile

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			Expect(resource.Status.Conditions).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Type":   Equal(v1alpha1.ClusterControlPlaneIPEnsured),
				"Status": Equal(corev1.ConditionTrue),
			})))
			Expect(resource.Status.Ready).To(BeTrue())
			Expect(resource.Spec.ControlPlaneEndpoint).To(Equal(v1alpha1.APIEndpoint{
				Host: "192.168.42.1",
				Port: 443,
			}))
		})
	})

	Context("reconciliation when external resources are provided", func() {
		var (
			nodeNetworkID  string
			controlPlaneIP string
		)
		BeforeEach(func() {
			By("creating a cluster resource and setting an ownership")

			nodeNetworkID = "test-network"
			controlPlaneIP = "192.168.42.1"

			resource.Spec = v1alpha1.MetalStackClusterSpec{
				ControlPlaneEndpoint: v1alpha1.APIEndpoint{},
				ProjectID:            "test-project",
				NodeNetworkID:        nodeNetworkID,
				ControlPlaneIP:       &controlPlaneIP,
				Partition:            "test-partition",
			}
		})

		When("creating a resource and setting an ownership", func() {
			It("should successfully reconcile", func() {
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("creating the cluster resource and setting the owner reference")
				owner := &clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "owner-",
						Namespace:    "default",
					},
				}
				Expect(k8sClient.Create(ctx, owner)).To(Succeed())

				resource.OwnerReferences = []metav1.OwnerReference{
					*metav1.NewControllerRef(owner, clusterv1beta1.GroupVersion.WithKind("Cluster")),
				}
				Expect(k8sClient.Update(ctx, resource)).To(Succeed())

				By("reconciling the resource")

				typeNamespacedName := types.NamespacedName{
					Name:      resource.Name,
					Namespace: "default",
				}

				controllerReconciler.MetalClient, _ = metalgoclient.NewMetalMockClient(testingT, &metalgoclient.MetalMockFns{})

				Eventually(func() clusterv1beta1.Conditions {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespacedName,
					})
					Expect(err).ToNot(HaveOccurred())

					Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).ToNot(HaveOccurred())

					return resource.Status.Conditions
				}, "20s").Should(ContainElements(
					MatchFields(IgnoreExtras, Fields{
						"Type":   Equal(v1alpha1.ClusterControlPlaneIPEnsured),
						"Status": Equal(corev1.ConditionTrue),
					}),
				))

				Expect(resource.Status.Ready).To(BeTrue())
			})
		})

		When("referenced resources do not exist", func() {
			It("should succeed reconciling even if linked resources do not exist", func() {
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				By("creating the cluster resource and setting the owner reference")
				owner := &clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "owner-",
						Namespace:    "default",
					},
				}
				Expect(k8sClient.Create(ctx, owner)).To(Succeed())

				resource.OwnerReferences = []metav1.OwnerReference{
					*metav1.NewControllerRef(owner, clusterv1beta1.GroupVersion.WithKind("Cluster")),
				}
				Expect(k8sClient.Update(ctx, resource)).To(Succeed())

				By("reconciling the resource")

				typeNamespacedName := types.NamespacedName{
					Name:      resource.Name,
					Namespace: "default",
				}

				Eventually(func() clusterv1beta1.Conditions {
					_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespacedName,
					})
					Expect(err).ToNot(HaveOccurred())

					Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).ToNot(HaveOccurred())

					return resource.Status.Conditions
				}, "20s").Should(ContainElements(
					MatchFields(IgnoreExtras, Fields{
						"Type":   Equal(v1alpha1.ClusterControlPlaneIPEnsured),
						"Status": Equal(corev1.ConditionTrue),
					}),
				))

				Expect(resource.Status.Ready).To(BeTrue())
			})
		})
	})
})
