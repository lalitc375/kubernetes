package flatpod

// GC scaling measurement, used to compare representations and GC algorithms
// (Green Tea vs legacy) at informer-cache-like heap sizes. Skipped unless
// explicitly enabled:
//
//	GCBENCH=1 go test ./prototype/flatpod/ -run TestGCScaling -v -count=3
//	GCBENCH=1 GOEXPERIMENT=nogreenteagc go test ./prototype/flatpod/ -run TestGCScaling -v -count=3
//
// GCBENCH_PODS overrides the object count (default 20000, ~210 MiB as maps).

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
)

func TestGCScaling(t *testing.T) {
	if os.Getenv("GCBENCH") == "" {
		t.Skip("set GCBENCH=1 to run")
	}
	n := 20000
	if s := os.Getenv("GCBENCH_PODS"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v <= 0 {
			t.Fatalf("invalid GCBENCH_PODS %q", s)
		}
		n = v
	}
	pods := make([]*corev1.Pod, n)
	for i := range pods {
		pods[i] = samplePod(i)
	}

	measure := func(name string, build func() interface{}) {
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

		const rounds = 10
		start := time.Now()
		for i := 0; i < rounds; i++ {
			runtime.GC()
		}
		gc := time.Since(start) / rounds
		t.Logf("%d pods, %s: retained %d MiB, avg full GC %v",
			n, name, (int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20), gc)
		runtime.KeepAlive(x)
	}

	measure("maps", func() interface{} {
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

	measure("flat", func() interface{} {
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

	runtime.KeepAlive(pods)
}
