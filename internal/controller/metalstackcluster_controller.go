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
	"errors"
	"fmt"
	"net/http"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"

	"github.com/go-logr/logr"

	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	ipmodels "github.com/metal-stack/metal-go/api/client/ip"
	"github.com/metal-stack/metal-go/api/client/network"
	"github.com/metal-stack/metal-go/api/models"
	"github.com/metal-stack/metal-lib/pkg/tag"

	metalgo "github.com/metal-stack/metal-go"
)

// MetalStackClusterReconciler reconciles a MetalStackCluster object
type MetalStackClusterReconciler struct {
	MetalClient metalgo.Client
	Client      client.Client
}

type clusterReconciler struct {
	metalClient  metalgo.Client
	client       client.Client
	ctx          context.Context
	log          logr.Logger
	cluster      *clusterv1.Cluster
	infraCluster *v1alpha1.MetalStackCluster
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackclusters/finalizers,verbs=update

// Reconcile reconciles the reconciled cluster to be reconciled.
func (r *MetalStackClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		log          = ctrllog.FromContext(ctx)
		infraCluster = &v1alpha1.MetalStackCluster{}
	)

	if err := r.Client.Get(ctx, req.NamespacedName, infraCluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("resource no longer exists")
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, infraCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster == nil {
		log.Info("infrastructure cluster resource has no ownership yet")
		return ctrl.Result{}, nil
	}

	reconciler := &clusterReconciler{
		metalClient:  r.MetalClient,
		client:       r.Client,
		ctx:          ctx,
		log:          log,
		cluster:      cluster,
		infraCluster: infraCluster,
	}

	helper, err := patch.NewHelper(infraCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	if annotations.IsPaused(cluster, infraCluster) {
		conditions.MarkTrue(infraCluster, v1alpha1.ClusterPaused)
	} else {
		conditions.MarkFalse(infraCluster, v1alpha1.ClusterPaused, clusterv1.PausedV1Beta2Reason, clusterv1.ConditionSeverityInfo, "")
	}

	switch {
	case annotations.IsPaused(cluster, infraCluster):
		log.Info("reconciliation is paused")
	case !infraCluster.DeletionTimestamp.IsZero():
		err = reconciler.delete()
	case !controllerutil.ContainsFinalizer(infraCluster, v1alpha1.ClusterFinalizer):
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(infraCluster, v1alpha1.ClusterFinalizer)
	default:
		log.Info("reconciling cluster")
		err = reconciler.reconcile()
	}

	updateErr := helper.Patch(ctx, infraCluster)
	if updateErr != nil {
		err = errors.Join(err, fmt.Errorf("failed to update infra cluster: %w", updateErr))
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalStackClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MetalStackCluster{}).
		Named("metalstackcluster").
		WithEventFilter(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), mgr.GetLogger())).
		WithEventFilter(predicates.ResourceNotPaused(mgr.GetScheme(), mgr.GetLogger())).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.clusterToMetalStackCluster(mgr.GetLogger())),
			builder.WithPredicates(predicates.ClusterUnpaused(mgr.GetScheme(), mgr.GetLogger())),
		).
		Watches(&v1alpha1.MetalStackMachine{},
			handler.EnqueueRequestsFromMapFunc(r.metalStackMachineToMetalStackCluster(mgr.GetLogger())),
			builder.WithPredicates(predicates.ResourceNotPaused(mgr.GetScheme(), mgr.GetLogger())),
		).
		Complete(r)
}

func (r *MetalStackClusterReconciler) clusterToMetalStackCluster(log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		cluster, ok := o.(*clusterv1.Cluster)
		if !ok {
			log.Error(fmt.Errorf("expected a cluster, got %T", o), "failed to get cluster", "object", o)
			return nil
		}

		log := log.WithValues("cluster", cluster.Name, "namespace", cluster.Namespace)

		if cluster.Spec.InfrastructureRef == nil {
			return nil
		}
		if cluster.Spec.InfrastructureRef.GroupVersionKind().Kind != v1alpha1.MetalStackClusterResourceKind {
			return nil
		}

		infraCluster := &v1alpha1.MetalStackCluster{}
		infraName := types.NamespacedName{
			Namespace: cluster.Spec.InfrastructureRef.Namespace,
			Name:      cluster.Spec.InfrastructureRef.Name,
		}

		if err := r.Client.Get(ctx, infraName, infraCluster); err != nil {
			log.Error(err, "failed to get infra cluster")
			return nil
		}
		if annotations.IsExternallyManaged(infraCluster) {
			return nil
		}

		log.Info("cluster changed, reconcile", "infraCluster", infraCluster.Name)
		return []ctrl.Request{
			{
				NamespacedName: infraName,
			},
		}
	}
}

func (r *MetalStackClusterReconciler) metalStackMachineToMetalStackCluster(log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		infraMachine, ok := o.(*v1alpha1.MetalStackMachine)
		if !ok {
			log.Error(fmt.Errorf("expected an infra cluster, got %T", o), "failed to get infra machine", "object", o)
			return nil
		}

		log := log.WithValues("namespace", infraMachine.Namespace, "infraMachine", infraMachine.Name)

		machine, err := util.GetOwnerMachine(ctx, r.Client, infraMachine.ObjectMeta)
		if err != nil {
			log.Error(err, "failed to get owner machine")
		}
		if machine == nil {
			return nil
		}

		log = log.WithValues("machine", machine.Name)

		cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
		if err != nil {
			log.Error(err, "failed to get owner cluster")
			return nil
		}
		if cluster == nil {
			log.Info("machine resource has no cluster yet")
			return nil
		}

		log = log.WithValues("cluster", cluster.Name)

		infraCluster := &v1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Spec.InfrastructureRef.Namespace,
				Name:      cluster.Spec.InfrastructureRef.Name,
			},
		}
		err = r.Client.Get(ctx, client.ObjectKeyFromObject(infraCluster), infraCluster)
		if apierrors.IsNotFound(err) {
			log.Info("infrastructure cluster no longer exists")
			return nil
		}
		if err != nil {
			log.Error(err, "failed to get infra cluster")
			return nil
		}

		if cluster.Spec.InfrastructureRef.GroupVersionKind().Kind != v1alpha1.MetalStackClusterResourceKind {
			log.Info("different infra cluster", "kind", cluster.Spec.InfrastructureRef.GroupVersionKind().Kind)
			return nil
		}

		if annotations.IsExternallyManaged(infraCluster) {
			log.Info("infra cluster is externally managed")
			return nil
		}

		log.Info("metalstackmachine changed, reconcile", "infraCluster", infraCluster.Name)
		return []ctrl.Request{
			{
				NamespacedName: client.ObjectKeyFromObject(infraCluster),
			},
		}
	}
}

func (r *clusterReconciler) reconcile() error {
	if r.infraCluster.Spec.ControlPlaneEndpoint.Host == "" {
		ip, err := r.ensureControlPlaneIP()
		if err != nil {
			conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneIPEnsured, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
			return fmt.Errorf("unable to ensure control plane ip: %w", err)
		}
		conditions.MarkTrue(r.infraCluster, v1alpha1.ClusterControlPlaneIPEnsured)

		r.log.Info("reconciled control plane ip", "ip", ip)

		r.log.Info("setting control plane endpoint into cluster resource")

		r.infraCluster.Spec.ControlPlaneEndpoint = v1alpha1.APIEndpoint{
			Host: ip,
			Port: v1alpha1.ClusterControlPlaneEndpointDefaultPort,
		}

	}

	r.infraCluster.Status.Ready = true

	return nil
}

func (r *clusterReconciler) delete() error {
	var err error

	if !controllerutil.ContainsFinalizer(r.infraCluster, v1alpha1.ClusterFinalizer) {
		return nil
	}

	r.log.Info("reconciling resource deletion flow")

	err = r.ensureAllMetalStackMachinesAreGone()
	if err != nil {
		return err
	}

	err = r.ensureFirewallDeploymentIsGone()
	if err != nil {
		return nil
	}

	err = r.deleteControlPlaneIP()
	if err != nil {
		return fmt.Errorf("unable to delete control plane ip: %w", err)
	}
	r.infraCluster.Spec.ControlPlaneIP = nil

	r.log.Info("deletion finished, removing finalizer")
	controllerutil.RemoveFinalizer(r.infraCluster, v1alpha1.ClusterFinalizer)

	return err
}

func (r *clusterReconciler) ensureControlPlaneIP() (string, error) {
	if r.infraCluster.Spec.ControlPlaneIP != nil {
		return *r.infraCluster.Spec.ControlPlaneIP, nil
	}

	nwResp, err := r.metalClient.Network().FindNetworks(network.NewFindNetworksParams().WithBody(&models.V1NetworkFindRequest{
		Labels: map[string]string{
			tag.NetworkDefault: "",
		},
	}).WithContext(r.ctx), nil)
	if err != nil {
		return "", fmt.Errorf("error finding default network: %w", err)
	}

	if len(nwResp.Payload) != 1 {
		return "", fmt.Errorf("no distinct default network configured in the metal-api")
	}

	defaultNetwork := nwResp.Payload[0]
	resp, err := r.metalClient.IP().AllocateIP(ipmodels.NewAllocateIPParams().WithBody(&models.V1IPAllocateRequest{
		Description: fmt.Sprintf("%s control plane ip", r.infraCluster.GetClusterID()),
		Name:        r.infraCluster.GetName() + "-control-plane",
		Networkid:   defaultNetwork.ID,
		Projectid:   &r.infraCluster.Spec.ProjectID,
		Tags: []string{
			tag.New(tag.ClusterID, r.infraCluster.GetClusterID()),
			v1alpha1.TagControlPlanePurpose,
		},
		Type: ptr.To(models.V1IPBaseTypeEphemeral),
	}).WithContext(r.ctx), nil)
	if err != nil {
		return "", fmt.Errorf("error creating ip: %w", err)
	}
	if resp.Payload.Ipaddress == nil {
		return "", fmt.Errorf("error creating ip address")
	}

	return *resp.Payload.Ipaddress, nil
}

func (r *clusterReconciler) ensureAllMetalStackMachinesAreGone() error {
	infraMachines := &v1alpha1.MetalStackMachineList{}
	err := r.client.List(r.ctx, infraMachines, &client.ListOptions{
		Limit:     1,
		Namespace: r.cluster.Namespace,
		LabelSelector: labels.SelectorFromSet(labels.Set{
			clusterv1.ClusterNameLabel: r.cluster.Name,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to fetch machines: %w", err)
	}

	if len(infraMachines.Items) > 0 {
		return errors.New("waiting for all infra machines to be gone")
	}
	return nil
}

func (r *clusterReconciler) ensureFirewallDeploymentIsGone() error {
	fwdeploy := &v1alpha1.MetalStackFirewallDeployment{}

	err := r.client.Get(r.ctx, types.NamespacedName{
		Namespace: r.cluster.Namespace,
		Name:      r.infraCluster.Spec.FirewallDeploymentRef.Name,
	}, fwdeploy)

	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to fetch firewall deployment: %w", err)
	}

	if !fwdeploy.DeletionTimestamp.IsZero() {
		return errors.New("waiting for firewall deployment to be gone")
	}

	err = r.client.Delete(r.ctx, fwdeploy)
	if err != nil {
		return fmt.Errorf("failed to delete firewall deployment: %w", err)
	}

	r.log.Info("deleting firewall deployment", "name", fwdeploy.Name)
	return errors.New("waiting for firewall deployment to be gone")
}

func (r *clusterReconciler) deleteControlPlaneIP() error {
	if r.infraCluster.Spec.ControlPlaneIP == nil {
		return nil
	}

	resp, err := r.metalClient.IP().FindIP(ipmodels.NewFindIPParams().WithID(*r.infraCluster.Spec.ControlPlaneIP).WithContext(r.ctx), nil)
	if err != nil {
		var r *ipmodels.FindIPDefault
		if errors.As(err, &r) && r.Code() == http.StatusNotFound {
			return nil
		}

		return err
	}

	ip := resp.Payload

	if ip.Type != nil && *ip.Type == models.V1IPBaseTypeStatic {
		r.log.Info("skip deletion of static control plane ip")
		return nil
	}

	if ip.Ipaddress == nil {
		return fmt.Errorf("control plane ip address not set")
	}

	_, err = r.metalClient.IP().FreeIP(ipmodels.NewFreeIPParams().WithID(*ip.Ipaddress).WithContext(r.ctx), nil)
	if err != nil {
		return err
	}

	r.log.Info("deleted control plane ip", "address", *ip.Ipaddress)

	return nil
}
