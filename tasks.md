# Implementation Plan: Generic Native Type Embedding and Validation

This document outlines the step-by-step implementation plan for the design specified in `KEP-embedded-podtemplatespec.md`. 

**Testing Mandate:** After completing each step, you must build a Kubernetes API server from `HEAD` and deploy it (e.g., using a local `kind` cluster built from source) to empirically verify the changes before moving on to the next step.

## Task 1: Extend the CRD Schema Definition
**Goal:** Introduce the new `x-kubernetes-embedded-type` extension so that CRDs can legally declare it.
*   **Locations:** `staging/src/k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1/types_jsonschema.go` (and `v1beta1` equivalents).
*   **Action Items:**
    1. Add `XEmbeddedType *string` to the `JSONSchemaProps` struct with the appropriate `json` and `protobuf` tags.
    2. Run the code generation scripts (e.g., `hack/update-codegen.sh`) to update the protobuf definitions (`generated.proto`), deepcopy functions, and OpenAPI specs.
*   **Validation (Kind Cluster):** Apply a CRD manifest containing `x-kubernetes-embedded-type: "core/v1.PodTemplateSpec"`. Verify the API Server accepts the CRD and persists the extension in etcd without dropping the field.

## Task 2: Implement Structural Schema Validation Rules
**Goal:** Ensure CRD authors use the new extension correctly and prevent conflicting configurations.
*   **Locations:** `staging/src/k8s.io/apiextensions-apiserver/pkg/apiserver/schema/validation.go`
*   **Action Items:**
    1. Update the `ValidateStructural` function.
    2. Add a rule: If `XEmbeddedType` is set, the schema `type` must be `"object"`.
    3. Add a rule: `XEmbeddedType` is mutually exclusive with `XEmbeddedResource`.
    4. Ensure the string value matches a predefined set of allowed identifiers (initially, just `"core/v1.PodTemplateSpec"` for the MVP).
*   **Validation (Kind Cluster):** Attempt to apply an invalid CRD (e.g., `x-kubernetes-embedded-type` on a field of type `array`, or alongside `x-kubernetes-embedded-resource`). Verify the API Server rejects the CRD with a clear schema validation error.

## Task 3: Implement Dynamic Schema Injection
**Goal:** Automatically inject the native Kubernetes OpenAPI `$ref` into the published cluster schema to enable `kubectl explain` and proper client-side validation.
*   **Locations:** `staging/src/k8s.io/apiextensions-apiserver/pkg/controller/openapi/builder/builder.go`
*   **Action Items:**
    1. Hook into the `addEmbeddedProperties` or schema traversal logic.
    2. When the builder encounters a schema property with `XEmbeddedType` set to `"core/v1.PodTemplateSpec"`, mutate the schema to inject a `$ref` pointing to `io.k8s.api.core.v1.PodTemplateSpec`.
    3. Update the structural schema pruner (`pkg/apiserver/schema/pruning/algorithm.go`) to ensure it correctly prunes unknown fields using the injected native schema rather than blindly dropping all fields.
*   **Validation (Kind Cluster):** Create a valid CRD. Run `kubectl explain mycr.spec.template`. Verify it outputs the official Kubernetes documentation for a PodTemplateSpec.

## Task 4: Execute Native Validation on CR Payload
**Goal:** Dynamically parse the JSON and run the native Kubernetes validation functions when a user creates or updates a Custom Resource.
*   **Locations:** `staging/src/k8s.io/apiextensions-apiserver/pkg/registry/customresource/validator.go`
*   **Action Items:**
    1. Create an internal registry mapping `"core/v1.PodTemplateSpec"` to its native validation function (`validation.ValidatePodTemplateSpec`).
    2. Update `ValidateCustomResource` and `ValidateCustomResourceUpdate`.
    3. Traverse the unstructured CR JSON using the paths identified in the schema as having `XEmbeddedType`.
    4. Unmarshal the `map[string]interface{}` into a temporary `corev1.PodTemplateSpec` struct.
    5. Invoke the native validation function.
    6. Ensure the returned `field.ErrorList` correctly appends the JSON path prefix so the user knows exactly where the error occurred (e.g., `spec.template.spec.containers[0].name`).
*   **Validation (Kind Cluster):** Attempt to apply a Custom Resource with a subtle typo in the pod template (e.g., an uppercase letter in a container name). Verify the API Server synchronously rejects it with the exact native error message, properly localized to the nested path.

## Task 5: End-to-End Integration Testing
**Goal:** Prove the entire lifecycle works flawlessly in the apiserver integration test suite.
*   **Locations:** `staging/src/k8s.io/apiextensions-apiserver/test/integration/`
*   **Action Items:**
    1. Write a new integration test (e.g., `embedded_type_test.go`).
    2. Setup an in-memory test API Server instance.
    3. Create a CRD utilizing `x-kubernetes-embedded-type: "core/v1.PodTemplateSpec"`. Verify the CRD becomes established.
    4. Submit a valid CR. Verify it is accepted.
    5. Submit a CR with invalid embedded fields. Verify it is rejected with the correct validation errors.