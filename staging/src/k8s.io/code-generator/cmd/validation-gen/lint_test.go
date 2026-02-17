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

package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"k8s.io/code-generator/cmd/validation-gen/validators"
	"k8s.io/gengo/v2/codetags"
	"k8s.io/gengo/v2/types"
)

// fakeTagExtractor is a test implementation of tagExtractor that recognizes
// a fixed set of tag names by simple prefix matching on "+k8s:" comments.
type fakeTagExtractor struct {
	// knownTags is the set of tag names (without "+") that this extractor
	// considers to be valid validation tags, e.g. "k8s:minimum".
	knownTags []string
}

func (f fakeTagExtractor) ExtractTags(_ validators.Context, comments []string) ([]codetags.Tag, error) {
	var tags []codetags.Tag
	for _, c := range comments {
		for _, known := range f.knownTags {
			if strings.HasPrefix(c, "+"+known) {
				tags = append(tags, codetags.Tag{Name: known})
			}
		}
	}
	return tags, nil
}

// defaultFakeExtractor recognizes common validation tags for testing.
var defaultFakeExtractor = fakeTagExtractor{
	knownTags: []string{
		"k8s:minimum", "k8s:maxItems", "k8s:maxLength", "k8s:enum",
		"k8s:immutable", "k8s:format", "k8s:optional", "k8s:required",
	},
}

func ruleAlwaysPass(comments []string) (string, error) {
	return "", nil
}

func ruleAlwaysFail(comments []string) (string, error) {
	return "lintfail", nil
}

func ruleAlwaysErr(comments []string) (string, error) {
	return "", errors.New("linterr")
}

func mkCountRule(counter *int, realRule lintRule) lintRule {
	return func(comments []string) (string, error) {
		(*counter)++
		return realRule(comments)
	}
}

func TestLintCommentsRuleInvocation(t *testing.T) {
	tests := []struct {
		name              string
		rules             []lintRule
		commentLineGroups [][]string
		wantErr           bool
		wantCount         int
	}{
		{
			name:              "0 rules, 0 comments",
			rules:             []lintRule{},
			commentLineGroups: [][]string{},
			wantErr:           false,
			wantCount:         0,
		},
		{
			name:              "1 rule, 1 comment",
			rules:             []lintRule{ruleAlwaysPass},
			commentLineGroups: [][]string{{"comment"}},
			wantErr:           false,
			wantCount:         1,
		},
		{
			name:              "3 rules, 3 comments",
			rules:             []lintRule{ruleAlwaysPass, ruleAlwaysFail, ruleAlwaysErr},
			commentLineGroups: [][]string{{"comment1"}, {"comment2"}, {"comment3"}},
			wantErr:           true,
			wantCount:         9,
		},
		{
			name:              "1 rule, 1 comment, rule fails",
			rules:             []lintRule{ruleAlwaysFail},
			commentLineGroups: [][]string{{"comment"}},
			wantErr:           false,
			wantCount:         1,
		},
		{
			name:              "1 rule, 1 comment, rule errors",
			rules:             []lintRule{ruleAlwaysErr},
			commentLineGroups: [][]string{{"comment"}},
			wantErr:           true,
			wantCount:         1,
		},
		{
			name:              "3 rules, 1 comment, rule errors in the middle",
			rules:             []lintRule{ruleAlwaysPass, ruleAlwaysErr, ruleAlwaysFail},
			commentLineGroups: [][]string{{"comment"}},
			wantErr:           true,
			wantCount:         2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := 0
			rules := make([]lintRule, len(tt.rules))
			for i, rule := range tt.rules {
				rules[i] = mkCountRule(&counter, rule)
			}
			l := newLinter(rules...)
			for _, commentLines := range tt.commentLineGroups {
				_, err := l.lintComments(commentLines)
				gotErr := err != nil
				if gotErr != tt.wantErr {
					t.Errorf("lintComments() error = %v, wantErr %v", err, tt.wantErr)
				}
			}
			if counter != tt.wantCount {
				t.Errorf("expected %d rule invocations, got %d", tt.wantCount, counter)
			}
		})
	}
}

func TestRuleOptionalAndRequired(t *testing.T) {
	tests := []struct {
		name     string
		comments []string
		wantMsg  string
	}{
		{
			name:     "no comments",
			comments: []string{},
			wantMsg:  "",
		},
		{
			name:     "only optional",
			comments: []string{"+k8s:optional"},
			wantMsg:  "",
		},
		{
			name:     "only required",
			comments: []string{"+k8s:required"},
			wantMsg:  "",
		},
		{
			name:     "optional required",
			comments: []string{"+k8s:optional", "+k8s:required"},
			wantMsg:  `conflicting tags: {\+k8s:optional, \+k8s:required}`,
		},
		{
			name:     "required optional",
			comments: []string{"+k8s:optional", "+k8s:required"},
			wantMsg:  `conflicting tags: {\+k8s:optional, \+k8s:required}`,
		},
		{
			name:     "optional empty required",
			comments: []string{"+k8s:optional", "", "+k8s:required"},
			wantMsg:  `conflicting tags: {\+k8s:optional, \+k8s:required}`,
		},
		{
			name:     "empty required empty empty optional empty",
			comments: []string{"", "+k8s:optional", "", "", "+k8s:required", ""},
			wantMsg:  `conflicting tags: {\+k8s:optional, \+k8s:required}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ruleOptionalAndRequired(tt.comments)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			} else if tt.wantMsg != "" {
				re := regexp.MustCompile(tt.wantMsg)
				if !re.MatchString(msg) {
					t.Errorf("message:\n\t%s\ndoes not match:\n\t%s", msg, re.String())
				}
			}
		})
	}
}

func TestRuleRequiredAndDefault(t *testing.T) {
	tests := []struct {
		name     string
		comments []string
		wantMsg  string
	}{
		{
			name:     "no comments",
			comments: []string{},
			wantMsg:  "",
		},
		{
			name:     "only required",
			comments: []string{"+k8s:required"},
			wantMsg:  "",
		},
		{
			name:     "only default",
			comments: []string{"+default=somevalue"},
			wantMsg:  "",
		},
		{
			name:     "required default",
			comments: []string{"+k8s:required", "+default=somevalue"},
			wantMsg:  `conflicting tags: {\+default, \+k8s:required}`,
		},
		{
			name:     "default required",
			comments: []string{"+default=somevalue", "+k8s:required"},
			wantMsg:  `conflicting tags: {\+default, \+k8s:required}`,
		},
		{
			name:     "required empty default",
			comments: []string{"+k8s:required", "", "+default=somevalue"},
			wantMsg:  `conflicting tags: {\+default, \+k8s:required}`,
		},
		{
			name:     "empty default empty empty required empty",
			comments: []string{"", "+default=somevalue", "", "", "+k8s:required", ""},
			wantMsg:  `conflicting tags: {\+default, \+k8s:required}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ruleRequiredAndDefault(tt.comments)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			} else if tt.wantMsg != "" {
				re := regexp.MustCompile(tt.wantMsg)
				if !re.MatchString(msg) {
					t.Errorf("message:\n\t%s\ndoes not match:\n\t%s", msg, re.String())
				}
			}
		})
	}
}

func TestConflictingTagsRule(t *testing.T) {
	tests := []struct {
		name     string
		comments []string
		tags     []string
		wantMsg  string
	}{
		{
			name:     "no comments",
			comments: []string{},
			tags:     []string{"+tag1", "+tag2"},
			wantMsg:  "",
		},
		{
			name:     "only tag1",
			comments: []string{"+tag1"},
			tags:     []string{"+tag1", "+tag2"},
			wantMsg:  "",
		},
		{
			name:     "tag1, empty, tag2",
			comments: []string{"+tag1", "", "+tag2"},
			tags:     []string{"+tag1", "+tag2"},
			wantMsg:  `conflicting tags: {\+tag1, \+tag2}`,
		},
		{
			name:     "3 lines 2 tags match",
			comments: []string{"tag3", "+tag1", "+tag2=value"},
			tags:     []string{"+tag1", "+tag2", "+tag3"},
			wantMsg:  `conflicting tags: {\+tag1, \+tag2}`,
		},
		{
			name:     "3 tags all match",
			comments: []string{"+tag3", "+tag1", "+tag2=value"},
			tags:     []string{"+tag1", "+tag2", "+tag3"},
			wantMsg:  `conflicting tags: {\+tag1, \+tag2, \+tag3}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := conflictingTagsRule("test", tt.tags...)(tt.comments)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			} else if tt.wantMsg != "" {
				re := regexp.MustCompile(tt.wantMsg)
				if !re.MatchString(msg) {
					t.Errorf("message:\n\t%s\ndoes not match:\n\t%s", msg, re.String())
				}
			}
		})
	}
}

func TestLintType(t *testing.T) {
	tests := []struct {
		name        string
		typeToLint  *types.Type
		wantCount   int
		expectError bool
	}{
		{
			name: "No comments",
			typeToLint: &types.Type{
				Name:         types.Name{Package: "testpkg", Name: "TestType"},
				CommentLines: nil,
			},
			wantCount:   0,
			expectError: false,
		},
		{
			name: "Valid comments",
			typeToLint: &types.Type{
				Name:         types.Name{Package: "testpkg", Name: "TestType"},
				CommentLines: []string{"+k8s:optional"},
			},
			wantCount:   1,
			expectError: false,
		},
		{
			name: "Pointer type",
			typeToLint: &types.Type{
				Name:         types.Name{Package: "testpkg", Name: "TestPointer"},
				Kind:         types.Pointer,
				Elem:         &types.Type{Name: types.Name{Package: "testpkg", Name: "ElemType"}, CommentLines: []string{"+k8s:optional"}},
				CommentLines: []string{"+k8s:optional"},
			},
			wantCount:   2,
			expectError: false,
		},
		{
			name: "Slice of pointers",
			typeToLint: &types.Type{
				Name: types.Name{Package: "testpkg", Name: "TestSlice"},
				Kind: types.Slice,
				Elem: &types.Type{
					Name:         types.Name{Package: "testpkg", Name: "PointerElem"},
					Kind:         types.Pointer,
					Elem:         &types.Type{Name: types.Name{Package: "testpkg", Name: "ElemType"}, CommentLines: []string{"+k8s:optional"}},
					CommentLines: []string{"+k8s:optional"},
				},
				CommentLines: []string{"+k8s:optional"},
			},
			wantCount:   3,
			expectError: false,
		},
		{
			name: "Map to pointers",
			typeToLint: &types.Type{
				Name: types.Name{Package: "testpkg", Name: "TestMap"},
				Kind: types.Map,
				Key:  &types.Type{Name: types.Name{Package: "testpkg", Name: "KeyType"}, CommentLines: []string{"+k8s:required"}},
				Elem: &types.Type{
					Name:         types.Name{Package: "testpkg", Name: "PointerElem"},
					Kind:         types.Pointer,
					Elem:         &types.Type{Name: types.Name{Package: "testpkg", Name: "ElemType"}, CommentLines: []string{"+k8s:optional"}},
					CommentLines: []string{"+k8s:optional"},
				},
				CommentLines: []string{"+k8s:optional"},
			},
			wantCount:   4,
			expectError: false,
		},
		{
			name: "Alias to pointers",
			typeToLint: &types.Type{
				Name: types.Name{Package: "testpkg", Name: "TestAlias"},
				Kind: types.Alias,
				Underlying: &types.Type{
					Name:         types.Name{Package: "testpkg", Name: "PointerElem"},
					Kind:         types.Pointer,
					Elem:         &types.Type{Name: types.Name{Package: "testpkg", Name: "ElemType"}, CommentLines: []string{"+k8s:optional"}},
					CommentLines: []string{"+k8s:optional"},
				},
				CommentLines: []string{"+k8s:optional"},
			},
			wantCount:   3,
			expectError: false,
		},
		{
			name: "Struct with members",
			typeToLint: &types.Type{
				Name: types.Name{Package: "testpkg", Name: "TestStruct"},
				Kind: types.Struct,
				Members: []types.Member{
					{
						Name:         "Field1",
						Type:         &types.Type{Name: types.Name{Package: "testpkg", Name: "FieldType"}},
						CommentLines: []string{"+k8s:optional"},
					},
					{
						Name:         "Field2",
						Type:         &types.Type{Name: types.Name{Package: "testpkg", Name: "FieldType"}},
						CommentLines: []string{"+k8s:required"},
					},
				},
			},
			wantCount:   2,
			expectError: false,
		},
		{
			name: "Nested types",
			typeToLint: &types.Type{
				Name: types.Name{Package: "testpkg", Name: "TestStruct"},
				Kind: types.Struct,
				Members: []types.Member{
					{
						Name: "Field1",
						Type: &types.Type{
							Name:         types.Name{Package: "testpkg", Name: "NestedStruct"},
							Kind:         types.Struct,
							CommentLines: []string{"+k8s:optional"},
							Members: []types.Member{
								{
									Name:         "NestedField1",
									Type:         &types.Type{Name: types.Name{Package: "testpkg", Name: "NestedFieldType"}},
									CommentLines: []string{"+k8s:required"},
								},
							},
						},
					},
				},
			},
			wantCount:   3,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := 0
			rules := []lintRule{mkCountRule(&counter, ruleAlwaysPass)}
			l := newLinter(rules...)
			if err := l.lintType(tt.typeToLint); err != nil {
				t.Fatal(err)
			}
			gotErr := len(l.lintErrors) > 0
			if gotErr != tt.expectError {
				t.Errorf("LintType() errors = %v, expectError %v", l.lintErrors, tt.expectError)
			}
			if counter != tt.wantCount {
				t.Errorf("expected %d rule invocations, got %d", tt.wantCount, counter)
			}
		})
	}
}

func TestHasAnyValidationTag(t *testing.T) {
	tests := []struct {
		name     string
		comments []string
		want     bool
	}{
		{
			name:     "empty",
			comments: []string{},
			want:     false,
		},
		{
			name:     "no k8s tags",
			comments: []string{"just a comment"},
			want:     false,
		},
		{
			name:     "optional only",
			comments: []string{"+k8s:optional"},
			want:     false,
		},
		{
			name:     "required only",
			comments: []string{"+k8s:required"},
			want:     false,
		},
		{
			name:     "unrecognized k8s tag",
			comments: []string{"+k8s:openapi-gen=true"},
			want:     false,
		},
		{
			name:     "minimum tag",
			comments: []string{"+k8s:minimum=0"},
			want:     true,
		},
		{
			name:     "enum tag",
			comments: []string{"+k8s:enum={a,b}"},
			want:     true,
		},
		{
			name:     "mixed with optional",
			comments: []string{"+k8s:optional", "+k8s:minimum=0"},
			want:     true,
		},
	}

	l := newLinter()
	l.validator = defaultFakeExtractor

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := l.hasAnyValidationTag(tt.comments); got != tt.want {
				t.Errorf("hasAnyValidationTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasRequirednessTag(t *testing.T) {
	tests := []struct {
		name     string
		comments []string
		want     bool
	}{
		{
			name:     "empty",
			comments: []string{},
			want:     false,
		},
		{
			name:     "no requireness",
			comments: []string{"+k8s:minimum=0"},
			want:     false,
		},
		{
			name:     "optional",
			comments: []string{"+k8s:optional"},
			want:     true,
		},
		{
			name:     "required",
			comments: []string{"+k8s:required"},
			want:     true,
		},
		{
			name:     "optional with value",
			comments: []string{"+k8s:optional=true"},
			want:     true,
		},
		{
			name:     "conditional optional",
			comments: []string{`+k8s:alpha(since:"1.35")=+k8s:optional`},
			want:     true,
		},
		{
			name:     "conditional required",
			comments: []string{`+k8s:alpha(since:"1.35")=+k8s:required`},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasRequirednessTag(tt.comments); got != tt.want {
				t.Errorf("hasRequirednessTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLintRequiredness(t *testing.T) {
	tests := []struct {
		name           string
		typeToLint     *types.Type
		wantHasVal     bool
		wantErrorCount int
	}{
		{
			name: "pointer field without validation - no error",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name: "Foo",
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "Bar"},
						Kind: types.Pointer,
						Elem: &types.Type{Name: types.Name{Package: "", Name: "string"}},
					},
				}},
			},
			wantHasVal:     false,
			wantErrorCount: 0,
		},
		{
			name: "pointer field with direct validation, no requireness - error",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name:         "Foo",
					CommentLines: []string{"+k8s:minimum=0"},
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "Bar"},
						Kind: types.Pointer,
						Elem: &types.Type{Name: types.Name{Package: "", Name: "int"}},
					},
				}},
			},
			wantHasVal:     true,
			wantErrorCount: 1,
		},
		{
			name: "pointer field with transitive validation, no requireness - error",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name: "Foo",
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "Nested"},
						Kind: types.Pointer,
						Elem: &types.Type{
							Name: types.Name{Package: "pkg", Name: "Inner"},
							Kind: types.Struct,
							Members: []types.Member{{
								Name:         "Bar",
								CommentLines: []string{"+k8s:minimum=0"},
								Type:         &types.Type{Name: types.Name{Package: "", Name: "int"}},
							}},
						},
					},
				}},
			},
			wantHasVal:     true,
			wantErrorCount: 1,
		},
		{
			name: "pointer field with validation and +k8s:optional - no error",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name:         "Foo",
					CommentLines: []string{"+k8s:optional", "+k8s:minimum=0"},
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "Bar"},
						Kind: types.Pointer,
						Elem: &types.Type{Name: types.Name{Package: "", Name: "int"}},
					},
				}},
			},
			wantHasVal:     true,
			wantErrorCount: 0,
		},
		{
			name: "slice field with validation, no requireness - error",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name:         "Items",
					CommentLines: []string{"+k8s:maxItems=10"},
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "List"},
						Kind: types.Slice,
						Elem: &types.Type{Name: types.Name{Package: "", Name: "string"}},
					},
				}},
			},
			wantHasVal:     true,
			wantErrorCount: 1,
		},
		{
			name: "map field with validation, no requireness - error",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name:         "Data",
					CommentLines: []string{"+k8s:maxItems=5"},
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "M"},
						Kind: types.Map,
						Key:  &types.Type{Name: types.Name{Package: "", Name: "string"}},
						Elem: &types.Type{Name: types.Name{Package: "", Name: "string"}},
					},
				}},
			},
			wantHasVal:     true,
			wantErrorCount: 1,
		},
		{
			name: "non-pointer struct field with validation - no error (exempt)",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name: "Nested",
					Type: &types.Type{
						Name:         types.Name{Package: "pkg", Name: "Inner"},
						Kind:         types.Struct,
						CommentLines: []string{"+k8s:minimum=0"},
					},
				}},
			},
			wantHasVal:     true,
			wantErrorCount: 0,
		},
		{
			name: "recursive type with pointer to self - no infinite loop",
			typeToLint: func() *types.Type {
				t := &types.Type{
					Name: types.Name{Package: "pkg", Name: "Node"},
					Kind: types.Struct,
				}
				t.Members = []types.Member{{
					Name:         "Next",
					CommentLines: []string{"+k8s:optional"},
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "NodePtr"},
						Kind: types.Pointer,
						Elem: t, // cycle
					},
				}}
				return t
			}(),
			wantHasVal:     false,
			wantErrorCount: 0,
		},
		{
			name: "recursive type with validation - detects validation on first visit",
			typeToLint: func() *types.Type {
				t := &types.Type{
					Name:         types.Name{Package: "pkg", Name: "Node"},
					Kind:         types.Struct,
					CommentLines: []string{"+k8s:immutable"},
				}
				t.Members = []types.Member{{
					Name:         "Next",
					CommentLines: []string{"+k8s:optional"},
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "NodePtr"},
						Kind: types.Pointer,
						Elem: t, // cycle
					},
				}}
				return t
			}(),
			wantHasVal:     true,
			wantErrorCount: 0,
		},
		{
			name: "array field with transitive validation, no requireness - error",
			typeToLint: &types.Type{
				Name: types.Name{Package: "pkg", Name: "T"},
				Kind: types.Struct,
				Members: []types.Member{{
					Name: "Arr",
					Type: &types.Type{
						Name: types.Name{Package: "pkg", Name: "ArrType"},
						Kind: types.Array,
						Elem: &types.Type{
							Name:         types.Name{Package: "pkg", Name: "Inner"},
							Kind:         types.Struct,
							CommentLines: []string{"+k8s:enum={a,b}"},
						},
					},
				}},
			},
			wantHasVal:     true,
			wantErrorCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLinter()
			l.validator = defaultFakeExtractor
			gotHasVal, err := l.lintRequiredness(tt.typeToLint)
			if err != nil {
				t.Fatalf("lintRequiredness() unexpected error: %v", err)
			}
			if gotHasVal != tt.wantHasVal {
				t.Errorf("lintRequiredness() hasValidation = %v, want %v", gotHasVal, tt.wantHasVal)
			}
			totalErrors := 0
			for _, errs := range l.lintErrors {
				totalErrors += len(errs)
			}
			if totalErrors != tt.wantErrorCount {
				t.Errorf("lintRequiredness() error count = %d, want %d; errors: %v", totalErrors, tt.wantErrorCount, l.lintErrors)
			}
		})
	}
}
