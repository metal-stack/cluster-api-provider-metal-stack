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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	metalgo "github.com/metal-stack/metal-go"
	ipmodels "github.com/metal-stack/metal-go/api/client/ip"
	metalmachine "github.com/metal-stack/metal-go/api/client/machine"
	"github.com/metal-stack/metal-go/api/models"
	"github.com/metal-stack/metal-lib/pkg/tag"
)

const defaultProviderMachineRequeueTime = time.Second * 30

var (
	errProviderMachineNotFound     = errors.New("provider machine not found")
	errProviderMachineTooManyFound = errors.New("multiple provider machines found")
)

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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MetalStackMachine object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
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
			Namespace: cluster.Spec.InfrastructureRef.Namespace,
			Name:      cluster.Spec.InfrastructureRef.Name,
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

	if !infraMachine.DeletionTimestamp.IsZero() {
		err = reconciler.delete()
	} else if !controllerutil.ContainsFinalizer(infraMachine, v1alpha1.MachineFinalizer) {
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(infraMachine, v1alpha1.MachineFinalizer)
	} else {
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
		Complete(r)
}

func (r *machineReconciler) reconcile() (ctrl.Result, error) {
	if r.infraCluster.Spec.ControlPlaneEndpoint.Host == "" {
		return ctrl.Result{}, errors.New("waiting until control plane ip was set to infrastructure cluster spec")
	}

	if r.clusterMachine.Spec.Bootstrap.DataSecretName == nil {
		return ctrl.Result{}, errors.New("waiting until bootstrap data secret was created")
	}

	r.log.Info("reconciling machine")

	m, err := r.findProviderMachine()
	if err != nil && !errors.Is(err, errProviderMachineNotFound) {
		conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineCreated, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
		return ctrl.Result{}, err
	}

	if errors.Is(err, errProviderMachineNotFound) {
		m, err = r.create()
		if err != nil {
			conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineCreated, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to create machine at provider: %w", err)
		}
	}
	conditions.MarkTrue(r.infraMachine, v1alpha1.ProviderMachineCreated)

	if m.ID == nil {
		return ctrl.Result{}, errors.New("machine allocated but got no provider ID")
	}
	r.infraMachine.Spec.ProviderID = "metal://" + *m.ID

	result := ctrl.Result{}

	isReady, err := r.getMachineStatus(m)
	if err != nil {
		conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineHealthy, "NotHealthy", clusterv1.ConditionSeverityWarning, "%s", err)
		result.RequeueAfter = defaultProviderMachineRequeueTime
	} else {
		conditions.MarkTrue(r.infraMachine, v1alpha1.ProviderMachineHealthy)
	}

	if isReady {
		conditions.MarkTrue(r.infraMachine, v1alpha1.ProviderMachineReady)
		r.infraMachine.Status.Ready = isReady
	} else {
		conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineReady, "NotReady", clusterv1.ConditionSeverityWarning, "machine is not in phoned home state")
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

	if util.IsControlPlaneMachine(r.clusterMachine) {
		ips = append(ips, r.infraCluster.Spec.ControlPlaneEndpoint.Host)

		resp, err := r.metalClient.IP().FindIP(ipmodels.NewFindIPParams().WithID(r.infraCluster.Spec.ControlPlaneEndpoint.Host).WithContext(r.ctx), nil)
		if err != nil {
			return nil, fmt.Errorf("unable to lookup control plane ip: %w", err)
		}

		nws = append(nws, &models.V1MachineAllocationNetwork{
			Autoacquire: ptr.To(false),
			Networkid:   resp.Payload.Networkid,
		})
	}

	resp, err := r.metalClient.Machine().AllocateMachine(metalmachine.NewAllocateMachineParamsWithContext(r.ctx).WithBody(&models.V1MachineAllocateRequest{
		Partitionid:   &r.infraCluster.Spec.Partition,
		Projectid:     &r.infraCluster.Spec.ProjectID,
		PlacementTags: []string{tag.New(tag.ClusterID, string(r.infraCluster.GetUID()))},
		Tags:          r.machineTags(),
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
	mfr := &models.V1MachineFindRequest{
		ID:                strings.TrimPrefix(r.infraMachine.Spec.ProviderID, "metal://"),
		AllocationProject: r.infraCluster.Spec.ProjectID,
		Tags:              r.machineTags(),
	}

	resp, err := r.metalClient.Machine().FindMachines(metalmachine.NewFindMachinesParamsWithContext(r.ctx).WithBody(mfr), nil)
	if err != nil {
		return nil, err
	}

	switch len(resp.Payload) {
	case 0:
		// metal-stack machine already freed
		return nil, errProviderMachineNotFound
	case 1:
		return resp.Payload[0], nil
	default:
		return nil, errProviderMachineTooManyFound
	}
}

func (r *machineReconciler) machineTags() []string {
	tags := []string{
		tag.New(tag.ClusterID, string(r.infraCluster.GetUID())),
		tag.New(v1alpha1.TagInfraMachineID, string(r.infraMachine.GetUID())),
	}

	if util.IsControlPlaneMachine(r.clusterMachine) {
		tags = append(tags, v1alpha1.TagControlPlanePurpose)
	}

	return tags
}
