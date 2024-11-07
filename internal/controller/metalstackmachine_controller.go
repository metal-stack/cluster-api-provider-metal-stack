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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	infrastructurev1alpha1 "github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	metalgo "github.com/metal-stack/metal-go"
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

	infraCluster := &v1alpha1.MetalStackCluster{}
	infraClusterKey := client.ObjectKey{
		Namespace: cluster.Spec.InfrastructureRef.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	err = r.Client.Get(ctx, infraClusterKey, infraCluster)
	if apierrors.IsNotFound(err) {
		log.Info("infrastructure cluster no longer exists")
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		statusErr := r.status(ctx, infraMachine, infraCluster)
		if statusErr != nil {
			err = errors.Join(err, fmt.Errorf("unable to update status: %w", statusErr))
		}
	}()

	if !infraMachine.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(infraMachine, v1alpha1.MachineFinalizer) {
			return ctrl.Result{}, nil
		}

		log.Info("reconciling resource deletion flow")
		err := r.delete(ctx, log, infraMachine, infraCluster)
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

	if infraMachine.Spec.ProviderID == "" {
		log.Info("creating provider machine")
		err = r.create(ctx, log, infraMachine, infraCluster)
	} else {
		err = r.reconcile(ctx, log, infraMachine, infraCluster)
	}

	return ctrl.Result{}, err // remember to return err here and not nil because the defer func can influence this
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalStackMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1alpha1.MetalStackMachine{}).
		Named("metalstackmachine").
		Complete(r)
}

func (r *MetalStackMachineReconciler) create(ctx context.Context, _ logr.Logger, infraMachine *v1alpha1.MetalStackMachine, infraCluster *v1alpha1.MetalStackCluster) error {
	helper, err := patch.NewHelper(infraMachine, r.Client)
	if err != nil {
		return err
	}

	// TODO: Find any existing machine by tag first
	const TagInfraMachineID = "machine.metal-stack.infrastructure.cluster.x-k8s.io/id"
	infraMachineOwnerTag := fmt.Sprintf("%s=%s", TagInfraMachineID, infraMachine.GetUID())

	networks := []*models.V1MachineAllocationNetwork{
		// TODO:
		// {
		// 	Autoacquire: ptr.To(true),
		// 	Networkid:   new(string),
		// },
	}

	resp, err := r.MetalClient.Machine().AllocateMachine(metalmachine.NewAllocateMachineParamsWithContext(ctx).WithBody(&models.V1MachineAllocateRequest{
		Partitionid:   &infraCluster.Spec.Partition,
		Projectid:     &infraCluster.Spec.ProjectID,
		PlacementTags: []string{fmt.Sprintf("%s=%s", tag.ClusterID, infraCluster.GetUID())},

		Tags:        []string{infraMachineOwnerTag}, // TODO: more tags!
		Name:        infraMachine.Name,
		Hostname:    infraMachine.Name,
		Sizeid:      &infraMachine.Spec.Size,
		Imageid:     &infraMachine.Spec.Image,
		Description: fmt.Sprintf("%s/%s for cluster %s/%s", infraMachine.Namespace, infraMachine.Name, infraCluster.Namespace, infraCluster.Name),
		Networks:    networks,
		// TODO: UserData, SSHPubKeys, Tags, ...
	}), nil)
	if err != nil {
		return errors.New("failed to allocate machine")
	}

	if resp.Payload.ID == nil {
		return errors.New("failed to allocate machine, got no provider ID")
	}
	providerID := *resp.Payload.ID

	infraMachine.Spec.ProviderID = providerID

	err = helper.Patch(ctx, infraMachine)
	if err != nil {
		// TODO:
		return fmt.Errorf("failed to update infra machine provider ID %q, eventually manual deletion is required, %w", providerID, err)
	}
	return nil
}

func (r *MetalStackMachineReconciler) reconcile(ctx context.Context, _ logr.Logger, infraMachine *v1alpha1.MetalStackMachine, infraCluster *v1alpha1.MetalStackCluster) error {
	err := r.findProviderMachine(ctx, infraMachine, infraCluster)
	if err != nil && !errors.Is(err, errProviderMachineNotFound) {
		return err
	}
	if !errors.Is(err, errProviderMachineNotFound) {
		return nil
	}

	return errors.New("metal-stack provider machine is gone, but should not have been freed")
}

func (r *MetalStackMachineReconciler) delete(ctx context.Context, log logr.Logger, infraMachine *v1alpha1.MetalStackMachine, infraCluster *v1alpha1.MetalStackCluster) error {
	err := r.findProviderMachine(ctx, infraMachine, infraCluster)
	if errors.Is(err, errProviderMachineNotFound) {
		// metal-stack machine already freed
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete metal-stack machine due: %w", err)
	}

	_, err = r.MetalClient.Machine().FreeMachine(metalmachine.NewFreeMachineParamsWithContext(ctx).WithID(infraMachine.Spec.ProviderID), nil)
	if err != nil {
		return fmt.Errorf("failed to delete metal-stack machine due: %w", err)
	}
	log.Info("freed metal-stack machine")
	return nil
}

func (r *MetalStackMachineReconciler) status(_ context.Context, _ *v1alpha1.MetalStackMachine, _ *v1alpha1.MetalStackCluster) error {
	return nil
}

func (r *MetalStackMachineReconciler) findProviderMachine(ctx context.Context, infraMachine *v1alpha1.MetalStackMachine, infraCluster *v1alpha1.MetalStackCluster) error {
	mfr := &models.V1MachineFindRequest{
		ID:                infraMachine.Spec.ProviderID,
		AllocationProject: infraCluster.Spec.ProjectID,
		Tags:              []string{fmt.Sprintf("%s%s", tag.ClusterID, infraCluster.GetUID())},
	}

	resp, err := r.MetalClient.Machine().FindMachines(metalmachine.NewFindMachinesParamsWithContext(ctx).WithBody(mfr), nil)
	if err != nil {
		return err
	}

	switch len(resp.Payload) {
	case 0:
		// metal-stack machine already freed
		return errProviderMachineNotFound
	case 1:
		return nil
	default:
		return errProviderMachineTooManyFound
	}
}
