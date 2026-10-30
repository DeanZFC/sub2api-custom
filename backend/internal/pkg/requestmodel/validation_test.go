package requestmodel

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateJSONModelFields(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantDuplicate bool
	}{
		{"single model", `{"model":"gpt-6-astra","input":"hi"}`, false},
		{"single escaped key", `{"m\u006fdel":"gpt-6-astra"}`, false},
		{"single case variant", `{"Model":"gpt-6-astra"}`, false},
		{"different models", `{"model":"gpt-6-astra","model":"gpt-6.1-sol"}`, true},
		{"reverse order", `{"model":"gpt-6.1-sol","model":"gpt-6-astra"}`, true},
		{"same models", `{"model":"gpt-6-astra","model":"gpt-6-astra"}`, true},
		{"case variants", `{"model":"first","Model":"last"}`, true},
		{"only case variants", `{"MODEL":"first","Model":"last"}`, true},
		{"escaped equivalent", `{"model":"first","m\u006fdel":"last"}`, true},
		{"escaped first key", `{"\u006dodel":"first","model":"last"}`, true},
		{"escaped case variant", `{"model":"first","\u004dODEL":"last"}`, true},
		{"null last", `{"model":"first","model":null}`, true},
		{"null first", `{"model":null,"model":"last"}`, true},
		{"number last", `{"model":"first","model":42}`, true},
		{"object last", `{"model":"first","model":{}}`, true},
		{"array first", `{"model":[],"model":"last"}`, true},
		{"boolean values", `{"model":true,"Model":false}`, true},
		{"empty values", `{"model":"","model":""}`, true},
		{"three duplicates", `{"model":null,"model":"first","model":"last"}`, true},
		{"nonadjacent duplicate", `{"model":"first","input":[{"model":"nested"}],"model":"last"}`, true},
		{"nested models are not selectors", `{"model":"first","tools":[{"parameters":{"model":"a","Model":"b"}}],"input":[{"model":"nested"}]}`, false},
		{"model text in input", `{"model":"first","input":"\"model\":\"last\""}`, false},
		{"similar field names", `{"model":"first"," model":"a","model.name":"b","models":[]}`, false},
		{"missing model", `{"input":"hi"}`, false},
		{"empty", ``, false},
		{"invalid json", `{"model":"first","model":`, false},
		{"trailing document", `{"model":"first"}{"model":"last"}`, false},
		{"array", `[{"model":"first"},{"model":"last"}]`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJSONModelFields([]byte(tt.body))
			if errors.Is(err, ErrDuplicateModelFields) != tt.wantDuplicate {
				t.Fatalf("ValidateJSONModelFields() = %v, want duplicate=%v", err, tt.wantDuplicate)
			}
		})
	}
}

func BenchmarkValidateJSONModelFieldsLargeInput(b *testing.B) {
	body := []byte(`{"model":"gpt-6-astra","input":"` + strings.Repeat("x", 1024*1024) + `"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ValidateJSONModelFields(body); err != nil {
			b.Fatal(err)
		}
	}
}

func TestValidateWebSocketModelFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{"response duplicate", `{"type":"response.create","model":"first","model":"last"}`, ErrDuplicateModelFields},
		{"session model duplicate", `{"type":"session.update","session":{"model":"first","model":"last"}}`, ErrDuplicateModelFields},
		{"session model case variant", `{"type":"session.update","session":{"model":"first","Model":"last"}}`, ErrDuplicateModelFields},
		{"session model escaped", `{"type":"session.update","session":{"model":"first","m\u006fdel":"last"}}`, ErrDuplicateModelFields},
		{"session model null", `{"type":"session.update","session":{"model":"first","model":null}}`, ErrDuplicateModelFields},
		{"session duplicate", `{"type":"session.update","session":{"model":"first"},"session":{"model":"last"}}`, ErrDuplicateSessionFields},
		{"session case variant", `{"type":"session.update","session":{"model":"first"},"Session":{"model":"last"}}`, ErrDuplicateSessionFields},
		{"session escaped", `{"type":"session.update","session":{"model":"first"},"\u0073ession":{"model":"last"}}`, ErrDuplicateSessionFields},
		{"session null duplicate", `{"type":"session.update","session":null,"session":{"model":"last"}}`, ErrDuplicateSessionFields},
		{"session selector with omitted type", `{"session":{"model":"first","Model":"last"}}`, ErrDuplicateModelFields},
		{"response session selector", `{"type":"response.create","session":{"model":"first","Model":"last"}}`, ErrDuplicateModelFields},
		{"valid session update", `{"type":"session.update","session":{"model":"last"}}`, nil},
		{"independent protocol locations", `{"model":"first","session":{"model":"last"}}`, nil},
		{"tool fields untouched", `{"type":"session.update","session":{"model":"last","tools":[{"parameters":{"model":1,"model":2}}]}}`, nil},
		{"user input untouched", `{"type":"response.create","model":"first","input":[{"session":{"model":1,"model":2}}]}`, nil},
		{"invalid json", `{"session":`, nil},
		{"non object", `[]`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateWebSocketModelFields([]byte(tt.body)); !errors.Is(err, tt.want) {
				t.Fatalf("ValidateWebSocketModelFields() = %v, want %v", err, tt.want)
			}
		})
	}
}
