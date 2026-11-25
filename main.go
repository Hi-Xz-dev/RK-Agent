package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func run() error {
	args := os.Args[1:]
	command := "--ask"
	if len(args) > 0 {
		switch args[0] {
		case "--ask", "--index", "--retrieve", "--serve":
			command, args = args[0], args[1:]
		}
	}
	question := strings.TrimSpace(strings.Join(args, " "))
	noQuestion := command == "--index" || command == "--serve"
	if (noQuestion && len(args) > 0) || (!noQuestion && question == "") {
		return fmt.Errorf("用法: --serve、--index、--retrieve '完整问题' 或 --ask '完整问题'")
	}

	retrieval := retrievalClient{
		httpClient: &http.Client{Timeout: 90 * time.Second},
		embedURL:   os.Getenv("RK_EMBED_URL"),
		qdrantURL:  strings.TrimRight(os.Getenv("RK_QDRANT_URL"), "/"),
	}
	if retrieval.embedURL == "" || retrieval.qdrantURL == "" {
		return fmt.Errorf("请设置 RK_EMBED_URL 和 RK_QDRANT_URL")
	}
	if command == "--serve" {
		return serveWeb(retrieval)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	switch command {
	case "--index":
		return retrieval.indexPapers(ctx)
	case "--retrieve":
		hit, err := retrieval.retrieve(ctx, question)
		if err != nil {
			return err
		}
		if hit == nil {
			fmt.Println("向量库没有返回摘录。")
			return nil
		}
		fmt.Printf("相似度 %.3f｜%s\n", hit.Score, hit.Payload.excerpt())
		return nil
	default:
		result, err := runAgent(ctx, retrieval, question)
		if err != nil {
			return err
		}
		fmt.Println(result.Text)
		if result.Source != nil {
			fmt.Printf("检索来源：%s，第 %d 页\n", result.Source.Paper, result.Source.Page)
		}
		return nil
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
