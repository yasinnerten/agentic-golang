package rag

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ExtractedContent struct {
	Text     string `json:"text"`
	MimeType string `json:"mime_type"`
	Pages    int    `json:"pages"`
	Words    int    `json:"words"`
}

func ExtractFromFile(filePath string) (*ExtractedContent, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return ExtractFromBytes(data, filepath.Ext(filePath))
}

func ExtractFromBytes(data []byte, ext string) (*ExtractedContent, error) {
	ext = strings.ToLower(ext)
	switch ext {
	case ".txt", ".md", ".markdown":
		return extractPlainText(data, "text/plain"), nil
	case ".csv":
		return extractCSV(data)
	case ".json", ".jsonl":
		return extractJSON(data)
	case ".docx":
		return extractDOCX(data)
	case ".html", ".htm":
		return extractHTML(data)
	default:
		return extractPlainText(data, "application/octet-stream"), nil
	}
}

func extractPlainText(data []byte, mime string) *ExtractedContent {
	text := string(data)
	return &ExtractedContent{
		Text:     text,
		MimeType: mime,
		Pages:    1,
		Words:    countWords(text),
	}
}

func extractCSV(data []byte) (*ExtractedContent, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}
	var sb strings.Builder
	for _, row := range records {
		sb.WriteString(strings.Join(row, " | "))
		sb.WriteString("\n")
	}
	text := sb.String()
	return &ExtractedContent{
		Text:     text,
		MimeType: "text/csv",
		Pages:    1,
		Words:    countWords(text),
	}, nil
}

func extractJSON(data []byte) (*ExtractedContent, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		text := string(data)
		return &ExtractedContent{
			Text:     text,
			MimeType: "application/json",
			Pages:    1,
			Words:    countWords(text),
		}, nil
	}
	text := buf.String()
	return &ExtractedContent{
		Text:     text,
		MimeType: "application/json",
		Pages:    1,
		Words:    countWords(text),
	}, nil
}

func extractDOCX(data []byte) (*ExtractedContent, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open docx: %w", err)
	}
	var sb strings.Builder
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open document.xml: %w", err)
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("read document.xml: %w", err)
			}
			text := stripXMLTags(string(content))
			sb.WriteString(text)
		}
	}
	text := strings.TrimSpace(sb.String())
	return &ExtractedContent{
		Text:     text,
		MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Pages:    1,
		Words:    countWords(text),
	}, nil
}

func extractHTML(data []byte) (*ExtractedContent, error) {
	text := stripHTMLTags(string(data))
	return &ExtractedContent{
		Text:     text,
		MimeType: "text/html",
		Pages:    1,
		Words:    countWords(text),
	}, nil
}

func stripXMLTags(s string) string {
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			sb.WriteString(" ")
			continue
		}
		if !inTag {
			sb.WriteRune(r)
		}
	}
	result := sb.String()
	result = strings.ReplaceAll(result, "  ", " ")
	result = strings.ReplaceAll(result, "\n ", "\n")
	return strings.TrimSpace(result)
}

func stripHTMLTags(s string) string {
	return stripXMLTags(s)
}

func countWords(text string) int {
	return len(strings.Fields(text))
}
