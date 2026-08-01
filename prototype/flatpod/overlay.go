package flatpod

// Mutation support. The tape is append-only: new values are parsed onto the
// end of the existing nodes/data slabs, and an edit overlay records pending
// set/delete operations on object keys. Array mutations rebuild the array as
// a small list of ref nodes pointing at the original elements, then record a
// key edit on the array's parent object. Reads and serialization consult the
// overlay; Compact folds everything into a fresh minimal tape.
//
// Not supported (replace the whole array instead): removing array elements,
// and mutating arrays nested directly inside arrays. A Flat must not be
// mutated concurrently with readers.

import (
	"fmt"
	"strconv"
)

// edit is one overlay entry: a pending set or delete of an object key.
// It contains no pointers, so the overlay stays as invisible to the GC
// as the tape itself.
type edit struct {
	del    bool
	target uint32 // object node this edit applies to
	keyOff uint32
	keyLen uint32
	val    uint32 // replacement/added value node; unused when del
}

// SetString sets the string value at path, creating intermediate objects
// as needed (like unstructured.SetNestedField).
func (f *Flat) SetString(v string, path ...string) error {
	off := uint32(len(f.data))
	f.data = append(f.data, v...)
	idx := f.pushNode(node{kind: kindString, a: uint64(off), b: uint64(len(v))})
	return f.setNode(idx, path)
}

func (f *Flat) SetInt(v int64, path ...string) error {
	idx := f.pushNode(node{kind: kindInt, a: uint64(v)})
	return f.setNode(idx, path)
}

func (f *Flat) SetBool(v bool, path ...string) error {
	k := kindFalse
	if v {
		k = kindTrue
	}
	idx := f.pushNode(node{kind: k})
	return f.setNode(idx, path)
}

// SetJSON sets an arbitrary JSON-encoded subtree at path.
func (f *Flat) SetJSON(raw []byte, path ...string) error {
	idx, err := f.parseInto(raw)
	if err != nil {
		return err
	}
	return f.setNode(idx, path)
}

// Remove deletes the object key at path. Missing paths and type conflicts
// are a no-op, matching unstructured.RemoveNestedField.
func (f *Flat) Remove(path ...string) error {
	if len(path) == 0 {
		return fmt.Errorf("flatpod: empty path")
	}
	pos, ok, err := f.walk(path[:len(path)-1], false)
	if err != nil || !ok {
		return nil
	}
	last := path[len(path)-1]
	switch f.nodes[pos.idx].kind {
	case kindObject:
		koff, klen := f.appendKey(last)
		f.upsertEdit(edit{del: true, target: pos.idx, keyOff: koff, keyLen: klen})
		return nil
	case kindArray:
		return fmt.Errorf("flatpod: removing array elements is unsupported; replace the array instead")
	default:
		return nil
	}
}

// ArrayAppendJSON appends a JSON-encoded element to the array at path.
func (f *Flat) ArrayAppendJSON(raw []byte, path ...string) error {
	valIdx, err := f.parseInto(raw)
	if err != nil {
		return err
	}
	pos, ok, err := f.walk(path, false)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("flatpod: no array at path %v", path)
	}
	if f.nodes[pos.idx].kind != kindArray {
		return fmt.Errorf("flatpod: value at path %v is not an array", path)
	}
	if !pos.parentIsObj {
		return fmt.Errorf("flatpod: appending to arrays nested directly in arrays is unsupported")
	}
	newArr := f.rebuildArray(pos.idx, -1, valIdx)
	f.upsertEdit(edit{target: pos.parentObj, keyOff: pos.parentKeyOff, keyLen: pos.parentKeyLen, val: newArr})
	return nil
}

// Compact folds the overlay and any orphaned tape regions into a fresh
// minimal tape.
func (f *Flat) Compact() (*Flat, error) {
	return Parse(f.JSON())
}

// EditCount reports pending overlay entries, e.g. to decide when to Compact.
func (f *Flat) EditCount() int {
	return len(f.edits)
}

// parseInto appends raw's node tree to the tape and returns its root index.
// On error, partially appended nodes remain as unreferenced garbage until
// Compact.
func (f *Flat) parseInto(raw []byte) (uint32, error) {
	idx := uint32(len(f.nodes))
	p := &parser{buf: raw, f: f}
	p.skipWS()
	if err := p.parseValue(); err != nil {
		return 0, err
	}
	p.skipWS()
	if p.pos != len(p.buf) {
		return 0, p.errf("trailing data")
	}
	return idx, nil
}

func (f *Flat) setNode(valIdx uint32, path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("flatpod: empty path")
	}
	pos, _, err := f.walk(path[:len(path)-1], true)
	if err != nil {
		return err
	}
	last := path[len(path)-1]
	n := f.nodes[pos.idx]
	switch n.kind {
	case kindObject:
		koff, klen := f.appendKey(last)
		f.upsertEdit(edit{target: pos.idx, keyOff: koff, keyLen: klen, val: valIdx})
		return nil
	case kindArray:
		i, aerr := strconv.Atoi(last)
		if aerr != nil || i < 0 || uint64(i) >= n.a {
			return fmt.Errorf("flatpod: invalid array index %q", last)
		}
		if !pos.parentIsObj {
			return fmt.Errorf("flatpod: mutating arrays nested directly in arrays is unsupported")
		}
		newArr := f.rebuildArray(pos.idx, i, valIdx)
		f.upsertEdit(edit{target: pos.parentObj, keyOff: pos.parentKeyOff, keyLen: pos.parentKeyLen, val: newArr})
		return nil
	default:
		return fmt.Errorf("flatpod: cannot set %q inside a scalar", last)
	}
}

// walkPos is a resolved container plus the object key that points at it,
// needed when the container itself must be replaced (array rebuilds).
type walkPos struct {
	idx          uint32
	parentObj    uint32
	parentKeyOff uint32
	parentKeyLen uint32
	parentIsObj  bool
}

// walk resolves path, deref'ing overlay edits and ref nodes. With create
// set, missing object keys become new empty objects. ok=false reports a
// missing path element (only possible when !create).
func (f *Flat) walk(path []string, create bool) (walkPos, bool, error) {
	if len(f.nodes) == 0 {
		return walkPos{}, false, fmt.Errorf("flatpod: empty document")
	}
	pos := walkPos{idx: f.deref(0)}
	for _, elem := range path {
		n := f.nodes[pos.idx]
		switch n.kind {
		case kindObject:
			if e, ok := f.editFor(pos.idx, elem); ok {
				if !e.del {
					pos = walkPos{idx: f.deref(e.val), parentObj: pos.idx, parentKeyOff: e.keyOff, parentKeyLen: e.keyLen, parentIsObj: true}
					continue
				}
			} else {
				j := pos.idx + 1
				found := false
				for m := uint64(0); m < n.a; m++ {
					key := f.nodes[j]
					valIdx := j + 1
					if string(f.data[key.a:key.a+key.b]) == elem {
						pos = walkPos{idx: f.deref(valIdx), parentObj: pos.idx, parentKeyOff: uint32(key.a), parentKeyLen: uint32(key.b), parentIsObj: true}
						found = true
						break
					}
					j = f.nodes[valIdx].next
				}
				if found {
					continue
				}
			}
			if !create {
				return walkPos{}, false, nil
			}
			obj := pos.idx
			koff, klen := f.appendKey(elem)
			child := f.pushNode(node{kind: kindObject})
			f.upsertEdit(edit{target: obj, keyOff: koff, keyLen: klen, val: child})
			pos = walkPos{idx: child, parentObj: obj, parentKeyOff: koff, parentKeyLen: klen, parentIsObj: true}
		case kindArray:
			i, err := strconv.Atoi(elem)
			if err != nil || i < 0 || uint64(i) >= n.a {
				return walkPos{}, false, fmt.Errorf("flatpod: invalid array index %q", elem)
			}
			j := pos.idx + 1
			for k := 0; k < i; k++ {
				j = f.nodes[j].next
			}
			pos = walkPos{idx: f.deref(j)}
		default:
			return walkPos{}, false, fmt.Errorf("flatpod: cannot traverse scalar at %q", elem)
		}
	}
	return pos, true, nil
}

// rebuildArray creates a new array node whose elements are refs to the
// original elements, substituting valIdx at index replace, or appending it
// when replace is negative.
func (f *Flat) rebuildArray(arr uint32, replace int, valIdx uint32) uint32 {
	n := f.nodes[arr]
	newArr := f.pushNode(node{kind: kindArray})
	count := n.a
	j := arr + 1
	for m := uint64(0); m < n.a; m++ {
		if int(m) == replace {
			f.pushNode(node{kind: kindRef, a: uint64(valIdx)})
		} else {
			f.pushNode(node{kind: kindRef, a: uint64(j)})
		}
		j = f.nodes[j].next
	}
	if replace < 0 {
		f.pushNode(node{kind: kindRef, a: uint64(valIdx)})
		count++
	}
	f.nodes[newArr].a = count
	f.nodes[newArr].next = uint32(len(f.nodes))
	return newArr
}

func (f *Flat) deref(i uint32) uint32 {
	for f.nodes[i].kind == kindRef {
		i = uint32(f.nodes[i].a)
	}
	return i
}

func (f *Flat) appendKey(k string) (uint32, uint32) {
	off := uint32(len(f.data))
	f.data = append(f.data, k...)
	return off, uint32(len(k))
}

// upsertEdit replaces any existing edit for the same (target, key), giving
// last-write-wins semantics.
func (f *Flat) upsertEdit(e edit) {
	kb := f.data[e.keyOff : e.keyOff+e.keyLen]
	for i := range f.edits {
		o := &f.edits[i]
		if o.target == e.target && string(f.data[o.keyOff:o.keyOff+o.keyLen]) == string(kb) {
			*o = e
			return
		}
	}
	f.edits = append(f.edits, e)
}

func (f *Flat) editFor(target uint32, key string) (edit, bool) {
	for _, e := range f.edits {
		if e.target == target && string(f.data[e.keyOff:e.keyOff+e.keyLen]) == key {
			return e, true
		}
	}
	return edit{}, false
}

func (f *Flat) editForBytes(target uint32, key []byte) (edit, bool) {
	for _, e := range f.edits {
		if e.target == target && string(f.data[e.keyOff:e.keyOff+e.keyLen]) == string(key) {
			return e, true
		}
	}
	return edit{}, false
}

func (f *Flat) objectHasKey(obj uint32, key []byte) bool {
	n := f.nodes[obj]
	j := obj + 1
	for m := uint64(0); m < n.a; m++ {
		k := f.nodes[j]
		if string(f.data[k.a:k.a+k.b]) == string(key) {
			return true
		}
		j = f.nodes[j+1].next
	}
	return false
}
