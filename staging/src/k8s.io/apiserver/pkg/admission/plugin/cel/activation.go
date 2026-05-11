/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/cel-go/interpreter"

	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/apiserver/pkg/cel/library"
	"k8s.io/apiserver/pkg/cel/openapi"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
)

// prepareVal returns a CEL-compatible value for the given object.
// It uses TypedToVal for native objects with schemas, the underlying map for Unstructured, and falls back to JSON conversion.
func prepareVal(obj any, schema common.Schema) (any, error) {
	if obj == nil {
		return nil, nil
	}
	// Short-circuit for unstructured objects.
	if u, ok := obj.(*unstructured.Unstructured); ok {
		if u == nil {
			return nil, nil
		}
		return u.Object, nil
	}

	val := reflect.ValueOf(obj)
	if val.Kind() == reflect.Ptr && val.IsNil() {
		return nil, nil
	}

	if schema != nil {
		return common.TypedToVal(obj, schema), nil
	}

	v, err := convertObjectToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return v.Object, nil
}

func resolveSchema(schemaResolver resolver.SchemaResolver, gvk schema.GroupVersionKind) (*openapi.Schema, error) {
	if schemaResolver == nil {
		return nil, nil
	}
	s, err := schemaResolver.ResolveSchema(gvk)
	if err != nil {
		if errors.Is(err, resolver.ErrSchemaNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &openapi.Schema{Schema: common.WithTypeAndObjectMeta(s)}, nil
}

// newActivation creates an activation for CEL admission plugins from the given request, admission chain and
// variable binding information.
func newActivation(compositionCtx CompositionContext, versionedAttr *admission.VersionedAttributes, request *admissionv1.AdmissionRequest, inputs OptionalVariableBindings, namespace *v1.Namespace, schemaResolver resolver.SchemaResolver) (*evaluationActivation, error) {
	var oldObjectVal, objectVal, requestVal, namespaceVal any
	var err error

	var objectSchema, oldObjectSchema, namespaceSchema common.Schema
	if schemaResolver != nil {
		if schema, err := resolveSchema(schemaResolver, versionedAttr.VersionedKind); err == nil && schema != nil {
			objectSchema = schema
			oldObjectSchema = schema
		}
		if schema, err := resolveSchema(schemaResolver, v1.SchemeGroupVersion.WithKind("Namespace")); err == nil && schema != nil {
			namespaceSchema = schema
		}
	}

	oldObjectVal, err = prepareVal(versionedAttr.VersionedOldObject, oldObjectSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare oldObject variable for evaluation: %w", err)
	}

	objectVal, err = prepareVal(versionedAttr.VersionedObject, objectSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare object variable for evaluation: %w", err)
	}

	var paramsVal any
	var authorizerVal, requestResourceAuthorizerVal any
	if inputs.VersionedParams != nil {
		// GVK is not available for inputs.VersionedParams. We can't use the schema to resolve the params type.
		// TODO: Pass schema for versioned params object, after providing overrides to get GroupVersionKind for params.
		paramsVal, err = prepareVal(inputs.VersionedParams, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare params variable for evaluation: %w", err)
		}
	}

	if inputs.Authorizer != nil {
		authorizerVal = library.NewAuthorizerVal(versionedAttr.GetUserInfo(), inputs.Authorizer)
		requestResourceAuthorizerVal = library.NewResourceAuthorizerVal(versionedAttr.GetUserInfo(), inputs.Authorizer, versionedAttr)
	}

	requestUnstr, err := convertObjectToUnstructured(request)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare request variable for evaluation: %w", err)
	}
	requestVal = requestUnstr.Object

	namespaceVal, err = prepareVal(namespace, namespaceSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare namespace variable for evaluation: %w", err)
	}

	va := &evaluationActivation{
		object:                    objectVal,
		oldObject:                 oldObjectVal,
		params:                    paramsVal,
		request:                   requestVal,
		namespace:                 namespaceVal,
		authorizer:                authorizerVal,
		requestResourceAuthorizer: requestResourceAuthorizerVal,
	}

	// composition is an optional feature that only applies for ValidatingAdmissionPolicy and MutatingAdmissionPolicy.
	if compositionCtx != nil {
		va.variables = compositionCtx.Variables(va)
	}
	return va, nil
}

type evaluationActivation struct {
	object, oldObject, params, request, namespace    any
	authorizer, requestResourceAuthorizer, variables any
}

// ResolveName returns a value from the activation by qualified name, or false if the name
// could not be found.
func (a *evaluationActivation) ResolveName(name string) (any, bool) {
	switch name {
	case ObjectVarName:
		return a.object, true
	case OldObjectVarName:
		return a.oldObject, true
	case ParamsVarName:
		return a.params, true // params may be null
	case RequestVarName:
		return a.request, true
	case NamespaceVarName:
		return a.namespace, true
	case AuthorizerVarName:
		return a.authorizer, a.authorizer != nil
	case RequestResourceAuthorizerVarName:
		return a.requestResourceAuthorizer, a.requestResourceAuthorizer != nil
	case VariableVarName: // variables always present
		return a.variables, true
	default:
		return nil, false
	}
}

// Parent returns the parent of the current activation, may be nil.
// If non-nil, the parent will be searched during resolve calls.
func (a *evaluationActivation) Parent() interpreter.Activation {
	return nil
}

// Evaluate runs a compiled CEL admission plugin expression using the provided activation and CEL
// runtime cost budget.
func (a *evaluationActivation) Evaluate(ctx context.Context, compositionCtx CompositionContext, compilationResult CompilationResult, remainingBudget int64) (EvaluationResult, int64, error) {
	var evaluation = EvaluationResult{}
	if compilationResult.ExpressionAccessor == nil { // in case of placeholder
		return evaluation, remainingBudget, nil
	}

	evaluation.ExpressionAccessor = compilationResult.ExpressionAccessor
	if compilationResult.Error != nil {
		evaluation.Error = &cel.Error{
			Type:   cel.ErrorTypeInvalid,
			Detail: fmt.Sprintf("compilation error: %v", compilationResult.Error),
			Cause:  compilationResult.Error,
		}
		return evaluation, remainingBudget, nil
	}
	if compilationResult.Program == nil {
		evaluation.Error = &cel.Error{
			Type:   cel.ErrorTypeInternal,
			Detail: "unexpected internal error compiling expression",
		}
		return evaluation, remainingBudget, nil
	}
	t1 := time.Now()
	evalResult, evalDetails, err := compilationResult.Program.ContextEval(ctx, a)
	// budget may be spent due to lazy evaluation of composited variables
	if compositionCtx != nil {
		compositionCost := compositionCtx.GetAndResetCost()
		if compositionCost > remainingBudget {
			return evaluation, -1, &cel.Error{
				Type:   cel.ErrorTypeInvalid,
				Detail: "validation failed due to running out of cost budget, no further validation rules will be run",
				Cause:  cel.ErrOutOfBudget,
			}
		}
		remainingBudget -= compositionCost
	}
	elapsed := time.Since(t1)
	evaluation.Elapsed = elapsed
	if evalDetails == nil {
		return evaluation, -1, &cel.Error{
			Type:   cel.ErrorTypeInternal,
			Detail: fmt.Sprintf("runtime cost could not be calculated for expression: %v, no further expression will be run", compilationResult.ExpressionAccessor.GetExpression()),
		}
	} else {
		rtCost := evalDetails.ActualCost()
		if rtCost == nil {
			return evaluation, -1, &cel.Error{
				Type:   cel.ErrorTypeInvalid,
				Detail: fmt.Sprintf("runtime cost could not be calculated for expression: %v, no further expression will be run", compilationResult.ExpressionAccessor.GetExpression()),
				Cause:  cel.ErrOutOfBudget,
			}
		} else {
			if *rtCost > math.MaxInt64 || int64(*rtCost) > remainingBudget {
				return evaluation, -1, &cel.Error{
					Type:   cel.ErrorTypeInvalid,
					Detail: "validation failed due to running out of cost budget, no further validation rules will be run",
					Cause:  cel.ErrOutOfBudget,
				}
			}
			remainingBudget -= int64(*rtCost)
		}
	}
	if err != nil {
		evaluation.Error = &cel.Error{
			Type:   cel.ErrorTypeInvalid,
			Detail: fmt.Sprintf("expression '%v' resulted in error: %v", compilationResult.ExpressionAccessor.GetExpression(), err),
		}
	} else {
		evaluation.EvalResult = evalResult
	}
	return evaluation, remainingBudget, nil
}
