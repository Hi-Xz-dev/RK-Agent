package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

type agentAnswer struct {
	Text       string      `json:"answer"`
	Source     *paperChunk `json:"source,omitempty"`
	SourceOnly bool        `json:"sourceOnly,omitempty"`
}

func chatConfig() (*ollama.ChatModelConfig, error) {
	baseURL := os.Getenv("RK_OLLAMA_URL")
	modelName := os.Getenv("RK_MODEL")
	if baseURL == "" || modelName == "" {
		return nil, fmt.Errorf("请设置 RK_OLLAMA_URL 和 RK_MODEL")
	}
	thinking := false
	if value := os.Getenv("RK_THINK"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("RK_THINK 需要为 true 或 false: %w", err)
		}
		thinking = enabled
	}
	return &ollama.ChatModelConfig{
		BaseURL:  baseURL,
		Model:    modelName,
		Timeout:  90 * time.Second,
		Thinking: &ollama.ThinkValue{Value: thinking},
		Options:  &ollama.Options{NumPredict: 1024},
	}, nil
}

// runAgent 把聊天模型与检索工具交给 Eino，再返回答案和检索来源。
func runAgent(ctx context.Context, retrieval retrievalClient, question string) (agentAnswer, error) {
	config, err := chatConfig()
	if err != nil {
		return agentAnswer{}, err
	}
	chatModel, err := ollama.NewChatModel(ctx, config)
	if err != nil {
		return agentAnswer{}, err
	}

	// 工具只负责检索。Eino 负责调用它并把结果加回模型的对话。
	type searchInput struct {
		Query string `json:"query" jsonschema:"description=完整的用户问题"`
	}
	search, err := utils.InferTool("search_papers", "用完整问题检索论文原文和页码",
		func(ctx context.Context, input searchInput) (*paperChunk, error) {
			if strings.TrimSpace(input.Query) == "" {
				return nil, fmt.Errorf("检索问题不能为空")
			}
			hit, err := retrieval.retrieve(ctx, input.Query)
			if err != nil || hit == nil {
				return nil, err
			}
			return &hit.Payload, nil
		})
	if err != nil {
		return agentAnswer{}, err
	}

	instruction := "先调用 search_papers，用完整问题查资料。只依据摘录和用户提供的条件，直接给出简短结论。算术题给出算式。资料不足时说明资料不足。"
	if noThinkQwen(config) {
		// 本地 Qwen3 模板仍会生成思考起始标签，补充模型支持的关闭指令。
		instruction += "\n/no_think"
	}
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "research_knowledge_agent",
		Description:   "根据论文摘录回答科研问题",
		Instruction:   instruction,
		Model:         chatModel,
		MaxIterations: 4,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{search}},
		},
	})
	if err != nil {
		return agentAnswer{}, err
	}

	// Runner 产出工具和模型事件。这里只取需要显示的检索来源与最终文本。
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	events := runner.Query(ctx, question)
	var source *paperChunk
	var searched bool
	var answer string
	for {
		event, ok := events.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return agentAnswer{}, event.Err
		}
		reply, _, err := adk.GetMessage(event)
		if err != nil {
			return agentAnswer{}, err
		}
		if reply == nil {
			continue
		}
		switch {
		case reply.Role == schema.Tool:
			if err := json.Unmarshal([]byte(reply.Content), &source); err != nil {
				return agentAnswer{}, fmt.Errorf("读取检索工具结果: %w", err)
			}
			searched = true
			fmt.Fprintln(os.Stderr, "[Eino 工具] search_papers")
		case reply.Role == schema.Assistant && len(reply.ToolCalls) == 0:
			if reply.ResponseMeta != nil && reply.ResponseMeta.FinishReason == "length" {
				return agentAnswer{}, fmt.Errorf("模型在输出上限内未完成答案")
			}
			content := reply.Content
			if _, final, found := strings.Cut(content, "</think>"); found {
				content = final
			}
			answer = strings.TrimSpace(content)
		}
	}
	if !searched || answer == "" {
		return agentAnswer{}, fmt.Errorf("Agent 未完成检索与问答")
	}
	return agentAnswer{Text: answer, Source: source}, nil
}

func noThinkQwen(config *ollama.ChatModelConfig) bool {
	return config.Thinking.Value == false && strings.HasPrefix(config.Model, "qwen3:")
}
