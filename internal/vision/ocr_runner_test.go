package vision

import "testing"

func TestParseOCRResultNormalizesMissingIndexes_BitsUT(t *testing.T) {
	result, err := parseOCRResult([]byte(`{
		"status":"success",
		"width":800,
		"height":600,
		"text":"登录",
		"lines":[{"text":"登录","confidence":0.98,"bbox":[100,200,160,230],"center":{"x":130,"y":215}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 800 || result.Height != 600 || len(result.Lines) != 1 {
		t.Fatalf("result = %#v", result)
	}
	line := result.Lines[0]
	if line.Index != 1 || line.Text != "登录" || line.Center.X != 130 || line.Center.Y != 215 {
		t.Fatalf("line = %#v", line)
	}
}

func TestParseOCRResultRejectsInvalidBBox_BitsUT(t *testing.T) {
	_, err := parseOCRResult([]byte(`{
		"status":"success",
		"width":800,
		"height":600,
		"lines":[{"text":"登录","confidence":0.98,"bbox":[100,200],"center":{"x":130,"y":215}}]
	}`))
	if err == nil {
		t.Fatal("expected invalid bbox error")
	}
}

func TestResultErrorPreservesHelperError_BitsUT(t *testing.T) {
	err := resultError([]byte(`{
		"status":"error",
		"code":"browser_ocr_dependency_missing",
		"message":"missing rapidocr",
		"hint":"install dependencies"
	}`))
	toolErr, ok := err.(*ToolError)
	if !ok {
		t.Fatalf("error type = %T, want *ToolError", err)
	}
	if toolErr.Code != "browser_ocr_dependency_missing" || toolErr.Hint != "install dependencies" {
		t.Fatalf("tool error = %#v", toolErr)
	}
}
