package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	embeddingModel = "qwen3-embedding:0.6b"
	collectionPath = "/collections/paper_chunks"
)

type paperChunk struct {
	Paper string `json:"paper"`
	Page  int    `json:"page"`
	Text  string `json:"text"`
}

var paperChunks = []paperChunk{
	{Paper: "练习论文 A", Page: 3, Text: "实验样本为 120 份。"},
	{Paper: "练习论文 B", Page: 2, Text: "实验采用问卷调查方法。"},
}

//go:embed krusty-krab-rules.md
var krustyKrabRules string

func (chunk paperChunk) excerpt() string {
	return fmt.Sprintf("%s，第 %d 页。%s", chunk.Paper, chunk.Page, chunk.Text)
}

type retrievalClient struct {
	httpClient *http.Client
	embedURL   string
	qdrantURL  string
}

// embed 让 Ollama 把每条文本变成一个向量。建立索引和检索使用同一个模型。
func (client retrievalClient) embed(ctx context.Context, texts []string) ([][]float32, error) {
	var response struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	body := map[string]any{"model": embeddingModel, "input": texts, "truncate": false}
	if err := client.request(ctx, http.MethodPost, client.embedURL, body, &response); err != nil {
		return nil, fmt.Errorf("生成向量: %w", err)
	}
	if len(response.Embeddings) != len(texts) {
		return nil, fmt.Errorf("Ollama 返回了 %d 个向量，预期 %d 个", len(response.Embeddings), len(texts))
	}
	for _, vector := range response.Embeddings {
		if len(vector) == 0 || len(vector) != len(response.Embeddings[0]) {
			return nil, fmt.Errorf("Ollama 返回的向量维度不一致")
		}
	}
	return response.Embeddings, nil
}

// indexPapers 将练习摘录和守则各节转成向量，再将向量和出处一起存入 Qdrant。
func (client retrievalClient) indexPapers(ctx context.Context) error {
	chunks := append([]paperChunk(nil), paperChunks...)
	// Markdown 的每个二级标题对应一个检索片段。页码用于沿用现有引用格式。
	sections := strings.Split(strings.TrimSpace(krustyKrabRules), "\n## ")
	title := strings.TrimSpace(strings.TrimPrefix(sections[0], "# "))
	for i, section := range sections[1:] {
		chunks = append(chunks, paperChunk{Paper: title, Page: i + 1, Text: strings.TrimSpace(section)})
	}

	var texts []string
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}
	vectors, err := client.embed(ctx, texts)
	if err != nil {
		return err
	}

	collectionURL := client.qdrantURL + collectionPath
	var collection struct {
		Result struct {
			Exists bool `json:"exists"`
		} `json:"result"`
	}
	if err := client.request(ctx, http.MethodGet, collectionURL+"/exists", nil, &collection); err != nil {
		return fmt.Errorf("检查 Qdrant 集合: %w", err)
	}
	if !collection.Result.Exists {
		body := map[string]any{"vectors": map[string]any{"size": len(vectors[0]), "distance": "Cosine"}}
		if err := client.request(ctx, http.MethodPut, collectionURL, body, nil); err != nil {
			// 另一条导入命令也可能在此时建好了集合。
			checkErr := client.request(ctx, http.MethodGet, collectionURL+"/exists", nil, &collection)
			if checkErr != nil || !collection.Result.Exists {
				return fmt.Errorf("创建 Qdrant 集合: %w", err)
			}
		}
	}

	type point struct {
		ID      int        `json:"id"`
		Vector  []float32  `json:"vector"`
		Payload paperChunk `json:"payload"`
	}
	var points []point
	for i, chunk := range chunks {
		// 练习论文仍使用原来的 ID，守则片段按固定页序接在后面。
		points = append(points, point{ID: i + 1, Vector: vectors[i], Payload: chunk})
	}
	if err := client.request(ctx, http.MethodPut, collectionURL+"/points?wait=true", map[string]any{"points": points}, nil); err != nil {
		return fmt.Errorf("写入 Qdrant: %w", err)
	}
	fmt.Printf("已索引 %d 段摘录，向量维度 %d\n", len(points), len(vectors[0]))
	return nil
}

type searchHit struct {
	Score   float64    `json:"score"`
	Payload paperChunk `json:"payload"`
}

// retrieve 使用完整问题生成查询向量，取回最接近的原文和出处。
func (client retrievalClient) retrieve(ctx context.Context, question string) (*searchHit, error) {
	vectors, err := client.embed(ctx, []string{question})
	if err != nil {
		return nil, err
	}
	body := map[string]any{"query": vectors[0], "limit": 1, "with_payload": true}
	var response struct {
		Result struct {
			Points []searchHit `json:"points"`
		} `json:"result"`
	}
	if err := client.request(ctx, http.MethodPost, client.qdrantURL+collectionPath+"/points/query", body, &response); err != nil {
		return nil, fmt.Errorf("查询 Qdrant，请确认已执行 --index: %w", err)
	}
	if len(response.Result.Points) == 0 {
		return nil, nil
	}
	return &response.Result.Points[0], nil
}

func (client retrievalClient) request(ctx context.Context, method, url string, body, response any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	if response != nil {
		return json.NewDecoder(resp.Body).Decode(response)
	}
	return nil
}
