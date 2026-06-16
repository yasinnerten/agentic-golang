package rag

import (
	"strings"
	"testing"
)

func TestChunker_EmptyInput(t *testing.T) {
	c := NewChunker(100, 20)
	chunks := c.ChunkText("")
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty input, got %d", len(chunks))
	}
}

func TestChunker_ShortText(t *testing.T) {
	c := NewChunker(500, 100)
	text := "This is a short text that should fit in a single chunk."
	chunks := c.ChunkText(text)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Content != text {
		t.Errorf("chunk content mismatch")
	}
}

func TestChunker_MultipleParagraphs(t *testing.T) {
	c := NewChunker(100, 20)
	paragraphs := []string{
		strings.Repeat("word ", 30),
		strings.Repeat("word ", 30),
		strings.Repeat("word ", 30),
	}
	text := strings.Join(paragraphs, "\n\n")
	chunks := c.ChunkText(text)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks for long text, got %d", len(chunks))
	}
}

func TestChunker_Overlap(t *testing.T) {
	c := NewChunker(50, 20)
	text := strings.Repeat("This is a sentence. ", 20)
	chunks := c.ChunkText(text)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if chunk.Index != i {
			t.Errorf("chunk %d has wrong index %d", i, chunk.Index)
		}
	}
}

func TestChunker_LargeParagraph(t *testing.T) {
	c := NewChunker(100, 20)
	text := strings.Repeat("This is sentence. ", 50)
	chunks := c.ChunkText(text)
	if len(chunks) < 3 {
		t.Errorf("expected at least 3 chunks for very long paragraph, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk.Tokens <= 0 {
			t.Errorf("chunk should have positive token estimate")
		}
	}
}

func TestChunker_Defaults(t *testing.T) {
	c := NewChunker(0, 0)
	if c.ChunkSize != 500 {
		t.Errorf("expected default chunk size 500, got %d", c.ChunkSize)
	}
	if c.ChunkOverlap != 100 {
		t.Errorf("expected default overlap 100, got %d", c.ChunkOverlap)
	}
}

func TestEstimateTokens(t *testing.T) {
	result := estimateTokens("hello world")
	if result <= 0 {
		t.Errorf("expected positive token estimate")
	}
}

func TestLastNRunes(t *testing.T) {
	result := lastNRunes("hello world", 5)
	if result != "world" {
		t.Errorf("expected 'world', got %q", result)
	}
	result = lastNRunes("hi", 10)
	if result != "hi" {
		t.Errorf("expected 'hi', got %q", result)
	}
}
