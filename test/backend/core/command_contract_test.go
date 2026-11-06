//go:build quillweave_whitebox

package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestJSONContractRejectsNullAndNegative retains null and negative-value rejection across supported JSON schemas.
func TestJSONContractRejectsNullAndNegative(t *testing.T) {
	models := testSettings()
	raw := jsonText(models)
	invalid := strings.Replace(raw, `"routes":{}`, `"routes":{"ask":null}`, 1)
	var decoded ModelSettings
	assert(t, decodeJSON([]byte(invalid), &decoded) != nil, "A null route was treated as inheritance")
	provider := defaultProvider()
	source := jsonText(provider)
	source = strings.Replace(source, `"context_chars":24000`, `"context_chars":-1`, 1)
	assert(t, decodeJSON([]byte(source), &provider) != nil, "A negative context budget was accepted")
	var entity Entity
	assert(t, decodeJSON([]byte(`{"id":"","name":"Character","kind":"character","aliases":[null],"description":""}`), &entity) != nil, "A null alias became an empty string")
	project := testProject()
	var fields map[string]json.RawMessage
	_ = decodeJSON(rawMessage(project), &fields)
	delete(fields, "chapters")
	var missing Project
	assert(t, decodeJSON(rawMessage(fields), &missing) != nil, "A missing legacy required field was accepted")
}
