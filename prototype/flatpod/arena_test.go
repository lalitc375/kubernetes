//go:build goexperiment.arenas

package flatpod

import (
	"arena"
	"runtime"
	"testing"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

func TestArenaRoundTrip(t *testing.T) {
	a := arena.NewArena()
	defer a.Free()

	pod := samplePod(1)
	f, err := FromPodArena(a, pod)
	if err != nil {
		t.Fatalf("FromPodArena: %v", err)
	}

	if v, ok := f.GetString("spec", "containers", "0", "image"); !ok || v != "nginx:1.27" {
		t.Errorf("image = %q, %v; want nginx:1.27", v, ok)
	}
	got, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if !apiequality.Semantic.DeepEqual(pod, got) {
		t.Fatalf("round-trip through arena mismatch")
	}
}

// Strings returned by GetString are copies, so they must survive Free.
func TestArenaStringOutlivesFree(t *testing.T) {
	a := arena.NewArena()
	f, err := FromPodArena(a, samplePod(1))
	if err != nil {
		t.Fatalf("FromPodArena: %v", err)
	}
	name, ok := f.GetString("metadata", "name")
	a.Free()
	if !ok || name != "web-1" {
		t.Errorf("name = %q, %v; want web-1", name, ok)
	}
}

// Mutation of an arena-backed Flat is memory-safe: every mutation path
// appends past the exact-capacity arena slices, so the slices reallocate
// onto the regular GC heap (spilling out of the arena, by design).
func TestArenaMutationSpills(t *testing.T) {
	a := arena.NewArena()
	defer a.Free()

	f, err := FromPodArena(a, samplePod(1))
	if err != nil {
		t.Fatalf("FromPodArena: %v", err)
	}
	if err := f.SetString("nginx:1.28", "spec", "containers", "0", "image"); err != nil {
		t.Fatalf("SetString: %v", err)
	}
	if v, ok := f.GetString("spec", "containers", "0", "image"); !ok || v != "nginx:1.28" {
		t.Errorf("image after set = %q, %v; want nginx:1.28", v, ok)
	}
	pod, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if pod.Spec.Containers[0].Image != "nginx:1.28" {
		t.Errorf("ToPod image = %q", pod.Spec.Containers[0].Image)
	}
}

func TestArenaRetainedHeapAndGC(t *testing.T) {
	const n = 1000
	src := make([]*Flat, 0, n)
	for i := 0; i < n; i++ {
		f, err := FromPod(samplePod(i))
		if err != nil {
			t.Fatalf("FromPod: %v", err)
		}
		src = append(src, f)
	}

	heapBytes, heapGC := measureRetained(func() interface{} {
		out := make([]*Flat, n)
		for i, f := range src {
			c := &Flat{nodes: append([]node(nil), f.nodes...), data: append([]byte(nil), f.data...)}
			out[i] = c
		}
		return out
	})

	measureRetained(func() interface{} { return nil })

	a := arena.NewArena()
	arenaBytes, arenaGC := measureRetained(func() interface{} {
		out := make([]*Flat, n)
		for i, f := range src {
			out[i] = f.CloneToArena(a)
		}
		return out
	})

	t.Logf("%d pods as heap Flat:  retained %d KiB, avg full GC %v", n, heapBytes/1024, heapGC)
	t.Logf("%d pods as arena Flat: retained %d KiB, avg full GC %v", n, arenaBytes/1024, arenaGC)

	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	a.Free()
	freeDur := time.Since(start)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("arena.Free() of all %d pods: %v, heap dropped %d KiB",
		n, freeDur, (int64(before.HeapAlloc)-int64(after.HeapAlloc))/1024)

	runtime.KeepAlive(src)
}

// Batch lifecycle: build a batch of 100 pods, then drop it. The arena
// version frees deterministically; the heap version leaves the work to GC.
func BenchmarkArenaBatchLifecycle(b *testing.B) {
	pods := make([]*Flat, 100)
	for i := range pods {
		f, err := FromPod(samplePod(i))
		if err != nil {
			b.Fatal(err)
		}
		pods[i] = f
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := arena.NewArena()
		for _, f := range pods {
			f.CloneToArena(a)
		}
		a.Free()
	}
}

func BenchmarkHeapBatchLifecycle(b *testing.B) {
	pods := make([]*Flat, 100)
	for i := range pods {
		f, err := FromPod(samplePod(i))
		if err != nil {
			b.Fatal(err)
		}
		pods[i] = f
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := make([]*Flat, len(pods))
		for j, f := range pods {
			out[j] = &Flat{nodes: append([]node(nil), f.nodes...), data: append([]byte(nil), f.data...)}
		}
		runtime.KeepAlive(out)
	}
}
