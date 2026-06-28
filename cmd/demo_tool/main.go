// Package main 演示 Tool 层：Agent 生成代码后真的写入磁盘并编译。
//
//	cd g:\beliveOnly\Cohort
//	go run ./cmd/demo_tool/
//
// 对比之前的 demo：
//   - 之前：Agent 只在对话里"说"代码，不会真的写文件
//   - 现在：Agent 调用 WriteFile Tool 把代码写入磁盘，再调 RunCommand Tool 编译
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"cohort/internal/action/builtin"
	"cohort/internal/foundation"
	"cohort/internal/llm"
	"cohort/internal/role"
	"cohort/internal/team"
	tools "cohort/internal/tool/builtin"
)

func main() {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Println("❌ 未设置 DEEPSEEK_API_KEY")
		return
	}
	model := os.Getenv("DEEPSEEK_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}
	baseURL := os.Getenv("DEEPSEEK_API_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}

	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("  Tool 层 Demo —— Agent 生成代码 → 写磁盘 → 编译")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  Model: %s\n\n", model)

	cfg := foundation.DefaultConfig()

	// ==================== Step 1: LLM 客户端 ====================
	engClient, _ := llm.NewClient("deepseek", llm.ProviderConfig{
		Model: model, APIKey: apiKey, BaseURL: baseURL,
		Temperature: 0.1, MaxTokens: 3072,
	})

	// ==================== Step 2: 创建 Engineer + 配备 Tool ====================
	charlie := role.NewRole("Charlie",
		role.WithProfile(
			"Senior Go Engineer",
			"Write clean, idiomatic Go code",
			"Use only Go standard library",
		),
		role.WithActions(builtin.NewWriteCode(engClient)),
		role.WithWatch("UserRequirement"),
		role.WithMemory(foundation.NewMemory(50)),
		// ★ 配备私有工具：写文件 + 编译
		role.WithTools(
			tools.NewWriteFileTool(),
			tools.NewRunCommandTool(),
		),
	)

	// ==================== Step 3: 组建 Team ====================
	fmt.Println("┌──────────────────────────────────────────────────────┐")
	fmt.Println("│  单 Agent + Tool 演示                                  │")
	fmt.Println("├──────────┬──────────────────┬────────────────────────┤")
	fmt.Println("│  Charlie │  Go Engineer     │  WriteCode              │")
	fmt.Println("│  工具:    │  WriteFile       │  写入磁盘               │")
	fmt.Println("│           │  RunCommand      │  编译代码               │")
	fmt.Println("└──────────┴──────────────────┴────────────────────────┘")

	t := team.NewTeam(cfg)
	t.Hire(charlie)
	t.SetMaxRound(3)

	task := "用 Go 标准库写一个简单的 HTTP 服务，GET /health 返回 {\"status\":\"ok\"}，监听 :8080。文件名用 main.go。"
	fmt.Printf("\n📋 需求: %s\n\n", task)
	fmt.Println("--- 开始（预计 30-60 秒）---")

	start := time.Now()
	history, err := t.Run(context.Background(), task)
	if err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
		return
	}
	elapsed := time.Since(start)

	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  完成！耗时 %v\n", elapsed)
	fmt.Println(strings.Repeat("=", 70))

	// 展示结果
	allMsgs := history.Get(0)
	for _, msg := range allMsgs {
		if msg.CauseBy == "UserRequirement" {
			continue
		}
		fmt.Println(msg.Content)
		fmt.Println()
	}

	fmt.Println("========== Tool 层 Demo 完成 ==========")
	fmt.Println()
	fmt.Println("💡 检查 workspace 目录，代码已经写入磁盘。可以 cd 进去 go run . 直接运行。")
}
