// Package judge adds optional TypeSafe System One (Jev) judgements on top of
// vector retrieval. Vector search answers "what is textually close to the
// query"; the judge answers "does this repository actually satisfy the need".
//
// Judgements are advisory: a caller falls back to plain vector search when the
// judge is nil, misconfigured, or fails, so the feature is strictly opt-in.
package judge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	jev "github.com/mheers/typesafeai-systemone-jev-go"
)

const (
	// DefaultMinRelevance is the lowest normalised relevance a candidate may
	// have and still be handed to an answering model. Calibrate it against
	// real queries before changing it.
	DefaultMinRelevance = 0.5

	// maxCandidates bounds the shortlist sent in one request: one Score
	// question per candidate. The state plus questions must fit TypeSafe's
	// context window.
	maxCandidates = 40

	// maxExcerpt bounds each candidate's text in the request.
	maxExcerpt = 1000

	// defaultCallTimeout bounds one judged shortlist end to end.
	defaultCallTimeout = 15 * time.Second
)

// relevanceLevels is the ordered rubric applied to every candidate. The
// normalized relevance is the probability-weighted score divided by
// len(relevanceLevels)-1, so it lands in [0,1].
var relevanceLevels = []any{
	"Not related to the need",
	"On a related topic, but does not help with the need",
	"Useful context for someone with the need",
	"Software that directly helps satisfy the need",
}

// Candidate is one retrieved item offered to the judge.
type Candidate struct {
	// ID is a stable identifier, typically "owner/name".
	ID string
	// Title is a short label, typically the repository description.
	Title string
	// Excerpt is the retrieved text to judge.
	Excerpt string
}

// RankedCandidate is one judged candidate.
type RankedCandidate struct {
	ID         string
	Relevance  float64 // normalized to [0,1]
	Confidence float64 // how peaked the model's score distribution was
}

// Ranking is the outcome of judging a shortlist against a need.
type Ranking struct {
	// Answerable is the probability that at least one candidate directly
	// satisfies the need.
	Answerable float64
	// Items holds one entry per candidate, in input order.
	Items []RankedCandidate
}

// Top returns the candidates whose relevance meets min, ordered best first.
// Ties keep their original (retrieval) order.
func (r Ranking) Top(min float64) []RankedCandidate {
	items := make([]RankedCandidate, 0, len(r.Items))
	for _, item := range r.Items {
		if item.Relevance >= min {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Relevance > items[j].Relevance
	})
	return items
}

// Reranker judges how well each candidate satisfies a need.
type Reranker interface {
	// Rank scores every candidate against the need. The returned items
	// correspond to the candidates by ID.
	Rank(ctx context.Context, need string, candidates []Candidate) (Ranking, error)
	// MinRelevance is the lowest judged relevance that still deserves to be
	// passed to an answering model.
	MinRelevance() float64
}

// Options configures the TypeSafe client. Empty values fall back to the
// SDK's environment variables (TYPESAFE_API_KEY, TYPESAFE_BASE_URL,
// TYPESAFE_DEFAULT_MODEL).
type Options struct {
	APIKey       string
	BaseURL      string
	Model        string
	MinRelevance float64
	CallTimeout  time.Duration
}

// Client asks TypeSafe for retrieval judgements.
type Client struct {
	client       *jev.Client
	minRelevance float64
	callTimeout  time.Duration
}

// New builds a judge client from options and the environment.
func New(opts Options) (*Client, error) {
	var sdkOpts []jev.Option
	if opts.APIKey != "" {
		sdkOpts = append(sdkOpts, jev.WithAPIKey(opts.APIKey))
	}
	if opts.BaseURL != "" {
		sdkOpts = append(sdkOpts, jev.WithBaseURL(opts.BaseURL))
	}
	if opts.Model != "" {
		sdkOpts = append(sdkOpts, jev.WithModel(opts.Model))
	}
	client, err := jev.NewClient(sdkOpts...)
	if err != nil {
		return nil, err
	}

	min := opts.MinRelevance
	if min <= 0 {
		min = DefaultMinRelevance
	}
	timeout := opts.CallTimeout
	if timeout <= 0 {
		timeout = defaultCallTimeout
	}
	return &Client{client: client, minRelevance: min, callTimeout: timeout}, nil
}

// Model reports the model the client will ask.
func (c *Client) Model() string { return c.client.Model() }

// MinRelevance implements Reranker.
func (c *Client) MinRelevance() float64 { return c.minRelevance }

// Rank asks one request: an answerability question for the shortlist plus one
// Score question per candidate. Every question is evaluated in parallel.
func (c *Client) Rank(ctx context.Context, need string, candidates []Candidate) (Ranking, error) {
	if strings.TrimSpace(need) == "" {
		return Ranking{}, fmt.Errorf("judge: need must not be empty")
	}
	if len(candidates) == 0 {
		return Ranking{}, nil
	}
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}

	stateCandidates := make([]map[string]string, len(candidates))
	questions := jev.Questions{
		"answerable": jev.Noul{
			Instructions: "Does at least one entry in `candidates` describe software that " +
				"directly satisfies `need`? Judge direct usefulness, not topical similarity.",
			Criteria: &jev.NoulCriteria{
				True: "At least one candidate is software that directly helps satisfy the need.",
				False: "Every candidate is a curated list, a learning resource, unrelated software, " +
					"or only tangentially related.",
			},
		},
	}
	for i, candidate := range candidates {
		stateCandidates[i] = map[string]string{
			"id":      candidate.ID,
			"title":   candidate.Title,
			"excerpt": truncate(candidate.Excerpt, maxExcerpt),
		}
		questions[fmt.Sprintf("candidate_%d", i)] = jev.Score{
			Instructions: fmt.Sprintf("How well does `candidates[%d]` satisfy `need`? "+
				"Judge direct usefulness for the need, not topical similarity.", i),
			Criteria: relevanceLevels,
		}
	}

	response, err := c.client.SystemOne(ctx, jev.SystemOneRequest{
		State:     map[string]any{"need": need, "candidates": stateCandidates},
		Questions: questions,
	}, jev.WithCallTimeout(c.callTimeout))
	if err != nil {
		return Ranking{}, fmt.Errorf("judge: %w", err)
	}

	ranking := Ranking{Items: make([]RankedCandidate, len(candidates))}
	if answerable, ok := response.Noul("answerable"); ok {
		ranking.Answerable = answerable.Noul
	}
	maxLevel := float64(len(relevanceLevels) - 1)
	for i, candidate := range candidates {
		ranking.Items[i] = RankedCandidate{ID: candidate.ID}
		if score, ok := response.Score(fmt.Sprintf("candidate_%d", i)); ok {
			ranking.Items[i].Relevance = score.Score / maxLevel
			ranking.Items[i].Confidence = score.Confidence
		}
	}
	return ranking, nil
}

// truncate cuts s to at most n bytes without splitting a UTF-8 rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
