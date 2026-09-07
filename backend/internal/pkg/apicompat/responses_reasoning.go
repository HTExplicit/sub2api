package apicompat

import "encoding/json"

// UnmarshalJSON retains raw members alongside the known string options. Keeping
// json.RawMessage instead of map[string]any is important: a provider extension
// may contain numbers that cannot be represented exactly by float64.
func (r *ResponsesReasoning) UnmarshalJSON(data []byte) error {
	type alias ResponsesReasoning
	var known alias
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*r = ResponsesReasoning(known)
	r.rawFields = fields
	return nil
}

// MarshalJSON overlays deliberately edited known options without losing any
// other original member. Explicit null and empty string differ from omission:
// they remain unchanged unless their typed option was actually changed.
func (r ResponsesReasoning) MarshalJSON() ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(r.rawFields)+5)
	for key, raw := range r.rawFields {
		fields[key] = raw
	}
	for key, value := range r.knownFields() {
		original, present := r.rawFields[key]
		if present {
			var originalValue string
			if err := json.Unmarshal(original, &originalValue); err != nil {
				return nil, err
			}
			if originalValue == value {
				continue
			}
		} else if value == "" {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		fields[key] = encoded
	}
	return json.Marshal(fields)
}

// Clone creates an independent configuration for a converted/replayed request.
// Changes to its known options or raw storage cannot mutate the source request.
func (r *ResponsesReasoning) Clone() *ResponsesReasoning {
	if r == nil {
		return nil
	}
	cloned := *r
	if r.rawFields != nil {
		cloned.rawFields = make(map[string]json.RawMessage, len(r.rawFields))
		for key, raw := range r.rawFields {
			cloned.rawFields[key] = append(json.RawMessage(nil), raw...)
		}
	}
	return &cloned
}

// HasField distinguishes a client-specified null/empty option from an omitted
// one, so adapters can preserve explicit intent when applying their defaults.
func (r *ResponsesReasoning) HasField(name string) bool {
	if r == nil {
		return false
	}
	if _, present := r.rawFields[name]; present {
		return true
	}
	return r.knownFields()[name] != ""
}

func (r ResponsesReasoning) knownFields() map[string]string {
	return map[string]string{
		"effort":           r.Effort,
		"summary":          r.Summary,
		"mode":             r.Mode,
		"context":          r.Context,
		"generate_summary": r.GenerateSummary,
	}
}
