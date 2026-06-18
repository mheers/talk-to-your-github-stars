package embed

import (
	"context"
	"fmt"

	"github.com/mheers/talk-to-your-github-stars/internal/config"
	"github.com/sashabaranov/go-openai"
)

// Client creates embeddings using an OpenAI-compatible API.
type Client struct {
	client *openai.Client
	model  string
}

// New builds an embedder from configuration.
func New(cfg *config.Config) *Client {
	oc := openai.DefaultConfig(cfg.OpenAIKey)
	oc.BaseURL = cfg.OpenAIBaseURL
	return &Client{
		client: openai.NewClientWithConfig(oc),
		model:  cfg.EmbeddingModel,
	}
}

// Embed returns embeddings for the provided texts in input order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if c.client == nil || c.model == "" {
		return nil, fmt.Errorf("embedder not configured")
	}

	resp, err := c.client.CreateEmbeddings(ctx, openai.EmbeddingRequestStrings{
		Input: texts,
		Model: openai.EmbeddingModel(c.model),
	})
	if err != nil {
		return nil, err
	}

	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || int(d.Index) >= len(out) {
			continue
		}
		vec := make([]float32, len(d.Embedding))
		for i, v := range d.Embedding {
			vec[i] = float32(v)
		}
		out[d.Index] = vec
	}
	return out, nil
}
