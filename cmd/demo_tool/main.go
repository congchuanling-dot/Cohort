// Package main 演示公有 Tool 层：Environment 注册一次，所有 Role 自动继承。
//
//	cd g:\beliveOnly\Cohort
//	go run ./cmd/demo_tool/
//
// 对比之前的做法：
//   - 之前：每个 Role 手动 role.WithTools(WriteFile, RunCommand) —— 重复
//   - 现在：env.RegisterPublicTool(WriteFile) —— 一次注册，全员共享
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
	fmt.Println("  公有 Tool 层 Demo —— Environment 注册，全员共享")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  Model: %s\n\n", model)

	cfg := foundation.DefaultConfig()

	// ==================== Step 1: LLM 客户端 ====================
	engClient, _ := llm.NewClient("deepseek", llm.ProviderConfig{
		Model: model, APIKey: apiKey, BaseURL: baseURL,
		Temperature: 0.1, MaxTokens: 3072,
	})

	// ==================== Step 2: 创建 Team + 注册公有工具 ====================
	t := team.NewTeam(cfg)
	t.SetMaxRound(3)

	// ★ 注册一次，所有 Role 都自动能用
	t.RegisterTool(tools.NewWriteFileTool())
	t.RegisterTool(tools.NewRunCommandTool())
	t.RegisterTool(tools.NewReadFileTool())

	// ==================== Step 3: 创建 Role（不需要手动带工具）====================
	charlie := role.NewRole("Charlie",
		role.WithProfile("Senior Go Engineer", "Write clean Go code", "Standard library only"),
		role.WithActions(builtin.NewWriteCode(engClient)),
		role.WithWatch("UserRequirement"),
		role.WithMemory(foundation.NewMemory(50)),
		// ★ 不写 WithTools —— WriteFile/RunCommand/ReadFile 从 Environment 自动继承
	)

	// 如果有私有工具，仍然可以加：
	// charlie := role.NewRole("Charlie",
	//     role.WithTools(goFmtTool),  // ← 只有 Charlie 能用
	// )

	t.Hire(charlie)

	fmt.Println("┌──────────────────────────────────────────────────────┐")
	fmt.Println("│  公有工具（Environment 级别，所有 Role 继承）           │")
	fmt.Println("├────────────────────┬─────────────────────────────────┤")
	fmt.Println("│  WriteFile          │  写入文件到磁盘                  │")
	fmt.Println("│  RunCommand         │  执行系统命令（编译/测试）       │")
	fmt.Println("│  ReadFile           │  读取文件内容                   │")
	fmt.Println("└────────────────────┴─────────────────────────────────┘")

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

	allMsgs := history.Get(0)
	for _, msg := range allMsgs {
		if msg.CauseBy == "UserRequirement" {
			continue
		}
		fmt.Println(msg.Content)
	}

	fmt.Println("========== 公有 Tool 层 Demo 完成 ==========")
}
