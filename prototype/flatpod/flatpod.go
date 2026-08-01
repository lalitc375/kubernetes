// Package flatpod prototypes a flat ("tape") representation of a Kubernetes
// object. Instead of a map[string]interface{} tree — hundreds of small,
// pointer-dense heap allocations — the whole JSON document lives in exactly
// two allocations: a tape of fixed-size nodes and a byte buffer holding all
// string data. Neither contains pointers, so the GC never scans inside them,
// and dropping the Flat frees everything at once.
package flatpod

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	corev1 "k8s.io/api/core/v1"
)

const (
	kindNull uint8 = iota
	kindFalse
	kindTrue
	kindInt
	kindFloat
	kindString
	kindObject
	kindArray
)

// node is one tape entry. next is the index one past this node's subtree,
// which makes sibling traversal O(1). Meaning of a/b by kind:
// string: a=offset, b=length into data; int: a=int64 bits; float: a=float64
// bits; object/array: a=member/element count.
type node struct {
	kind uint8
	next uint32
	a, b uint64
}

// Flat is a Kubernetes object stored as a flat tape: two heap allocations
// regardless of how deeply nested the object is.
type Flat struct {
	nodes []node
	data  []byte
}

// FromPod converts a typed Pod into the flat representation.
func FromPod(pod *corev1.Pod) (*Flat, error) {
	buf, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	return Parse(buf)
}

// ToPod converts the flat representation back into a typed Pod.
func (f *Flat) ToPod() (*corev1.Pod, error) {
	pod := &corev1.Pod{}
	if err := json.Unmarshal(f.JSON(), pod); err != nil {
		return nil, err
	}
	return pod, nil
}

// Parse builds a Flat from JSON bytes.
func Parse(buf []byte) (*Flat, error) {
	p := &parser{buf: buf, f: &Flat{
		nodes: make([]node, 0, 128),
		data:  make([]byte, 0, len(buf)/2),
	}}
	p.skipWS()
	if err := p.parseValue(); err != nil {
		return nil, err
	}
	p.skipWS()
	if p.pos != len(p.buf) {
		return nil, p.errf("trailing data")
	}
	return p.f, nil
}

// JSON serializes the tape back to JSON bytes.
func (f *Flat) JSON() []byte {
	return f.AppendJSON(make([]byte, 0, len(f.data)*2))
}

func (f *Flat) AppendJSON(out []byte) []byte {
	if len(f.nodes) == 0 {
		return append(out, "null"...)
	}
	out, _ = f.appendValue(out, 0)
	return out
}

// Size is the retained memory of the representation in bytes.
func (f *Flat) Size() int {
	return int(unsafe.Sizeof(node{}))*len(f.nodes) + len(f.data)
}

// GetString returns the string at path. Path elements index object keys, or
// array elements when they parse as integers.
func (f *Flat) GetString(path ...string) (string, bool) {
	n, ok := f.leaf(kindString, path)
	if !ok {
		return "", false
	}
	return string(f.data[n.a : n.a+n.b]), true
}

func (f *Flat) GetInt(path ...string) (int64, bool) {
	n, ok := f.leaf(kindInt, path)
	if !ok {
		return 0, false
	}
	return int64(n.a), true
}

func (f *Flat) GetFloat(path ...string) (float64, bool) {
	idx, ok := f.lookup(path)
	if !ok {
		return 0, false
	}
	switch n := f.nodes[idx]; n.kind {
	case kindFloat:
		return math.Float64frombits(n.a), true
	case kindInt:
		return float64(int64(n.a)), true
	}
	return 0, false
}

func (f *Flat) GetBool(path ...string) (bool, bool) {
	idx, ok := f.lookup(path)
	if !ok {
		return false, false
	}
	switch f.nodes[idx].kind {
	case kindTrue:
		return true, true
	case kindFalse:
		return false, true
	}
	return false, false
}

func (f *Flat) ArrayLen(path ...string) (int, bool) {
	n, ok := f.leaf(kindArray, path)
	if !ok {
		return 0, false
	}
	return int(n.a), true
}

func (f *Flat) leaf(kind uint8, path []string) (node, bool) {
	idx, ok := f.lookup(path)
	if !ok || f.nodes[idx].kind != kind {
		return node{}, false
	}
	return f.nodes[idx], true
}

func (f *Flat) lookup(path []string) (uint32, bool) {
	if len(f.nodes) == 0 {
		return 0, false
	}
	idx := uint32(0)
	for _, elem := range path {
		n := f.nodes[idx]
		switch n.kind {
		case kindObject:
			j := idx + 1
			found := false
			for m := uint64(0); m < n.a; m++ {
				key := f.nodes[j]
				valIdx := j + 1
				if string(f.data[key.a:key.a+key.b]) == elem {
					idx = valIdx
					found = true
					break
				}
				j = f.nodes[valIdx].next
			}
			if !found {
				return 0, false
			}
		case kindArray:
			i, err := strconv.Atoi(elem)
			if err != nil || i < 0 || uint64(i) >= n.a {
				return 0, false
			}
			j := idx + 1
			for k := 0; k < i; k++ {
				j = f.nodes[j].next
			}
			idx = j
		default:
			return 0, false
		}
	}
	return idx, true
}

func (f *Flat) appendValue(out []byte, i uint32) ([]byte, uint32) {
	n := f.nodes[i]
	switch n.kind {
	case kindNull:
		return append(out, "null"...), i + 1
	case kindTrue:
		return append(out, "true"...), i + 1
	case kindFalse:
		return append(out, "false"...), i + 1
	case kindInt:
		return strconv.AppendInt(out, int64(n.a), 10), i + 1
	case kindFloat:
		return strconv.AppendFloat(out, math.Float64frombits(n.a), 'g', -1, 64), i + 1
	case kindString:
		return appendJSONString(out, f.data[n.a:n.a+n.b]), i + 1
	case kindObject:
		out = append(out, '{')
		j := i + 1
		for m := uint64(0); m < n.a; m++ {
			if m > 0 {
				out = append(out, ',')
			}
			key := f.nodes[j]
			out = appendJSONString(out, f.data[key.a:key.a+key.b])
			out = append(out, ':')
			out, j = f.appendValue(out, j+1)
		}
		return append(out, '}'), j
	case kindArray:
		out = append(out, '[')
		j := i + 1
		for m := uint64(0); m < n.a; m++ {
			if m > 0 {
				out = append(out, ',')
			}
			out, j = f.appendValue(out, j)
		}
		return append(out, ']'), j
	}
	return out, i + 1
}

const hexDigits = "0123456789abcdef"

func appendJSONString(out, s []byte) []byte {
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			out = append(out, '\\', '"')
		case c == '\\':
			out = append(out, '\\', '\\')
		case c == '\n':
			out = append(out, '\\', 'n')
		case c == '\r':
			out = append(out, '\\', 'r')
		case c == '\t':
			out = append(out, '\\', 't')
		case c < 0x20:
			out = append(out, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		default:
			out = append(out, c)
		}
	}
	return append(out, '"')
}

type parser struct {
	buf []byte
	pos int
	f   *Flat
}

func (p *parser) errf(format string, args ...interface{}) error {
	return fmt.Errorf("flatpod: offset %d: %s", p.pos, fmt.Sprintf(format, args...))
}

func (p *parser) skipWS() {
	for p.pos < len(p.buf) {
		switch p.buf[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) push(n node) uint32 {
	idx := uint32(len(p.f.nodes))
	n.next = idx + 1
	p.f.nodes = append(p.f.nodes, n)
	return idx
}

func (p *parser) parseValue() error {
	if p.pos >= len(p.buf) {
		return p.errf("unexpected end of input")
	}
	switch c := p.buf[p.pos]; c {
	case '{':
		return p.parseObject()
	case '[':
		return p.parseArray()
	case '"':
		off, n, err := p.parseString()
		if err != nil {
			return err
		}
		p.push(node{kind: kindString, a: off, b: n})
		return nil
	case 't':
		return p.parseLit("true", node{kind: kindTrue})
	case 'f':
		return p.parseLit("false", node{kind: kindFalse})
	case 'n':
		return p.parseLit("null", node{kind: kindNull})
	default:
		return p.parseNumber()
	}
}

func (p *parser) parseObject() error {
	idx := p.push(node{kind: kindObject})
	p.pos++
	p.skipWS()
	count := uint64(0)
	if p.pos < len(p.buf) && p.buf[p.pos] == '}' {
		p.pos++
	} else {
		for {
			p.skipWS()
			if p.pos >= len(p.buf) || p.buf[p.pos] != '"' {
				return p.errf("expected object key")
			}
			off, n, err := p.parseString()
			if err != nil {
				return err
			}
			p.push(node{kind: kindString, a: off, b: n})
			p.skipWS()
			if p.pos >= len(p.buf) || p.buf[p.pos] != ':' {
				return p.errf("expected ':'")
			}
			p.pos++
			p.skipWS()
			if err := p.parseValue(); err != nil {
				return err
			}
			count++
			p.skipWS()
			if p.pos >= len(p.buf) {
				return p.errf("unterminated object")
			}
			if p.buf[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.buf[p.pos] == '}' {
				p.pos++
				break
			}
			return p.errf("expected ',' or '}'")
		}
	}
	p.f.nodes[idx].a = count
	p.f.nodes[idx].next = uint32(len(p.f.nodes))
	return nil
}

func (p *parser) parseArray() error {
	idx := p.push(node{kind: kindArray})
	p.pos++
	p.skipWS()
	count := uint64(0)
	if p.pos < len(p.buf) && p.buf[p.pos] == ']' {
		p.pos++
	} else {
		for {
			p.skipWS()
			if err := p.parseValue(); err != nil {
				return err
			}
			count++
			p.skipWS()
			if p.pos >= len(p.buf) {
				return p.errf("unterminated array")
			}
			if p.buf[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.buf[p.pos] == ']' {
				p.pos++
				break
			}
			return p.errf("expected ',' or ']'")
		}
	}
	p.f.nodes[idx].a = count
	p.f.nodes[idx].next = uint32(len(p.f.nodes))
	return nil
}

// parseString consumes a JSON string (cursor on the opening quote) and
// appends its unescaped bytes to f.data, returning offset and length.
func (p *parser) parseString() (uint64, uint64, error) {
	p.pos++
	start := len(p.f.data)
	for {
		if p.pos >= len(p.buf) {
			return 0, 0, p.errf("unterminated string")
		}
		c := p.buf[p.pos]
		if c == '"' {
			p.pos++
			break
		}
		if c != '\\' {
			p.f.data = append(p.f.data, c)
			p.pos++
			continue
		}
		if p.pos+1 >= len(p.buf) {
			return 0, 0, p.errf("unterminated escape")
		}
		e := p.buf[p.pos+1]
		switch e {
		case '"', '\\', '/':
			p.f.data = append(p.f.data, e)
			p.pos += 2
		case 'b':
			p.f.data = append(p.f.data, '\b')
			p.pos += 2
		case 'f':
			p.f.data = append(p.f.data, '\f')
			p.pos += 2
		case 'n':
			p.f.data = append(p.f.data, '\n')
			p.pos += 2
		case 'r':
			p.f.data = append(p.f.data, '\r')
			p.pos += 2
		case 't':
			p.f.data = append(p.f.data, '\t')
			p.pos += 2
		case 'u':
			r, err := p.hex4(p.pos + 2)
			if err != nil {
				return 0, 0, err
			}
			p.pos += 6
			ru := rune(r)
			if utf16.IsSurrogate(ru) {
				ru = utf8.RuneError
				if p.pos+5 < len(p.buf) && p.buf[p.pos] == '\\' && p.buf[p.pos+1] == 'u' {
					if r2, err2 := p.hex4(p.pos + 2); err2 == nil {
						if dec := utf16.DecodeRune(rune(r), rune(r2)); dec != utf8.RuneError {
							ru = dec
							p.pos += 6
						}
					}
				}
			}
			p.f.data = utf8.AppendRune(p.f.data, ru)
		default:
			return 0, 0, p.errf("invalid escape \\%c", e)
		}
	}
	return uint64(start), uint64(len(p.f.data) - start), nil
}

func (p *parser) hex4(i int) (uint32, error) {
	if i+4 > len(p.buf) {
		return 0, p.errf("truncated \\u escape")
	}
	var v uint32
	for j := 0; j < 4; j++ {
		c := p.buf[i+j]
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | uint32(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | uint32(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | uint32(c-'A'+10)
		default:
			return 0, p.errf("invalid hex digit %q", c)
		}
	}
	return v, nil
}

func (p *parser) parseLit(lit string, n node) error {
	if p.pos+len(lit) > len(p.buf) || string(p.buf[p.pos:p.pos+len(lit)]) != lit {
		return p.errf("invalid literal")
	}
	p.pos += len(lit)
	p.push(n)
	return nil
}

func (p *parser) parseNumber() error {
	start := p.pos
	isFloat := false
	for p.pos < len(p.buf) {
		c := p.buf[p.pos]
		if (c >= '0' && c <= '9') || c == '-' || c == '+' {
			p.pos++
			continue
		}
		if c == '.' || c == 'e' || c == 'E' {
			isFloat = true
			p.pos++
			continue
		}
		break
	}
	if start == p.pos {
		return p.errf("invalid character %q", p.buf[start])
	}
	s := string(p.buf[start:p.pos])
	if !isFloat {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			p.push(node{kind: kindInt, a: uint64(v)})
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return p.errf("invalid number %q", s)
	}
	p.push(node{kind: kindFloat, a: math.Float64bits(v)})
	return nil
}
