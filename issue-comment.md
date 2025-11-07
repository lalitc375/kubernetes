40Hi @brejman,

Thank you for your patience and for the detailed discussion on this issue. After further analysis and reviewing the logs, I have a much clearer and more precise explanation.

This is not a bug in the Server-Side Apply (SSA) merge logic itself, but rather that the SSA logic, which behaving as designed, can produce an object that is invalid according to the CRD schema, whereas a non-SSA apply produces a different, valid object.

The core of the issue is the difference in the final object sent to the validator.

### Server-Side Apply (SSA) Outcome

When an SSA manager stops managing a field (`arrayMap`) while other fields within the same object (`spec`) are still present, the SSA merge logic sets the field to `null`.

<details>
<summary>Logs for SSA showing `arrayMap: null`</summary>

```
I1106 21:24:49.773262       1 schema.go:182] CRDValidationDebug: Validating object at path "", json: {"apiVersion":"example.com/v1alpha1","kind":"MyCrd","metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"apiVersion\":\"example.com/v1alpha1\",\"kind\":\"MyCrd\",\"metadata\":{\"name\":\"repro\"},\"spec\":{\"baz\":\"test1\"}}\\n"},"creationTimestamp":"2025-11-06T21:21:26Z","generation":5,"managedFields":[{"apiVersion":"example.com/v1alpha1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{"f:kubectl.kubernetes.io/last-applied-configuration":{}}}},"manager":"kubectl-last-applied","operation":"Apply"},{"apiVersion":"example.com/v1alpha1","fieldsType":"FieldsV1","fieldsV1":{"f:spec":{"f:baz":{}}},"manager":"kubectl","operation":"Apply","time":"2025-11-06T21:24:49Z"}],"name":"repro","resourceVersion":"2706","uid":"120958ab-2746-40e8-8093-623ac0dd6dc6"},"spec":{"arrayMap":null,"baz":"test1"}}
I1106 21:24:49.773283       1 schema.go:127] CRDValidationDebug: Validating object at path "spec", object: map[arrayMap:<nil> baz:test1]
I1106 21:24:49.773292       1 schema.go:182] CRDValidationDebug: Validating object at path "spec", json: {"arrayMap":null,"baz":"test1"}
I1106 21:24:49.773304       1 schema.go:127] CRDValidationDebug: Validating object at path "spec.arrayMap", object: <nil>
I1106 21:24:49.773313       1 type.go:137] CRDValidationDebug: Validating null object at path "spec.arrayMap", nullable in schema: false
```
</details>

This results in an object like `{"spec":{"arrayMap":null,"baz":"test1"}}`. The CRD validator correctly rejects this object because the `arrayMap` field is present with a `null` value, but the schema does not have `nullable: true`.

### Non-Server-Side Apply Outcome

When using a traditional client-side `kubectl apply`, the client computes a patch to remove the `arrayMap` field entirely.

<details>
<summary>Logs for non-SSA showing `arrayMap` is absent</summary>

```
I1106 21:27:15.961635       1 schema.go:182] CRDValidationDebug: Validating object at path "", json: {"apiVersion":"example.com/v1alpha1","kind":"MyCrd","metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"apiVersion\":\"example.com/v1alpha1\",\"kind\":\"MyCrd\",\"metadata\":{\"annotations\":{},\"name\":\"repro\"},\"spec\":{\"baz\":\"test1\"}}\\n"},"creationTimestamp":"2025-11-06T21:21:26Z","generation":5,"managedFields":[{"apiVersion":"example.com/v1alpha1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{"f:kubectl.kubernetes.io/last-applied-configuration":{}}}},"manager":"kubectl-client-side-apply","operation":"Update","time":"2025-11-06T21:27:15Z"}],"name":"repro","resourceVersion":"2706","uid":"120958ab-2746-40e8-8093-623ac0dd6dc6"},"spec":{"baz":"test1"}}
I1106 21:27:15.961658       1 schema.go:127] CRDValidationDebug: Validating object at path "spec", object: map[baz:test1]
I1106 21:27:15.961669       1 schema.go:182] CRDValidationDebug: Validating object at path "spec", json: {"baz":"test1"}
```
</details>

This results in an object like `{"spec":{"baz":"test1"}}`. The `arrayMap` field is simply absent. This object is valid according to the schema, so the operation succeeds.

### Scenario 1: Create with SSA, then Remove with SSA (No Patch)

When an SSA manager removes the *only* field within an object, the SSA logic prunes the parent object itself to `null`.

<details>
<summary>Logs for SSA showing `spec: null`</summary>

```
I1106 22:28:11.036621       1 store.go:667] CRDValidationDebug: SSA: user intent: {"apiVersion":"example.com/v1alpha1","kind":"MyCrd","metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"apiVersion\":\"example.com/v1alpha1\",\"kind\":\"MyCrd\",\"metadata\":{\"name\":\"repro\"},\"spec\":{}}\\n"},"creationTimestamp":"2025-11-06T22:27:30Z","generation":1,"managedFields":[{"apiVersion":"example.com/v1alpha1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{"f:kubectl.kubernetes.io/last-applied-configuration":{}}}},"manager":"kubectl-last-applied","operation":"Apply"},{"apiVersion":"example.com/v1alpha1","fieldsType":"FieldsV1","fieldsV1":{"f:spec":{}},"manager":"kubectl","operation":"Apply","time":"2025-11-06T22:28:11Z"}],"name":"repro","resourceVersion":"1614","uid":"066dcc71-8e17-44ed-8ec2-874078c78b9c"},"spec":null}
I1106 22:28:11.036794       1 schema.go:182] CRDValidationDebug: Validating object at path "", json: {"apiVersion":"example.com/v1alpha1","kind":"MyCrd","metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"apiVersion\":\"example.com/v1alpha1\",\"kind\":\"MyCrd\",\"metadata\":{\"name\":\"repro\"},\"spec\":{}}\\n"},"creationTimestamp":"2025-11-06T22:27:30Z","generation":2,"managedFields":[{"apiVersion":"example.com/v1alpha1","fieldsType":"FieldsV1","fieldsV1":{"f:metadata":{"f:annotations":{"f:kubectl.kubernetes.io/last-applied-configuration":{}}}},"manager":"kubectl-last-applied","operation":"Apply"},{"apiVersion":"example.com/v1alpha1","fieldsType":"FieldsV1","fieldsV1":{"f:spec":{}},"manager":"kubectl","operation":"Apply","time":"2025-11-06T22:28:11Z"}],"name":"repro","resourceVersion":"1614","uid":"066dcc71-8e17-44ed-8ec2-874078c78b9c"},"spec":null}
```
</details>

This results in an object like `{"spec": null}`. The CRD validator correctly rejects this because the schema defines `spec` as `type: object`, but receives `null`.

### Conclusion

The inconsistent behavior you observed is caused by these two different approaches generating two different final objects. The SSA-generated object is invalid against the schema, while the non-SSA-generated object is valid.

This highlights a challenging interaction between the strict ownership model of SSA and CRD validation. While SSA is functioning correctly, its output in this specific multi-manager scenario (especially when interacting with a non-SSA manager) can lead to validation failures that users might not expect.

The immediate workaround is to add `nullable: true` to the schema to accommodate SSA's behavior. For a longer-term solution, we might consider if there's a way to improve this interaction, perhaps by providing more explicit error messages that guide the user on how to resolve the ownership conflict that led to the `null` value.



