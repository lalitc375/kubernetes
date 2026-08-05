//go:build goexperiment.arenas

// Package arenatyped experiments with allocating typed API objects
// (e.g. *corev1.Pod) in a Go user arena. Build and test with:
//
//	GOEXPERIMENT=arenas go test ./prototype/arenatyped/ -v
//
// arena.New[corev1.Pod] only places the outer struct in the arena; the tree
// hanging off it — strings, slices, pointers, maps — is allocated by normal
// code paths on the GC heap. CloneToArena therefore deep-copies the object
// with reflection, relocating into the arena everything the arena API can
// hold:
//   - struct fields, in place (the shallow copy already lives in the arena)
//   - pointer targets, via reflect.ArenaNew
//   - slice backing arrays, via reflect.ArenaNew of an equivalent array type
//   - string bytes, via arena.MakeSlice + unsafe.String
//
// What stays on the GC heap:
//   - Maps (hmap headers and buckets): the runtime has no arena API for
//     them. They are rebuilt fresh so the clone shares no structure with the
//     source, and their keys/values are relocated where possible.
//   - Whatever unexported fields reference (resource.Quantity's internal
//     string, time.Time's *Location): reflection cannot rewrite unexported
//     fields, so the shallow copy keeps pointing at the source's data. This
//     is memory-safe — the GC scans arena chunks, so those heap objects stay
//     alive as long as the arena does — but it pins fragments of the source.
//   - Interface field payloads, left untouched for the same reason.
//
// Ownership rules match the other arena prototypes: nothing reachable from
// the returned object may be used after a.Free() (including strings read
// out of it, which are not copies); one arena should hold a batch of
// objects. Objects with pointer cycles are not supported (API types have
// none); shared pointers are duplicated.
package arenatyped

import (
	"arena"
	"reflect"
	"unsafe"
)

// CloneToArena deep-copies *src into a and returns the arena-resident copy.
func CloneToArena[T any](a *arena.Arena, src *T) *T {
	dst := arena.New[T](a)
	*dst = *src
	relocate(a, reflect.ValueOf(dst).Elem())
	return dst
}

// relocate rewrites v — addressable memory already holding a shallow copy —
// so that everything reachable through its exported fields lives in the
// arena (maps excepted).
func relocate(a *arena.Arena, v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		if len(s) == 0 {
			return
		}
		b := arena.MakeSlice[byte](a, len(s), len(s))
		copy(b, s)
		v.SetString(unsafe.String(&b[0], len(b)))
	case reflect.Slice:
		if v.IsNil() {
			return
		}
		n := v.Len()
		arr := reflect.ArenaNew(a, reflect.ArrayOf(n, v.Type().Elem()))
		s := arr.Elem().Slice(0, n)
		reflect.Copy(s, v)
		v.Set(s)
		if pointerFreeKind(v.Type().Elem().Kind()) {
			return
		}
		for i := 0; i < n; i++ {
			relocate(a, s.Index(i))
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		np := reflect.ArenaNew(a, v.Type().Elem())
		np.Elem().Set(v.Elem())
		relocate(a, np.Elem())
		v.Set(np)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				relocate(a, f)
			}
		}
	case reflect.Array:
		if pointerFreeKind(v.Type().Elem().Kind()) {
			return
		}
		for i := 0; i < v.Len(); i++ {
			relocate(a, v.Index(i))
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		m := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			k := reflect.New(v.Type().Key()).Elem()
			k.Set(it.Key())
			relocate(a, k)
			val := reflect.New(v.Type().Elem()).Elem()
			val.Set(it.Value())
			relocate(a, val)
			m.SetMapIndex(k, val)
		}
		v.Set(m)
	}
}

func pointerFreeKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return true
	}
	return false
}
