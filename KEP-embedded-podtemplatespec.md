# KEP: Generic Native Type Embedding and Validation in CustomResources

## Table of Contents
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [User Journey](#user-journey)
  - [CRD Developers](#crd-developers)
  - [Custom Resource Users](#custom-resource-users)
- [Proposal](#proposal)
  - [OpenAPI Schema Extension](#openapi-schema-extension)
  - [Supported Types](#supported-types)
  - [Validation Execution](#validation-execution)
- [Design Details](#design-details)
  - [CRD Schema Validation](#crd-schema-validation)
  - [CustomResource Validator Update](#customresource-validator-update)
- [Backward Compatibility](#backward-compatibility)
- [Alternatives Considered](#alternatives-considered)

## Summary

This proposal introduces a generic mechanism for validating standard Kubernetes types embedded within Custom Resources (CRs). By defining a new OpenAPI schema extension (`x-kubernetes-embedded-type: <TypeIdentifier>`), developers can indicate that a specific field in their CustomResourceDefinition (CRD) contains a standard Kubernetes struct (such as `PodTemplateSpec` or `PersistentVolumeClaimTemplate`). The Kubernetes API Server will then dynamically apply the corresponding native validation rules against these fields during CR creation and updates.

## Motivation

Operators and custom controllers frequently compose higher-level abstractions by embedding lower-level Kubernetes types. Today, CRD authors who embed these types have to either:
1. Re-implement complex validation using CEL rules.
2. Rely on an out-of-tree ValidatingWebhookConfiguration to parse and validate the embedded types.
3. Skip validation entirely, leading to obscure failures later in the controller reconciliation loop when the underlying API server rejects the resource creation.

The most common example is `PodTemplateSpec` (used in custom workload controllers), but developers also frequently embed:
- `PersistentVolumeClaimTemplate` (for stateful workloads)
- `JobTemplateSpec` (for custom CronJobs or workflow engines)
- `ServiceSpec`
- `NetworkPolicySpec`
- `VolumeSource`

Beyond validation logic, copying massive native structural schemas (like `PodTemplateSpec`) directly into a CRD's `openAPIV3Schema` dramatically inflates the size of the CRD object. This bloat increases the storage burden on etcd and degrades performance by increasing the payload size transferred over the wire during API requests (e.g., when controllers or `kubectl` fetch the CRD schema).

Providing native validation and dynamic schema injection for a curated list of embeddable types ensures consistent, built-in, and comprehensive validation equivalent to native APIs, while keeping CRD payloads lightweight.

### Goals
- Introduce a generic OpenAPI extension to declare embedded native types within CRDs.
- Define an initial list of supported embeddable types, starting with `PodTemplateSpec`.
- Automatically execute native validation logic (e.g., `ValidatePodTemplateSpec`) on these fields during CR creation and updates.
- Map validation errors correctly to the JSON path of the embedded object.
- Ensure full backward compatibility for existing CRDs and CRs.

### Non-Goals
- Supporting embedding and validating arbitrary external types or non-standard Kubernetes APIs.
- Modifying how `x-kubernetes-embedded-resource` works (which is strictly for embedding full `runtime.Object`s with apiVersion/kind).

## User Journey

### CRD Developers
A developer is building a custom workload controller called `MyApp` which dynamically spawns pods. To allow users to configure these pods, they decide to embed a `PodTemplateSpec` directly in the `MyApp` CRD under `spec.template`.

Instead of writing hundreds of lines of CEL rules or deploying a validating webhook just to validate the pod template, the developer simply adds the extension `x-kubernetes-embedded-type: "core/v1.PodTemplateSpec"` to the `template` object in the OpenAPI v3 schema of their CRD manifest.

Here is a sample CRD snippet illustrating this setup:

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: myapps.example.com
spec:
  group: example.com
  names:
    kind: MyApp
    plural: myapps
  scope: Namespaced
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              replicas:
                type: integer
                minimum: 1
              template:
                type: object
                x-kubernetes-embedded-type: "core/v1.PodTemplateSpec"
```

When they apply the CRD to the cluster, the Kubernetes API server accepts the CRD and dynamically injects the `PodTemplateSpec` OpenAPI schema into the CRD. It also registers that any `MyApp` resource created must now pass the strict, native validations for the `spec.template` field.

### Custom Resource Users
A cluster user is creating a new `MyApp` instance via a YAML manifest.

1. **Discoverability**: Before writing the YAML, the user wants to know what fields they can configure inside `spec.template`. Because the API server dynamically injected a native `$ref` into the OpenAPI schema during CRD creation, the user can simply run:
   ```bash
   kubectl explain myapp.spec.template
   ```
   This will correctly output the native Kubernetes documentation and field lists for a `PodTemplateSpec`, seamlessly bridging the gap between the custom resource and core Kubernetes types.

2. **Validation Feedback**: The user makes a typo in the pod template by providing a container name that violates DNS sub-domain rules (e.g., using an underscore).

Here is a sample `MyApp` custom resource that the user attempts to apply:

```yaml
apiVersion: example.com/v1alpha1
kind: MyApp
metadata:
  name: my-web-app
spec:
  replicas: 3
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
      - name: "My_Web_Container" # Invalid name (contains underscores and uppercase letters)
        image: nginx:1.14.2
        ports:
        - containerPort: 80
```

The user runs `kubectl apply -f myapp.yaml`.

The Kubernetes API server synchronously rejects the request with a clear, localized error message indicating exactly which nested field is invalid:

```text
The MyApp "my-web-app" is invalid: spec.template.spec.containers[0].name: Invalid value: "My_Web_Container": a lowercase RFC 1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character (e.g. 'my-name',  or '123-abc', regex used for validation is '[a-z0-9]([-a-z0-9]*[a-z0-9])?')
```

The error stems directly from native Kubernetes validation logic, allowing the user to quickly fix the YAML without ever having to look at controller logs or deal with opaque webhook errors.

### Migrating API Versions (e.g. v1beta1 to v1)
A developer has an existing Custom Resource Definition with a `v1beta1` version that embeds an older native type (e.g., `batch/v1beta1.JobTemplateSpec`). Kubernetes is deprecating the `batch/v1beta1` API, so the developer wants to release a `v1` version of their CRD that uses `batch/v1.JobTemplateSpec`.

1. **Authoring the New Version**: The developer adds the `v1` version to their CRD manifest. In the schema for `v1`, they update the embedded type extension: `x-kubernetes-embedded-type: "batch/v1.JobTemplateSpec"`.
2. **Implementing Conversion**: Because native Kubernetes types often change field names or structures between beta and GA, the developer implements a standard CRD Conversion Webhook. This webhook is responsible for translating the JSON representation of the embedded `batch/v1beta1` JobTemplateSpec into the new `batch/v1` format.
3. **Deployment**: They deploy the updated CRD and the conversion webhook to the cluster.
4. **End-User Experience**: Existing custom resources stored in etcd as `v1beta1` are seamlessly translated to `v1` on read using the webhook. Any new validation rules native to `batch/v1.JobTemplateSpec` will apply on future updates, fully decoupling the native API migration from the custom controller's core logic.

## Proposal

### OpenAPI Schema Extension

We propose adding a new OpenAPI v3 vendor extension: `x-kubernetes-embedded-type`. It will accept a string corresponding to the identifier of the embedded type.

```yaml
properties:
  spec:
    type: object
    properties:
      template:
        type: object
        x-kubernetes-embedded-type: "core/v1.PodTemplateSpec"
```

### Dynamic Schema Injection

Kubernetes requires CRDs to have a complete structural schema. However, requiring developers to copy-paste the massive OpenAPI schema for `PodTemplateSpec` into their CRD is a terrible user experience. It would also statically bind their CRD to the schema of a specific Kubernetes version.

To solve this, when a CRD is created or updated, the `apiextensions-apiserver` will dynamically inject the native OpenAPI structural schema for the declared `x-kubernetes-embedded-type` into the CRD's schema. 

This means:
1. Developers **do not** need to provide the schema for the embedded type.
2. Developers **do not** need to use `x-kubernetes-preserve-unknown-fields: true`.
3. The API server's standard schema pruner will correctly strip unknown fields based on the current cluster version's definition of the embedded type.

### Supported Types

The `apiextensions-apiserver` will maintain a registry of supported identifiers mapped to their respective Go structs and validation functions. Initially, this registry will support:

- `"batch/v1.JobTemplateSpec"` -> maps to `batchv1.JobTemplateSpec` and uses `ValidateJobTemplateSpec`.
  - *Current Ecosystem Example:* **[KEDA (Kubernetes Event-driven Autoscaling)](https://github.com/kedacore/keda)** (~10.1k stars) embeds the JobSpec natively in its `ScaledJob` struct. ([View source: `ScaledJobSpec.JobTargetRef`](https://github.com/kedacore/keda/blob/main/apis/keda/v1alpAlsoha1/scaledjob_types.go#L55))
- `"core/v1.PodTemplateSpec"` -> maps to `corev1.PodTemplateSpec` and uses `ValidatePodTemplateSpec`.
  - *Current Ecosystem Examples:* **[Argo Rollouts](https://github.com/argoproj/argo-rollouts)** (~3.5k stars) natively embeds it in its `Rollout` struct. ([View source: `RolloutSpec.Template`](https://github.com/argoproj/argo-rollouts/blob/master/pkg/apis/rollouts/v1alpha1/types.go#L52))
  - *(Note: Projects like **Prometheus Operator** currently have to maintain massive custom equivalents, like `EmbeddedPersistentVolumeClaim` or custom Pod specs, due to the historical lack of native validation for embedded schemas).*
- `"core/v1.PersistentVolumeClaimTemplate"` -> maps to `corev1.PersistentVolumeClaimTemplate` and uses `ValidatePersistentVolumeClaimTemplate`.
  - *Current Ecosystem Examples:* **[OpenTelemetry Operator](https://github.com/open-telemetry/opentelemetry-operator)** (~1.5k stars) natively embeds it in its `Instrumentation` struct for persistent storage. ([View source: `InstrumentationSpec.VolumeClaimTemplate`](https://github.com/open-telemetry/opentelemetry-operator/blob/main/apis/v1alpha1/instrumentation_types.go#L172))
- `"resource.k8s.io/v1alpha3.ResourceClaimTemplateSpec"` (and `v1beta1`/`v1` variants) -> used for Dynamic Resource Allocation (DRA) requests and maps to `resourcev1alpha3.ResourceClaimTemplateSpec`.
  - *Current Ecosystem Examples:* Emerging AI workload operators and batch scheduling systems. While currently passed through native Pod templates, workload controllers that compose custom clusters (e.g., custom MPI operators or Slurm operators) will embed these directly as DRA adoption grows for GPU/TPU provisioning.

Additional types (like `ServiceSpec` or `NetworkPolicySpec`) can be added to this registry in future releases as demand dictates.

### Validation Execution

When a Custom Resource is submitted (Create or Update), the `customResourceValidator` in `apiextensions-apiserver` processes the request. The schema validator will traverse the schema and identify fields marked with `x-kubernetes-embedded-type`.

For each matching field:
1. The type identifier is looked up in the supported embedded types registry.
2. The unstructured data at that field path is unmarshaled into the corresponding native Go struct (e.g., `corev1.PodTemplateSpec`).
3. The registered validation function (e.g., `validation.ValidatePodTemplateSpec(struct, fieldPath)`) is invoked.
4. The resulting `field.ErrorList` is appended to the CR's overall validation errors, with the path properly scoped.

## Design Details

### CRD Schema Definition (`apiextensions-apiserver`)

We will add a new field to `JSONSchemaProps` in `k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1/types_jsonschema.go`:

```go
// x-kubernetes-embedded-type defines that the value is an embedded standard Kubernetes type.
XEmbeddedType *string `json:"x-kubernetes-embedded-type,omitempty" protobuf:"bytes,XX,opt,name=xKubernetesEmbeddedType"`
```

### CRD Schema Validation

In `pkg/apiserver/schema/validation.go`, we will add rules for the new extension:
- `x-kubernetes-embedded-type` must be a string.
- The string must match an identifier in the supported embedded types registry.
- It can only be applied to fields of `type: object`.
- It is mutually exclusive with `x-kubernetes-embedded-resource`.

### CustomResource Validator Update

Currently, `pkg/registry/customresource/validator.go` and `pkg/apiserver/validation/validation.go` handle CR validation. 
We will extend `SchemaValidator` to track paths containing `x-kubernetes-embedded-type`.

During `ValidateCustomResource` and `ValidateCustomResourceUpdate`:
1. The validation logic will dynamically identify the target struct based on the string identifier.
2. It will unmarshal the nested `map[string]interface{}` into the target struct.
3. It will call the associated update or create validation function.
4. If the unmarshaling fails (e.g., incompatible types that slipped past openapi validation), a schema validation error is returned.

### Impact on Ecosystem Tooling

Because we are using Option 2 (Dynamic Schema Injection), the burden on ecosystem tooling is drastically reduced.

1. **`client-go` and `kubectl`:** **No changes required.**
   - Because the `apiextensions-apiserver` dynamically injects standard OpenAPI `$ref` pointers into the published cluster OpenAPI document, clients reading the schema (like `kubectl explain`, `kubectl apply` client-side validation, or dynamic `client-go` clients) will simply follow the `$ref` to the native type. They do not need to know that `x-kubernetes-embedded-type` even exists.
2. **`controller-tools` (`controller-gen`):** **Requires update.**
   - The Kubebuilder `controller-gen` tool (located in `sigs.k8s.io/controller-tools`) parses Go structs to generate CRD YAML manifests. It will be updated to support this design in a completely **backward-compatible** way.
   - **New Marker Definition:** A new marker `+kubebuilder:validation:XEmbeddedType` will be added to `pkg/crd/markers/validation.go`.
     ```go
     // +kubebuilder:validation:XEmbeddedType
     // XEmbeddedType marks a field as an embedded standard Kubernetes type.
     //
     // Example:
     //
     //  // +kubebuilder:validation:XEmbeddedType="core/v1.PodTemplateSpec"
     //  Template corev1.PodTemplateSpec
     //
     // +controllertools:marker:generateHelp:category="CRD validation"
     type XEmbeddedType string
     ```
   - **Marker Implementation:** The `ApplyToSchema` interface will be implemented for `XEmbeddedType`. Depending on the `k8s.io/apiextensions-apiserver` dependency version imported by `controller-tools`, it will either map to the new `JSONSchemaProps.XEmbeddedType` field or safely inject it via `schema.Extensions.AddExtension("x-kubernetes-embedded-type", string(m))`.
   - **Type Inference (Auto-Detection):** To provide a seamless developer experience, `controller-gen`'s type parser (`pkg/crd/parser.go`) will be updated with an internal registry of known native types (e.g., `k8s.io/api/core/v1.PodTemplateSpec`). When the parser encounters a field using one of these known types, it will automatically short-circuit recursion. It will skip generating the deep structural schema for the type and instead implicitly apply the `XEmbeddedType` marker. This guarantees backward compatibility: existing projects will magically shrink their CRD size and gain native validation simply by updating their `controller-gen` binary, without writing any new markers.

### Handling Version Skew and Forward Compatibility

A significant edge case arises due to version skew between the API Server and the Custom Controller:
1. The cluster is upgraded to Kubernetes v1.31. The API Server now dynamically injects the v1.31 `PodTemplateSpec` schema, which includes a brand new field (e.g., `spec.newFeature`).
2. The user's Custom Controller is still compiled against `client-go` v1.30.
3. A user creates a CR utilizing `spec.template.spec.newFeature`. The API Server successfully validates and accepts it.
4. The Controller fetches the CR. Because the Go struct (`corev1.PodTemplateSpec`) in v1.30 does not have `newFeature`, standard `encoding/json.Unmarshal` will silently drop the field.
5. If the Controller performs a traditional "Read-Modify-Write" cycle (fetch, mutate struct, update), the new field is lost. The Controller creates the child Pod omitting the user's requested feature, leading to a silent failure of intent.

**How Kubernetes Ecosystem Tools Solve This**

This is a fundamental limitation of typed clients in Go. However, Kubernetes native tooling (`kubectl`, `client-go`) has already solved this version skew problem through specific architectural patterns. Developers utilizing `x-kubernetes-embedded-type` should adopt one of these established patterns:

1. **Server-Side Apply (The `kubectl apply` Approach):**
   Modern Kubernetes relies on Server-Side Apply (SSA) rather than Read-Modify-Write. `kubectl apply` does not download the resource, parse it into a typed Go struct, and upload it. Instead, it sends the raw YAML/JSON as a patch to the API server. The API Server performs the merge. 
   **Solution for Operators:** Advanced controllers should retain the original JSON payload of the embedded template (or use unstructured objects) and construct an SSA patch (`PatchType: ApplyPatchType`) for the child object. This ensures any fields the API Server accepted are blindly passed through to the child object, preserving forward compatibility perfectly.

2. **Untyped Passthrough (The Dynamic Client Approach):**
   When `kubectl` or operators need to handle arbitrary resources without dropping fields, they use `k8s.io/client-go/dynamic` and `unstructured.Unstructured`. Because `Unstructured` is backed by a `map[string]interface{}`, no fields are dropped during JSON decoding.
   **Solution for Operators:** If a controller does not need to explicitly read or mutate the embedded template (e.g., it purely passes it down to a Deployment), the developer can type the field as `runtime.RawExtension` or `*unstructured.Unstructured` in their Go struct instead of `corev1.PodTemplateSpec`. They still annotate it with `+kubebuilder:validation:XEmbeddedType="core/v1.PodTemplateSpec"`. The API Server strictly validates the YAML, but the controller receives the raw byte array/map, preserving all future fields when generating the child object via unstructured APIs.

3. **Strict Decoding (The Validation Approach):**
   When Kubernetes *must* decode into a typed struct and wants to ensure no data is silently lost, it uses the specialized `sigs.k8s.io/json` package which supports `UnmarshalStrict`, or relies on Server-Side Field Validation (`--validate=strict`).
   **Solution for Operators:** Controller authors can configure their clients (e.g., `controller-runtime`) to use **Strict Decoding**. If the controller encounters a field in the JSON that does not exist in its compiled Go struct, decoding fails explicitly rather than silently dropping the field. The controller can catch this error, update the CR's status to `Degraded` with the message `"Strict decoding failed: unknown field 'newFeature'"`, and refuse to process the resource. This forces a loud failure, alerting the cluster admin that the operator must be upgraded to support the new Kubernetes API features.

When a developer migrates an embedded type from an older version (e.g., `batch/v1beta1.JobTemplateSpec`) to a newer version (e.g., `batch/v1.JobTemplateSpec`) across CRD versions, they must use a standard CRD Conversion Webhook.

**For the initial MVP**, no explicit tooling is provided for this conversion. The developer's webhook must manually parse the JSON at the embedded path and map the fields to the new structure, just as they do for their own custom fields.

**Future Enhancements:**
Native Kubernetes types are complex, and manual conversion in a webhook is error-prone. While the API Server possesses the internal `runtime.Scheme` to convert these types automatically, exposing this functionality is out of scope for the MVP. Future iterations of this design may explore:
1.  **Helper Libraries:** Publishing a Go package (e.g., in `controller-runtime` or `apimachinery`) that exposes wrappers around native conversion functions so webhook authors can easily translate the raw JSON.
2.  **Auto-Conversion in API Server:** Allowing the API Server to automatically detect a change in `x-kubernetes-embedded-type` across CRD versions and natively convert the nested JSON block *before* passing the rest of the object to the user's conversion webhook.

## Backward Compatibility

- **Existing CRDs**: CRDs that do not use `x-kubernetes-embedded-type` will continue to function exactly as they do today. 
- **CRD Updates**: If a user updates an existing CRD to add `x-kubernetes-embedded-type` to a field, existing Custom Resources in the cluster might be technically invalid under the new native validation rules. This is consistent with standard Kubernetes behavior where tightening a schema only affects new creates/updates.
- **API Server Downgrades**: If the API server is downgraded to a version that doesn't understand `x-kubernetes-embedded-type`, the extension is simply ignored. The field falls back to standard OpenAPI structural validation.

## Alternatives Considered

- **Manual Schema Definition with Preserve Unknown Fields (Option 1):** An earlier iteration of this proposal required developers to manually add `x-kubernetes-preserve-unknown-fields: true` to bypass the API server's structural schema pruner, since copying the full native schema is impractical. This was rejected because relying on `preserve-unknown-fields` allows users to submit completely arbitrary garbage fields alongside valid template fields, which are then persisted to etcd. Dynamic schema injection solves this by securely enforcing the structural schema without burdening the developer.
- **Boolean flags per type (e.g., `x-kubernetes-embedded-pod-template: true`):** A simpler initial approach, but it doesn't scale well. As developers request support for `VolumeClaimTemplate`, `JobTemplateSpec`, etc., we would need to add a new boolean extension for every type, cluttering the CRD schema specification. A generic string-based identifier is much more extensible.
- **ValidatingWebhookConfiguration:** Relying entirely on user-deployed webhooks. This forces developers to write boilerplate validation servers, increases cluster dependency graph complexity, and increases latency for API requests.