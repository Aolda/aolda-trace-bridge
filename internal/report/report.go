package report

import (
	"encoding/json"
	"fmt"
	"os"
)

type Report struct {
	Info     map[string]any  `json:"info"`
	TraceID  string          `json:"trace_id,omitempty"`
	ParentID string          `json:"parent_id,omitempty"`
	Children []Node          `json:"children"`
	Stats    map[string]Stat `json:"stats,omitempty"`
	Extra    map[string]any  `json:"-"`
}

type Node struct {
	Info     map[string]any `json:"info"`
	TraceID  string         `json:"trace_id,omitempty"`
	ParentID string         `json:"parent_id,omitempty"`
	Children []Node         `json:"children"`
}

type Stat struct {
	Count    int     `json:"count"`
	Duration float64 `json:"duration"`
}

func Parse(data []byte) (Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("parse report json: %w", err)
	}
	if r.Info == nil {
		r.Info = map[string]any{}
	}
	return r, nil
}

func LoadFile(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, fmt.Errorf("read report fixture: %w", err)
	}
	return Parse(data)
}

func (r Report) SpanNodes() []Node {
	return r.Children
}

func InfoString(info map[string]any, key string) string {
	if info == nil {
		return ""
	}
	value, ok := info[key]
	if !ok {
		return ""
	}
	s, _ := value.(string)
	return s
}

func InfoFloat(info map[string]any, key string) (float64, bool) {
	if info == nil {
		return 0, false
	}
	switch value := info[key].(type) {
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
