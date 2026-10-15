package main

import (
	"context"
	"testing"
	"time"

	healthv1alpha1 "github.com/Azure/aks-health-signal/api/health/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func hcr(name string, scope healthv1alpha1.HealthCheckRequestScope, age time.Duration) *healthv1alpha1.HealthCheckRequest {
	return &healthv1alpha1.HealthCheckRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID("uid-" + name), CreationTimestamp: metav1.NewTime(time.Now().Add(-age))},
		Spec:       healthv1alpha1.HealthCheckRequestSpec{Scope: scope, TargetRef: healthv1alpha1.TargetRef{Name: name}},
	}
}

func TestReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := healthv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	objs := []client.Object{
		hcr("node-ok", healthv1alpha1.HealthCheckRequestScopeNode, time.Minute),
		hcr("node-bad", healthv1alpha1.HealthCheckRequestScopeNode, time.Minute),
		hcr("node-slow", healthv1alpha1.HealthCheckRequestScopeNode, time.Minute),
		hcr("nodepool-np1", healthv1alpha1.HealthCheckRequestScopeNodePool, time.Minute),
		hcr("node-new", healthv1alpha1.HealthCheckRequestScopeNode, 0),
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithStatusSubresource(&healthv1alpha1.HealthSignal{}).Build()
	m := &monitor{Client: c, provider: "bugbash", wait: 30 * time.Second, failed: targets("node-bad"), timeout: targets("node-slow")}

	for _, tc := range []struct {
		hcr        string
		wantType   healthv1alpha1.HealthSignalType
		wantStatus metav1.ConditionStatus
	}{
		{"node-ok", healthv1alpha1.NodeHealth, metav1.ConditionTrue},
		{"node-bad", healthv1alpha1.NodeHealth, metav1.ConditionFalse},
		{"node-slow", healthv1alpha1.NodeHealth, metav1.ConditionUnknown},
		{"nodepool-np1", healthv1alpha1.NodePoolHealth, metav1.ConditionTrue},
	} {
		t.Run(tc.hcr, func(t *testing.T) {
			if _, err := m.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: tc.hcr}}); err != nil {
				t.Fatal(err)
			}
			hs := &healthv1alpha1.HealthSignal{}
			if err := c.Get(context.Background(), client.ObjectKey{Name: tc.hcr + "-bugbash"}, hs); err != nil {
				t.Fatal(err)
			}
			if hs.Labels[healthv1alpha1.LabelHealthSignalProvider] != "bugbash" || hs.Spec.Type != tc.wantType {
				t.Errorf("label/type = %q/%q", hs.Labels[healthv1alpha1.LabelHealthSignalProvider], hs.Spec.Type)
			}
			if len(hs.OwnerReferences) != 1 || hs.OwnerReferences[0].UID != types.UID("uid-"+tc.hcr) {
				t.Errorf("ownerReferences = %v", hs.OwnerReferences)
			}
			if len(hs.Status.Conditions) != 1 || hs.Status.Conditions[0].Status != tc.wantStatus {
				t.Errorf("conditions = %v, want status %s", hs.Status.Conditions, tc.wantStatus)
			}
		})
	}

	t.Run("waits before creating the signal", func(t *testing.T) {
		res, err := m.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "node-new"}})
		if err != nil || res.RequeueAfter <= 0 {
			t.Fatalf("got %v, %v; want a requeue", res, err)
		}
		if err := c.Get(context.Background(), client.ObjectKey{Name: "node-new-bugbash"}, &healthv1alpha1.HealthSignal{}); err == nil {
			t.Fatal("signal created before --wait-time elapsed")
		}
	})
}
