/*
Copyright 2023 The Kubernetes Authors.

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

package openapi

import (
	"github.com/google/cel-go/common/types/ref"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/apiserver/pkg/cel/common"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

var _ common.Schema = (*Schema)(nil)
var _ common.SchemaOrBool = (*SchemaOrBool)(nil)

type Schema struct {
	Schema *spec.Schema
}

func (s *Schema) Type() string {
	if s == nil || s.Schema == nil || len(s.Schema.Type) == 0 {
		return ""
	}
	return s.Schema.Type[0]
}

func (s *Schema) Format() string {
	if s == nil || s.Schema == nil {
		return ""
	}
	return s.Schema.Format
}

func (s *Schema) Items() common.Schema {
	if s == nil || s.Schema == nil || s.Schema.Items == nil || s.Schema.Items.Schema == nil {
		return nil
	}
	return &Schema{s.Schema.Items.Schema}
}

func (s *Schema) Properties() map[string]common.Schema {
	if s == nil || s.Schema == nil || s.Schema.Properties == nil {
		return nil
	}
	res := make(map[string]common.Schema, len(s.Schema.Properties))
	for k, v := range s.Schema.Properties {
		res[k] = &Schema{&v}
	}
	return res
}

func (s *Schema) Property(name string) (common.Schema, bool) {
	if s == nil || s.Schema == nil || s.Schema.Properties == nil {
		return nil, false
	}
	if prop, ok := s.Schema.Properties[name]; ok {
		return &Schema{Schema: &prop}, true
	}
	return nil, false
}

func (s *Schema) AdditionalProperties() common.SchemaOrBool {
	if s == nil || s.Schema == nil || s.Schema.AdditionalProperties == nil {
		return nil
	}
	return &SchemaOrBool{s.Schema.AdditionalProperties}
}

func (s *Schema) Default() any {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.Default
}

func (s *Schema) Minimum() *float64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.Minimum
}

func (s *Schema) IsExclusiveMinimum() bool {
	if s == nil || s.Schema == nil {
		return false
	}
	return s.Schema.ExclusiveMinimum
}

func (s *Schema) Maximum() *float64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.Maximum
}

func (s *Schema) IsExclusiveMaximum() bool {
	if s == nil || s.Schema == nil {
		return false
	}
	return s.Schema.ExclusiveMaximum
}

func (s *Schema) MultipleOf() *float64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.MultipleOf
}

func (s *Schema) MinItems() *int64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.MinItems
}

func (s *Schema) MaxItems() *int64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.MaxItems
}

func (s *Schema) UniqueItems() bool {
	if s == nil || s.Schema == nil {
		return false
	}
	return s.Schema.UniqueItems
}

func (s *Schema) MinLength() *int64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.MinLength
}

func (s *Schema) MaxLength() *int64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.MaxLength
}

func (s *Schema) Pattern() string {
	if s == nil || s.Schema == nil {
		return ""
	}
	return s.Schema.Pattern
}

func (s *Schema) MaxProperties() *int64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.MaxProperties
}

func (s *Schema) MinProperties() *int64 {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.MinProperties
}

func (s *Schema) Required() []string {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.Required
}

func (s *Schema) Enum() []any {
	if s == nil || s.Schema == nil {
		return nil
	}
	return s.Schema.Enum
}

func (s *Schema) Nullable() bool {
	if s == nil || s.Schema == nil {
		return false
	}
	return s.Schema.Nullable
}

func (s *Schema) AllOf() []common.Schema {
	if s == nil || s.Schema == nil {
		return nil
	}
	var res []common.Schema
	for _, nestedSchema := range s.Schema.AllOf {
		res = append(res, &Schema{&nestedSchema})
	}
	return res
}

func (s *Schema) AnyOf() []common.Schema {
	if s == nil || s.Schema == nil {
		return nil
	}
	var res []common.Schema
	for _, nestedSchema := range s.Schema.AnyOf {
		res = append(res, &Schema{&nestedSchema})
	}
	return res
}

func (s *Schema) OneOf() []common.Schema {
	if s == nil || s.Schema == nil {
		return nil
	}
	var res []common.Schema
	for _, nestedSchema := range s.Schema.OneOf {
		res = append(res, &Schema{&nestedSchema})
	}
	return res
}

func (s *Schema) Not() common.Schema {
	if s == nil || s.Schema == nil || s.Schema.Not == nil {
		return nil
	}
	return &Schema{s.Schema.Not}
}

func (s *Schema) IsXIntOrString() bool {
	if s == nil || s.Schema == nil {
		return false
	}
	return isXIntOrString(s.Schema)
}

func (s *Schema) IsXEmbeddedResource() bool {
	if s == nil || s.Schema == nil {
		return false
	}
	return isXEmbeddedResource(s.Schema)
}

func (s *Schema) IsXPreserveUnknownFields() bool {
	if s == nil || s.Schema == nil {
		return false
	}
	return isXPreserveUnknownFields(s.Schema)
}

func (s *Schema) XListType() string {
	if s == nil || s.Schema == nil {
		return ""
	}
	return getXListType(s.Schema)
}

func (s *Schema) XMapType() string {
	if s == nil || s.Schema == nil {
		return ""
	}
	return getXMapType(s.Schema)
}

func (s *Schema) XListMapKeys() []string {
	if s == nil || s.Schema == nil {
		return nil
	}
	return getXListMapKeys(s.Schema)
}

func (s *Schema) XValidations() []common.ValidationRule {
	if s == nil || s.Schema == nil {
		return nil
	}
	return getXValidations(s.Schema)
}

func (s *Schema) WithTypeAndObjectMeta() common.Schema {
	if s == nil || s.Schema == nil {
		return nil
	}
	return &Schema{common.WithTypeAndObjectMeta(s.Schema)}
}

type SchemaOrBool struct {
	SchemaOrBool *spec.SchemaOrBool
}

func (s *SchemaOrBool) Schema() common.Schema {
	if s == nil || s.SchemaOrBool == nil || s.SchemaOrBool.Schema == nil {
		return nil
	}
	return &Schema{s.SchemaOrBool.Schema}
}

func (s *SchemaOrBool) Allows() bool {
	if s == nil || s.SchemaOrBool == nil {
		return false
	}
	return s.SchemaOrBool.Allows
}

func UnstructuredToVal(unstructured any, schema *spec.Schema) ref.Val {
	return common.UnstructuredToVal(unstructured, &Schema{schema})
}

func SchemaDeclType(s *spec.Schema, isResourceRoot bool) *apiservercel.DeclType {
	return common.SchemaDeclType(&Schema{Schema: s}, isResourceRoot)
}

func MakeMapList(sts *spec.Schema, items []interface{}) (rv common.MapList) {
	return common.MakeMapList(&Schema{Schema: sts}, items)
}
