package flatpod

import (
	"bytes"
	"testing"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

func mustFlat(t *testing.T) *Flat {
	t.Helper()
	f, err := FromPod(samplePod(1))
	if err != nil {
		t.Fatalf("FromPod: %v", err)
	}
	return f
}

func TestSetExistingField(t *testing.T) {
	f := mustFlat(t)
	if err := f.SetString("nginx:1.28", "spec", "containers", "0", "image"); err != nil {
		t.Fatalf("SetString: %v", err)
	}

	if v, ok := f.GetString("spec", "containers", "0", "image"); !ok || v != "nginx:1.28" {
		t.Errorf("read after set = %q, %v; want nginx:1.28", v, ok)
	}
	if v, ok := f.GetString("spec", "containers", "1", "image"); !ok || v != "envoyproxy/envoy:v1.30" {
		t.Errorf("sibling element changed: %q, %v", v, ok)
	}
	pod, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if pod.Spec.Containers[0].Image != "nginx:1.28" {
		t.Errorf("ToPod image = %q; want nginx:1.28", pod.Spec.Containers[0].Image)
	}
	if pod.Name != "web-1" || len(pod.Spec.Containers) != 2 {
		t.Errorf("unrelated fields changed: name=%q containers=%d", pod.Name, len(pod.Spec.Containers))
	}
}

func TestSetNewFieldCreatesIntermediates(t *testing.T) {
	f := mustFlat(t)
	if err := f.SetString("blue", "metadata", "labels", "color"); err != nil {
		t.Fatalf("SetString: %v", err)
	}
	if err := f.SetInt(42, "metadata", "extras", "nested", "count"); err != nil {
		t.Fatalf("SetInt through missing intermediates: %v", err)
	}

	if v, ok := f.GetString("metadata", "labels", "color"); !ok || v != "blue" {
		t.Errorf("labels.color = %q, %v; want blue", v, ok)
	}
	if v, ok := f.GetInt("metadata", "extras", "nested", "count"); !ok || v != 42 {
		t.Errorf("extras.nested.count = %d, %v; want 42", v, ok)
	}
	pod, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if pod.Labels["color"] != "blue" || len(pod.Labels) != 3 {
		t.Errorf("ToPod labels = %v", pod.Labels)
	}
}

func TestRemoveField(t *testing.T) {
	f := mustFlat(t)
	if err := f.Remove("metadata", "annotations", "checksum/config"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := f.GetString("metadata", "annotations", "checksum/config"); ok {
		t.Errorf("removed key still readable")
	}
	pod, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if len(pod.Annotations) != 0 {
		t.Errorf("ToPod annotations = %v; want empty", pod.Annotations)
	}
	if err := f.Remove("metadata", "no", "such", "path"); err != nil {
		t.Errorf("Remove of missing path: %v; want nil", err)
	}
}

func TestLastWriteWins(t *testing.T) {
	f := mustFlat(t)
	if err := f.SetString("a", "metadata", "labels", "app"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetString("b", "metadata", "labels", "app"); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.GetString("metadata", "labels", "app"); v != "b" {
		t.Errorf("after set,set = %q; want b", v)
	}

	if err := f.Remove("metadata", "labels", "app"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.GetString("metadata", "labels", "app"); ok {
		t.Errorf("key readable after set,remove")
	}

	if err := f.SetString("c", "metadata", "labels", "app"); err != nil {
		t.Fatal(err)
	}
	if v, ok := f.GetString("metadata", "labels", "app"); !ok || v != "c" {
		t.Errorf("after remove,set = %q, %v; want c", v, ok)
	}
}

func TestReplaceArrayElement(t *testing.T) {
	f := mustFlat(t)
	if err := f.SetJSON([]byte(`{"name":"web","image":"httpd:2.4"}`), "spec", "containers", "0"); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}
	if v, ok := f.GetString("spec", "containers", "0", "image"); !ok || v != "httpd:2.4" {
		t.Errorf("replaced element image = %q, %v", v, ok)
	}
	if n, ok := f.ArrayLen("spec", "containers"); !ok || n != 2 {
		t.Errorf("len(containers) = %d, %v; want 2", n, ok)
	}

	// Mutate inside the replacement subtree.
	if err := f.SetString("httpd:2.5", "spec", "containers", "0", "image"); err != nil {
		t.Fatalf("set inside replaced element: %v", err)
	}
	pod, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if pod.Spec.Containers[0].Image != "httpd:2.5" || pod.Spec.Containers[1].Name != "sidecar" {
		t.Errorf("ToPod containers = %+v", pod.Spec.Containers)
	}
}

func TestArrayAppend(t *testing.T) {
	f := mustFlat(t)
	if err := f.ArrayAppendJSON([]byte(`{"name":"init","image":"busybox:1.36"}`), "spec", "containers"); err != nil {
		t.Fatalf("ArrayAppendJSON: %v", err)
	}
	if n, ok := f.ArrayLen("spec", "containers"); !ok || n != 3 {
		t.Errorf("len(containers) = %d, %v; want 3", n, ok)
	}
	if v, ok := f.GetString("spec", "containers", "2", "image"); !ok || v != "busybox:1.36" {
		t.Errorf("appended element image = %q, %v", v, ok)
	}
	pod, err := f.ToPod()
	if err != nil {
		t.Fatalf("ToPod: %v", err)
	}
	if len(pod.Spec.Containers) != 3 || pod.Spec.Containers[2].Name != "init" {
		t.Errorf("ToPod containers = %+v", pod.Spec.Containers)
	}
}

func TestCompact(t *testing.T) {
	f := mustFlat(t)
	if err := f.SetString("nginx:1.28", "spec", "containers", "0", "image"); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove("metadata", "annotations", "checksum/config"); err != nil {
		t.Fatal(err)
	}
	if err := f.ArrayAppendJSON([]byte(`{"name":"init","image":"busybox:1.36"}`), "spec", "containers"); err != nil {
		t.Fatal(err)
	}
	if f.EditCount() == 0 {
		t.Fatalf("expected pending edits")
	}

	c, err := f.Compact()
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if c.EditCount() != 0 {
		t.Errorf("compacted EditCount = %d; want 0", c.EditCount())
	}
	if !bytes.Equal(c.JSON(), f.JSON()) {
		t.Errorf("compacted JSON differs:\n%s\nvs\n%s", c.JSON(), f.JSON())
	}
	if c.Size() >= f.Size() {
		t.Errorf("compacted size %d not smaller than overlay size %d", c.Size(), f.Size())
	}
	podF, err := f.ToPod()
	if err != nil {
		t.Fatal(err)
	}
	podC, err := c.ToPod()
	if err != nil {
		t.Fatal(err)
	}
	if !apiequality.Semantic.DeepEqual(podF, podC) {
		t.Errorf("compacted pod differs from overlay pod")
	}
}

func TestMutationErrors(t *testing.T) {
	f := mustFlat(t)
	if err := f.SetString("x", "metadata", "name", "sub"); err == nil {
		t.Errorf("set through scalar succeeded")
	}
	if err := f.SetString("x", "spec", "containers", "9", "image"); err == nil {
		t.Errorf("set with out-of-range index succeeded")
	}
	if err := f.SetJSON([]byte(`{"broken`), "metadata", "labels", "app"); err == nil {
		t.Errorf("set with invalid JSON succeeded")
	}
	if err := f.SetString("x"); err == nil {
		t.Errorf("set with empty path succeeded")
	}
}
