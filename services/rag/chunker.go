package rag

import (
	"strings"
	"unicode/utf8"
)

type Chunk struct {
	Index   int    `json:"index"`
	Content string `json:"content"`
	Tokens  int    `json:"tokens"`
}

type Chunker struct {
	ChunkSize    int
	ChunkOverlap int
}

func NewChunker(chunkSize, chunkOverlap int) *Chunker {
	if chunkSize <= 0 {
		chunkSize = 500
	}
	if chunkOverlap <= 0 {
		chunkOverlap = chunkSize / 5
	}
	if chunkOverlap >= chunkSize {
		chunkOverlap = chunkSize / 4
	}
	return &Chunker{ChunkSize: chunkSize, ChunkOverlap: chunkOverlap}
}

func (c *Chunker) ChunkText(text string) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	paragraphs := splitParagraphs(text)
	var chunks []Chunk
	var buf strings.Builder
	bufLen := 0
	idx := 0

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		if utf8.RuneCountInString(p) > c.ChunkSize {
			if buf.Len() > 0 {
				chunks = append(chunks, Chunk{Index: idx, Content: buf.String(), Tokens: estimateTokens(buf.String())})
				idx++
				buf.Reset()
				bufLen = 0
			}
			sentenceChunks := c.chunkBySentences(p)
			for _, sc := range sentenceChunks {
				chunks = append(chunks, Chunk{Index: idx, Content: sc, Tokens: estimateTokens(sc)})
				idx++
			}
			continue
		}

		if bufLen+utf8.RuneCountInString(p)+1 > c.ChunkSize && buf.Len() > 0 {
			chunks = append(chunks, Chunk{Index: idx, Content: buf.String(), Tokens: estimateTokens(buf.String())})
			idx++
			overlap := lastNRunes(buf.String(), c.ChunkOverlap)
			buf.Reset()
			buf.WriteString(overlap)
			buf.WriteString("\n\n")
			buf.WriteString(p)
			bufLen = utf8.RuneCountInString(overlap) + 2 + utf8.RuneCountInString(p)
		} else {
			if buf.Len() > 0 {
				buf.WriteString("\n\n")
				bufLen += 2
			}
			buf.WriteString(p)
			bufLen += utf8.RuneCountInString(p)
		}
	}

	if buf.Len() > 0 {
		chunks = append(chunks, Chunk{Index: idx, Content: buf.String(), Tokens: estimateTokens(buf.String())})
	}

	return chunks
}

func (c *Chunker) chunkBySentences(text string) []string {
	sentences := splitSentences(text)
	var result []string
	var buf strings.Builder
	bufLen := 0

	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		runeCount := utf8.RuneCountInString(s)
		if bufLen+runeCount+1 > c.ChunkSize && buf.Len() > 0 {
			result = append(result, strings.TrimSpace(buf.String()))
			overlap := lastNRunes(buf.String(), c.ChunkOverlap)
			buf.Reset()
			buf.WriteString(overlap)
			buf.WriteString(" ")
			buf.WriteString(s)
			bufLen = utf8.RuneCountInString(overlap) + 1 + runeCount
		} else {
			if buf.Len() > 0 {
				buf.WriteString(" ")
				bufLen++
			}
			buf.WriteString(s)
			bufLen += runeCount
		}
	}
	if buf.Len() > 0 {
		result = append(result, strings.TrimSpace(buf.String()))
	}
	return result
}

func splitParagraphs(text string) []string {
	return strings.Split(text, "\n\n")
}

func splitSentences(text string) []string {
	var sentences []string
	var buf strings.Builder
	for _, r := range text {
		buf.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			sentences = append(sentences, buf.String())
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		sentences = append(sentences, buf.String())
	}
	return sentences
}

func lastNRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

func estimateTokens(text string) int {
	return utf8.RuneCountInString(text) * 3 / 4
}
