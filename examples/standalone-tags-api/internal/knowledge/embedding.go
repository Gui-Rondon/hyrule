package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Ollama uses an explicitly configured /api/embed endpoint. The same model and
// dimensions must be used during ingestion and retrieval. No implicit provider
// fallback, remote document upload, or model download is performed.
type Ollama struct {
	URL, Model                  string
	Dimensions                  int
	Client                      *http.Client
	DocumentPrefix, QueryPrefix string
}

func (e *Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, e.DocumentPrefix+text)
}

// EmbedQuery permits asymmetric retrieval models to use their query task prompt.
func (e *Ollama) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, e.QueryPrefix+text)
}

func (e *Ollama) embed(ctx context.Context, text string) ([]float32, error) {
	if e.Client == nil || e.URL == "" || e.Model == "" || e.Dimensions < 1 || e.Dimensions > 4096 {
		return nil, errors.New("invalid embedding configuration")
	}
	body, _ := json.Marshal(map[string]any{"model": e.Model, "input": text, "truncate": false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := e.Client.Do(req)
	if err != nil {
		return nil, errors.New("embedding unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("embedding unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(raw) > 256*1024 {
		return nil, errors.New("invalid embedding response")
	}
	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Embeddings) != 1 || len(result.Embeddings[0]) != e.Dimensions {
		return nil, errors.New("embedding dimension mismatch")
	}
	if _, err = vectorLiteral(result.Embeddings[0]); err != nil {
		return nil, err
	}
	return result.Embeddings[0], nil
}

// EmbeddingContract binds stored vectors to model, dimension, task prompts and
// chunking version. A changed contract requires explicit document reingestion.
func EmbeddingContract(model string, dimensions int, documentPrefix, queryPrefix string) string {
	raw, _ := json.Marshal([]any{"runes-1200-v1", model, dimensions, documentPrefix, queryPrefix})
	return fmt.Sprintf("%s#%x", model, sha256.Sum256(raw))
}
