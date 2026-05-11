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

package initializer

import (
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
)

// schemaResolverInitializer injects the SchemaResolver into admission plugins
// that implement WantsSchemaResolver.
type schemaResolverInitializer struct {
	schemaResolver resolver.SchemaResolver
}

var _ admission.PluginInitializer = &schemaResolverInitializer{}

// NewSchemaResolverInitializer returns a PluginInitializer that injects the given
// SchemaResolver into admission plugins that implement WantsSchemaResolver.
func NewSchemaResolverInitializer(schemaResolver resolver.SchemaResolver) admission.PluginInitializer {
	return &schemaResolverInitializer{schemaResolver: schemaResolver}
}

// Initialize checks whether the plugin implements WantsSchemaResolver and injects the resolver.
func (i *schemaResolverInitializer) Initialize(plugin admission.Interface) {
	if wants, ok := plugin.(WantsSchemaResolver); ok {
		wants.SetSchemaResolver(i.schemaResolver)
	}
}
