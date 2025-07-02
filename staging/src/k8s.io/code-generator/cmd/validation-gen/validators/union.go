/*
Copyright 2021 The Kubernetes Authors.

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

package validators

import (
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/code-generator/cmd/validation-gen/util"
	"k8s.io/gengo/v2/codetags"
	"k8s.io/gengo/v2/parser/tags"
	"k8s.io/gengo/v2/types"
)

var discriminatedUnionValidator = types.Name{Package: libValidationPkg, Name: "DiscriminatedUnion"}
var unionValidator = types.Name{Package: libValidationPkg, Name: "Union"}

var newDiscriminatedUnionMembership = types.Name{Package: libValidationPkg, Name: "NewDiscriminatedUnionMembership"}
var newUnionMembership = types.Name{Package: libValidationPkg, Name: "NewUnionMembership"}

func init() {
	// Unions are comprised of multiple tags, which need to share information
	// between them.  The tags are on struct fields, but the validation
	// actually pertains to the struct itself.
	shared := map[*types.Type]unions{}
	RegisterTypeValidator(unionTypeValidator{shared})
	RegisterTagValidator(unionDiscriminatorTagValidator{shared})
	RegisterTagValidator(unionMemberTagValidator{shared})
}

type unionTypeValidator struct {
	shared map[*types.Type]unions
}

func (unionTypeValidator) Init(_ Config) {}

func (unionTypeValidator) Name() string {
	return "unionTypeValidator"
}

func (utv unionTypeValidator) GetValidations(context Context) (Validations, error) {
	result := Validations{}

	// Gengo does not treat struct definitions as aliases, which is
	// inconsistent but unlikely to change. That means we don't REALLY need to
	// handle it here, but let's be extra careful and extract the most concrete
	// type possible.
	if util.NonPointer(util.NativeType(context.Type)).Kind != types.Struct {
		return result, nil
	}

	unions := utv.shared[context.Type]
	if len(unions) == 0 {
		return result, nil
	}

	// Sort the keys for stable output.
	keys := make([]string, 0, len(unions))
	for k := range unions {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, unionName := range keys {
		u := unions[unionName]
		if len(u.fieldMembers) > 0 || u.discriminator != nil {
			// TODO: Avoid the "local" here. This was added to to avoid errors caused when the package is an empty string.
			//       The correct package would be the output package but is not known here. This does not show up in generated code.
			// TODO: Append a consistent hash suffix to avoid generated name conflicts?
			supportVarName := PrivateVar{Name: "UnionMembershipFor" + context.Type.Name.Name + unionName, Package: "local"}
			ptrType := types.PointerTo(context.Type)

			if u.discriminator != nil {
				supportVar := Variable(supportVarName,
					Function(unionMemberTagName, DefaultFlags, newDiscriminatedUnionMembership,
						append([]any{*u.discriminator}, toSliceAny(u.fields)...)...))
				result.Variables = append(result.Variables, supportVar)

				var extractorArgs []any
				extractorArgs = append(extractorArgs, supportVarName)

				discriminatorExtractor := FunctionLiteral{
					Parameters: []ParamResult{{Name: "obj", Type: ptrType}},
					Results:    []ParamResult{{Type: types.String}},
					Body:       fmt.Sprintf("if obj != nil {return string(obj.%s)}; return \"\"", u.discriminatorMember.Name), // Cast to string
				}
				extractorArgs = append(extractorArgs, discriminatorExtractor)

				for _, member := range u.fieldMembers {
					extractor := FunctionLiteral{
						Parameters: []ParamResult{{Name: "obj", Type: ptrType}},
						Results:    []ParamResult{{Type: types.Any}},
						Body:       fmt.Sprintf("if obj != nil {return obj.%s}; return nil", member.Name),
					}
					extractorArgs = append(extractorArgs, extractor)
				}

				fn := Function(unionMemberTagName, DefaultFlags, discriminatedUnionValidator, extractorArgs...)
				result.Functions = append(result.Functions, fn)
			} else {
				supportVar := Variable(supportVarName, Function(unionMemberTagName, DefaultFlags, newUnionMembership, toSliceAny(u.fields)...))
				result.Variables = append(result.Variables, supportVar)

				var extractorArgs []any
				extractorArgs = append(extractorArgs, supportVarName)

				for _, member := range u.fieldMembers {
					extractor := FunctionLiteral{
						Parameters: []ParamResult{{Name: "obj", Type: ptrType}},
						Results:    []ParamResult{{Type: types.Any}},
						Body:       fmt.Sprintf("if obj != nil {return obj.%s}; return nil", member.Name),
					}
					extractorArgs = append(extractorArgs, extractor)
				}

				fn := Function(unionMemberTagName, DefaultFlags, unionValidator, extractorArgs...)
				result.Functions = append(result.Functions, fn)
			}
		}
	}

	return result, nil
}

func toSliceAny[T any](t []T) []any {
	result := make([]any, len(t))
	for i, v := range t {
		result[i] = v
	}
	return result
}

const (
	unionDiscriminatorTagName = "k8s:unionDiscriminator"
	unionMemberTagName        = "k8s:unionMember"
)

type unionDiscriminatorTagValidator struct {
	shared map[*types.Type]unions
}

func (unionDiscriminatorTagValidator) Init(_ Config) {}

func (unionDiscriminatorTagValidator) TagName() string {
	return unionDiscriminatorTagName
}

// Shared between unionDiscriminatorTagValidator and unionMemberTagValidator.
var unionTagValidScopes = sets.New(ScopeField)

func (unionDiscriminatorTagValidator) ValidScopes() sets.Set[Scope] {
	return unionTagValidScopes
}

func (udtv unionDiscriminatorTagValidator) GetValidations(context Context, tag codetags.Tag) (Validations, error) {
	// This tag can apply to value and pointer fields, as well as typedefs
	// (which should never be pointers). We need to check the concrete type.
	if t := util.NonPointer(util.NativeType(context.Type)); t != types.String {
		return Validations{}, fmt.Errorf("can only be used on string types (%s)", rootTypeString(context.Type, t))
	}
	if udtv.shared[context.Parent] == nil {
		udtv.shared[context.Parent] = unions{}
	}
	unionArg, _ := tag.NamedArg("union") // optional
	u := udtv.shared[context.Parent].getOrCreate(unionArg.Value)

	var discriminatorFieldName string
	if jsonAnnotation, ok := tags.LookupJSON(*context.Member); ok {
		discriminatorFieldName = jsonAnnotation.Name
		u.discriminator = &discriminatorFieldName
		u.discriminatorMember = context.Member
	}

	// This tag does not actually emit any validations, it just accumulates
	// information. The validation is done by the unionTypeValidator.
	return Validations{}, nil
}

func (udtv unionDiscriminatorTagValidator) Docs() TagDoc {
	return TagDoc{
		Tag:         udtv.TagName(),
		Scopes:      udtv.ValidScopes().UnsortedList(),
		Description: "Indicates that this field is the discriminator for a union.",
		Args: []TagArgDoc{{
			Name:        "union",
			Description: "<string>",
			Docs:        "the name of the union, if more than one exists",
			Type:        codetags.ArgTypeString,
		}},
	}
}

type unionMemberTagValidator struct {
	shared map[*types.Type]unions
}

func (unionMemberTagValidator) Init(_ Config) {}

func (unionMemberTagValidator) TagName() string {
	return unionMemberTagName
}

func (unionMemberTagValidator) ValidScopes() sets.Set[Scope] {
	return unionTagValidScopes
}

func (umtv unionMemberTagValidator) GetValidations(context Context, tag codetags.Tag) (Validations, error) {
	var fieldName string
	jsonTag, ok := tags.LookupJSON(*context.Member)
	if !ok {
		return Validations{}, fmt.Errorf("field %q is a union member but has no JSON struct field tag", context.Member)
	}
	fieldName = jsonTag.Name
	if len(fieldName) == 0 {
		return Validations{}, fmt.Errorf("field %q is a union member but has no JSON name", context.Member)
	}

	if umtv.shared[context.Parent] == nil {
		umtv.shared[context.Parent] = unions{}
	}
	unionArg, _ := tag.NamedArg("union") // optional
	var memberName string
	if memberNameArg, ok := tag.NamedArg("memberName"); ok { // optional
		memberName = memberNameArg.Value
	} else {
		memberName = context.Member.Name // default
	}

	u := umtv.shared[context.Parent].getOrCreate(unionArg.Value)
	u.fields = append(u.fields, [2]string{fieldName, memberName})
	u.fieldMembers = append(u.fieldMembers, context.Member)

	// This tag does not actually emit any validations, it just accumulates
	// information. The validation is done by the unionTypeValidator.
	return Validations{}, nil
}

func (umtv unionMemberTagValidator) Docs() TagDoc {
	return TagDoc{
		Tag:         umtv.TagName(),
		Scopes:      umtv.ValidScopes().UnsortedList(),
		Description: "Indicates that this field is a member of a union.",
		Args: []TagArgDoc{{
			Name:        "union",
			Description: "<string>",
			Docs:        "the name of the union, if more than one exists",
			Type:        codetags.ArgTypeString,
		}, {
			Name:        "memberName",
			Description: "<string>",
			Docs:        "the discriminator value for this member",
			Default:     "the field's name",
			Type:        codetags.ArgTypeString,
		}},
	}
}

// union defines how a union validation will be generated, based
// on +k8s:unionMember and +k8s:unionDiscriminator tags found in a go struct.
type union struct {
	// fields provides field information about all the members of the union.
	// Each item provides a fieldName and memberName pair, where [0] identifies
	// the field name and [1] identifies the union member Name. fields is index
	// aligned with fieldMembers.
	// If member name is not set, it defaults to the go struct field name.
	fields [][2]string
	// fieldMembers describes all the members of the union.
	fieldMembers []*types.Member

	// discriminator is the name of the discriminator field
	discriminator *string
	// discriminatorMember describes the discriminator field.
	discriminatorMember *types.Member
}

// unions represents all the unions for a go struct.
type unions map[string]*union

// getOrCreate gets a union by name, or initializes a new union by the given name.
func (us unions) getOrCreate(name string) *union {
	var u *union
	var ok bool
	if u, ok = us[name]; !ok {
		u = &union{}
		us[name] = u
	}
	return u
}
