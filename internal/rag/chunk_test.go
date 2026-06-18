package rag

import (
	"strings"
	"testing"
)

func TestChunkText(t *testing.T) {
	text := strings.Repeat("word ", 50)
	chunks := ChunkText(text, 80, 10)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	for _, c := range chunks {
		if len(c) > 90 {
			t.Fatalf("chunk too long: %d", len(c))
		}
	}
}
