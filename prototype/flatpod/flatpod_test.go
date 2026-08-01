package flatpod

import (
	"fmt"
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
	pod := samplePod(1)
	f, err := FromPod(pod)
	if err != nil {
		t.Fatalf("FromPod: %v", err)
	}
	got, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if !apiequality.Semantic.DeepEqual(pod, got) {
		t.Fatalf("round-trip mismatch:\nwant: %+v\ngot:  %+v", pod, got)
	}
	t.Logf("flat representation: %d nodes + %d data bytes = %d bytes total, 2 heap allocations",
		len(f.nodes), len(f.data), f.Size())
}

func TestAccessors(t *testing.T) {
	f, err := FromPod(samplePod(1))
	if err != nil {
		t.Fatalf("FromPod: %v", err)
	}

	if v, ok := f.GetString("metadata", "name"); !ok || v != "web-1" {
		t.Errorf("metadata.name = %q, %v; want web-1", v, ok)
	}
	if v, ok := f.GetString("metadata", "labels", "app"); !ok || v != "web" {
		t.Errorf("metadata.labels.app = %q, %v; want web", v, ok)
	}
	if v, ok := f.GetString("spec", "containers", "1", "image"); !ok || v != "envoyproxy/envoy:v1.30" {
		t.Errorf("spec.containers[1].image = %q, %v", v, ok)
	}
	if v, ok := f.GetInt("spec", "containers", "0", "ports", "0", "containerPort"); !ok || v != 8080 {
		t.Errorf("containerPort = %d, %v; want 8080", v, ok)
	}
	if v, ok := f.GetBool("spec", "hostNetwork"); !ok || !v {
		t.Errorf("spec.hostNetwork = %v, %v; want true", v, ok)
	}
	if v, ok := f.GetString("spec", "containers", "0", "resources", "requests", "cpu"); !ok || v != "100m" {
		t.Errorf("requests.cpu = %q, %v; want 100m", v, ok)
	}
	if n, ok := f.ArrayLen("spec", "containers"); !ok || n != 2 {
		t.Errorf("len(spec.containers) = %d, %v; want 2", n, ok)
	}
	if _, ok := f.GetString("metadata", "missing"); ok {
		t.Errorf("lookup of missing key succeeded")
	}
}

// measureRetained reports the retained heap of what build returns and the
// average time of a full GC cycle while that data is live.
func measureRetained(build func() interface{}) (int64, time.Duration) {
	// Several rounds so sync.Pool contents (drained one GC late) and other
	// stragglers from earlier phases don't pollute the baseline.
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

	mapBytes, mapGC := measureRetained(func() interface{} {
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

	flatBytes, flatGC := measureRetained(func() interface{} {
		out := make([]*Flat, n)
		for i := range pods {
			f, err := FromPod(pods[i])
			if err != nil {
				t.Fatalf("FromPod: %v", err)
			}
			out[i] = f
		}
		return out
	})

	t.Logf("%d pods as map[string]interface{}: retained %d KiB, avg full GC %v", n, mapBytes/1024, mapGC)
	t.Logf("%d pods as Flat:                   retained %d KiB, avg full GC %v", n, flatBytes/1024, flatGC)

	// Without this, pods' last use is inside the second closure and the GC
	// reclaims the typed objects mid-measurement, skewing the delta negative.
	runtime.KeepAlive(pods)
}

func BenchmarkConvertPodToFlat(b *testing.B) {
	pod := samplePod(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := FromPod(pod); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvertPodToUnstructuredMap(b *testing.B) {
	pod := samplePod(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(pod); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFlatToPod(b *testing.B) {
	f, err := FromPod(samplePod(1))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := f.ToPod(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnstructuredMapToPod(b *testing.B) {
	m, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(samplePod(1))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pod := &corev1.Pod{}
		if err := kruntime.DefaultUnstructuredConverter.FromUnstructured(m, pod); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadFlat(b *testing.B) {
	f, err := FromPod(samplePod(1))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := f.GetString("metadata", "labels", "app"); !ok {
			b.Fatal("missing")
		}
	}
}

func BenchmarkReadUnstructuredMap(b *testing.B) {
	m, err := kruntime.DefaultUnstructuredConverter.ToUnstructured(samplePod(1))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok, err := unstructured.NestedString(m, "metadata", "labels", "app"); err != nil || !ok {
			b.Fatal("missing")
		}
	}
}
