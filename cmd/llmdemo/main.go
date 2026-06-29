// Package main 用真实的 DEEPSEEK_API_KEY 测试 DeepSeek 调用。
//
//	cd g:\beliveOnly\Cohort
//	go run ./cmd/llmdemo/
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"cohort/internal/llm"
)

func main() {
	// 读取环境变量
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	baseURL := os.Getenv("DEEPSEEK_API_URL")
	model := os.Getenv("DEEPSEEK_MODEL")

	if apiKey == "" {
		fmt.Println("❌ 未设置 DEEPSEEK_API_KEY 环境变量")
		return
	}
	if model == "" {
		model = "deepseek-chat"
	}

	fmt.Println("========================================")
	fmt.Println("  DeepSeek 真实调用测试")
	fmt.Println("========================================")
	fmt.Printf("Model:   %s\n", model)
	fmt.Printf("BaseURL: %s\n", baseURL)
	fmt.Printf("Key:     %s...\n\n", apiKey[:10])

	// 创建客户端
	client, err := llm.NewClient("deepseek", llm.ProviderConfig{
		Model:       model,
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Temperature: 0.3,
		MaxTokens:   1024,
	})
	if err != nil {
		fmt.Printf("❌ 创建客户端失败: %v\n", err)
		return
	}
	fmt.Printf("客户端: %s\n\n", client.Name())

	// ========== 测试 1：同步对话 ==========
	fmt.Println("--- 测试 1：Chat ---")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := client.Chat(ctx, []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: "你是一个 Go 语言专家。用中文一句话回答。"},
		{Role: llm.RoleUser, Content: "goroutine 和线程的区别是什么？"},
	})
	if err != nil {
		fmt.Printf("❌ 失败: %v\n\n", err)
	} else {
		fmt.Printf("✅ 回答: %s\n", resp.Content)
		if resp.Usage != nil {
			fmt.Printf("   Token: prompt=%d completion=%d total=%d\n",
				resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		}
		fmt.Println()
	}

	// ========== 测试 2：代码生成 ==========
	fmt.Println("--- 测试 2：代码生成 ---")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()

	resp2, err := client.Chat(ctx2, []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: "你是一个资深 Go 工程师。写代码要简洁、有注释。"},
		{Role: llm.RoleUser, Content: "用 Go 写一个泛型版本的二分查找函数，包含错误处理。"},
	})
	if err != nil {
		fmt.Printf("❌ 失败: %v\n\n", err)
	} else {
		fmt.Printf("✅ 代码:\n%s\n", resp2.Content)
		if resp2.Usage != nil {
			fmt.Printf("   Token: prompt=%d completion=%d total=%d\n",
				resp2.Usage.PromptTokens, resp2.Usage.CompletionTokens, resp2.Usage.TotalTokens)
		}
		fmt.Println()
	}

	// ========== 测试 3：流式输出 ==========
	fmt.Println("--- 测试 3：ChatStream ---")
	ctx3, cancel3 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel3()

	ch, err := client.ChatStream(ctx3, []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: "简洁回答。"},
		{Role: llm.RoleUser, Content: "用一句话介绍 Go 语言的 channel。"},
	})
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
	} else {
		fmt.Print("✅ 流式: ")
		for chunk := range ch {
			if chunk.Error != nil {
				fmt.Printf("\n   ⚠️ 错误: %v\n", chunk.Error)
				break
			}
			if chunk.Done {
				fmt.Println("\n   ← 结束")
				break
			}
			fmt.Print(chunk.Content)
		}
	}

	fmt.Println("\n========== 全部完成 ==========")
}
