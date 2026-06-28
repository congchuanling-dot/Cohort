// Package main 演示软件公司 3 Agent 协作：PM → Architect → Engineer。
//
//	cd g:\beliveOnly\Cohort
//	go run ./cmd/demo_company/
//
// 自动读取 DEEPSEEK_API_KEY 环境变量。
//
// 协作流程：
//
//	User 需求
//	  │
//	  ├→ Alice (PM)        观察 UserRequirement → WritePRD     → 输出 PRD
//	  ├→ Bob   (Architect) 观察 WritePRD        → WriteDesign  → 输出技术设计
//	  └→ Charlie (Engineer) 观察 WriteDesign     → WriteCode    → 输出代码
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
)

func main() {
	// ==================== Step 1: 环境变量 ====================
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
	fmt.Println("  软件公司 3 Agent 协作 —— PM → Architect → Engineer")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  Model: %s | Key: %s...\n\n", model, apiKey[:10])

	// ==================== Step 2: 配置 ====================
	cfg := foundation.DefaultConfig()

	// ==================== Step 3: 共享 LLM 客户端 ====================
	rawClient, err := llm.NewClient("deepseek", llm.ProviderConfig{
		Model:       model,
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Temperature: 0.3,
		MaxTokens:   2048,
	})
	if err != nil {
		fmt.Printf("❌ 创建客户端失败: %v\n", err)
		return
	}

	// ★ 三个 Role 用不同温度：PM 用高一点（创造性），Engineer 用低一点（精确）
	pmClient, _ := llm.NewClient("deepseek", llm.ProviderConfig{
		Model: model, APIKey: apiKey, BaseURL: baseURL,
		Temperature: 0.5, MaxTokens: 2048,
	})
	archClient, _ := llm.NewClient("deepseek", llm.ProviderConfig{
		Model: model, APIKey: apiKey, BaseURL: baseURL,
		Temperature: 0.3, MaxTokens: 2048,
	})
	engClient, _ := llm.NewClient("deepseek", llm.ProviderConfig{
		Model: model, APIKey: apiKey, BaseURL: baseURL,
		Temperature: 0.1, MaxTokens: 3072,
	})
	_ = rawClient

	// ==================== Step 4: 创建 3 个 Role ====================
	alice := role.NewRole("Alice",
		role.WithProfile(
			"Senior Product Manager", "Write clear, comprehensive PRDs", "Output in Chinese markdown",
		),
		role.WithActions(builtin.NewWritePRD(pmClient)),
		role.WithWatch("UserRequirement"),
		role.WithMemory(foundation.NewMemory(50)),
	)

	bob := role.NewRole("Bob",
		role.WithProfile(
			"Senior System Architect", "Design scalable, production-ready systems", "Be specific: include file structure, function signatures, data models",
		),
		role.WithActions(builtin.NewWriteDesign(archClient)),
		role.WithWatch("WritePRD"),
		role.WithMemory(foundation.NewMemory(50)),
	)

	charlie := role.NewRole("Charlie",
		role.WithProfile(
			"Senior Go Engineer", "Write clean, idiomatic, production Go code", "Use only Go standard library, write complete runnable code",
		),
		role.WithActions(builtin.NewWriteCode(engClient)),
		role.WithWatch("WriteDesign"),
		role.WithMemory(foundation.NewMemory(50)),
	)

	// ==================== Step 5: 组建团队 ====================
	fmt.Println("┌─────────────────────────────────────────────────────────────────────┐")
	fmt.Println("│  团队成员                                                            │")
	fmt.Println("├──────────┬──────────────┬──────────────────┬────────────────────────┤")
	fmt.Println("│  姓名    │  角色         │  关注             │  产出                  │")
	fmt.Println("├──────────┼──────────────┼──────────────────┼────────────────────────┤")
	fmt.Println("│  Alice   │  PM          │  UserRequirement  │  WritePRD (PRD 文档)    │")
	fmt.Println("│  Bob     │  Architect   │  WritePRD         │  WriteDesign (技术设计) │")
	fmt.Println("│  Charlie │  Engineer    │  WriteDesign      │  WriteCode (代码)       │")
	fmt.Println("└──────────┴──────────────┴──────────────────┴────────────────────────┘")

	t := team.NewTeam(cfg)
	t.Hire(alice)
	t.Hire(bob)
	t.Hire(charlie)
	t.SetMaxRound(5)

	task := "用 Go 标准库写一个命令行 Todo 应用，支持：添加任务、列出任务、标记完成、删除任务，数据存 JSON 文件"

	fmt.Printf("\n📋 用户需求: %s\n\n", task)
	fmt.Println("--- 开始协作（预计 60-90 秒）---")
	fmt.Println()

	start := time.Now()
	ctx := context.Background()
	history, err := t.Run(ctx, task)

	if err != nil {
		fmt.Printf("❌ 协作失败: %v\n", err)
		return
	}
	elapsed := time.Since(start)

	// ==================== Step 6: 展示结果 ====================
	fmt.Println()
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  协作完成！总耗时 %v\n", elapsed)
	fmt.Println(strings.Repeat("=", 70))

	allMsgs := history.Get(0)
	fmt.Printf("全局消息数: %d\n\n", len(allMsgs))

	stageColors := map[string]string{
		"UserRequirement": "📝",
		"WritePRD":        "📋",
		"WriteDesign":     "🏗️",
		"WriteCode":       "💻",
	}

	for _, msg := range allMsgs {
		emoji := stageColors[msg.CauseBy]
		if emoji == "" {
			emoji = "📌"
		}

		fmt.Println(strings.Repeat("━", 68))
		fmt.Printf("  %s 第 %s 阶段 | 产出者: %s | 类型: %s\n",
			emoji, stageName(msg.CauseBy), msg.SentFrom, msg.CauseBy)
		fmt.Println(strings.Repeat("━", 68))

		// 截断显示：PRD 和 Design 显示前 1500 字符，Code 显示全部
		content := msg.Content
		if msg.CauseBy != "WriteCode" && len(content) > 1500 {
			content = content[:1500] + "\n\n...（省略，完整内容见全局历史）"
		}
		fmt.Println(content)
		fmt.Println()
	}

	fmt.Println("========== 软件公司 3 Agent 协作 Demo 完成 ==========")
	fmt.Println()
	fmt.Println("💡 提示：你可以修改 task 变量来尝试不同的需求。")
}

func stageName(causeBy string) string {
	switch causeBy {
	case "UserRequirement":
		return "需求输入"
	case "WritePRD":
		return "PRD 撰写"
	case "WriteDesign":
		return "技术设计"
	case "WriteCode":
		return "代码生成"
	default:
		return causeBy
	}
}
