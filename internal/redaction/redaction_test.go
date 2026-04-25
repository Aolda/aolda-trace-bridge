package redaction

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactDBParamsAndSensitiveKeys(t *testing.T) {
	input := map[string]any{
		"db": map[string]any{
			"statement": "SELECT * FROM user WHERE id = %(pk_1)s",
			"params": map[string]any{
				"pk_1": "real-value",
			},
		},
		"token": "secret-token",
		"nested": map[string]any{
			"Authorization": "Bearer secret",
		},
	}

	redacted := Redact(input, Options{
		RedactDBParams:      true,
		RedactDBStatement:   false,
		RedactSensitiveKeys: true,
	})

	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, leaked := range []string{"real-value", "secret-token", "Bearer secret"} {
		if strings.Contains(output, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, output)
		}
	}
	if !strings.Contains(output, "SELECT * FROM user") {
		t.Fatalf("statement should remain when redact_db_statement=false: %s", output)
	}
}

func TestRedactDBStatement(t *testing.T) {
	input := map[string]any{
		"db": map[string]any{
			"statement": "SELECT secret",
			"params":    map[string]any{},
		},
	}

	redacted := Redact(input, Options{
		RedactDBParams:      true,
		RedactDBStatement:   true,
		RedactSensitiveKeys: true,
	})
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SELECT secret") {
		t.Fatalf("statement leaked: %s", string(data))
	}
}
