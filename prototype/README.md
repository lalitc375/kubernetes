# GC-pressure experiments for Kubernetes object representations

Prototypes exploring how to reduce GC pressure from Kubernetes objects held
in memory (e.g. informer caches), by changing the data representation and/or
backing memory with Go user arenas.

All measurements below are from the reference machine: **Apple M1 (8 cores,
arm64), Go 1.26.0 (Green Tea GC default)**. To evaluate on another machine,
run the commands in each section and compare. Expect machine-dependent
results: Green Tea gains grow with core count and its SIMD scan kernel is
x86 AVX-512 only; arena chunk management costs depend on the OS page fault
path.

## Packages

| Package | What it is |
|---|---|
| `flatpod` | Pointer-free "tape" representation of a Pod (simdjson-style): one node array + one byte buffer, GC never scans inside. Bidirectional Pod conversion, path accessors, mutation via an edit overlay, `Compact()`. |
| `arenaunstructured` | Real `map[string]interface{}` unstructured objects with string bytes, string headers, boxed scalars, and `[]interface{}` backing arrays placed in a Go user arena (manual eface construction). Map skeletons stay on the GC heap — the runtime has no arena API for maps. |
| `arenatyped` | Typed `*corev1.Pod` deep-cloned into an arena via reflection (`reflect.ArenaNew`): structs, pointer targets, slice backing arrays, string bytes. Maps rebuilt on heap; unexported fields keep (GC-pinned) heap references. |
| `flatpod/arena.go` | Flat tape cloned into an arena: retained GC-visible heap ~0. |

Arena code is gated on `GOEXPERIMENT=arenas` (the experiment is frozen
upstream but still ships; measurement only, not for production).

## How to run

```sh
# Correctness + retained-heap/GC measurements (logs numbers):
go test ./prototype/flatpod/ -run TestRetainedHeapAndGC -v
GOEXPERIMENT=arenas go test ./prototype/flatpod/ ./prototype/arenaunstructured/ ./prototype/arenatyped/ -v

# Conversion/read/lifecycle benchmarks:
go test ./prototype/flatpod/ -bench . -benchmem
GOEXPERIMENT=arenas go test ./prototype/flatpod/ -bench BatchLifecycle -benchmem
GOEXPERIMENT=arenas go test ./prototype/arenaunstructured/ ./prototype/arenatyped/ -bench . -benchmem

# GC scaling / Green Tea vs legacy GC comparison (~210 MiB heap; use
# GCBENCH_PODS=N to change scale):
GCBENCH=1 go test ./prototype/flatpod/ -run TestGCScaling -v -count=3
GCBENCH=1 GOEXPERIMENT=nogreenteagc go test ./prototype/flatpod/ -run TestGCScaling -v -count=3
```

## Results: retained memory and GC cost (1000 Pods)

| Representation | Retained GC heap | Avg full GC cycle | Dealloc |
|---|---|---|---|
| Unstructured `map[string]interface{}` | 10.8 MiB | 1.80 ms | GC |
| Unstructured, arena-backed internals | 4.6 MiB (map skeletons) | 0.99 ms | Free() < 1 µs + GC for maps |
| Typed `*corev1.Pod` (heap) | 4.4 MiB | 0.94 ms | GC |
| Typed, arena clone | 2.0 MiB (maps + unexported pins) | 0.78 ms | Free() < 1 µs + GC for maps |
| Flat tape (heap) | 3.6 MiB | 0.57 ms | GC |
| Flat tape, arena clone | ~0 | 0.37 ms | Free() < 1 µs |

Arena memory is still mapped (it lives outside `HeapAlloc`); the gain is
that the GC neither scans nor reclaims it, and a batch frees in one call.

The common floor for both arena variants is the same hard limit: **Go maps
cannot live in arenas** (no `MakeMap` API; hmap headers and buckets are
runtime-internal allocations), and reflection cannot rewrite unexported
fields (`resource.Quantity` internals, `time.Time`).

## Results: CPU (per Pod, Apple M1)

| Operation | ns/op | allocs/op |
|---|---|---|
| Pod → unstructured map | 10,858 | 128 |
| Pod → unstructured map, arena-backed | 17,654 | 159 (31 heap: one per map) |
| Pod DeepCopy (generated) | 843 | 12 |
| Pod → arena clone (reflection) | 4,074 | 43 |
| Pod → Flat | 4,994 | 32 |
| Read one field: Flat / unstructured | 34 / 29 | 0 / 0 |

Construction penalties are mostly prototype artifacts: each arena variant
builds the object on the heap first and then copies it into the arena, and
`arenatyped` uses the reflect interpreter instead of generated code. In the
realistic wire path (decode JSON/protobuf directly into the arena) the
double-pass disappears and construction cost is roughly the decode cost you
pay anyway — arena-aware decoding was the motivating use case of the Go
arena experiment. The intrinsic arena tax (slower per-alloc path + chunk
map/unmap on `Free`) is the smaller share; it shows in flatpod's
BatchLifecycle benchmark (34.8 µs arena vs 22.8 µs heap per 100-pod batch,
2 vs 301 allocs).

## Results: Green Tea GC vs legacy GC (Go 1.25 behavior)

Measured on the same Go 1.26 toolchain with `GOEXPERIMENT=nogreenteagc` as
the A/B switch.

- 1000 pods (~10 MiB): no measurable difference; run-to-run variance (±30%)
  dominates in both modes.
- 20,000 pods (TestGCScaling): maps 210 MiB retained — Green Tea ~24.2 ms
  vs legacy ~25.8 ms per cycle (**~6%**); Flat 72 MiB retained — ~7.1 ms vs
  ~7.6 ms (**~7%**).

Takeaway: on this machine the GC algorithm is second-order versus data
layout — Green Tea buys single-digit percent, while the representation and
lifetime changes buy 55–100%. Both attack the same problem from opposite
ends (Green Tea scans pointer-dense heaps faster; Flat/arenas leave little
to scan), and they compose. Re-measure on a many-core AVX-512 x86 machine,
where Green Tea's advantage should be larger.

## Caveats

- `GOEXPERIMENT=arenas` is frozen upstream and cannot ship in production;
  the supported production analogues are pooled/reused backing slices for
  Flat, or generated arena-free relocation code.
- Nothing reachable from an arena-backed object may be used after
  `Free()`. flatpod copies strings out of accessors (safe); the
  unstructured/typed variants return interior references (not safe).
- Arena-backed unstructured/typed objects must not be mutated in place;
  flatpod supports mutation via its edit overlay.
