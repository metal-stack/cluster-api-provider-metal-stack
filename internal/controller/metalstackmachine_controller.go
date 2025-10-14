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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capierrors "sigs.k8s.io/cluster-api/errors" //nolint:staticcheck
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	metalgo "github.com/metal-stack/metal-go"
	metalmachine "github.com/metal-stack/metal-go/api/client/machine"
	"github.com/metal-stack/metal-go/api/models"
	"github.com/metal-stack/metal-lib/pkg/pointer"
	"github.com/metal-stack/metal-lib/pkg/tag"
)

const defaultProviderMachineRequeueTime = time.Second * 30

var errProviderMachineNotFound = errors.New("provider machine not found")

// MetalStackMachineReconciler reconciles a MetalStackMachine object
type MetalStackMachineReconciler struct {
	MetalClient metalgo.Client
	Client      client.Client
}

type machineReconciler struct {
	metalClient    metalgo.Client
	client         client.Client
	ctx            context.Context
	log            logr.Logger
	infraCluster   *v1alpha1.MetalStackCluster
	clusterMachine *clusterv1.Machine
	infraMachine   *v1alpha1.MetalStackMachine
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconile reconciles a MetalStackMachine object.
// Creates, updates and deletes the actual metalstack infra machine entities.
func (r *MetalStackMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		log          = ctrllog.FromContext(ctx)
		infraMachine = &v1alpha1.MetalStackMachine{}
	)

	if err := r.Client.Get(ctx, req.NamespacedName, infraMachine); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("machine no longer exists")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	machine, err := util.GetOwnerMachine(ctx, r.Client, infraMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		log.Info("infrastructure machine resource has no ownership yet")
		return ctrl.Result{}, err
	}

	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("machine resource has no cluster yet")
		return ctrl.Result{}, err
	}

	infraCluster := &v1alpha1.MetalStackCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster.Spec.InfrastructureRef.Name,
		},
	}
	err = r.Client.Get(ctx, client.ObjectKeyFromObject(infraCluster), infraCluster)
	if apierrors.IsNotFound(err) {
		log.Info("infrastructure cluster no longer exists")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	reconciler := &machineReconciler{
		metalClient:    r.MetalClient,
		client:         r.Client,
		ctx:            ctx,
		log:            log,
		infraCluster:   infraCluster,
		clusterMachine: machine,
		infraMachine:   infraMachine,
	}

	helper, err := patch.NewHelper(infraMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	var result ctrl.Result

	if annotations.IsPaused(cluster, infraMachine) {
		conditions.Set(infraMachine, metav1.Condition{
			Status: metav1.ConditionTrue,
			Type:   clusterv1.PausedCondition,
		})
	} else {
		conditions.Set(infraMachine, metav1.Condition{
			Status: metav1.ConditionFalse,
			Type:   clusterv1.PausedCondition,
			Reason: clusterv1.PausedReason,
		})
	}

	switch {
	case annotations.IsPaused(cluster, infraMachine):
		log.Info("reconciliation is paused")
	case !infraMachine.DeletionTimestamp.IsZero():
		err = reconciler.delete()
	case !controllerutil.ContainsFinalizer(infraMachine, v1alpha1.MachineFinalizer):
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(infraMachine, v1alpha1.MachineFinalizer)
	default:
		result, err = reconciler.reconcile()
	}

	updateErr := helper.Patch(ctx, infraMachine)
	if updateErr != nil {
		err = errors.Join(err, fmt.Errorf("failed to update infra machine: %w", updateErr))
	}

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalStackMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MetalStackMachine{}).
		Named("metalstackmachine").
		WithEventFilter(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), mgr.GetLogger())).
		WithEventFilter(predicates.ResourceNotPaused(mgr.GetScheme(), mgr.GetLogger())).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.clusterToMetalStackMachine(mgr.GetLogger())),
			builder.WithPredicates(predicates.ClusterUnpaused(mgr.GetScheme(), mgr.GetLogger())),
		).
		Watches(
			&v1alpha1.MetalStackCluster{},
			handler.EnqueueRequestsFromMapFunc(r.metalStackClusterToMetalStackMachine(mgr.GetLogger())),
			builder.WithPredicates(predicates.ResourceNotPaused(mgr.GetScheme(), mgr.GetLogger())),
		).
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(r.machineToMetalStackMachine(mgr.GetLogger())),
			builder.WithPredicates(predicates.ResourceNotPaused(mgr.GetScheme(), mgr.GetLogger())),
		).
		Complete(r)
}

func (r *MetalStackMachineReconciler) clusterToMetalStackMachine(log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		cluster, ok := o.(*clusterv1.Cluster)
		if !ok {
			log.Error(fmt.Errorf("expected a cluster, got %T", o), "failed to get cluster", "object", o)
			return nil
		}

		log := log.WithValues("cluster", cluster.Name, "namespace", cluster.Namespace)

		infraMachineList := &v1alpha1.MetalStackMachineList{}
		err := r.Client.List(ctx, infraMachineList, &client.ListOptions{
			Namespace: cluster.Namespace,
			LabelSelector: labels.SelectorFromSet(labels.Set{
				clusterv1.ClusterNameLabel: cluster.Name,
			}),
		})
		if err != nil {
			log.Error(err, "failed to get infra machines")
			return nil
		}

		var reqs []ctrl.Request
		for _, infraMachine := range infraMachineList.Items {
			log.Info("cluster changed, reconcile", "infraMachine", infraMachine.Name)
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&infraMachine),
			})
		}
		return reqs
	}
}

func (r *MetalStackMachineReconciler) metalStackClusterToMetalStackMachine(log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		infraCluster, ok := o.(*v1alpha1.MetalStackCluster)
		if !ok {
			log.Error(fmt.Errorf("expected an infra cluster, got %T", o), "failed to get cluster", "object", o)
			return nil
		}

		log := log.WithValues("infraCluster", infraCluster.Name, "namespace", infraCluster.Namespace)

		clusterName, ok := infraCluster.Labels[clusterv1.ClusterNameLabel]
		if !ok {
			return nil
		}

		infraMachineList := &v1alpha1.MetalStackMachineList{}
		err := r.Client.List(ctx, infraMachineList, &client.ListOptions{
			Namespace: infraCluster.Namespace,
			LabelSelector: labels.SelectorFromSet(labels.Set{
				clusterv1.ClusterNameLabel: clusterName,
			}),
		})
		if err != nil {
			log.Error(err, "failed to get infra machines")
			return nil
		}

		var reqs []ctrl.Request
		for _, infraMachine := range infraMachineList.Items {
			log.Info("metalstackcluster changed, reconcile", "infraMachine", infraMachine.Name)
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&infraMachine),
			})
		}
		return reqs
	}
}

func (r *MetalStackMachineReconciler) machineToMetalStackMachine(log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		machine, ok := o.(*clusterv1.Machine)
		if !ok {
			log.Error(fmt.Errorf("expected a machine, got %T", o), "failed to get machine", "object", o)
			return nil
		}

		log := log.WithValues("machine", machine.Name, "namespace", machine.Namespace)

		clusterName, ok := machine.Labels[clusterv1.ClusterNameLabel]
		if !ok {
			return nil
		}
		deploymentName, ok := machine.Labels[clusterv1.MachineDeploymentNameLabel]
		if !ok {
			return nil
		}
		machineSetName, ok := machine.Labels[clusterv1.MachineSetNameLabel]
		if !ok {
			return nil
		}

		infraMachineList := &v1alpha1.MetalStackMachineList{}
		err := r.Client.List(ctx, infraMachineList, &client.ListOptions{
			Namespace: machine.Namespace,
			LabelSelector: labels.SelectorFromSet(labels.Set{
				clusterv1.ClusterNameLabel:           clusterName,
				clusterv1.MachineDeploymentNameLabel: deploymentName,
				clusterv1.MachineSetNameLabel:        machineSetName,
			}),
		})
		if err != nil {
			log.Error(err, "failed to get infra machines")
			return nil
		}

		var reqs []ctrl.Request
		for _, infraMachine := range infraMachineList.Items {
			log.Info("machine changed, reconcile", "infraMachine", infraMachine.Name)
			reqs = append(reqs, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&infraMachine),
			})
		}
		return reqs
	}
}

func (r *machineReconciler) reconcile() (ctrl.Result, error) {
	if r.infraCluster.Spec.ControlPlaneEndpoint.Host == "" {
		return ctrl.Result{}, errors.New("waiting until control plane ip was set to infrastructure cluster spec")
	}

	if r.clusterMachine.Spec.Bootstrap.DataSecretName == nil {
		return ctrl.Result{}, errors.New("waiting until bootstrap data secret was created")
	}

	r.log.Info("reconciling machine")

	var (
		m   *models.V1MachineResponse
		err error
	)

	if r.infraMachine.Spec.ProviderID != "" {
		m, err = r.findProviderMachine()
		if errors.Is(err, errProviderMachineNotFound) {
			r.infraMachine.Status.FailureReason = pointer.Pointer(capierrors.UpdateMachineError)
			r.infraMachine.Status.FailureMessage = pointer.Pointer("machine has been deleted externally")
			return ctrl.Result{}, errors.New("machine has been deleted externally")
		}
		if err != nil {
			conditions.Set(r.infraMachine, metav1.Condition{
				Status:  metav1.ConditionFalse,
				Type:    v1alpha1.ProviderMachineCreated,
				Reason:  "InternalError",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	} else {
		m, err = r.create()
		if err != nil {
			conditions.Set(r.infraMachine, metav1.Condition{
				Status:  metav1.ConditionFalse,
				Type:    v1alpha1.ProviderMachineCreated,
				Reason:  "InternalError",
				Message: err.Error(),
			})
			return ctrl.Result{}, fmt.Errorf("unable to create machine at provider: %w", err)
		}
	}

	conditions.Set(r.infraMachine, metav1.Condition{
		Status: metav1.ConditionTrue,
		Type:   v1alpha1.ProviderMachineCreated,
	})

	if m.ID == nil {
		return ctrl.Result{}, errors.New("machine allocated but got no provider ID")
	}
	r.infraMachine.Spec.ProviderID = encodeProviderID(m)

	r.patchMachineLabels(m)

	result := ctrl.Result{}

	isReady, err := r.getMachineStatus(m)
	if err != nil {
		conditions.Set(r.infraMachine, metav1.Condition{
			Status:  metav1.ConditionFalse,
			Type:    v1alpha1.ProviderMachineHealthy,
			Reason:  "NotHealthy",
			Message: err.Error(),
		})
		result.RequeueAfter = defaultProviderMachineRequeueTime
	} else {
		conditions.Set(r.infraMachine, metav1.Condition{
			Status: metav1.ConditionTrue,
			Type:   v1alpha1.ProviderMachineHealthy,
		})
	}

	if isReady {
		conditions.Set(r.infraMachine, metav1.Condition{
			Status: metav1.ConditionTrue,
			Type:   v1alpha1.ProviderMachineReady,
		})
		r.infraMachine.Status.Ready = isReady
	} else {
		conditions.Set(r.infraMachine, metav1.Condition{
			Status:  metav1.ConditionFalse,
			Type:    v1alpha1.ProviderMachineReady,
			Reason:  "NotReady",
			Message: "machine is not in phoned home state",
		})
		result.RequeueAfter = defaultProviderMachineRequeueTime
	}

	r.infraMachine.Status.Addresses = r.getMachineAddresses(m)

	return result, nil
}

func (r *machineReconciler) delete() error {
	if !controllerutil.ContainsFinalizer(r.infraMachine, v1alpha1.MachineFinalizer) {
		return nil
	}

	r.log.Info("reconciling resource deletion flow")

	m, err := r.findProviderMachine()
	if errors.Is(err, errProviderMachineNotFound) {
		r.log.Info("machine already freed, removing finalizer")
		controllerutil.RemoveFinalizer(r.infraMachine, v1alpha1.MachineFinalizer)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to find provider machine: %w", err)
	}

	_, err = r.metalClient.Machine().FreeMachine(metalmachine.NewFreeMachineParamsWithContext(r.ctx).WithID(*m.ID), nil)
	if err != nil {
		return fmt.Errorf("failed to delete provider machine: %w", err)
	}

	r.log.Info("freed provider machine")

	r.log.Info("deletion finished, removing finalizer")
	controllerutil.RemoveFinalizer(r.infraMachine, v1alpha1.MachineFinalizer)

	return nil
}

func (r *machineReconciler) create() (*models.V1MachineResponse, error) {
	bootstrapSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      *r.clusterMachine.Spec.Bootstrap.DataSecretName,
			Namespace: r.infraMachine.Namespace,
		},
	}
	err := r.client.Get(r.ctx, client.ObjectKeyFromObject(bootstrapSecret), bootstrapSecret)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch bootstrap secret: %w", err)
	}

	var (
		ips []string
		nws = []*models.V1MachineAllocationNetwork{
			{
				Autoacquire: ptr.To(true),
				Networkid:   &r.infraCluster.Spec.NodeNetworkID,
			},
		}
	)

	resp, err := r.metalClient.Machine().AllocateMachine(metalmachine.NewAllocateMachineParamsWithContext(r.ctx).WithBody(&models.V1MachineAllocateRequest{
		Partitionid:   &r.infraCluster.Spec.Partition,
		Projectid:     &r.infraCluster.Spec.ProjectID,
		PlacementTags: []string{tag.New(tag.ClusterID, r.infraCluster.GetClusterID())},
		Tags:          append(r.machineTags(), r.additionalMachineTags()...),
		Name:          r.infraMachine.Name,
		Hostname:      r.infraMachine.Name,
		Sizeid:        &r.infraMachine.Spec.Size,
		Imageid:       &r.infraMachine.Spec.Image,
		Description:   fmt.Sprintf("%s/%s for cluster %s/%s", r.infraMachine.Namespace, r.infraMachine.Name, r.infraCluster.Namespace, r.infraCluster.Name),
		Networks:      nws,
		Ips:           ips,
		UserData:      string(bootstrapSecret.Data["value"]),
		// TODO: SSHPubKeys, ...
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate machine: %w", err)
	}

	return resp.Payload, nil
}

func (r *machineReconciler) getMachineStatus(mr *models.V1MachineResponse) (bool, error) {
	var errs []error

	switch l := ptr.Deref(mr.Liveliness, ""); l {
	case "Alive":
	default:
		errs = append(errs, fmt.Errorf("machine is not alive but %q", l))
	}

	if mr.Events != nil {
		if mr.Events.CrashLoop != nil && *mr.Events.CrashLoop {
			errs = append(errs, errors.New("machine is in a crash loop"))
		}
		if mr.Events.FailedMachineReclaim != nil && *mr.Events.FailedMachineReclaim {
			errs = append(errs, errors.New("machine reclaim is failing"))
		}
	}

	isReady := mr.Events != nil && len(mr.Events.Log) > 0 && ptr.Deref(mr.Events.Log[0].Event, "") == "Phoned Home"

	return isReady, errors.Join(errs...)
}

func (r *machineReconciler) getMachineAddresses(m *models.V1MachineResponse) clusterv1.MachineAddresses {
	var maddrs clusterv1.MachineAddresses

	if m.Allocation.Hostname != nil {
		maddrs = append(maddrs, clusterv1.MachineAddress{
			Type:    clusterv1.MachineHostName,
			Address: *m.Allocation.Hostname,
		})
	}

	for _, nw := range m.Allocation.Networks {
		switch ptr.Deref(nw.Networktype, "") {
		case "privateprimaryunshared":
			for _, ip := range nw.Ips {
				maddrs = append(maddrs, clusterv1.MachineAddress{
					Type:    clusterv1.MachineInternalIP,
					Address: ip,
				})
			}
		case "external":
			for _, ip := range nw.Ips {
				maddrs = append(maddrs, clusterv1.MachineAddress{
					Type:    clusterv1.MachineExternalIP,
					Address: ip,
				})
			}
		}
	}

	return maddrs
}

func (r *machineReconciler) findProviderMachine() (*models.V1MachineResponse, error) {
	if r.infraMachine.Spec.ProviderID == "" {
		return nil, errProviderMachineNotFound
	}

	resp, err := r.metalClient.Machine().FindMachine(metalmachine.NewFindMachineParams().WithContext(r.ctx).WithID(decodeProviderID(r.infraMachine.Spec.ProviderID)), nil)

	var errResp *metalmachine.FindMachineDefault
	if errors.As(err, &errResp) && errResp.Code() == http.StatusNotFound {
		return nil, errProviderMachineNotFound
	}
	if err != nil {
		conditions.Set(r.infraMachine, metav1.Condition{
			Status:  metav1.ConditionFalse,
			Type:    v1alpha1.ProviderMachineCreated,
			Reason:  "InternalError",
			Message: err.Error(),
		})
		return nil, err
	}

	if resp.Payload.Allocation == nil || resp.Payload.Allocation.Project == nil || *resp.Payload.Allocation.Project != r.infraCluster.Spec.ProjectID {
		return nil, errProviderMachineNotFound
	}

	return resp.Payload, nil
}

func (r *machineReconciler) patchMachineLabels(m *models.V1MachineResponse) {
	if r.infraMachine.Labels == nil {
		r.infraMachine.Labels = make(map[string]string)
	}

	if m.Allocation != nil && m.Allocation.Hostname != nil {
		r.infraMachine.Labels[corev1.LabelHostname] = *m.Allocation.Hostname
	}
	if m.Partition != nil && m.Partition.ID != nil {
		r.infraMachine.Labels[corev1.LabelTopologyZone] = *m.Partition.ID
	}
	if m.Rackid != "" {
		r.infraMachine.Labels[tag.MachineRack] = m.Rackid
	}

	tagMap := tag.NewTagMap(m.Tags)

	if asn, ok := tagMap.Value(tag.MachineNetworkPrimaryASN); ok {
		r.infraMachine.Labels[tag.MachineNetworkPrimaryASN] = asn
	}
	if chassis, ok := tagMap.Value(tag.MachineChassis); ok {
		r.infraMachine.Labels[tag.MachineChassis] = chassis
	}
}

func (r *machineReconciler) machineTags() []string {
	tags := []string{
		tag.New(tag.ClusterID, r.infraCluster.Spec.NodeNetworkID),
		tag.New(v1alpha1.TagInfraClusterResource, fmt.Sprintf("%s.%s", r.infraCluster.Namespace, r.infraCluster.Name)),
		tag.New(v1alpha1.TagInfraMachineResource, fmt.Sprintf("%s.%s", r.infraMachine.Namespace, r.infraMachine.Name)),
	}

	if util.IsControlPlaneMachine(r.clusterMachine) {
		tags = append(tags, v1alpha1.TagControlPlanePurpose)
	}

	return tags
}

func (r *machineReconciler) additionalMachineTags() []string {
	tags := []string{
		tag.New(corev1.LabelTopologyZone, r.infraCluster.Spec.Partition),
		tag.New(corev1.LabelHostname, r.infraMachine.Name),
	}

	return tags
}

func encodeProviderID(m *models.V1MachineResponse) string {
	return fmt.Sprintf("metal://%s/%s", pointer.SafeDeref(pointer.SafeDeref(m.Partition).ID), pointer.SafeDeref(m.ID))
}

func decodeProviderID(id string) string {
	withPartition := strings.TrimPrefix(id, "metal://")
	_, res, _ := strings.Cut(withPartition, "/")
	return res
}
