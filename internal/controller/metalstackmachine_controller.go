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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	infrastructurev1alpha1 "github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	metalgo "github.com/metal-stack/metal-go"
	ipmodels "github.com/metal-stack/metal-go/api/client/ip"
	metalmachine "github.com/metal-stack/metal-go/api/client/machine"
	"github.com/metal-stack/metal-go/api/models"
	"github.com/metal-stack/metal-lib/pkg/tag"
)

var (
	errProviderMachineNotFound     = errors.New("provider machine not found")
	errProviderMachineTooManyFound = errors.New("multiple provider machines found")
)

// MetalStackMachineReconciler reconciles a MetalStackMachine object
type MetalStackMachineReconciler struct {
	MetalClient metalgo.Client
	Client      client.Client
	Scheme      *runtime.Scheme
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackmachines/finalizers,verbs=update

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

	defer func() {
		statusErr := r.status(ctx, infraCluster, machine, infraMachine)
		if statusErr != nil {
			err = errors.Join(err, fmt.Errorf("unable to update status: %w", statusErr))
		}
	}()

	if !infraMachine.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(infraMachine, v1alpha1.MachineFinalizer) {
			return ctrl.Result{}, nil
		}

		log.Info("reconciling resource deletion flow")
		err := r.delete(ctx, log, infraCluster, infraMachine, machine)
		if err != nil {
			return ctrl.Result{}, err
		}

		log.Info("deletion finished, removing finalizer")
		controllerutil.RemoveFinalizer(infraMachine, v1alpha1.MachineFinalizer)
		if err := r.Client.Update(ctx, infraMachine); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to remove finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}

	log.Info("reconciling machine")

	if !controllerutil.ContainsFinalizer(infraMachine, v1alpha1.MachineFinalizer) {
		log.Info("adding finalizer")

		controllerutil.AddFinalizer(infraMachine, v1alpha1.MachineFinalizer)
		if err := r.Client.Update(ctx, infraMachine); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to add finalizer: %w", err)
		}

		return ctrl.Result{}, nil
	}

	if infraCluster.Status.NodeNetworkID == nil {
		// this should not happen because before setting this id the cluster status should not become ready, but we check it anyway
		return ctrl.Result{}, errors.New("waiting until node network id was set to cluster status")
	}

	if infraCluster.Spec.ControlPlaneEndpoint.Host == "" {
		return ctrl.Result{}, errors.New("waiting until control plane ip was set to cluster spec")
	}

	err = r.reconcile(ctx, log, infraCluster, machine, infraMachine)

	return ctrl.Result{}, err // remember to return err here and not nil because the defer func can influence this
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalStackMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1alpha1.MetalStackMachine{}).
		Named("metalstackmachine").
		Complete(r)
}

func (r *MetalStackMachineReconciler) reconcile(ctx context.Context, log logr.Logger, infraCluster *v1alpha1.MetalStackCluster, clusterMachine *clusterv1.Machine, infraMachine *v1alpha1.MetalStackMachine) error {
	m, err := r.findProviderMachine(ctx, infraCluster, infraMachine, util.IsControlPlaneMachine(clusterMachine))
	if err != nil && !errors.Is(err, errProviderMachineNotFound) {
		return err
	}

	if errors.Is(err, errProviderMachineNotFound) {
		m, err = r.create(ctx, infraCluster, infraMachine, util.IsControlPlaneMachine(clusterMachine))
		if err != nil {
			return fmt.Errorf("unable to create machine at provider: %w", err)
		}
	}

	if m.ID == nil {
		return errors.New("machine allocated but got no provider ID")
	}

	log.Info("setting provider id into machine resource")

	helper, err := patch.NewHelper(infraMachine, r.Client)
	if err != nil {
		return err
	}

	infraMachine.Spec.ProviderID = *m.ID

	err = helper.Patch(ctx, infraMachine) // TODO:check whether patch is not executed when no changes occur
	if err != nil {
		return fmt.Errorf("failed to update infra machine provider ID %q: %w", infraMachine.Spec.ProviderID, err)
	}

	return nil
}

func (r *MetalStackMachineReconciler) delete(ctx context.Context, log logr.Logger, infraCluster *v1alpha1.MetalStackCluster, infraMachine *v1alpha1.MetalStackMachine, clusterMachine *clusterv1.Machine) error {
	m, err := r.findProviderMachine(ctx, infraCluster, infraMachine, util.IsControlPlaneMachine(clusterMachine))
	if errors.Is(err, errProviderMachineNotFound) {
		// metal-stack machine already freed
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to find provider machine: %w", err)
	}

	_, err = r.MetalClient.Machine().FreeMachine(metalmachine.NewFreeMachineParamsWithContext(ctx).WithID(*m.ID), nil)
	if err != nil {
		return fmt.Errorf("failed to delete provider machine: %w", err)
	}

	log.Info("freed provider machine")

	return nil
}

func (r *MetalStackMachineReconciler) create(ctx context.Context, infraCluster *v1alpha1.MetalStackCluster, infraMachine *v1alpha1.MetalStackMachine, isControlPlaneMachine bool) (*models.V1MachineResponse, error) {
	var (
		ips []string
		nws = []*models.V1MachineAllocationNetwork{
			{
				Autoacquire: ptr.To(true),
				Networkid:   infraCluster.Status.NodeNetworkID,
			},
		}
	)

	if isControlPlaneMachine {
		ips = append(ips, infraCluster.Spec.ControlPlaneEndpoint.Host)

		resp, err := r.MetalClient.IP().FindIP(ipmodels.NewFindIPParams().WithID(infraCluster.Spec.ControlPlaneEndpoint.Host).WithContext(ctx), nil)
		if err != nil {
			return nil, fmt.Errorf("unable to lookup control plane ip: %w", err)
		}

		nws = append(nws, &models.V1MachineAllocationNetwork{
			Autoacquire: ptr.To(false),
			Networkid:   resp.Payload.Networkid,
		})
	}

	resp, err := r.MetalClient.Machine().AllocateMachine(metalmachine.NewAllocateMachineParamsWithContext(ctx).WithBody(&models.V1MachineAllocateRequest{
		Partitionid:   &infraCluster.Spec.Partition,
		Projectid:     &infraCluster.Spec.ProjectID,
		PlacementTags: []string{tag.New(tag.ClusterID, string(infraCluster.GetUID()))},
		Tags:          machineTags(infraCluster, infraMachine, isControlPlaneMachine),
		Name:          infraMachine.Name,
		Hostname:      infraMachine.Name,
		Sizeid:        &infraMachine.Spec.Size,
		Imageid:       &infraMachine.Spec.Image,
		Description:   fmt.Sprintf("%s/%s for cluster %s/%s", infraMachine.Namespace, infraMachine.Name, infraCluster.Namespace, infraCluster.Name),
		Networks:      nws,
		Ips:           ips,
		// TODO: UserData, SSHPubKeys, ...
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate machine: %w", err)
	}

	return resp.Payload, nil
}

func (r *MetalStackMachineReconciler) status(ctx context.Context, infraCluster *v1alpha1.MetalStackCluster, clusterMachine *clusterv1.Machine, infraMachine *v1alpha1.MetalStackMachine) error {
	var (
		g, _             = errgroup.WithContext(ctx)
		conditionUpdates = make(chan func())

		// TODO: probably there is a helper for this available somewhere?
		allConditionsTrue = func() bool {
			for _, c := range infraMachine.Status.Conditions {
				if c.Status != corev1.ConditionTrue {
					return false
				}
			}

			return true
		}
	)

	defer func() {
		close(conditionUpdates)
	}()

	g.Go(func() error {
		m, err := r.findProviderMachine(ctx, infraCluster, infraMachine, util.IsControlPlaneMachine(clusterMachine))

		conditionUpdates <- func() {
			switch {
			case err != nil && !errors.Is(err, errProviderMachineNotFound):
				conditions.MarkFalse(infraMachine, v1alpha1.ProviderMachineCreated, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
				conditions.MarkFalse(infraMachine, v1alpha1.ProviderMachineHealthy, "NotHealthy", clusterv1.ConditionSeverityWarning, "machine not created")
			case err != nil && errors.Is(err, errProviderMachineNotFound):
				conditions.MarkFalse(infraMachine, v1alpha1.ProviderMachineCreated, "NotCreated", clusterv1.ConditionSeverityError, "%s", err.Error())
				conditions.MarkFalse(infraMachine, v1alpha1.ProviderMachineHealthy, "NotHealthy", clusterv1.ConditionSeverityWarning, "machine not created")
			default:
				if infraMachine.Spec.ProviderID == *m.ID {
					conditions.MarkTrue(infraMachine, v1alpha1.ProviderMachineCreated)
				} else {
					conditions.MarkFalse(infraMachine, v1alpha1.ProviderMachineCreated, "NotSet", clusterv1.ConditionSeverityWarning, "provider id was not yet patched into the machine's spec")
				}

				var errs []error

				switch l := ptr.Deref(m.Liveliness, ""); l {
				case "Alive":
				default:
					errs = append(errs, fmt.Errorf("machine is not alive but %q", l))
				}

				if m.Events != nil {
					if m.Events.CrashLoop != nil && *m.Events.CrashLoop {
						errs = append(errs, errors.New("machine is in a crash loop"))
					}
					if m.Events.FailedMachineReclaim != nil && *m.Events.FailedMachineReclaim {
						errs = append(errs, errors.New("machine reclaim is failing"))
					}
				}

				if len(errs) == 0 {
					conditions.MarkTrue(infraMachine, v1alpha1.ProviderMachineHealthy)
				} else {
					conditions.MarkFalse(infraMachine, v1alpha1.ProviderMachineHealthy, "NotHealthy", clusterv1.ConditionSeverityWarning, "%s", errors.Join(errs...).Error())
				}

				infraMachine.Status.Addresses = nil

				if m.Allocation.Hostname != nil {
					infraMachine.Status.Addresses = append(infraMachine.Status.Addresses, clusterv1.MachineAddress{
						Type:    clusterv1.MachineHostName,
						Address: *m.Allocation.Hostname,
					})
				}

				for _, nw := range m.Allocation.Networks {
					switch ptr.Deref(nw.Networktype, "") {
					case "privateprimaryunshared":
						for _, ip := range nw.Ips {
							infraMachine.Status.Addresses = append(infraMachine.Status.Addresses, clusterv1.MachineAddress{
								Type:    clusterv1.MachineInternalIP,
								Address: ip,
							})
						}
					case "external":
						for _, ip := range nw.Ips {
							infraMachine.Status.Addresses = append(infraMachine.Status.Addresses, clusterv1.MachineAddress{
								Type:    clusterv1.MachineExternalIP,
								Address: ip,
							})
						}
					}
				}
			}
		}

		return err
	})

	go func() {
		for u := range conditionUpdates {
			u()
		}
	}()

	groupErr := g.Wait()
	if groupErr == nil && allConditionsTrue() {
		infraMachine.Status.Ready = true
	}

	err := r.Client.Status().Update(ctx, infraMachine)

	return errors.Join(groupErr, err)
}

func (r *MetalStackMachineReconciler) findProviderMachine(ctx context.Context, infraCluster *v1alpha1.MetalStackCluster, infraMachine *v1alpha1.MetalStackMachine, isControlPlaneMachine bool) (*models.V1MachineResponse, error) {
	mfr := &models.V1MachineFindRequest{
		ID:                infraMachine.Spec.ProviderID,
		AllocationProject: infraCluster.Spec.ProjectID,
		Tags:              machineTags(infraCluster, infraMachine, isControlPlaneMachine),
	}

	resp, err := r.MetalClient.Machine().FindMachines(metalmachine.NewFindMachinesParamsWithContext(ctx).WithBody(mfr), nil)
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

func machineTags(infraCluster *v1alpha1.MetalStackCluster, infraMachine *v1alpha1.MetalStackMachine, isControlPlaneMachine bool) []string {
	tags := []string{
		tag.New(tag.ClusterID, string(infraCluster.GetUID())),
		tag.New(v1alpha1.TagInfraMachineID, string(infraMachine.GetUID())),
	}

	if isControlPlaneMachine {
		tags = append(tags, v1alpha1.TagControlPlanePurpose)
	}

	return tags
}
