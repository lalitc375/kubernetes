package cel

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/apiserver/pkg/cel/openapi"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

func TestPrepareVal(t *testing.T) {
	unstrObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name": "test-pod",
			},
		},
	}
	podObj := &corev1.Pod{}
	schema := &openapi.Schema{Schema: &spec.Schema{}}
	var unstrNil *unstructured.Unstructured

	tests := []struct {
		name        string
		obj         any
		schema      common.Schema
		expectNil   bool
		expectIsMap bool
		expectKind  string
	}{
		{
			name:      "nil object",
			obj:       nil,
			schema:    nil,
			expectNil: true,
		},
		{
			name:      "nil pointer of unstructured",
			obj:       unstrNil,
			schema:    nil,
			expectNil: true,
		},
		{
			name:       "unstructured object",
			obj:        unstrObj,
			schema:     nil,
			expectKind: "Pod",
		},
		{
			name:   "native object with schema",
			obj:    podObj,
			schema: schema,
		},
		{
			name:        "native object without schema (fallback to unstructured)",
			obj:         podObj,
			schema:      nil,
			expectIsMap: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			val, err := prepareVal(tc.obj, tc.schema, runtimeschema.GroupVersionKind{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if (val == nil) != tc.expectNil {
				t.Fatalf("expectNil=%v, but got nil=%v (val=%v)", tc.expectNil, val == nil, val)
			}
			if tc.expectNil {
				return
			}

			valMap, ok := val.(map[string]interface{})
			if tc.expectKind != "" && (!ok || valMap["kind"] != tc.expectKind) {
				t.Fatalf("expected kind=%q, got %v", tc.expectKind, valMap["kind"])
			} else if tc.expectIsMap && !ok {
				t.Fatalf("expected map[string]interface{}, got %T", val)
			}
		})
	}
}
