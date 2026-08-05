//go:build goexperiment.arenas

package flatpod

// Experimental arena-backed storage for Flat. Build and test with:
//
//	GOEXPERIMENT=arenas go test ./prototype/flatpod/ -run Arena -v
//
// The arena experiment is frozen upstream (the proposal is on indefinite
// hold), but the package still ships with the toolchain behind the
// GOEXPERIMENT flag, which makes it usable for experiments like this one —
// not for production code.
//
// Why arenas compose well with Flat and not with map[string]interface{}:
// arenas can only hold memory the caller allocates explicitly (arena.New,
// arena.MakeSlice). A map's buckets are allocated by the runtime and cannot
// be placed in an arena, and every boxed scalar in an unstructured tree is
// a runtime allocation too. Flat's entire retained state is two slices of
// pointer-free elements, which is exactly the shape MakeSlice supports.
//
// Ownership rules:
//   - After a.Free(), any use of a Flat cloned into a crashes or corrupts
//     memory. Strings returned by GetString are copies and remain valid.
//   - One arena should hold a batch of objects (e.g. one list response, one
//     resync). Arenas allocate in 8 MiB chunks, so a per-object arena wastes
//     enormous amounts of memory.
//   - Mutating an arena-backed Flat is memory-safe but the appends spill to
//     the regular GC heap, losing the arena benefit; Compact into a fresh
//     arena instead.

import (
	"arena"

	corev1 "k8s.io/api/core/v1"
)

// CloneToArena copies f's retained state into a. The returned Flat's
// lifetime is bounded by the arena's: it must not be used after a.Free().
func (f *Flat) CloneToArena(a *arena.Arena) *Flat {
	c := arena.New[Flat](a)
	c.nodes = arena.MakeSlice[node](a, len(f.nodes), len(f.nodes))
	copy(c.nodes, f.nodes)
	c.data = arena.MakeSlice[byte](a, len(f.data), len(f.data))
	copy(c.data, f.data)
	if len(f.edits) > 0 {
		c.edits = arena.MakeSlice[edit](a, len(f.edits), len(f.edits))
		copy(c.edits, f.edits)
	}
	return c
}

// FromPodArena converts pod into a Flat whose retained memory lives in a.
// The conversion itself still makes transient GC-heap allocations; only the
// result is arena-resident.
func FromPodArena(a *arena.Arena, pod *corev1.Pod) (*Flat, error) {
	f, err := FromPod(pod)
	if err != nil {
		return nil, err
	}
	return f.CloneToArena(a), nil
}
