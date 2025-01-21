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

	"golang.org/x/sync/errgroup"
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

	defer func() {
		statusErr := reconciler.status()
		if statusErr != nil {
			err = errors.Join(err, fmt.Errorf("unable to update status: %w", statusErr))
		} else if !reconciler.infraMachine.Status.Ready {
			err = errors.New("machine is not yet ready, requeuing")
		}
	}()

	if !infraMachine.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(infraMachine, v1alpha1.MachineFinalizer) {
			return ctrl.Result{}, nil
		}

		log.Info("reconciling resource deletion flow")
		err := reconciler.delete()
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
		return ctrl.Result{}, errors.New("waiting until node network id was set to infrastructure cluster status")
	}

	if infraCluster.Spec.ControlPlaneEndpoint.Host == "" {
		return ctrl.Result{}, errors.New("waiting until control plane ip was set to infrastructure cluster spec")
	}

	if machine.Spec.Bootstrap.DataSecretName == nil {
		return ctrl.Result{}, errors.New("waiting until bootstrap data secret was created")
	}

	err = reconciler.reconcile()

	return ctrl.Result{}, err // remember to return err here and not nil because the defer func can influence this
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalStackMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1alpha1.MetalStackMachine{}).
		Named("metalstackmachine").
		Complete(r)
}

func (r *machineReconciler) reconcile() error {
	m, err := r.findProviderMachine()
	if err != nil && !errors.Is(err, errProviderMachineNotFound) {
		return err
	}

	if errors.Is(err, errProviderMachineNotFound) {
		m, err = r.create()
		if err != nil {
			return fmt.Errorf("unable to create machine at provider: %w", err)
		}
	}

	if m.ID == nil {
		return errors.New("machine allocated but got no provider ID")
	}

	r.log.Info("setting provider id into machine resource")

	helper, err := patch.NewHelper(r.infraMachine, r.client)
	if err != nil {
		return err
	}

	r.infraMachine.Spec.ProviderID = "metal://" + *m.ID

	err = helper.Patch(r.ctx, r.infraMachine) // TODO:check whether patch is not executed when no changes occur
	if err != nil {
		return fmt.Errorf("failed to update infra machine provider ID %q: %w", r.infraMachine.Spec.ProviderID, err)
	}

	return nil
}

func (r *machineReconciler) delete() error {
	m, err := r.findProviderMachine()
	if errors.Is(err, errProviderMachineNotFound) {
		// metal-stack machine already freed
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

	sshSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.infraCluster.Name + "-ssh-keypair",
			Namespace: r.infraCluster.Namespace,
		},
	}
	err = r.client.Get(r.ctx, client.ObjectKeyFromObject(sshSecret), sshSecret)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch ssh secret: %w", err)
	}

	sshPubKey, ok := sshSecret.Data["id_rsa.pub"]
	if !ok {
		return nil, errors.New("ssh secret does not contain public key")
	}

	var (
		ips []string
		nws = []*models.V1MachineAllocationNetwork{
			{
				Autoacquire: ptr.To(true),
				Networkid:   r.infraCluster.Status.NodeNetworkID,
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
		SSHPubKeys:    []string{string(sshPubKey)},
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate machine: %w", err)
	}

	return resp.Payload, nil
}

func (r *machineReconciler) status() error {
	var (
		g, _             = errgroup.WithContext(r.ctx)
		conditionUpdates = make(chan func())

		// TODO: probably there is a helper for this available somewhere?
		allConditionsTrue = func() bool {
			for _, c := range r.infraMachine.Status.Conditions {
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
		m, err := r.findProviderMachine()

		conditionUpdates <- func() {
			switch {
			case err != nil && !errors.Is(err, errProviderMachineNotFound):
				conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineCreated, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
				conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineHealthy, "NotHealthy", clusterv1.ConditionSeverityWarning, "machine not created")
				conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineReady, "NotReady", clusterv1.ConditionSeverityWarning, "machine not created")
			case err != nil && errors.Is(err, errProviderMachineNotFound):
				conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineCreated, "NotCreated", clusterv1.ConditionSeverityError, "%s", err.Error())
				conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineHealthy, "NotHealthy", clusterv1.ConditionSeverityWarning, "machine not created")
				conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineReady, "NotReady", clusterv1.ConditionSeverityWarning, "machine not created")
			default:
				if r.infraMachine.Spec.ProviderID == "metal://"+*m.ID {
					conditions.MarkTrue(r.infraMachine, v1alpha1.ProviderMachineCreated)
				} else {
					conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineCreated, "NotSet", clusterv1.ConditionSeverityWarning, "provider id was not yet patched into the machine's spec")
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

				if m.Events != nil && len(m.Events.Log) > 0 && ptr.Deref(m.Events.Log[0].Event, "") == "Phoned Home" {
					conditions.MarkTrue(r.infraMachine, v1alpha1.ProviderMachineReady)
				} else {
					conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineReady, "NotReady", clusterv1.ConditionSeverityWarning, "machine is not in phoned home state")
				}

				if len(errs) == 0 {
					conditions.MarkTrue(r.infraMachine, v1alpha1.ProviderMachineHealthy)
				} else {
					conditions.MarkFalse(r.infraMachine, v1alpha1.ProviderMachineHealthy, "NotHealthy", clusterv1.ConditionSeverityWarning, "%s", errors.Join(errs...).Error())
				}

				r.infraMachine.Status.Addresses = nil

				if m.Allocation.Hostname != nil {
					r.infraMachine.Status.Addresses = append(r.infraMachine.Status.Addresses, clusterv1.MachineAddress{
						Type:    clusterv1.MachineHostName,
						Address: *m.Allocation.Hostname,
					})
				}

				for _, nw := range m.Allocation.Networks {
					switch ptr.Deref(nw.Networktype, "") {
					case "privateprimaryunshared":
						for _, ip := range nw.Ips {
							r.infraMachine.Status.Addresses = append(r.infraMachine.Status.Addresses, clusterv1.MachineAddress{
								Type:    clusterv1.MachineInternalIP,
								Address: ip,
							})
						}
					case "external":
						for _, ip := range nw.Ips {
							r.infraMachine.Status.Addresses = append(r.infraMachine.Status.Addresses, clusterv1.MachineAddress{
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
		r.infraMachine.Status.Ready = true
	}

	err := r.client.Status().Update(r.ctx, r.infraMachine)

	return errors.Join(groupErr, err)
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
