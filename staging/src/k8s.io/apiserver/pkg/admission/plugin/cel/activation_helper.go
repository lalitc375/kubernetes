package cel

import (
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
)

// createAdmissionRequestValue generates a map[string]interface{} representation of an AdmissionRequest.
// This is done manually to avoid the significant overhead of unstructured reflection loops and allocations.
// This may be replaced with an automated code-generation utility once such a utility becomes available.
// The 'object' and 'oldObject' fields are omitted from the native AdmissionRequest serialization
// and are instead stitched in directly from their parsed CEL variables.
func createAdmissionRequestValue(request *admissionv1.AdmissionRequest, objectVal, oldObjectVal any) (map[string]interface{}, error) {
	if request == nil {
		return nil, nil
	}

	reqMap := map[string]interface{}{
		"uid": string(request.UID),
		"kind": map[string]interface{}{
			"group":   request.Kind.Group,
			"version": request.Kind.Version,
			"kind":    request.Kind.Kind,
		},
		"resource": map[string]interface{}{
			"group":    request.Resource.Group,
			"version":  request.Resource.Version,
			"resource": request.Resource.Resource,
		},
		"operation": string(request.Operation),
	}

	if request.Name != "" {
		reqMap["name"] = request.Name
	}
	if request.Namespace != "" {
		reqMap["namespace"] = request.Namespace
	}

	if request.SubResource != "" {
		reqMap["subResource"] = request.SubResource
	}

	if request.RequestKind != nil {
		reqMap["requestKind"] = map[string]interface{}{
			"group":   request.RequestKind.Group,
			"version": request.RequestKind.Version,
			"kind":    request.RequestKind.Kind,
		}
	}

	if request.RequestResource != nil {
		reqMap["requestResource"] = map[string]interface{}{
			"group":    request.RequestResource.Group,
			"version":  request.RequestResource.Version,
			"resource": request.RequestResource.Resource,
		}
	}

	if request.RequestSubResource != "" {
		reqMap["requestSubResource"] = request.RequestSubResource
	}

	userInfo := map[string]interface{}{}
	if request.UserInfo.Username != "" {
		userInfo["username"] = request.UserInfo.Username
	}
	if request.UserInfo.UID != "" {
		userInfo["uid"] = request.UserInfo.UID
	}
	if len(request.UserInfo.Groups) > 0 {
		// CEL native type expects slices as []interface{} or typed slices that convert naturally
		// []string maps easily to CEL lists.
		userInfo["groups"] = request.UserInfo.Groups
	}
	if len(request.UserInfo.Extra) > 0 {
		extra := map[string]interface{}{}
		for k, v := range request.UserInfo.Extra {
			extra[k] = v // ExtraValue is []string
		}
		userInfo["extra"] = extra
	}
	reqMap["userInfo"] = userInfo

	if request.DryRun != nil {
		reqMap["dryRun"] = *request.DryRun
	}

	reqMap["options"] = nil
	// Only populate Options if the underlying object is not nil or there is raw JSON data.
	// We convert the whole RawExtension so that custom ToUnstructured handling is preserved
	// (e.g., returning nil when it is effectively empty).
	if request.Options.Object != nil || len(request.Options.Raw) > 0 {
		optionsMap, err := convertObjectToUnstructured(&request.Options)
		if err != nil {
			return nil, fmt.Errorf("failed to convert options to unstructured: %w", err)
		}
		if optionsMap != nil && optionsMap.Object != nil && len(optionsMap.Object) > 0 {
			reqMap["options"] = optionsMap.Object
		} 
	} 

	reqMap["object"] = objectVal
	reqMap["oldObject"] = oldObjectVal

	return reqMap, nil
}
