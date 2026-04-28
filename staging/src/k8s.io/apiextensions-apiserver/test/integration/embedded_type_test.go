package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/registry/customresource"
	"k8s.io/apiextensions-apiserver/test/integration/fixtures"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func init() {
	customresource.RegisterEmbeddedValidator("core/v1.PodTemplateSpec", func(fldPath *field.Path, obj map[string]interface{}) field.ErrorList {
		var allErrs field.ErrorList
		
		// Unmarshal into corev1.PodTemplateSpec
		b, err := json.Marshal(obj)
		if err != nil {
			return append(allErrs, field.Invalid(fldPath, obj, "failed to marshal embedded object"))
		}
		var podTemplate corev1.PodTemplateSpec
		if err := json.Unmarshal(b, &podTemplate); err != nil {
			return append(allErrs, field.Invalid(fldPath, obj, "failed to unmarshal into corev1.PodTemplateSpec"))
		}

		// Mock native validation: reject uppercase container names
		for i, c := range podTemplate.Spec.Containers {
			if strings.ToLower(c.Name) != c.Name {
				allErrs = append(allErrs, field.Invalid(fldPath.Child("spec", "containers").Index(i).Child("name"), c.Name, "must be lowercase"))
			}
		}

		return allErrs
	})
}

func TestEmbeddedTypeValidation(t *testing.T) {
	tearDown, apiExtensionClient, dynamicClient, err := fixtures.StartDefaultServerWithClients(t)
	if err != nil {
		t.Fatal(err)
	}
	defer tearDown()

	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "foos.mygroup.example.com"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "mygroup.example.com",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"template": {
											Type: "object",
											XEmbeddedType: func() *string { s := "core/v1.PodTemplateSpec"; return &s }(),
										},
									},
								},
							},
						},
					},
				},
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   "foos",
				Singular: "foo",
				Kind:     "Foo",
				ListKind: "FooList",
			},
		},
	}

	_, err = apiExtensionClient.ApiextensionsV1().CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create CRD: %v", err)
	}

	err = wait.PollImmediate(100*time.Millisecond, 5*time.Second, func() (bool, error) {
		c, err := apiExtensionClient.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crd.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range c.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("CRD did not become established: %v", err)
	}

	gvr := schema.GroupVersionResource{Group: "mygroup.example.com", Version: "v1", Resource: "foos"}
	crClient := dynamicClient.Resource(gvr).Namespace("default")

	// 1. Valid CR
	validCR := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mygroup.example.com/v1",
			"kind":       "Foo",
			"metadata": map[string]interface{}{
				"name": "valid-foo",
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "my-container",
								"image": "nginx",
							},
						},
					},
				},
			},
		},
	}

	_, err = crClient.Create(context.TODO(), validCR, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create valid CR: %v", err)
	}

	// 2. Invalid CR (uppercase container name)
	invalidCR := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mygroup.example.com/v1",
			"kind":       "Foo",
			"metadata": map[string]interface{}{
				"name": "invalid-foo",
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "My-Container", // UPPERCASE
								"image": "nginx",
							},
						},
					},
				},
			},
		},
	}

	_, err = crClient.Create(context.TODO(), invalidCR, metav1.CreateOptions{})
	if err == nil {
		t.Fatalf("expected validation error for invalid CR, but it succeeded")
	}

	if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "spec.template.spec.containers[0].name: Invalid value: \"My-Container\": must be lowercase") {
		t.Fatalf("unexpected error message: %v", err)
	}
}