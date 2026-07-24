package desktop

import (
	"errors"
	"testing"
)

func TestParseHelperEnvelopeSuccess_BitsUT(t *testing.T) {
	envelope, err := parseHelperEnvelope([]byte(`{"status":"success","data":{"platform":"darwin"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "success" || string(envelope.Data) != `{"platform":"darwin"}` {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestHelperEnvelopePreservesStructuredError_BitsUT(t *testing.T) {
	envelope, err := parseHelperEnvelope([]byte(`{"status":"error","code":"desktop_permission_denied","message":"denied","hint":"grant access"}`))
	if err != nil {
		t.Fatal(err)
	}
	var toolErr *ToolError
	if !errors.As(envelope.toolError(), &toolErr) {
		t.Fatalf("error = %T, want *ToolError", envelope.toolError())
	}
	if toolErr.Code != "desktop_permission_denied" || toolErr.Hint != "grant access" {
		t.Fatalf("tool error = %#v", toolErr)
	}
}

func TestParseHelperEnvelopeRejectsUnknownStatus_BitsUT(t *testing.T) {
	if _, err := parseHelperEnvelope([]byte(`{"status":"pending","data":{}}`)); err == nil {
		t.Fatal("parseHelperEnvelope unexpectedly succeeded")
	}
}
