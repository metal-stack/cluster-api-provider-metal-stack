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

	"golang.org/x/sync/errgroup"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"

	"github.com/go-logr/logr"

	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	infrastructurev1alpha1 "github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	ipmodels "github.com/metal-stack/metal-go/api/client/ip"
	"github.com/metal-stack/metal-go/api/client/network"
	"github.com/metal-stack/metal-go/api/models"
	"github.com/metal-stack/metal-lib/pkg/tag"

	metalgo "github.com/metal-stack/metal-go"
)

var (
	errProviderIPNotFound     = errors.New("provider ip not found")
	errProviderIPTooManyFound = errors.New("multiple provider ips found")
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

	defer func() {
		statusErr := reconciler.status()
		if statusErr != nil {
			err = errors.Join(err, fmt.Errorf("unable to update status: %w", statusErr))
		} else if !reconciler.infraCluster.Status.Ready {
			err = errors.New("cluster is not yet ready, requeuing")
		}
	}()

	if !infraCluster.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(infraCluster, v1alpha1.ClusterFinalizer) {
			return ctrl.Result{}, nil
		}

		log.Info("reconciling resource deletion flow")
		err := reconciler.delete()
		if err != nil {
			return ctrl.Result{}, err
		}

		log.Info("deletion finished, removing finalizer")
		controllerutil.RemoveFinalizer(infraCluster, v1alpha1.ClusterFinalizer)
		if err := r.Client.Update(ctx, infraCluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to remove finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}

	log.Info("reconciling cluster")

	if !controllerutil.ContainsFinalizer(infraCluster, v1alpha1.ClusterFinalizer) {
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(infraCluster, v1alpha1.ClusterFinalizer)
		if err := r.Client.Update(ctx, infraCluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to add finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}

	err = reconciler.reconcile()

	return ctrl.Result{}, err // remember to return err here and not nil because the defer func can influence this
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalStackClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1alpha1.MetalStackCluster{}).
		Named("metalstackcluster").
		WithEventFilter(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), mgr.GetLogger())).
		// TODO: implement resource paused from cluster-api's predicates?
		Complete(r)
}

func (r *clusterReconciler) reconcile() error {
	nodeNetworkID, err := r.ensureNodeNetwork()
	if err != nil {
		return fmt.Errorf("unable to ensure node network: %w", err)
	}

	r.log.Info("reconciled node network", "network-id", nodeNetworkID)

	if r.infraCluster.Spec.ControlPlaneEndpoint.Host == "" {
		ip, err := r.ensureControlPlaneIP()
		if err != nil {
			return fmt.Errorf("unable to ensure control plane ip: %w", err)
		}

		r.log.Info("reconciled control plane ip", "ip", *ip.Ipaddress)

		r.log.Info("setting control plane endpoint into cluster resource")

		helper, err := patch.NewHelper(r.infraCluster, r.client)
		if err != nil {
			return err
		}

		r.infraCluster.Spec.ControlPlaneEndpoint = infrastructurev1alpha1.APIEndpoint{
			Host: *ip.Ipaddress,
			Port: v1alpha1.ClusterControlPlaneEndpointDefaultPort,
		}

		err = helper.Patch(r.ctx, r.infraCluster) // TODO:check whether patch is not executed when no changes occur
		if err != nil {
			return fmt.Errorf("failed to update infra cluster control plane endpoint: %w", err)
		}
		return nil
	}

	return err
}

func (r *clusterReconciler) delete() error {
	var err error
	defer func() {
		statusErr := r.status()
		if statusErr != nil {
			err = errors.Join(err, fmt.Errorf("unable to update status: %w", statusErr))
		}
	}()

	err = r.deleteControlPlaneIP()
	if err != nil {
		return fmt.Errorf("unable to delete control plane ip: %w", err)
	}

	err = r.deleteNodeNetwork()
	if err != nil {
		return fmt.Errorf("unable to delete node network: %w", err)
	}

	return err
}

func (r *clusterReconciler) ensureNodeNetwork() (string, error) {
	nws, err := r.findNodeNetwork()
	if err != nil {
		return "", err
	}

	switch len(nws) {
	case 0:
		resp, err := r.metalClient.Network().AllocateNetwork(network.NewAllocateNetworkParams().WithBody(&models.V1NetworkAllocateRequest{
			Projectid:   r.infraCluster.Spec.ProjectID,
			Partitionid: r.infraCluster.Spec.Partition,
			Name:        r.infraCluster.GetName(),
			Description: fmt.Sprintf("%s/%s", r.infraCluster.GetNamespace(), r.infraCluster.GetName()),
			Labels:      map[string]string{tag.ClusterID: string(r.infraCluster.GetUID())},
		}).WithContext(r.ctx), nil)
		if err != nil {
			return "", fmt.Errorf("error creating node network: %w", err)
		}

		return *resp.Payload.ID, nil
	case 1:
		nw := nws[0]

		if len(nw.Prefixes) == 0 {
			return "", errors.New("node network exists but the prefix is gone")
		}

		return *nw.ID, nil
	default:
		return "", fmt.Errorf("more than a single node network exists for this cluster, operator investigation is required")
	}
}

func (r *clusterReconciler) deleteNodeNetwork() error {
	if r.infraCluster.Spec.NodeNetworkID != nil {
		r.log.Info("skip deletion of node network")
		return nil
	}

	nws, err := r.findNodeNetwork()
	if err != nil {
		return err
	}

	switch len(nws) {
	case 0:
		return nil
	case 1:
		nw := nws[0]

		if nw.ID == nil {
			return fmt.Errorf("node network id not set")
		}

		_, err := r.metalClient.Network().FreeNetwork(network.NewFreeNetworkParams().WithID(*nw.ID).WithContext(r.ctx), nil)
		if err != nil {
			return err
		}
		r.log.Info("deleted node network")

		return nil
	default:
		return errors.New("more than a single node network exists for this cluster, operator investigation is required")
	}
}

func (r *clusterReconciler) findNodeNetwork() ([]*models.V1NetworkResponse, error) {
	if r.infraCluster.Spec.NodeNetworkID != nil {
		resp, err := r.metalClient.Network().FindNetwork(network.NewFindNetworkParams().WithID(*r.infraCluster.Spec.NodeNetworkID).WithContext(r.ctx), nil)
		if err != nil {
			return nil, err
		}

		return []*models.V1NetworkResponse{resp.Payload}, nil
	}

	resp, err := r.metalClient.Network().FindNetworks(network.NewFindNetworksParams().WithBody(&models.V1NetworkFindRequest{
		Projectid:   r.infraCluster.Spec.ProjectID,
		Partitionid: r.infraCluster.Spec.Partition,
		Labels:      map[string]string{tag.ClusterID: string(r.infraCluster.GetUID())},
	}).WithContext(r.ctx), nil)
	if err != nil {
		return nil, err
	}

	return resp.Payload, nil
}

func (r *clusterReconciler) ensureControlPlaneIP() (*models.V1IPResponse, error) {
	ip, err := r.findControlPlaneIP()
	if ip != nil {
		return ip, nil
	}
	if errors.Is(err, errProviderIPTooManyFound) {
		return nil, fmt.Errorf("more than a single control plane ip exists for this cluster, operator investigation is required")
	}
	if err != nil && !errors.Is(err, errProviderIPNotFound) {
		return nil, err
	}

	nwResp, err := r.metalClient.Network().FindNetworks(network.NewFindNetworksParams().WithBody(&models.V1NetworkFindRequest{
		Labels: map[string]string{
			tag.NetworkDefault: "",
		},
	}).WithContext(r.ctx), nil)
	if err != nil {
		return nil, fmt.Errorf("error finding default network: %w", err)
	}

	if len(nwResp.Payload) != 1 {
		return nil, fmt.Errorf("no distinct default network configured in the metal-api")
	}

	resp, err := r.metalClient.IP().AllocateIP(ipmodels.NewAllocateIPParams().WithBody(&models.V1IPAllocateRequest{
		Description: fmt.Sprintf("%s/%s control plane ip", r.infraCluster.GetNamespace(), r.infraCluster.GetName()),
		Name:        r.infraCluster.GetName() + "-control-plane",
		Networkid:   nwResp.Payload[0].ID,
		Projectid:   &r.infraCluster.Spec.ProjectID,
		Tags: []string{
			tag.New(tag.ClusterID, string(r.infraCluster.GetUID())),
			v1alpha1.TagControlPlanePurpose,
		},
		Type: ptr.To(models.V1IPBaseTypeEphemeral),
	}).WithContext(r.ctx), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating ip: %w", err)
	}

	return resp.Payload, nil
}

func (r *clusterReconciler) deleteControlPlaneIP() error {
	if r.infraCluster.Spec.ControlPlaneIP != nil {
		r.log.Info("skip deletion of provided control plane ip")
		return nil
	}
	ip, err := r.findControlPlaneIP()
	if err != nil && errors.Is(err, errProviderIPNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("unable to delete control plane ip: %w", err)
	}
	if ip.Type != nil && *ip.Type == models.V1IPBaseTypeStatic {
		r.log.Info("skip deletion of static control plane ip")
		return nil
	}

	if ip.Ipaddress == nil {
		return fmt.Errorf("control plane ip address not set")
	}

	if ip.Type != nil && *ip.Type == models.V1IPAllocateRequestTypeStatic {
		r.log.Info("skipping deletion of static control plane ip", "ip", *ip.Ipaddress)
		return nil
	}
	_, err = r.metalClient.IP().FreeIP(ipmodels.NewFreeIPParams().WithID(*ip.Ipaddress).WithContext(r.ctx), nil)
	if err != nil {
		return err
	}
	r.log.Info("deleted control plane ip", "address", *ip.Ipaddress)

	return nil
}

func (r *clusterReconciler) findControlPlaneIP() (*models.V1IPResponse, error) {
	if r.infraCluster.Spec.ControlPlaneIP != nil {
		resp, err := r.metalClient.IP().FindIP(ipmodels.NewFindIPParams().WithID(*r.infraCluster.Spec.ControlPlaneIP).WithContext(r.ctx), nil)
		if err != nil {
			return nil, err
		}

		return resp.Payload, nil
	}

	resp, err := r.metalClient.IP().FindIPs(ipmodels.NewFindIPsParams().WithBody(&models.V1IPFindRequest{
		Projectid: r.infraCluster.Spec.ProjectID,
		Tags: []string{
			tag.New(tag.ClusterID, string(r.infraCluster.GetUID())),
			v1alpha1.TagControlPlanePurpose,
		},
	}).WithContext(r.ctx), nil)
	if err != nil {
		return nil, err
	}

	switch len(resp.Payload) {
	case 0:
		return nil, errProviderIPNotFound
	case 1:
		return resp.Payload[0], nil
	default:
		return nil, errProviderIPTooManyFound
	}
}

func (r *clusterReconciler) status() error {
	var (
		g, _             = errgroup.WithContext(r.ctx)
		conditionUpdates = make(chan func())

		// TODO: probably there is a helper for this available somewhere?
		allConditionsTrue = func() bool {
			for _, c := range r.infraCluster.Status.Conditions {
				if c.Status != corev1.ConditionTrue {
					return false
				}
			}

			return true
		}
	)

	g.Go(func() error {
		nws, err := r.findNodeNetwork()

		conditionUpdates <- func() {
			if err != nil {
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterNodeNetworkEnsured, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
				return
			}

			switch len(nws) {
			case 0:
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterNodeNetworkEnsured, "NotCreated", clusterv1.ConditionSeverityError, "node network was not yet created")
			case 1:
				nw := nws[0]

				if len(nw.Prefixes) > 0 {
					r.infraCluster.Status.NodeCIDR = &nw.Prefixes[0]
				}
				r.infraCluster.Status.NodeNetworkID = nw.ID

				conditions.MarkTrue(r.infraCluster, v1alpha1.ClusterNodeNetworkEnsured)
			default:
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterNodeNetworkEnsured, "InternalError", clusterv1.ConditionSeverityError, "more than a single node network exists for this cluster, operator investigation is required")
			}
		}

		return err
	})

	g.Go(func() error {
		ip, err := r.findControlPlaneIP()

		conditionUpdates <- func() {
			if errors.Is(err, errProviderIPNotFound) {
				if r.infraCluster.Spec.ControlPlaneEndpoint.Host != "" {
					conditions.MarkTrue(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured)
				} else {
					conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "NotCreated", clusterv1.ConditionSeverityError, "control plane ip was not yet created")
				}

			}
			if errors.Is(err, errProviderIPTooManyFound) {
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "InternalError", clusterv1.ConditionSeverityError, "more than a single control plane ip exists for this cluster, operator investigation is required")
			}
			if err != nil {
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
				return
			}

			if r.infraCluster.Spec.ControlPlaneEndpoint.Host == *ip.Ipaddress {
				conditions.MarkTrue(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured)
			} else {
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "NotSet", clusterv1.ConditionSeverityWarning, "control plane ip was not yet patched into the cluster's spec")
			}
		}

		return err
	})

	ready := make(chan bool)
	defer func() {
		close(ready)
	}()

	go func() {
		for u := range conditionUpdates {
			u()
		}
		ready <- true
	}()

	groupErr := g.Wait()

	close(conditionUpdates)
	<-ready

	if groupErr == nil && allConditionsTrue() {
		r.infraCluster.Status.Ready = true
	}

	err := r.client.Status().Update(r.ctx, r.infraCluster)

	return errors.Join(groupErr, err)
}
