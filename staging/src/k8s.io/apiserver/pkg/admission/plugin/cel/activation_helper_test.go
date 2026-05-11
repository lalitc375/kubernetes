package cel

import (
	"reflect"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
)

func TestAdmissionRequestStructFields(t *testing.T) {
	// If the AdmissionRequest struct changes, we want to fail the tests
	// to prompt developers to update createAdmissionRequestValue function
	// so that it stays in sync.
	req := admissionv1.AdmissionRequest{}
	numFields := reflect.TypeOf(req).NumField()
	
	// If you added a new field to AdmissionRequest, you MUST update
	// createAdmissionRequestValue in activation_helper.go and then
	// update this expected number of fields.
	expectedFields := 15
	if numFields != expectedFields {
		t.Fatalf("AdmissionRequest struct has %d fields, expected %d. Please update createAdmissionRequestValue and this test.", numFields, expectedFields)
	}
}

func TestCreateAdmissionRequestValue(t *testing.T) {
	dryRun := true

	req := &admissionv1.AdmissionRequest{
		UID: types.UID("test-uid"),
		Kind: metav1.GroupVersionKind{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
		},
		Resource: metav1.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "deployments",
		},
		SubResource: "status",
		RequestKind: &metav1.GroupVersionKind{
			Group:   "apps",
			Version: "v1beta1",
			Kind:    "Deployment",
		},
		RequestResource: &metav1.GroupVersionResource{
			Group:    "apps",
			Version:  "v1beta1",
			Resource: "deployments",
		},
		RequestSubResource: "status",
		Name:               "test-deployment",
		Namespace:          "test-namespace",
		Operation:          admissionv1.Update,
		UserInfo: authenticationv1.UserInfo{
			Username: "test-user",
			UID:      "user-uid",
			Groups:   []string{"system:authenticated"},
			Extra: map[string]authenticationv1.ExtraValue{
				"some-key": {"some-value"},
			},
		},
		DryRun: &dryRun,
		Options: runtime.RawExtension{
			Raw: []byte(`{"kind":"UpdateOptions","apiVersion":"meta.k8s.io/v1"}`),
		},
	}

	// We pass objects down instead of relying on the raw json, similar to the real implementation
	objectVal := map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(3)}}
	oldObjectVal := map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}}

	customMap, err := createAdmissionRequestValue(req, objectVal, oldObjectVal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// To compare, we construct the equivalent map through standard JSON marshal/unmarshal + unstructured
	// which is what we want to emulate exactly.
	// But first, we inject object and oldObject
	reqForJson := req.DeepCopy()
	objBytes, _ := json.Marshal(objectVal)
	oldObjBytes, _ := json.Marshal(oldObjectVal)
	reqForJson.Object.Raw = objBytes
	reqForJson.OldObject.Raw = oldObjBytes

	// emulate unstructured conversion
	u, err := convertObjectToUnstructured(reqForJson)
	if err != nil {
		t.Fatalf("failed unstructured conversion: %v", err)
	}
	expectedMap := u.Object

	// Normalize customMap through JSON to align types (e.g. int64 to float64, []string to []interface{})
	// so reflect.DeepEqual can perform an exact match.
	customBytes, _ := json.Marshal(customMap)
	var normalizedCustomMap map[string]interface{}
	json.Unmarshal(customBytes, &normalizedCustomMap)

	if !reflect.DeepEqual(normalizedCustomMap, expectedMap) {
		t.Errorf("createAdmissionRequestValue output doesn't match unstructured output.\nCustom:\n%v\n\nExpected:\n%v", normalizedCustomMap, expectedMap)
	}
}

// TestCreateAdmissionRequestValueEmpty tests empty and nil values.
func TestCreateAdmissionRequestValueEmpty(t *testing.T) {
	req := &admissionv1.AdmissionRequest{
		UID: types.UID("test-uid"),
		Kind: metav1.GroupVersionKind{
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",
		},
		Resource: metav1.GroupVersionResource{
			Group:    "apps",
			Version:  "v1",
			Resource: "deployments",
		},
		Operation: admissionv1.Create,
	}

	customMap, err := createAdmissionRequestValue(req, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	u, err := convertObjectToUnstructured(req)
	if err != nil {
		t.Fatalf("failed unstructured conversion: %v", err)
	}
	expectedMap := u.Object

	// Normalize customMap through JSON to align types (e.g. int64 to float64, []string to []interface{})
	// so reflect.DeepEqual can perform an exact match.
	customBytes, _ := json.Marshal(customMap)
	var normalizedCustomMap map[string]interface{}
	json.Unmarshal(customBytes, &normalizedCustomMap)

	if !reflect.DeepEqual(normalizedCustomMap, expectedMap) {
		t.Errorf("createAdmissionRequestValue output doesn't match unstructured output.\nCustom:\n%v\n\nExpected:\n%v", normalizedCustomMap, expectedMap)
	}
}
