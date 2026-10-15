// healthmonitor answers AKS upgrade gate HealthCheckRequests with HealthSignals.
//
// Every request gets a Healthy signal, except requests named by --failed-target
// (Unhealthy) and --timeout-target (left Unknown, so the RP times out).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	healthv1alpha1 "github.com/Azure/aks-health-signal/api/health/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// monitor writes one HealthSignal per HealthCheckRequest.
type monitor struct {
	client.Client
	provider string
	wait     time.Duration
	failed   map[string]bool
	timeout  map[string]bool
}

func (m *monitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hcr := &healthv1alpha1.HealthCheckRequest{}
	if err := m.Get(ctx, req.NamespacedName, hcr); err != nil || !hcr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if left := m.wait - time.Since(hcr.CreationTimestamp.Time); left > 0 {
		return ctrl.Result{RequeueAfter: left}, nil
	}

	hs := &healthv1alpha1.HealthSignal{}
	err := m.Get(ctx, client.ObjectKey{Name: hcr.Name + "-" + m.provider}, hs)
	if apierrors.IsNotFound(err) {
		hs = m.newSignal(hcr)
		err = m.Create(ctx, hs)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	// A signal left over from an earlier request with the same name is still being garbage collected.
	if !metav1.IsControlledBy(hs, hcr) {
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if len(hs.Status.Conditions) > 0 {
		return ctrl.Result{}, nil
	}

	cond := m.verdict(hcr.Name)
	meta.SetStatusCondition(&hs.Status.Conditions, cond)
	if err := m.Status().Update(ctx, hs); err != nil {
		return ctrl.Result{}, err
	}
	log.FromContext(ctx).Info("answered", "hcr", hcr.Name, "scope", hcr.Spec.Scope, "signal", hs.Name, "status", cond.Status, "reason", cond.Reason)
	return ctrl.Result{}, nil
}

func (m *monitor) newSignal(hcr *healthv1alpha1.HealthCheckRequest) *healthv1alpha1.HealthSignal {
	signalType := map[healthv1alpha1.HealthCheckRequestScope]healthv1alpha1.HealthSignalType{
		healthv1alpha1.HealthCheckRequestScopeNode:     healthv1alpha1.NodeHealth,
		healthv1alpha1.HealthCheckRequestScopeNodePool: healthv1alpha1.NodePoolHealth,
		healthv1alpha1.HealthCheckRequestScopeCluster:  healthv1alpha1.ClusterHealth,
	}[hcr.Spec.Scope]
	return &healthv1alpha1.HealthSignal{
		ObjectMeta: metav1.ObjectMeta{
			Name:            hcr.Name + "-" + m.provider,
			Labels:          map[string]string{healthv1alpha1.LabelHealthSignalProvider: m.provider},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(hcr, healthv1alpha1.GroupVersion.WithKind("HealthCheckRequest"))},
		},
		Spec: healthv1alpha1.HealthSignalSpec{Type: signalType},
	}
}

func (m *monitor) verdict(hcrName string) metav1.Condition {
	cond := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Healthy", Message: "reported healthy by " + m.provider}
	switch {
	case m.failed[hcrName]:
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, "Unhealthy", "reported unhealthy by "+m.provider
	case m.timeout[hcrName]:
		cond.Status, cond.Reason, cond.Message = metav1.ConditionUnknown, "InProgress", "never finishes, so the upgrade gate times out"
	}
	return cond
}

// targets parses a comma-separated list of HealthCheckRequest names.
func targets(s string) map[string]bool {
	set := map[string]bool{}
	for _, name := range strings.Split(s, ",") {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = true
		}
	}
	return set
}

func main() {
	// --kubeconfig is registered by controller-runtime; without it, KUBECONFIG or ~/.kube/config is used.
	provider := flag.String("provider-name", "", "provider registered in the UpgradeGatePolicy (required)")
	wait := flag.Duration("wait-time", 30*time.Second, "how long after a HealthCheckRequest appears to create its HealthSignal")
	failed := flag.String("failed-target", "", "comma-separated HealthCheckRequest names to report Unhealthy")
	timeout := flag.String("timeout-target", "", "comma-separated HealthCheckRequest names to leave Unknown, so the RP times out")
	probeAddr := flag.String("health-probe-bind-address", "0", "address for /healthz and /readyz, e.g. :8081 (0 disables)")
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	if *provider == "" {
		fmt.Fprintln(os.Stderr, "--provider-name is required")
		os.Exit(2)
	}

	scheme := runtime.NewScheme()
	if err := healthv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	// Metrics disabled so several monitors (one per provider) can run side by side.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: *probeAddr,
	})
	if err != nil {
		panic(err)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		panic(err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		panic(err)
	}
	m := &monitor{Client: mgr.GetClient(), provider: *provider, wait: *wait, failed: targets(*failed), timeout: targets(*timeout)}
	if err := ctrl.NewControllerManagedBy(mgr).For(&healthv1alpha1.HealthCheckRequest{}).Complete(m); err != nil {
		panic(err)
	}

	ctrl.Log.Info("watching HealthCheckRequests", "provider", m.provider, "waitTime", m.wait, "failedTarget", *failed, "timeoutTarget", *timeout)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		panic(err)
	}
}
