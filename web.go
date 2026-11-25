package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"
)

//go:embed web.html
var webPage string

func serveWeb(retrieval retrievalClient) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, webPage)
	})
	mux.HandleFunc("POST /api/ask", func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" {
			http.Error(w, "请提交 JSON 格式的问题。", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil {
			http.Error(w, "无法读取请求，或问题过长。", http.StatusBadRequest)
			return
		}
		var input struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(body, &input); err != nil || strings.TrimSpace(input.Question) == "" {
			http.Error(w, "请输入问题。", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
		defer cancel()
		result, err := askKnowledge(ctx, retrieval, strings.TrimSpace(input.Question))
		if err != nil {
			log.Printf("Agent 问答失败: %v", err)
			http.Error(w, "暂时无法完成问答。请稍后重试，或查看服务日志。", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(result)
	})

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      190 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Print("RK-Agent 网页服务监听 :8080")
	return server.ListenAndServe()
}
