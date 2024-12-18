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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

	fcmv2 "github.com/metal-stack/firewall-controller-manager/api/v2"
	metalgo "github.com/metal-stack/metal-go"
)

// MetalStackClusterReconciler reconciles a MetalStackCluster object
type MetalStackClusterReconciler struct {
	MetalClient metalgo.Client
	Client      client.Client
	Scheme      *runtime.Scheme
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
// +kubebuilder:rbac:groups=firewall.metal-stack.io,resources=firewalldeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete

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
			err = errors.New("cluster is not yet ready, requeueing")
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
		WithEventFilter(predicates.ResourceIsNotExternallyManaged(mgr.GetLogger())).
		// TODO: implement resource paused from cluster-api's predicates?
		Complete(r)
}

func (r *clusterReconciler) reconcile() error {
	sshPubKey, err := r.ensureSshKeyPair(r.ctx)
	if err != nil {
		return fmt.Errorf("unable to ensure ssh key pair: %w", err)
	}

	r.log.Info("reconciled ssh key pair")

	nodeNetworkID, err := r.ensureNodeNetwork()
	if err != nil {
		return fmt.Errorf("unable to ensure node network: %w", err)
	}

	r.log.Info("reconciled node network", "network-id", nodeNetworkID)

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

	fwdeploy, err := r.ensureFirewallDeployment(nodeNetworkID, sshPubKey)
	if err != nil {
		return fmt.Errorf("unable to ensure firewall deployment: %w", err)
	}

	r.log.Info("reconciled firewall deployment", "name", fwdeploy.Name, "namespace", fwdeploy.Namespace)

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

	err = r.deleteFirewallDeployment()
	if err != nil {
		return fmt.Errorf("unable to delete firewall deployment: %w", err)
	}

	r.log.Info("deleted firewall deployment")

	err = r.deleteControlPlaneIP()
	if err != nil {
		return fmt.Errorf("unable to delete control plane ip: %w", err)
	}

	r.log.Info("deleted control plane ip")

	err = r.deleteNodeNetwork()
	if err != nil {
		return fmt.Errorf("unable to delete node network: %w", err)
	}

	r.log.Info("deleted node network")

	err = r.deleteSshKeyPair(r.ctx)
	if err != nil {
		return fmt.Errorf("unable to delete ssh key pair: %w", err)
	}

	r.log.Info("deleted ssh key pair")

	return err
}

func (r *clusterReconciler) ensureSshKeyPair(ctx context.Context) (string, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.cluster.Name + "-ssh-keypair",
			Namespace: r.cluster.Namespace,
		},
	}

	err := r.client.Get(ctx, client.ObjectKeyFromObject(secret), secret)
	if err == nil {
		if key, ok := secret.Data["id_rsa.pub"]; ok {
			return string(key), nil
		}
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", err
	}

	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}

	// generate and write public key
	pubKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", err
	}

	secret.Data = map[string][]byte{
		"id_rsa":     pem.EncodeToMemory(privateKeyBlock),
		"id_rsa.pub": ssh.MarshalAuthorizedKey(pubKey),
	}

	err = r.client.Create(ctx, secret)
	if err != nil {
		return "", err
	}

	return string(ssh.MarshalAuthorizedKey(pubKey)), nil
}

func (r *clusterReconciler) deleteSshKeyPair(ctx context.Context) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ssh-keypair-" + r.cluster.Name,
			Namespace: r.cluster.Namespace,
		},
	}

	err := r.client.Delete(ctx, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	return nil
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

		return nil
	default:
		return errors.New("more than a single node network exists for this cluster, operator investigation is required")
	}
}

func (r *clusterReconciler) findNodeNetwork() ([]*models.V1NetworkResponse, error) {
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
	ips, err := r.findControlPlaneIP()
	if err != nil {
		return nil, err
	}

	switch len(ips) {
	case 0:
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
			Type: ptr.To(models.V1IPBaseTypeStatic),
		}).WithContext(r.ctx), nil)
		if err != nil {
			return nil, fmt.Errorf("error creating ip: %w", err)
		}

		return resp.Payload, nil
	case 1:
		return ips[0], nil
	default:
		return nil, fmt.Errorf("more than a single control plane ip exists for this cluster, operator investigation is required")
	}
}

func (r *clusterReconciler) deleteControlPlaneIP() error {
	ips, err := r.findControlPlaneIP()
	if err != nil {
		return err
	}

	switch len(ips) {
	case 0:
		return nil
	case 1:
		ip := ips[0]

		if ip.Ipaddress == nil {
			return fmt.Errorf("control plane ip address not set")
		}

		_, err := r.metalClient.IP().FreeIP(ipmodels.NewFreeIPParams().WithID(*ip.Ipaddress).WithContext(r.ctx), nil)
		if err != nil {
			return err
		}

		return nil
	default:
		return fmt.Errorf("more than a single control plane ip exists for this cluster, operator investigation is required")
	}
}

func (r *clusterReconciler) findControlPlaneIP() ([]*models.V1IPResponse, error) {
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

	return resp.Payload, nil
}

func (r *clusterReconciler) ensureFirewallDeployment(nodeNetworkID, sshPubKey string) (*fcmv2.FirewallDeployment, error) {
	deploy := &fcmv2.FirewallDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.infraCluster.Name,
			Namespace: r.infraCluster.Namespace,
		},
		Spec: fcmv2.FirewallDeploymentSpec{
			Template: fcmv2.FirewallTemplateSpec{
				Spec: fcmv2.FirewallSpec{
					Partition: r.infraCluster.Spec.Partition,
					Project:   r.infraCluster.Spec.ProjectID,
				},
			},
		},
	}

	_, err := controllerutil.CreateOrUpdate(r.ctx, r.client, deploy, func() error {
		if deploy.Annotations == nil {
			deploy.Annotations = map[string]string{}
		}
		deploy.Annotations[fcmv2.ReconcileAnnotation] = strconv.FormatBool(true)
		deploy.Annotations[fcmv2.FirewallNoControllerConnectionAnnotation] = strconv.FormatBool(true)

		if deploy.Labels == nil {
			deploy.Labels = map[string]string{}
		}

		deploy.Spec.Replicas = 1
		deploy.Spec.Selector = map[string]string{
			tag.ClusterID: string(r.infraCluster.GetUID()),
		}

		deploy.Spec.Template.Spec.InitialRuleSet = &fcmv2.InitialRuleSet{
			Egress: []fcmv2.EgressRule{
				{
					Comment:  "allow outgoing http",
					Ports:    []int32{80},
					Protocol: fcmv2.NetworkProtocolTCP,
					To:       []string{"0.0.0.0/0"},
				},
				{
					Comment:  "allow outgoing https",
					Ports:    []int32{443},
					Protocol: fcmv2.NetworkProtocolTCP,
					To:       []string{"0.0.0.0/0"},
				},
				{
					Comment:  "allow outgoing dns via tcp",
					Ports:    []int32{53},
					Protocol: fcmv2.NetworkProtocolTCP,
					To:       []string{"0.0.0.0/0"},
				},
				{
					Comment:  "allow outgoing dns and ntp via udp",
					Ports:    []int32{53, 123},
					Protocol: fcmv2.NetworkProtocolUDP,
					To:       []string{"0.0.0.0/0"},
				},
			},
			Ingress: []fcmv2.IngressRule{
				{
					Comment:  "allow incoming ssh",
					Ports:    []int32{22},
					Protocol: fcmv2.NetworkProtocolTCP,
					From:     []string{"0.0.0.0/0"}, // TODO: restrict cidr
				},
				{
					Comment:  "allow incoming https to kube-apiserver",
					Ports:    []int32{443},
					Protocol: fcmv2.NetworkProtocolTCP,
					From:     []string{"0.0.0.0/0"}, // TODO: restrict cidr
				},
			},
		}

		if deploy.Spec.Template.Labels == nil {
			deploy.Spec.Template.Labels = map[string]string{}
		}
		deploy.Spec.Template.Labels[tag.ClusterID] = string(r.infraCluster.GetUID())

		deploy.Spec.Template.Spec.Size = r.infraCluster.Spec.Firewall.Size
		deploy.Spec.Template.Spec.Image = r.infraCluster.Spec.Firewall.Image
		deploy.Spec.Template.Spec.Networks = append(r.infraCluster.Spec.Firewall.AdditionalNetworks, nodeNetworkID)
		deploy.Spec.Template.Spec.RateLimits = r.infraCluster.Spec.Firewall.RateLimits
		deploy.Spec.Template.Spec.EgressRules = r.infraCluster.Spec.Firewall.EgressRules
		deploy.Spec.Template.Spec.LogAcceptedConnections = ptr.Deref(r.infraCluster.Spec.Firewall.LogAcceptedConnections, false)

		// TODO: this needs to be a controller configuration
		deploy.Spec.Template.Spec.InternalPrefixes = nil
		deploy.Spec.Template.Spec.ControllerVersion = "v2.3.5"                                                                   // TODO: this needs to be a controller configuration
		deploy.Spec.Template.Spec.ControllerURL = "https://images.metal-stack.io/firewall-controller/v2.3.5/firewall-controller" // TODO: this needs to be a controller configuration
		deploy.Spec.Template.Spec.NftablesExporterVersion = ""
		deploy.Spec.Template.Spec.NftablesExporterURL = ""

		// TODO: we need to allow internet connection for the nodes before the firewall-controller can connect to the control-plane
		// the FCM currently does not support this
		deploy.Spec.Template.Spec.Userdata = "{}"
		deploy.Spec.Template.Spec.SSHPublicKeys = []string{sshPubKey}

		// TODO: consider auto update machine image feature

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error creating firewall deployment: %w", err)
	}

	return deploy, nil
}

func (r *clusterReconciler) deleteFirewallDeployment() error {
	// TODO: consider a retry here, which is actually anti-pattern but in this case could be beneficial

	deploy := &fcmv2.FirewallDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.infraCluster.Name,
			Namespace: r.infraCluster.Namespace,
		},
	}

	err := r.client.Get(r.ctx, client.ObjectKeyFromObject(deploy), deploy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("error getting firewall deployment: %w", err)
	}

	if deploy.DeletionTimestamp == nil {
		err = r.client.Delete(r.ctx, deploy)
		if err != nil {
			return fmt.Errorf("error deleting firewall deployment: %w", err)
		}

		return errors.New("firewall deployment was deleted, process is still ongoing")
	}

	return errors.New("firewall deployment is still ongoing")
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

	defer func() {
		close(conditionUpdates)
	}()

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
		ips, err := r.findControlPlaneIP()

		conditionUpdates <- func() {
			if err != nil {
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
				return
			}

			switch len(ips) {
			case 0:
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "NotCreated", clusterv1.ConditionSeverityError, "control plane ip was not yet created")
			case 1:
				if r.infraCluster.Spec.ControlPlaneEndpoint.Host == *ips[0].Ipaddress {
					conditions.MarkTrue(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured)
				} else {
					conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "NotSet", clusterv1.ConditionSeverityWarning, "control plane ip was not yet patched into the cluster's spec")
				}
			default:
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterControlPlaneEndpointEnsured, "InternalError", clusterv1.ConditionSeverityError, "more than a single control plane ip exists for this cluster, operator investigation is required")
			}
		}

		return err
	})

	g.Go(func() error {
		deploy := &fcmv2.FirewallDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.infraCluster.Name,
				Namespace: r.infraCluster.Namespace,
			},
		}

		err := r.client.Get(r.ctx, client.ObjectKeyFromObject(deploy), deploy)

		conditionUpdates <- func() {
			if err != nil && !apierrors.IsNotFound(err) {
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterFirewallDeploymentReady, "InternalError", clusterv1.ConditionSeverityError, "%s", err.Error())
				return
			}

			if apierrors.IsNotFound(err) {
				conditions.MarkFalse(r.infraCluster, v1alpha1.ClusterFirewallDeploymentReady, "NotCreated", clusterv1.ConditionSeverityError, "firewall deployment was not yet created")
				return
			}

			// switch {
			// case deploy.Spec.Replicas == deploy.Status.ReadyReplicas:
			conditions.MarkTrue(r.infraCluster, v1alpha1.ClusterFirewallDeploymentReady)
			// default:
			// 	conditions.MarkFalse(infraCluster, v1alpha1.ClusterFirewallDeploymentReady, "Unhealthy", clusterv1.ConditionSeverityWarning, "not all firewalls are healthy")
			// }
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
		r.infraCluster.Status.Ready = true
	}

	err := r.client.Status().Update(r.ctx, r.infraCluster)

	return errors.Join(groupErr, err)
}
