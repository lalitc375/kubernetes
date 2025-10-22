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

package eachkey

import (
	"testing"
)

func Test(t *testing.T) {
	st := localSchemeBuilder.Test(t)

	st.Value(&Struct{
		// All zero values.
	}).ExpectValid()

	st.Value(&Struct{
		MapField:                 map[string]string{"a": "A", "b": "B"},
		MapTypedefField:          map[UnvalidatedStringType]string{"a": "A", "b": "B"},
		MapValidatedTypedefField: map[ValidatedStringType]string{"a": "A", "b": "B"},
		MapTypeField:             UnvalidatedMapType{"a": "A", "b": "B"},
		ValidatedMapTypeField:    ValidatedMapType{"a": "A", "b": "B"},
	}).ExpectValidateFalseByPath(map[string][]string{
		"mapField[a]": {
			"Struct.MapField(keys)",
		},
		"mapField[b]": {
			"Struct.MapField(keys)",
		},
		"mapTypedefField[a]": {
			"Struct.MapTypedefField(keys)",
		},
		"mapTypedefField[b]": {
			"Struct.MapTypedefField(keys)",
		},
		"mapValidatedTypedefField[a]": {
			"Struct.MapValidatedTypedefField(keys)",
			"ValidatedStringType",
		},
		"mapValidatedTypedefField[b]": {
			"Struct.MapValidatedTypedefField(keys)",
			"ValidatedStringType",
		},
		"mapTypeField[a]": {
			"Struct.MapTypeField(keys)",
		},
		"mapTypeField[b]": {
			"Struct.MapTypeField(keys)",
		},
		"validatedMapTypeField[a]": {
			"Struct.ValidatedMapTypeField(keys)",
			"ValidatedMapType(keys)",
		},
		"validatedMapTypeField[b]": {
			"Struct.ValidatedMapTypeField(keys)",
			"ValidatedMapType(keys)",
		},
	})
}
