//go:build goexperiment.arenas

package arenaunstructured

import (
	"arena"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
)

func samplePod(i int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("web-%d", i),
			Namespace:   "prod",
			Labels:      map[string]string{"app": "web", "tier": "frontend"},
			Annotations: map[string]string{"checksum/config": "abc123"},
		},
		Spec: corev1.PodSpec{
			HostNetwork:   true,
			NodeName:      "node-1",
			RestartPolicy: corev1.RestartPolicyAlways,
			Containers: []corev1.Container{
				{
					Name:  "web",
					Image: "nginx:1.27",
					Ports: []corev1.ContainerPort{
						{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
					},
					Env: []corev1.EnvVar{{Name: "MODE", Value: "prod"}},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
				{Name: "sidecar", Image: "envoyproxy/envoy:v1.30"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.12",
			Conditions: []corev1.PodCondition{
				{
					Type:               corev1.PodReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
				},
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	a := arena.NewArena()
	defer a.Free()

	pod := samplePod(1)
	plain, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(pod)
	if err != nil {
		t.Fatalf("ToUnstructured: %v", err)
	}
	m, err := ToUnstructuredArena(a, pod)
	if err != nil {
		t.Fatalf("ToUnstructuredArena: %v", err)
	}

	if !reflect.DeepEqual(plain, m) {
		t.Fatalf("arena-backed map differs from plain unstructured map")
	}
	got := &corev1.Pod{}
	if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(m, got); err != nil {
		t.Fatalf("FromUnstructured: %v", err)
	}
	if !apiequality.Semantic.DeepEqual(pod, got) {
		t.Fatalf("round-trip through arena-backed unstructured mismatch")
	}
}

func TestNestedAccessors(t *testing.T) {
	a := arena.NewArena()
	defer a.Free()

	m, err := ToUnstructuredArena(a, samplePod(1))
	if err != nil {
		t.Fatalf("ToUnstructuredArena: %v", err)
	}

	if v, ok, err := unstructured.NestedString(m, "metadata", "name"); err != nil || !ok || v != "web-1" {
		t.Errorf("metadata.name = %q, %v, %v; want web-1", v, ok, err)
	}
	if v, ok, err := unstructured.NestedString(m, "metadata", "labels", "app"); err != nil || !ok || v != "web" {
		t.Errorf("metadata.labels.app = %q, %v, %v; want web", v, ok, err)
	}
	if v, ok, err := unstructured.NestedBool(m, "spec", "hostNetwork"); err != nil || !ok || !v {
		t.Errorf("spec.hostNetwork = %v, %v, %v; want true", v, ok, err)
	}
	containers, ok, err := unstructured.NestedSlice(m, "spec", "containers")
	if err != nil || !ok || len(containers) != 2 {
		t.Fatalf("spec.containers = %d elems, %v, %v; want 2", len(containers), ok, err)
	}
	c0, isMap := containers[0].(map[string]interface{})
	if !isMap {
		t.Fatalf("containers[0] is %T; want map", containers[0])
	}
	if v, ok, err := unstructured.NestedString(c0, "image"); err != nil || !ok || v != "nginx:1.27" {
		t.Errorf("containers[0].image = %q, %v, %v; want nginx:1.27", v, ok, err)
	}
}

// measureRetained reports the retained heap of what build returns and the
// average time of a full GC cycle while that data is live.
func measureRetained(build func() interface{}) (int64, time.Duration) {
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	x := build()
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	const rounds = 5
	start := time.Now()
	for i := 0; i < rounds; i++ {
		runtime.GC()
	}
	gc := time.Since(start) / rounds

	runtime.KeepAlive(x)
	return int64(after.HeapAlloc) - int64(before.HeapAlloc), gc
}

func TestRetainedHeapAndGC(t *testing.T) {
	const n = 1000
	pods := make([]*corev1.Pod, n)
	for i := range pods {
		pods[i] = samplePod(i)
	}

	plainBytes, plainGC := measureRetained(func() interface{} {
		out := make([]map[string]interface{}, n)
		for i := range pods {
			m, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(pods[i])
			if err != nil {
				t.Fatalf("ToUnstructured: %v", err)
			}
			out[i] = m
		}
		return out
	})

	measureRetained(func() interface{} { return nil })

	a := arena.NewArena()
	arenaBytes, arenaGC := measureRetained(func() interface{} {
		out := make([]map[string]interface{}, n)
		for i := range pods {
			m, err := ToUnstructuredArena(a, pods[i])
			if err != nil {
				t.Fatalf("ToUnstructuredArena: %v", err)
			}
			out[i] = m
		}
		return out
	})

	t.Logf("%d pods plain unstructured: retained %d KiB, avg full GC %v", n, plainBytes/1024, plainGC)
	t.Logf("%d pods arena unstructured: retained %d KiB (map skeletons), avg full GC %v", n, arenaBytes/1024, arenaGC)

	start := time.Now()
	a.Free()
	t.Logf("arena.Free() of the non-map memory of %d pods: %v", n, time.Since(start))

	// See flatpod's TestRetainedHeapAndGC: without this, pods' last use is
	// inside the second closure and the GC reclaims the typed objects
	// mid-measurement, skewing the delta negative.
	runtime.KeepAlive(pods)
}

func BenchmarkToUnstructured(b *testing.B) {
	pod := samplePod(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(pod); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkToUnstructuredArena(b *testing.B) {
	pod := samplePod(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := arena.NewArena()
		if _, err := ToUnstructuredArena(a, pod); err != nil {
			b.Fatal(err)
		}
		a.Free()
	}
}

// BenchmarkCloneOnly isolates the clone cost from the typed→unstructured
// conversion it wraps.
func BenchmarkCloneOnly(b *testing.B) {
	m, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(samplePod(1))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := arena.NewArena()
		CloneMap(a, m)
		a.Free()
	}
}
