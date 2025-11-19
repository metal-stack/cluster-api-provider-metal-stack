package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"

	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"

	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	fcmv2 "github.com/metal-stack/firewall-controller-manager/api/v2"
	"github.com/metal-stack/metal-go/api/client/firewall"
	"github.com/metal-stack/metal-go/api/client/machine"
	"github.com/metal-stack/metal-go/api/models"
	"github.com/metal-stack/metal-lib/pkg/tag"

	metalgo "github.com/metal-stack/metal-go"
)

type MetalStackFirewallDeploymentReconciler struct {
	MetalClient metalgo.Client
	Client      client.Client
}

type firewallDeploymentReconciler struct {
	metalClient        metalgo.Client
	client             client.Client
	ctx                context.Context
	log                logr.Logger
	cluster            *clusterv1.Cluster
	infraCluster       *v1alpha1.MetalStackCluster
	firewallDeployment *v1alpha1.MetalStackFirewallDeployment
	firewallTemplate   *v1alpha1.MetalStackFirewallTemplate
}

//
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackfirewalldeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackfirewalldeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackfirewalldeployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackfirewalltemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metalstackfirewalltemplates/finalizers,verbs=update

func (r *MetalStackFirewallDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		log        = ctrllog.FromContext(ctx)
		fwdeploy   = &v1alpha1.MetalStackFirewallDeployment{}
		fwtemplate = &v1alpha1.MetalStackFirewallTemplate{}
	)

	log.Info("starting reconciliation for metal-stack firewall deployment")

	if err := r.Client.Get(ctx, req.NamespacedName, fwdeploy); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("metal-stack firewall deployment resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	infraCluster, err := GetOwnerMetalStackCluster(ctx, r.Client, fwdeploy.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get owner metal-stack cluster: %w", err)
	}

	if infraCluster == nil {
		log.Info("metal-stack firewall deployment is not associated with a metal-stack cluster yet")
		return ctrl.Result{}, nil
	}

	log = log.WithValues("metalStackCluster", fmt.Sprintf("%s/%s", infraCluster.Namespace, infraCluster.Name))
	cluster, err := util.GetOwnerCluster(ctx, r.Client, infraCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster == nil {
		log.Info("metal-stack firewall deployment is not associated with a cluster yet")
		return ctrl.Result{}, nil
	}
	log = log.WithValues("cluster", fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Name))

	if err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: fwdeploy.Namespace,
		Name:      fwdeploy.Spec.FirewallTemplateRef.Name,
	}, fwtemplate); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("metal-stack firewall template resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	reconciler := &firewallDeploymentReconciler{
		metalClient:        r.MetalClient,
		client:             r.Client,
		ctx:                ctx,
		log:                log,
		cluster:            cluster,
		infraCluster:       infraCluster,
		firewallDeployment: fwdeploy,
		firewallTemplate:   fwtemplate,
	}

	helper, err := patch.NewHelper(fwdeploy, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	isPaused := annotations.IsPaused(cluster, fwdeploy) || annotations.IsPaused(cluster, infraCluster)
	if isPaused {
		conditions.MarkTrue(fwdeploy, v1alpha1.ClusterPaused)
	} else {
		conditions.MarkFalse(fwdeploy, v1alpha1.ClusterPaused, clusterv1.PausedV1Beta2Condition, clusterv1.ConditionSeverityInfo, "")
	}

	switch {
	case isPaused:
		log.Info("reconciliation is paused")
	case !fwdeploy.DeletionTimestamp.IsZero():
		err = reconciler.delete()
	case !controllerutil.ContainsFinalizer(fwdeploy, v1alpha1.FirewallDeploymentFinalizer):
		log.Info("adding finalizer")
		controllerutil.AddFinalizer(fwdeploy, v1alpha1.FirewallDeploymentFinalizer)
	default:
		log.Info("reconciling firewall deployment")
		err = reconciler.reconcile()
	}

	updateErr := helper.Patch(ctx, fwdeploy)
	if updateErr != nil {
		err = errors.Join(err, fmt.Errorf("failed to patch firewall deployment %s/%s: %w", fwdeploy.Namespace, fwdeploy.Name, updateErr))
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalStackFirewallDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := mgr.GetCache().IndexField(context.TODO(), &v1alpha1.MetalStackFirewallDeployment{}, "spec.firewallTemplateRef.name", func(obj client.Object) []string {
		fwdeploy := obj.(*v1alpha1.MetalStackFirewallDeployment)
		if fwdeploy.Spec.FirewallTemplateRef == nil {
			return nil
		}
		return []string{fwdeploy.Spec.FirewallTemplateRef.Name}
	})
	if err != nil {
		panic(err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MetalStackFirewallDeployment{}).
		Named("metalstackfirewalldeployment").
		WithEventFilter(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), mgr.GetLogger())).
		Watches(
			&v1alpha1.MetalStackCluster{},
			handler.EnqueueRequestsFromMapFunc(r.metalStackClusterToMetalStackFirewallDeployment(mgr.GetLogger())),
			builder.WithPredicates(predicates.ClusterUnpaused(mgr.GetScheme(), mgr.GetLogger())),
		).
		Watches(&v1alpha1.MetalStackFirewallTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.metalStackFirewallTemplateToMetalStackFirewallDeployment(mgr.GetLogger())),
			builder.WithPredicates(predicates.ResourceNotPaused(mgr.GetScheme(), mgr.GetLogger())),
		).
		WithEventFilter(predicates.ResourceNotPaused(mgr.GetScheme(), mgr.GetLogger())).
		Complete(r)
}

func (r *MetalStackFirewallDeploymentReconciler) metalStackClusterToMetalStackFirewallDeployment(log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		infraCluster, ok := o.(*v1alpha1.MetalStackCluster)
		if !ok {
			log.Error(fmt.Errorf("expected a metal-stack cluster, got %T", o), "failed to get cluster", "object", o)
			return nil
		}

		if infraCluster.Spec.FirewallDeploymentRef == nil {
			return nil
		}

		return []ctrl.Request{
			{
				NamespacedName: types.NamespacedName{
					Namespace: infraCluster.Namespace,
					Name:      infraCluster.Spec.FirewallDeploymentRef.Name,
				},
			},
		}
	}
}

func (r *MetalStackFirewallDeploymentReconciler) metalStackFirewallTemplateToMetalStackFirewallDeployment(log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []ctrl.Request {
		fwtemp, ok := o.(*v1alpha1.MetalStackFirewallTemplate)
		if !ok {
			log.Error(fmt.Errorf("expected a metal-stack firewall template, got %T", o), "failed to get firewall template", "object", o)
			return nil
		}

		fwdeployList := &v1alpha1.MetalStackFirewallDeploymentList{}
		err := r.Client.List(ctx, fwdeployList, &client.ListOptions{
			Namespace: fwtemp.Namespace,
			FieldSelector: fields.SelectorFromSet(fields.Set{
				"spec.firewallTemplateRef.name": fwtemp.Name,
			}),
		})
		if err != nil {
			log.Error(err, "failed to list firewall deployments for firewall template", "firewallTemplate", fwtemp.Name)
			return nil
		}

		var reqs []ctrl.Request
		for _, fw := range fwdeployList.Items {
			log.Info("queueing firewall deployment reconciliation for firewall template change", "firewallDeployment", fw.Name)
			reqs = append(reqs, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: fw.Namespace,
					Name:      fw.Name,
				},
			})
		}
		return reqs
	}
}

func (r *firewallDeploymentReconciler) reconcile() error {
	if r.infraCluster.Spec.NodeNetworkID == "" {
		conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterFirewallDeploymentEnsured, "MissingNodeNetworkID", clusterv1.ConditionSeverityWarning, "NodeNetworkID is not set on MetalStackCluster")
		return fmt.Errorf("node network ID is not set on MetalStackCluster %s/%s", r.infraCluster.Namespace, r.infraCluster.Name)
	}

	err := r.ensureFirewallTemplateOwnerRefAndFinalizer()
	if err != nil {
		return fmt.Errorf("unable to ensure firewall template owner reference: %w", err)
	}

	if r.firewallDeployment.Spec.ManagedResourceRef == nil {
		err = r.ensureFirewallDeployment()
		if err != nil {
			conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterFirewallDeploymentEnsured, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
			return fmt.Errorf("unable to ensure firewall deployment: %w", err)
		}
		conditions.MarkTrue(r.infraCluster, v1alpha1.ClusterFirewallDeploymentEnsured)
	}

	r.firewallDeployment.Status.Ready = true

	return nil
}

func (r *firewallDeploymentReconciler) delete() error {
	var err error

	if !controllerutil.ContainsFinalizer(r.firewallDeployment, v1alpha1.FirewallDeploymentFinalizer) {
		r.log.Info("finalizer not present, skipping deletion flow")
		return nil
	}

	r.log.Info("reconciling resource deletion flow")

	err = r.ensureAllMetalStackMachinesAreGone()
	if err != nil {
		return err
	}

	err = r.deleteFirewallDeployment()
	if err != nil {
		return err
	}

	r.log.Info("deletion finished, removing finalizer")
	controllerutil.RemoveFinalizer(r.firewallDeployment, v1alpha1.FirewallDeploymentFinalizer)

	return nil
}

func (r *firewallDeploymentReconciler) ensureAllMetalStackMachinesAreGone() error {
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

func (r *firewallDeploymentReconciler) ensureFirewallTemplateOwnerRefAndFinalizer() error {
	ownerref := &metav1.OwnerReference{
		APIVersion: v1alpha1.GroupVersion.String(),
		Kind:       v1alpha1.MetalStackFirewallDeploymentKind,
		Name:       r.firewallDeployment.Name,
		UID:        r.firewallDeployment.UID,
	}
	if util.HasOwnerRef(r.firewallTemplate.OwnerReferences, *ownerref) {
		return nil
	}

	helper, err := patch.NewHelper(r.firewallTemplate, r.client)
	if err != nil {
		return fmt.Errorf("failed to create patch helper for firewall template: %w", err)
	}

	r.firewallTemplate.OwnerReferences = util.EnsureOwnerRef(r.firewallTemplate.OwnerReferences, *ownerref)

	err = helper.Patch(r.ctx, r.firewallTemplate)
	if err != nil {
		return fmt.Errorf("failed to patch firewall template with owner reference: %w", err)
	}

	return nil
}

func (r *firewallDeploymentReconciler) ensureFirewallDeployment() error {
	// TODO: migrate this to actually create a firewall deployment
	// Temporarily we will instead create the firewall manually

	var (
		name = fmt.Sprintf("%s-firewall", r.infraCluster.GetName())
		tags = []string{
			tag.New(tag.ClusterID, r.infraCluster.Spec.NodeNetworkID),
			tag.New(v1alpha1.TagInfraClusterResource, fmt.Sprintf("%s.%s", r.infraCluster.Namespace, r.infraCluster.Name)),
			tag.New(fcmv2.FirewallControllerManagedByAnnotation, "cluster-api-provider-metal-stack"),
			tag.New((v1alpha1.TagFirewallDeploymentResource), fmt.Sprintf("%s.%s", r.firewallDeployment.Namespace, r.firewallDeployment.Name)),
		}
	)

	fwFindResp, err := r.metalClient.Firewall().FindFirewalls(firewall.NewFindFirewallsParamsWithContext(r.ctx).WithBody(&models.V1FirewallFindRequest{
		PartitionID:       r.firewallTemplate.Spec.Partition,
		Sizeid:            r.firewallTemplate.Spec.Size,
		AllocationImageID: r.firewallTemplate.Spec.Image,
		Tags:              tags,
	}), nil)
	if err != nil {
		return fmt.Errorf("error finding firewall deployments: %w", err)
	}

	if len(fwFindResp.Payload) > 1 {
		fwids := make([]string, 0, len(fwFindResp.Payload))
		for _, fw := range fwFindResp.Payload {
			if fw.ID != nil {
				fwids = append(fwids, *fw.ID)
			}
		}
		r.log.Info("multiple firewalls found, manual intervention needed due to manual roll", "firewalls", fwids)
	}

	if len(fwFindResp.Payload) == 1 {
		return nil
	}

	networkIDs := r.firewallTemplate.Spec.Networks
	if !slices.Contains(networkIDs, r.infraCluster.Spec.NodeNetworkID) {
		networkIDs = append(networkIDs, r.infraCluster.Spec.NodeNetworkID)
	}

	networks := make([]*models.V1MachineAllocationNetwork, 0, len(networkIDs))
	for _, n := range networkIDs {
		network := &models.V1MachineAllocationNetwork{
			Networkid:   &n,
			Autoacquire: ptr.To(true),
		}
		networks = append(networks, network)
	}

	egressRules := make([]*models.V1FirewallEgressRule, 0, len(r.firewallTemplate.Spec.InitialRuleSet.Egress))
	for _, er := range r.firewallTemplate.Spec.InitialRuleSet.Egress {
		egressRules = append(egressRules, &models.V1FirewallEgressRule{
			Comment:  er.Comment,
			Ports:    er.Ports,
			Protocol: string(er.Protocol),
			To:       er.To,
		})
	}

	ingressRules := make([]*models.V1FirewallIngressRule, 0, len(r.firewallTemplate.Spec.InitialRuleSet.Ingress))
	for _, ir := range r.firewallTemplate.Spec.InitialRuleSet.Ingress {
		ingressRules = append(ingressRules, &models.V1FirewallIngressRule{
			Comment:  ir.Comment,
			Ports:    ir.Ports,
			Protocol: string(ir.Protocol),
			From:     ir.From,
		})
	}

	fwresp, err := r.metalClient.Firewall().AllocateFirewall(firewall.NewAllocateFirewallParamsWithContext(r.ctx).WithBody(&models.V1FirewallCreateRequest{
		Hostname:    name,
		Name:        name,
		Description: fmt.Sprintf("firewall for cluster %s", r.infraCluster.GetName()),
		Partitionid: ptr.To(r.infraCluster.Spec.Partition),
		Projectid:   &r.infraCluster.Spec.ProjectID,
		Tags:        tags,
		SSHPubKeys:  []string{},
		Networks:    networks,
		Imageid:     ptr.To(r.firewallTemplate.Spec.Image),
		Sizeid:      ptr.To(r.firewallTemplate.Spec.Size),
		FirewallRules: &models.V1FirewallRules{
			Egress:  egressRules,
			Ingress: ingressRules,
		},
	}), nil)
	if err != nil {
		return fmt.Errorf("error creating firewall deployment: %w", err)
	}

	r.log.Info("created firewall deployment", "firewallID", fwresp.Payload.ID)

	r.firewallDeployment.Spec.ManagedResourceRef = &v1alpha1.MetalStackManagedResourceRef{
		Name: fmt.Sprintf("metal://%s/%s", r.infraCluster.Spec.Partition, *fwresp.Payload.ID),
	}

	return nil
}

func (r *firewallDeploymentReconciler) deleteFirewallDeployment() error {
	// TODO: migrate this to actually delete a firewall deployment
	// Temporarily we will instead delete all firewalls manually

	var (
		tags = []string{
			tag.New(tag.ClusterID, r.infraCluster.Spec.NodeNetworkID),
			tag.New(v1alpha1.TagInfraClusterResource, fmt.Sprintf("%s.%s", r.infraCluster.Namespace, r.infraCluster.Name)),
			tag.New(fcmv2.FirewallControllerManagedByAnnotation, "cluster-api-provider-metal-stack"),
			tag.New((v1alpha1.TagFirewallDeploymentResource), fmt.Sprintf("%s.%s", r.firewallDeployment.Namespace, r.firewallDeployment.Name)),
		}
	)

	fwFindResp, err := r.metalClient.Firewall().FindFirewalls(firewall.NewFindFirewallsParamsWithContext(r.ctx).WithBody(&models.V1FirewallFindRequest{
		PartitionID: r.infraCluster.Spec.Partition,
		Tags:        tags,
	}), nil)
	if err != nil {
		return fmt.Errorf("error finding firewall deployments: %w", err)
	}

	if len(fwFindResp.Payload) == 0 {
		return nil
	}

	var errs []error
	for _, fw := range fwFindResp.Payload {
		if fw.ID == nil {
			continue
		}

		_, err := r.metalClient.Machine().FreeMachine(machine.NewFreeMachineParamsWithContext(r.ctx).WithID(*fw.ID), nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting firewall deployment %s: %w", *fw.ID, err))
			continue
		}
		r.log.Info("deleted firewall deployment", "machine-id", *fw.ID)
	}

	return errors.Join(errs...)
}

// GetOwnerMetalStackCluster returns the Cluster object owning the current resource.
func GetOwnerMetalStackCluster(ctx context.Context, c client.Client, obj metav1.ObjectMeta) (*v1alpha1.MetalStackCluster, error) {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind != v1alpha1.MetalStackClusterKind {
			continue
		}
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			return nil, pkgerrors.WithStack(err)
		}
		if gv.Group == v1alpha1.GroupVersion.Group {
			return GetInfraClusterByName(ctx, c, obj.Namespace, ref.Name)
		}
	}
	return nil, nil
}

// GetInfraClusterByName finds and return a Cluster object using the specified params.
func GetInfraClusterByName(ctx context.Context, c client.Client, namespace, name string) (*v1alpha1.MetalStackCluster, error) {
	infraCluster := &v1alpha1.MetalStackCluster{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}

	if err := c.Get(ctx, key, infraCluster); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to get Cluster/%s", name)
	}

	return infraCluster, nil
}
