package llm

import "encoding/json"

// Tool describes a single function the model may call, following the
// OpenAI-compatible function-calling schema. Arguments is a JSON Schema
// object describing the parameters.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ToolCall is a single function invocation requested by the model. The
// Arguments field holds a JSON-encoded object of the call arguments.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// UnmarshalJSON accepts arguments both as a JSON string (OpenAI style) and
// as a raw object (backend-native style).
func (tc *ToolCall) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	tc.ID = raw.ID
	tc.Type = raw.Type
	tc.Function.Name = raw.Function.Name
	if len(raw.Function.Arguments) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.Function.Arguments, &s); err == nil {
		tc.Function.Arguments = s
		return nil
	}
	tc.Function.Arguments = string(raw.Function.Arguments)
	return nil
}

func NewTool(name, description string, params map[string]any) Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  params,
		},
	}
}
