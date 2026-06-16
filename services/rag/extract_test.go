package rag

import (
	"testing"
)

func TestExtractFromBytes_Txt(t *testing.T) {
	content := "Hello world, this is a test document."
	ec, err := ExtractFromBytes([]byte(content), ".txt")
	if err != nil {
		t.Fatal(err)
	}
	if ec.Text != content {
		t.Errorf("text mismatch: got %q", ec.Text)
	}
	if ec.Words == 0 {
		t.Error("expected non-zero words")
	}
}

func TestExtractFromBytes_Markdown(t *testing.T) {
	content := "# Title\n\nSome **bold** text."
	ec, err := ExtractFromBytes([]byte(content), ".md")
	if err != nil {
		t.Fatal(err)
	}
	if ec.Text != content {
		t.Errorf("text mismatch")
	}
}

func TestExtractFromBytes_CSV(t *testing.T) {
	csvData := "name,age\nAlice,30\nBob,25"
	ec, err := ExtractFromBytes([]byte(csvData), ".csv")
	if err != nil {
		t.Fatal(err)
	}
	if ec.Text == "" {
		t.Error("expected non-empty text")
	}
	if ec.Words == 0 {
		t.Error("expected non-zero words")
	}
}

func TestExtractFromBytes_JSON(t *testing.T) {
	jsonData := `{"key": "value", "nested": {"a": 1}}`
	ec, err := ExtractFromBytes([]byte(jsonData), ".json")
	if err != nil {
		t.Fatal(err)
	}
	if ec.Text == "" {
		t.Error("expected non-empty text")
	}
}

func TestExtractFromBytes_DOCX_InvalidData(t *testing.T) {
	_, err := ExtractFromBytes([]byte("not a docx"), ".docx")
	if err == nil {
		t.Error("expected error for invalid docx data")
	}
}

func TestExtractFromBytes_Unknown(t *testing.T) {
	content := "some binary content"
	ec, err := ExtractFromBytes([]byte(content), ".pdf")
	if err != nil {
		t.Fatal(err)
	}
	if ec.Text != content {
		t.Errorf("expected fallback to plain text")
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello world", 2},
		{"", 0},
		{"one", 1},
		{"a b c d e", 5},
	}
	for _, tt := range tests {
		got := countWords(tt.input)
		if got != tt.want {
			t.Errorf("countWords(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestStripXMLTags(t *testing.T) {
	input := `<w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:t> world</w:t></w:r></w:p>`
	result := stripXMLTags(input)
	if result == "" {
		t.Error("expected non-empty result")
	}
}
