package rag

import (
	"strings"
)

// ChunkText splits text into overlapping chunks of roughly maxChars.
// It splits on word boundaries to keep chunks readable.
func ChunkText(text string, maxChars int, overlapChars int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var chunks []string
	var b strings.Builder
	var overlap []string

	flush := func() {
		s := strings.TrimSpace(b.String())
		if s == "" {
			return
		}
		chunks = append(chunks, s)
		b.Reset()
		for _, w := range overlap {
			b.WriteString(w)
			b.WriteByte(' ')
		}
	}

	for _, w := range words {
		// +1 for the space we are about to add
		if b.Len() > 0 && b.Len()+len(w)+1 > maxChars {
			flush()
		}
		b.WriteString(w)
		b.WriteByte(' ')
		overlap = append(overlap, w)
		for len(overlap) > 0 && charsLen(overlap) > overlapChars {
			overlap = overlap[1:]
		}
	}

	if b.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(b.String()))
	}
	return chunks
}

func charsLen(words []string) int {
	n := 0
	for _, w := range words {
		n += len(w) + 1
	}
	return n
}
