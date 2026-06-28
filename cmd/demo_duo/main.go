// Package main 演示双 Agent 协作：PM 写 PRD → Reviewer 评审。
//
//	cd g:\beliveOnly\Cohort
//	go run ./cmd/demo_duo/
//
// 自动读取 DEEPSEEK_API_KEY 环境变量。
//
// 协作流程：
//
//	User → Alice(PM) 写 PRD → Bob(Reviewer) 评审 PRD → 输出结果
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
	// ==================== Step 1: 获取 API Key ====================
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Println("❌ 未设置 DEEPSEEK_API_KEY 环境变量")
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

	fmt.Println("================================================================")
	fmt.Println("  双 Agent 协作 Demo —— PM(Alice) 写 PRD → Reviewer(Bob) 评审")
	fmt.Println("================================================================")
	fmt.Printf("Model: %s\n", model)
	fmt.Printf("Key:   %s...\n\n", apiKey[:10])

	// ==================== Step 2: 配置 ====================
	cfg := foundation.DefaultConfig()
	cfg.Agent.MaxReactLoop = 5

	// ==================== Step 3: 创建 LLM 客户端 ====================
	rawClient, err := llm.NewClient("deepseek", llm.ProviderConfig{
		Model:       model,
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Temperature: 0.3,
		MaxTokens:   1024,
	})
	if err != nil {
		fmt.Printf("❌ 创建 DeepSeek 客户端失败: %v\n", err)
		return
	}
	fmt.Printf("LLM 客户端: %s\n\n", rawClient.Name())

	// ==================== Step 4: 创建 Role ====================
	// Alice: Product Manager，关注 UserRequirement，执行 WritePRD
	alice := role.NewRole("Alice",
		role.WithProfile(
			"Senior Product Manager with 10 years experience",
			"Write clear, comprehensive PRD documents",
			"Output in Chinese, use markdown format",
		),
		role.WithActions(builtin.NewWritePRD(rawClient)),
		role.WithWatch("UserRequirement"), // 只关注用户需求
		role.WithMemory(foundation.NewMemory(100)),
	)

	// Bob: Reviewer，关注 WritePRD，执行 WriteCodeReview
	bob := role.NewRole("Bob",
		role.WithProfile(
			"Senior Technical Reviewer with 15 years experience",
			"Review PRDs and provide constructive feedback",
			"Be specific and actionable in reviews. Use Chinese.",
		),
		role.WithActions(builtin.NewWriteCodeReview(rawClient)),
		role.WithWatch("WritePRD"), // 只关注 PRD 产出
		role.WithMemory(foundation.NewMemory(100)),
	)

	fmt.Printf("Alice: %s → action=WritePRD, watch=UserRequirement\n", alice.Profile)
	fmt.Printf("Bob:   %s → action=WriteCodeReview, watch=WritePRD\n\n", bob.Profile)

	// ==================== Step 5: 组建 Team ====================
	t := team.NewTeam(cfg)
	t.Hire(alice)
	t.Hire(bob)
	t.SetMaxRound(3) // 最多 3 轮

	fmt.Println("--- 开始协作 ---")
	start := time.Now()

	ctx := context.Background()
	history, err := t.Run(ctx, "写一个网页版 2048 游戏的需求文档，面向移动端用户")

	if err != nil {
		fmt.Printf("❌ 协作失败: %v\n", err)
		return
	}

	elapsed := time.Since(start)

	// ==================== Step 6: 查看结果 ====================
	fmt.Println("\n================================================================")
	fmt.Printf("  协作完成！耗时 %v\n", elapsed)
	fmt.Println("================================================================")

	allMsgs := history.Get(0)
	fmt.Printf("全局历史消息数: %d\n\n", len(allMsgs))

	for _, msg := range allMsgs {
		causeLabel := msg.CauseBy
		switch causeLabel {
		case "UserRequirement":
			causeLabel = "📝 用户需求"
		case "WritePRD":
			causeLabel = "📋 PRD 文档"
		case "WriteCodeReview":
			causeLabel = "🔍 评审意见"
		}
		fmt.Printf("╔══ %s ══╗\n", strings.Repeat("═", 60))
		fmt.Printf("║ From: %-54s ║\n", msg.SentFrom)
		fmt.Printf("║ Type: %-54s ║\n", causeLabel)
		fmt.Printf("╚══%s══╝\n", strings.Repeat("═", 60))
		fmt.Println(msg.Content)
		fmt.Println()
	}

	fmt.Println("========== 双 Agent 协作 Demo 完成 ==========")
}
