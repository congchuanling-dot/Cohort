// Package main 演示 LLM 调用层的所有核心用法。
//
//	cd g:\beliveOnly\Cohort
//	go run ./cmd/llmdemo/
//
// 不需要任何 API Key，全部用 MockClient 和 EchoResponder 演示。
// 设置环境变量 COHORT_LLM_API_KEY 后可跑真实调用（需联网）。
package main

import (
	"context"
	"fmt"
	"time"

	"cohort/internal/foundation"
	"cohort/internal/llm"
)

func main() {
	// ================================================================
	fmt.Println("================================================================")
	fmt.Println("  LLM 调用层 Demo —— 多 Provider + 三级配置继承")
	fmt.Println("================================================================")

	demoAvailableProviders()
	demoNewClient()
	demoLLMResolver()
	demoMockClient()
	demoStreamChunk()
	demoEchoResponder()
	demoDeepSeekComposition()
	demoRealCallIfKeySet()

	fmt.Println("\n========== Demo 全部完成 ==========")
}

// ================================================================
// 1. 查看已注册的 Provider（init() 自动注册，一个 import 就能用）
// ================================================================
func demoAvailableProviders() {
	fmt.Println("\n========== 1. AvailableProviders ==========")
	providers := llm.AvailableProviders()
	fmt.Printf("已注册的 Provider (%d 个): %v\n", len(providers), providers)
	fmt.Println("→ 这些 Provider 在各自文件的 init() 里自动注册，无需手动配置")
}

// ================================================================
// 2. NewClient —— 通过 Provider 名称创建客户端
// ================================================================
func demoNewClient() {
	fmt.Println("\n========== 2. NewClient ==========")

	// 方式 A：直接创建 OpenAI 客户端
	openaiClient, err := llm.NewClient("openai", llm.ProviderConfig{
		Model:       "gpt-4o",
		APIKey:      "sk-xxx",
		Temperature: 0.7,
		MaxTokens:   2048,
	})
	if err != nil {
		fmt.Printf("创建 OpenAI 失败: %v\n", err)
	} else {
		fmt.Printf("创建成功: %s\n", openaiClient.Name())
	}

	// 方式 B：创建 DeepSeek 客户端（不填 BaseURL = 用默认 api.deepseek.com）
	dsClient, _ := llm.NewClient("deepseek", llm.ProviderConfig{
		Model:       "deepseek-chat",
		APIKey:      "sk-ds-xxx",
		Temperature: 0.3,
		MaxTokens:   4096,
	})
	fmt.Printf("创建成功: %s（BaseURL 自动 = api.deepseek.com/v1）\n", dsClient.Name())

	// 方式 C：创建 Custom 客户端（自建代理）
	customClient, _ := llm.NewClient("custom", llm.ProviderConfig{
		Model:   "qwen-2.5-72b",
		APIKey:  "my-key",
		BaseURL: "https://my-proxy.com/v1",
		Extra: map[string]string{
			"auth_header": "X-API-Key",    // 自定义认证头
			"auth_prefix": "Bearer",       // 认证前缀
		},
	})
	fmt.Printf("创建成功: %s（自定义认证头: X-API-Key）\n", customClient.Name())

	// 方式 D：创建不存在的 Provider
	_, err = llm.NewClient("gpt4free", llm.ProviderConfig{})
	fmt.Printf("创建不存在的 Provider: %v\n", err)
}

// ================================================================
// 3. LLMResolver —— 三级配置继承
// ================================================================
func demoLLMResolver() {
	fmt.Println("\n========== 3. LLMResolver（三级配置继承）==========")

	// 模拟一段配置：全局默认 deepseek，Alex 用 Claude，WriteCodeReview 用 Haiku
	cfg := &foundation.Config{
		LLM: foundation.LLMConfig{
			Provider:    "deepseek",
			Model:       "deepseek-chat",
			Temperature: 0.3,
			MaxTokens:   4096,
		},
		Roles: foundation.RolesLLMConfig{
			"Alex": { // Engineer 用 Claude Opus 写代码
				Provider:    "anthropic",
				Model:       "claude-opus-4-8",
				Temperature: 0.1,
			},
			"Edward": { // QA 用 DeepSeek 写测试（便宜）
				Provider: "deepseek",
				Model:    "deepseek-chat",
			},
		},
		Actions: foundation.ActionsLLMConfig{
			"WriteCodeReview": { // 代码审查用 Haiku（更快更便宜）
				Provider: "anthropic",
				Model:    "claude-haiku-4-5",
			},
		},
	}

	resolver := llm.NewLLMResolver(cfg)

	// 场景 1：Alex 执行 "WriteCode"
	prov, pc := resolver.Resolve("Alex", "WriteCode")
	fmt.Printf("\nAlex + WriteCode:\n  → actions.WriteCode 无覆盖\n")
	fmt.Printf("  → roles.Alex 覆盖 → Provider=%s, Model=%s, Temp=%.1f\n",
		prov, pc.Model, pc.Temperature)

	// 场景 2：Alex 执行 "WriteCodeReview"（Action 覆盖优先级更高）
	prov, pc = resolver.Resolve("Alex", "WriteCodeReview")
	fmt.Printf("\nAlex + WriteCodeReview:\n  → actions.WriteCodeReview 覆盖！\n")
	fmt.Printf("  → Provider=%s, Model=%s（Action 覆盖了 Role 的 claude-opus-4-8）\n", prov, pc.Model)

	// 场景 3：Edward 执行 "WriteTest"
	prov, pc = resolver.Resolve("Edward", "WriteTest")
	fmt.Printf("\nEdward + WriteTest:\n  → actions.WriteTest 无覆盖\n")
	fmt.Printf("  → roles.Edward 覆盖 → Provider=%s, Model=%s\n", prov, pc.Model)

	// 场景 4：Alice 执行 "WritePRD"（Alice 没在 roles 里配置，用全局默认）
	prov, pc = resolver.Resolve("Alice", "WritePRD")
	fmt.Printf("\nAlice + WritePRD（无 Role/Action 覆盖）:\n")
	fmt.Printf("  → 全局默认 → Provider=%s, Model=%s\n", prov, pc.Model)

	// 场景 5：字段级 merge：Alex 覆盖了 Provider+Model+Temperature，
	//          但 MaxTokens 没填 → 继承全局默认 4096
	prov, pc = resolver.Resolve("Alex", "WriteCode")
	fmt.Printf("\nAlex 的 MaxTokens 继承验证:\n")
	fmt.Printf("  → MaxTokens=%d（继承自全局默认，因为 Alex 覆盖里没填）\n", pc.MaxTokens)
}

// ================================================================
// 4. MockClient —— 不需要 API Key 就能测试
// ================================================================
func demoMockClient() {
	fmt.Println("\n========== 4. MockClient（测试桩） ==========")

	ctx := context.Background()

	// 创建一个 Mock 客户端，预设返回内容
	mock := llm.NewMockClient().
		SetResponse("Hello, I'm a mock LLM!").
		SetTokenCount(42)

	// 调用 Chat
	resp, err := mock.Chat(ctx, []llm.ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi!"},
	})
	if err != nil {
		fmt.Printf("Mock 调用失败: %v\n", err)
	} else {
		fmt.Printf("Mock 返回: %s\n", resp.Content)
		fmt.Printf("FinishReason: %s\n", resp.FinishReason)
		fmt.Printf("Token 用量: prompt=%d, completion=%d, total=%d\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}

	// Mock 可以预设错误
	errMock := llm.NewMockClient().SetError(fmt.Errorf("模拟 API 错误"))
	_, err = errMock.Chat(ctx, nil)
	fmt.Printf("\n模拟错误: %v\n", err)
}

// ================================================================
// 5. ChatStream —— 流式输出
// ================================================================
func demoStreamChunk() {
	fmt.Println("\n========== 5. ChatStream（流式输出） ==========")

	mock := llm.NewMockClient().SetStreamChunks([]string{
		"根据", "需求", "分析", "，", "需要", "实现", "一个", "2048", "游戏", "。",
	})

	ch, err := mock.ChatStream(context.Background(), nil)
	if err != nil {
		fmt.Printf("Stream 失败: %v\n", err)
		return
	}

	fmt.Print("流式输出: ")
	for chunk := range ch {
		if chunk.Error != nil {
			fmt.Printf("\n  流错误: %v\n", chunk.Error)
			break
		}
		if chunk.Done {
			fmt.Println("\n  ← 流结束")
			break
		}
		fmt.Print(chunk.Content)
		time.Sleep(50 * time.Millisecond) // 模拟打字效果
	}
}

// ================================================================
// 6. EchoResponder —— 回显验证 prompt 构建
// ================================================================
func demoEchoResponder() {
	fmt.Println("\n========== 6. EchoResponder（回显测试） ==========")

	echo := llm.NewEchoResponder()

	// Chat：返回最后一条 user 消息的内容
	resp, _ := echo.Chat(context.Background(), []llm.ChatMessage{
		{Role: "system", Content: "You are a senior engineer."},
		{Role: "user", Content: "写一个排序函数"},
	})
	fmt.Printf("Chat 回显: %s\n", resp.Content)

	// ChatStream：也工作
	ch, _ := echo.ChatStream(context.Background(), []llm.ChatMessage{
		{Role: "user", Content: "写测试"},
	})
	for chunk := range ch {
		if chunk.Done {
			break
		}
		fmt.Printf("Stream 回显: %s\n", chunk.Content)
	}
	fmt.Println("→ EchoResponder 用于单元测试验证 prompt 构建，不需要 API Key")
}

// ================================================================
// 7. DeepSeek 组合复用 —— 证明只覆盖了 Name()
// ================================================================
func demoDeepSeekComposition() {
	fmt.Println("\n========== 7. DeepSeek 组合复用 ==========")

	// DeepSeek 嵌入 *openaiClient，只覆盖 Name() 和 BaseURL 默认值
	ds, _ := llm.NewClient("deepseek", llm.ProviderConfig{
		Model:  "deepseek-chat",
		APIKey: "sk-xxx",
	})

	// Name() 返回 "deepseek/deepseek-chat"，不是 "openai/deepseek-chat"
	fmt.Printf("Name(): %s\n", ds.Name())

	// Chat/ChatStream/CountTokens 全部继承自 openaiClient，零额外代码
	tokens := ds.CountTokens([]llm.ChatMessage{
		{Role: "user", Content: "Hello, how are you?"},
	})
	fmt.Printf("Token 估算: %d（继承自 openaiClient.CountTokens）\n", tokens)

	// Ollama 同理，也是嵌入 *openaiClient
	ol, _ := llm.NewClient("ollama", llm.ProviderConfig{
		Model:  "llama3.2",
		BaseURL: "http://localhost:11434/v1",
	})
	fmt.Printf("Ollama: %s（同样继承 openaiClient）\n", ol.Name())
}

// ================================================================
// 8. 如果设置了 COHORT_LLM_API_KEY，跑一个真实的 LLM 调用
// ================================================================
func demoRealCallIfKeySet() {
	fmt.Println("\n========== 8. 真实调用（需环境变量）==========")

	cfg := foundation.DefaultConfig()
	cfg.ApplyEnvOverrides()

	if cfg.LLM.APIKey == "" {
		fmt.Println("⚠️  未设置 COHORT_LLM_API_KEY，跳过真实调用")
		fmt.Println("   设置方法: $env:COHORT_LLM_API_KEY=\"sk-xxx\"")
		fmt.Println("   然后修改 provider/model 指向你的 API")
		return
	}

	fmt.Printf("检测到 API Key，尝试调用 %s/%s ...\n", cfg.LLM.Provider, cfg.LLM.Model)

	// 通过 register 创建客户端
	client, err := llm.NewClient(cfg.LLM.Provider, llm.ProviderConfig{
		Model:       cfg.LLM.Model,
		APIKey:      cfg.LLM.APIKey,
		BaseURL:     cfg.LLM.BaseURL,
		Temperature: cfg.LLM.Temperature,
		MaxTokens:   cfg.LLM.MaxTokens,
	})
	if err != nil {
		fmt.Printf("创建客户端失败: %v\n", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Chat(ctx, []llm.ChatMessage{
		{Role: "system", Content: "用一句话回答用户的问题。"},
		{Role: "user", Content: "什么是 Go 语言的 goroutine？"},
	})
	if err != nil {
		fmt.Printf("调用失败: %v\n", err)
		return
	}

	fmt.Printf("模型回复: %s\n", resp.Content)
	if resp.Usage != nil {
		fmt.Printf("Token: prompt=%d, completion=%d, total=%d\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}
}
