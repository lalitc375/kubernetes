//go:build goexperiment.arenas

package arenatyped

import (
	"arena"
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestCloneEquality(t *testing.T) {
	a := arena.NewArena()
	defer a.Free()

	src := samplePod(1)
	clone := CloneToArena(a, src)

	if !apiequality.Semantic.DeepEqual(src, clone) {
		t.Fatalf("clone is not semantically equal to source")
	}
	sj, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	cj, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sj, cj) {
		t.Fatalf("clone JSON differs:\n%s\nvs\n%s", sj, cj)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	a := arena.NewArena()
	defer a.Free()

	src := samplePod(1)
	clone := CloneToArena(a, src)

	src.Labels["app"] = "changed"
	src.Spec.Containers[0].Image = "changed:latest"
	src.Status.Conditions[0].Status = corev1.ConditionFalse

	if clone.Labels["app"] != "web" {
		t.Errorf("clone labels affected by source mutation: %v", clone.Labels)
	}
	if clone.Spec.Containers[0].Image != "nginx:1.27" {
		t.Errorf("clone containers affected by source mutation: %q", clone.Spec.Containers[0].Image)
	}
	if clone.Status.Conditions[0].Status != corev1.ConditionTrue {
		t.Errorf("clone conditions affected by source mutation")
	}

	clone.Labels["tier"] = "backend"
	clone.Spec.Containers[1].Name = "other"
	if src.Labels["tier"] != "frontend" || src.Spec.Containers[1].Name != "sidecar" {
		t.Errorf("source affected by clone mutation")
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

	heapBytes, heapGC := measureRetained(func() interface{} {
		out := make([]*corev1.Pod, n)
		for i := range pods {
			out[i] = pods[i].DeepCopy()
		}
		return out
	})

	measureRetained(func() interface{} { return nil })

	a := arena.NewArena()
	arenaBytes, arenaGC := measureRetained(func() interface{} {
		out := make([]*corev1.Pod, n)
		for i := range pods {
			out[i] = CloneToArena(a, pods[i])
		}
		return out
	})

	t.Logf("%d typed pods on heap:  retained %d KiB, avg full GC %v", n, heapBytes/1024, heapGC)
	t.Logf("%d typed pods in arena: retained %d KiB (maps + unexported pins), avg full GC %v", n, arenaBytes/1024, arenaGC)

	start := time.Now()
	a.Free()
	t.Logf("arena.Free() of %d typed pods: %v", n, time.Since(start))

	// Without this, pods' last use is inside the second closure and the GC
	// reclaims the sources mid-measurement, skewing the delta negative.
	runtime.KeepAlive(pods)
}

func BenchmarkDeepCopy(b *testing.B) {
	pod := samplePod(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pod.DeepCopy()
	}
}

func BenchmarkCloneToArena(b *testing.B) {
	pod := samplePod(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := arena.NewArena()
		CloneToArena(a, pod)
		a.Free()
	}
}
