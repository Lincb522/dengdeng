package gateway

import "encoding/json"

// stripAnthropicUnsupportedParamsremoves sampling parametersthat newer
// Anthropicmodels (for exampleclaude-fable-5 and opus 4.6+) reject with
// HTTP400 "temperatureis deprecatedfor this model".Dropping the field is
// safe: the upstream appliesits own defaultsampling when it is absent.
func stripAnthropicUnsupportedParams(body []byte) []byte {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	if _, ok := obj["temperature"]; !ok {
		return body
	}
	delete(obj, "temperature")
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}
