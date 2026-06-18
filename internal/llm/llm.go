package llm

import (
	"context"
	"fmt"
	"io"

	"github.com/mheers/talk-to-your-github-stars/internal/config"
	"github.com/sashabaranov/go-openai"
)

// Client talks to an OpenAI-compatible chat completions API.
type Client struct {
	client *openai.Client
	model  string
}

// New builds an LLM client from configuration.
func New(cfg *config.Config) *Client {
	oc := openai.DefaultConfig(cfg.OpenAIKey)
	oc.BaseURL = cfg.OpenAIBaseURL
	return &Client{
		client: openai.NewClientWithConfig(oc),
		model:  cfg.ChatModel,
	}
}

// Ask streams a chat completion. It sends every delta to out and returns when done.
func (c *Client) Ask(ctx context.Context, system, user string, out chan<- string) error {
	if c.client == nil || c.model == "" {
		return fmt.Errorf("llm not configured")
	}

	stream, err := c.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: system},
			{Role: openai.ChatMessageRoleUser, Content: user},
		},
		Stream: true,
	})
	if err != nil {
		return err
	}
	defer stream.Close()

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(resp.Choices) == 0 {
			continue
		}
		content := resp.Choices[0].Delta.Content
		if content != "" {
			out <- content
		}
	}
}

// Complete returns a full non-streaming chat completion.
func (c *Client) Complete(ctx context.Context, system, user string) (string, error) {
	if c.client == nil || c.model == "" {
		return "", fmt.Errorf("llm not configured")
	}

	resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: system},
			{Role: openai.ChatMessageRoleUser, Content: user},
		},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty completion response")
	}
	return resp.Choices[0].Message.Content, nil
}
