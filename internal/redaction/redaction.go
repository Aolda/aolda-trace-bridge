package redaction

import (
	"strings"
)

const Placeholder = "<redacted>"

type Options struct {
	RedactDBParams      bool
	RedactDBStatement   bool
	RedactSensitiveKeys bool
}

func Redact(value any, opts Options) any {
	return redactValue(value, opts, nil)
}

func redactValue(value any, opts Options, path []string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			childPath := append(path, key)
			if shouldRedactKey(key, path, opts) {
				out[key] = Placeholder
				continue
			}
			out[key] = redactValue(child, opts, childPath)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactValue(child, opts, path)
		}
		return out
	default:
		return value
	}
}

func shouldRedactKey(key string, parentPath []string, opts Options) bool {
	lower := strings.ToLower(key)
	parent := ""
	if len(parentPath) > 0 {
		parent = strings.ToLower(parentPath[len(parentPath)-1])
	}

	if opts.RedactDBParams && parent == "db" && lower == "params" {
		return true
	}
	if opts.RedactDBStatement && parent == "db" && lower == "statement" {
		return true
	}
	if !opts.RedactSensitiveKeys {
		return false
	}

	sensitive := []string{
		"password",
		"passwd",
		"secret",
		"token",
		"auth",
		"authorization",
		"cookie",
		"set-cookie",
		"credential",
		"connection_string",
	}
	for _, marker := range sensitive {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
