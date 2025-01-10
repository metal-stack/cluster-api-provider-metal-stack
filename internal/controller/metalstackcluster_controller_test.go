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
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrastructurev1alpha1 "github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	metalip "github.com/metal-stack/metal-go/api/client/ip"
	metalnetwork "github.com/metal-stack/metal-go/api/client/network"
	"github.com/metal-stack/metal-go/api/models"
	metalgoclient "github.com/metal-stack/metal-go/test/client"
	"github.com/metal-stack/metal-lib/pkg/testcommon"

	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("MetalStackCluster Controller", func() {
	const resourcePrefix = "test-resource-"

	var (
		ctx                  context.Context
		cancel               func()
		resource             *infrastructurev1alpha1.MetalStackCluster
		controllerReconciler *MetalStackClusterReconciler

		// typeNamespacedName = types.NamespacedName{
		// 	Name:      resourceName,
		// 	Namespace: "default",
		// }
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(suiteCtx)
		resource = &infrastructurev1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				// Name:      resourceName,
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

	Context("reconciliation with auto-acquiring dependent resources", func() {
		BeforeEach(func() {
			resource.Spec = infrastructurev1alpha1.MetalStackClusterSpec{
				ControlPlaneEndpoint: infrastructurev1alpha1.APIEndpoint{},
				ProjectID:            "test-project",
				NodeNetworkID:        nil,
				ControlPlaneIP:       nil,
				Partition:            "test-partition",
				Firewall: &infrastructurev1alpha1.Firewall{
					Size:               "v1-small-x86",
					Image:              "firewall-ubuntu-3.0",
					AdditionalNetworks: []string{"internet"},
				},
			}
		})

		FIt("should successfully reconcile", func() {
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
					findIPResponse := &metalip.FindIPsOK{}

					m.On("FindIPs", testcommon.MatchIgnoreContext(testingT, metalip.NewFindIPsParams().WithBody(&models.V1IPFindRequest{
						Projectid: "test-project",
						Tags: []string{
							"cluster.metal-stack.io/id=" + string(resource.UID),
							"metal-stack.infrastructure.cluster.x-k8s.io/purpose=control-plane",
						},
					})), nil).Return(findIPResponse, nil)

					m.On("AllocateIP", testcommon.MatchIgnoreContext(testingT, metalip.NewAllocateIPParams().WithBody(&models.V1IPAllocateRequest{
						Tags: []string{
							"cluster.metal-stack.io/id=" + string(resource.UID),
							"metal-stack.infrastructure.cluster.x-k8s.io/purpose=control-plane",
						},
						Name:        resource.Name + "-control-plane",
						Description: resource.Namespace + "/" + resource.Name + " control plane ip",
						Networkid:   ptr.To("internet"),
						Projectid:   ptr.To("test-project"),
						Type:        ptr.To("static"),
					})), nil).Run(func(args mock.Arguments) {
						findIPResponse = &metalip.FindIPsOK{
							Payload: []*models.V1IPResponse{
								{
									Ipaddress: ptr.To("192.168.42.1"),
								},
							},
						}
					}).Return(&metalip.AllocateIPCreated{
						Payload: &models.V1IPResponse{
							Ipaddress: ptr.To("192.168.42.1"),
						},
					}, nil)
				},
				Network: func(mock *mock.Mock) {
					mock.On("FindNetworks", testcommon.MatchIgnoreContext(testingT, metalnetwork.NewFindNetworksParams().WithBody(&models.V1NetworkFindRequest{
						Labels: map[string]string{
							"cluster.metal-stack.io/id": string(resource.UID),
						},
						Partitionid: "test-partition",
						Projectid:   "test-project",
					})), nil).Return(&metalnetwork.FindNetworksOK{}, nil).Once()

					mock.On("FindNetworks", testcommon.MatchIgnoreContext(testingT, metalnetwork.NewFindNetworksParams().WithBody(&models.V1NetworkFindRequest{
						Labels: map[string]string{
							"cluster.metal-stack.io/id": string(resource.UID),
						},
						Partitionid: "test-partition",
						Projectid:   "test-project",
					})), nil).Return(&metalnetwork.FindNetworksOK{
						Payload: []*models.V1NetworkResponse{{
							ID:       ptr.To("node-network-id"),
							Prefixes: []string{"192.168.42.0/24"},
						}},
					}, nil)

					mock.On("FindNetworks", testcommon.MatchIgnoreContext(testingT, metalnetwork.NewFindNetworksParams().WithBody(&models.V1NetworkFindRequest{
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

					mock.On("AllocateNetwork", testcommon.MatchIgnoreContext(testingT, metalnetwork.NewAllocateNetworkParams().WithBody(&models.V1NetworkAllocateRequest{
						Name:        resource.Name,
						Description: resource.Namespace + "/" + resource.Name,
						Labels: map[string]string{
							"cluster.metal-stack.io/id": string(resource.UID),
						},
						Partitionid: "test-partition",
						Projectid:   "test-project",
					})), nil).Return(&metalnetwork.AllocateNetworkCreated{
						Payload: &models.V1NetworkResponse{
							ID:       ptr.To("test-network"),
							Prefixes: []string{"192.168.42.0/24"},
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
			Expect(resource.Finalizers).To(ContainElement(infrastructurev1alpha1.ClusterFinalizer))

			// second reconcile

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// third reconcile

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())

			Expect(resource.Status.Conditions).To(ContainElement(clusterv1beta1.Condition{
				Type:    "ClusterNodeNetworkEnsured",
				Status:  "True",
				Reason:  "Ensured",
				Message: "Created",
			}))
		})
	})
	Context("reconciliation when external resources are provided", func() {
		BeforeEach(func() {
			By("creating a cluster resource and setting an ownership")

		})

		When("creating a resource and setting an ownership", func() {
			It("should successfully reconcile", func() {

			})
		})

		When("referenced resources do not exist", func() {
			It("should fail reconciling", func() {

			})
		})
	})

	Context("When reconciling a MetalStackCluster resource", func() {
		// const resourceName = "test-resource"

		// var (
		// 	ctx = context.Background()

		// 	resource           *infrastructurev1alpha1.MetalStackCluster
		// 	typeNamespacedName = types.NamespacedName{
		// 		Name:      resourceName,
		// 		Namespace: "default",
		// 	}
		// 	metalstackcluster = &infrastructurev1alpha1.MetalStackCluster{}
		// )

		// BeforeEach(func() {
		// 	resource = &infrastructurev1alpha1.MetalStackCluster{
		// 		ObjectMeta: metav1.ObjectMeta{
		// 			Name:      resourceName,
		// 			Namespace: "default",
		// 		},
		// 		Spec: infrastructurev1alpha1.MetalStackClusterSpec{
		// 			ControlPlaneEndpoint: infrastructurev1alpha1.APIEndpoint{},
		// 			ProjectID:            "test-project",
		// 			NodeNetworkID:        nil,
		// 			ControlPlaneIP:       nil,
		// 			Partition:            "test-partition",
		// 			Firewall:             &infrastructurev1alpha1.Firewall{},
		// 		},
		// 	}

		// 	By("creating the custom resource for the Kind MetalStackCluster")
		// 	err := k8sClient.Get(ctx, typeNamespacedName, metalstackcluster)
		// 	if err != nil && errors.IsNotFound(err) {
		// 		Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		// 	}
		// })

		// AfterEach(func() {
		// 	resource := &infrastructurev1alpha1.MetalStackCluster{}
		// 	err := k8sClient.Get(ctx, typeNamespacedName, resource)
		// 	Expect(err).NotTo(HaveOccurred())

		// 	By("Cleanup the specific resource instance MetalStackCluster")
		// 	Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		// })

		// it should not do anything when the resource has no ownership yet

		// it should reconcile successfully when the resource gets an ownership

		// it automatically allocates dependent resources

		// it should generate an SSH secret
		// it should allocate a node network
		// it should allocate a control plane ip
		// it should ensure a firewall deployment

		//  it is idempotent!
		// status conditions should be properly evaluated

		// it is possible to optionally provide control plane ip, node network id and firewall

		// it should delete the resource

		// it should delete all managed resources

		// it should not delete optionally provided resources

		// Context("should successfully reconcile the resource", Ordered, func() {
		// 	It("should not do anything when the resource has no ownership yet", func()  {

		// 	})

		// 	By("setting an ownership ownership yet")

		// 		It("it should reconcile successfully", func()  {

		// 		})

		// 		It("it should automatically allocate dependent resources", func()  {
		// 			// it should generate an SSH secret
		// 			// it should allocate a node network
		// 			// it should allocate a control plane ip
		// 			// it should ensure a firewall deployment
		// 		})
		// })
		// controllerReconciler := &MetalStackClusterReconciler{
		// 	Client: k8sClient,
		// 	Scheme: k8sClient.Scheme(),
		// }

		// _, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
		// 	NamespacedName: typeNamespacedName,
		// })
		// Expect(err).NotTo(HaveOccurred())

		// resource := &infrastructurev1alpha1.MetalStackCluster{}
		// err = k8sClient.Get(ctx, typeNamespacedName, resource)
		// Expect(err).NotTo(HaveOccurred())

		// Expect(resource.Status.Conditions).To(ContainElement("bla"))
	})
})
