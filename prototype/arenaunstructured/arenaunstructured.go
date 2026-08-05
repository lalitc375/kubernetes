//go:build goexperiment.arenas

// Package arenaunstructured experiments with backing an unstructured
// Kubernetes object's memory in a Go user arena while keeping the standard
// map[string]interface{} shape, so it stays compatible with
// unstructured.Nested* accessors and the runtime converter.
//
// Build and test with:
//
//	GOEXPERIMENT=arenas go test ./prototype/arenaunstructured/ -v
//
// What moves into the arena:
//   - string byte data, for both map keys and values
//   - string headers and the boxed scalar values (string, int64, float64)
//     that would each normally be their own tiny GC-heap allocation
//   - the backing arrays of []interface{} values, and their slice headers
//
// What CANNOT move into the arena, as a hard limit of the runtime: the maps
// themselves. make(map[string]interface{}) always allocates its hmap header
// and buckets from the GC heap — the arena API has no MakeMap, and Go maps
// grow through runtime-internal allocations that cannot be redirected. Every
// map in the tree therefore remains a GC-heap object.
//
// Ownership rules:
//   - The returned map references arena memory everywhere. After a.Free(),
//     any use of the map — including strings previously returned by
//     unstructured.NestedString, which are NOT copies — is a use after free.
//   - Do not mutate the tree with SetNestedField and friends: writes would
//     mix GC-heap values into arena-backed containers, which is memory-safe
//     but silently loses the arena benefit for those values.
//   - One arena should hold a batch of objects; arenas allocate 8 MiB
//     chunks, so per-object arenas waste memory.
//
// Unlike prototype/flatpod, the GC still scans this representation: arena
// chunks holding pointers (the eface slices, string headers) are traced like
// any heap memory, and the map skeletons are ordinary heap objects. The
// arena removes allocation count and gives batched, immediate reclamation —
// it does not remove mark/scan work. See the test log for measurements.
package arenaunstructured

import (
	"arena"
	"unsafe"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// eface mirrors the runtime layout of an empty interface.
type eface struct{ typ, data unsafe.Pointer }

func typePtr(v interface{}) unsafe.Pointer { return (*eface)(unsafe.Pointer(&v)).typ }

var (
	int64Type   = typePtr(int64(0))
	float64Type = typePtr(float64(0))
	stringType  = typePtr("")
	sliceType   = typePtr([]interface{}(nil))
)

// boxAt builds an interface{} whose payload lives at p (arena memory)
// instead of a fresh GC-heap box. data is written before typ so the GC can
// never observe a typed eface with an uninitialized payload pointer.
func boxAt(typ, p unsafe.Pointer) (i interface{}) {
	e := (*eface)(unsafe.Pointer(&i))
	e.data = p
	e.typ = typ
	return i
}

// arenaString copies s's bytes into the arena and returns a string header
// pointing at them.
func arenaString(a *arena.Arena, s string) string {
	if len(s) == 0 {
		return ""
	}
	b := arena.MakeSlice[byte](a, len(s), len(s))
	copy(b, s)
	return unsafe.String(&b[0], len(b))
}

// CloneValue deep-copies an unstructured value, backing everything except
// map skeletons with arena memory.
func CloneValue(a *arena.Arena, v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return CloneMap(a, t)
	case []interface{}:
		s := arena.New[[]interface{}](a)
		*s = arena.MakeSlice[interface{}](a, len(t), len(t))
		for i := range t {
			(*s)[i] = CloneValue(a, t[i])
		}
		return boxAt(sliceType, unsafe.Pointer(s))
	case string:
		p := arena.New[string](a)
		*p = arenaString(a, t)
		return boxAt(stringType, unsafe.Pointer(p))
	case int64:
		p := arena.New[int64](a)
		*p = t
		return boxAt(int64Type, unsafe.Pointer(p))
	case float64:
		p := arena.New[float64](a)
		*p = t
		return boxAt(float64Type, unsafe.Pointer(p))
	default:
		// bool boxes to static runtime values and nil allocates nothing;
		// anything unexpected passes through unchanged.
		return v
	}
}

// CloneMap deep-copies an unstructured object. The map skeletons are the
// only GC-heap allocations the clone makes.
func CloneMap(a *arena.Arena, m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[arenaString(a, k)] = CloneValue(a, v)
	}
	return out
}

// ToUnstructuredArena converts pod to an unstructured object backed by a.
// The conversion itself still makes transient GC-heap allocations; only the
// result is arena-backed (map skeletons excepted).
func ToUnstructuredArena(a *arena.Arena, pod *corev1.Pod) (map[string]interface{}, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pod)
	if err != nil {
		return nil, err
	}
	return CloneMap(a, m), nil
}
