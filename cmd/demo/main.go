// Package main 演示 infrastructure 层四个模块的用法。
// 运行: go run ./cmd/demo/
package main

import (
	"fmt"

	"cohort/internal/foundation"
)

func main() {
	fmt.Println("========== 1. Config 用法 ==========")
	// 方式一：直接用默认配置（零外部依赖）
	cfg := foundation.DefaultConfig()
	fmt.Printf("默认 Provider:  %s\n", cfg.LLM.Provider)
	fmt.Printf("默认 Model:     %s\n", cfg.LLM.Model)
	fmt.Printf("默认 Temperature: %.1f\n", cfg.LLM.Temperature)

	// 方式二：环境变量覆盖（实际部署时，CI/CD 注入 API Key 等敏感信息）
	// export COHORT_LLM_API_KEY=sk-xxx
	// export COHORT_LLM_PROVIDER=openai
	cfg.ApplyEnvOverrides()
	fmt.Printf("API Key 是否已设置: %v\n\n", cfg.LLM.APIKey != "")

	// 方式三：手动改字段（开发调试）
	cfg.LLM.Model = "gpt-4o"
	fmt.Printf("手动改后的 Model: %s\n\n", cfg.LLM.Model)

	// ================================================================
	fmt.Println("========== 2. Message 用法 ==========")
	// 创建一条用户消息
	userMsg := foundation.NewUserMessage("写一个网页版 2048 游戏")
	fmt.Printf("用户消息 ID:       %s\n", userMsg.ID[:8]+"...")
	fmt.Printf("用户消息 Content:  %s\n", userMsg.Content)
	fmt.Printf("用户消息 CauseBy:  %s\n", userMsg.CauseBy)
	fmt.Printf("用户消息 SendTo:   %v\n", userMsg.SendTo)

	// 创建一条系统消息（模拟 Agent 产出）
	sysMsg := foundation.NewSystemMessage(
		"## PRD 文档\n\n### 功能需求\n1. 4x4 网格...",
		"WritePRD", // cause_by: 由哪个 Action 产生
		"Alice",    // sent_from: 由哪个 Role 发送
	)
	fmt.Printf("\n系统消息 CauseBy: %s\n", sysMsg.CauseBy)
	fmt.Printf("系统消息 SentFrom: %s\n", sysMsg.SentFrom)

	// 路由判断 —— 这是 Environment 发消息时的核心逻辑
	fmt.Println("\n--- 路由判断 ---")
	fmt.Printf("userMsg 应该发给 Alice 吗？ %v\n", userMsg.ShouldSendTo("Alice")) // true (RouteToAll)
	fmt.Printf("sysMsg 应该发给 Bob 吗？   %v\n", sysMsg.ShouldSendTo("Bob"))     // true (RouteToAll)

	// watch 过滤 —— 这是 Role 决定是否处理消息的核心逻辑
	aliceWatch := map[string]bool{"UserRequirement": true} // Alice 只关注用户需求
	bobWatch := map[string]bool{"WritePRD": true}          // Bob 只关注 PRD 产出
	fmt.Println("\n--- Watch 过滤 ---")
	fmt.Printf("Alice 关注 userMsg 吗？ %v\n", userMsg.ShouldObserve(aliceWatch)) // true ("UserRequirement" 在 watch 中)
	fmt.Printf("Alice 关注 sysMsg 吗？  %v\n", sysMsg.ShouldObserve(aliceWatch))  // false ("WritePRD" 不在)
	fmt.Printf("Bob 关注 sysMsg 吗？    %v\n", sysMsg.ShouldObserve(bobWatch))    // true ("WritePRD" 在 watch 中)

	// 空 watch = 关注所有
	fmt.Printf("空 watch 关注 userMsg？  %v\n\n", userMsg.ShouldObserve(nil)) // true

	// ================================================================
	fmt.Println("========== 3. Memory 用法 ==========")
	// 创建 Memory（容量 100 条）
	mem := foundation.NewMemory(100)

	// 添加消息
	mem.Add(userMsg)
	mem.Add(sysMsg)
	mem.Add(foundation.NewSystemMessage("class Game { ... }", "WriteCode", "Alex"))
	mem.Add(foundation.NewSystemMessage("test passed", "WriteTest", "Edward"))

	fmt.Printf("消息总数: %d\n\n", mem.Count())

	// 按不同维度查询
	fmt.Println("--- Get(0) 获取全部 ---")
	for _, m := range mem.Get(0) {
		fmt.Printf("  [%s] %s\n", m.CauseBy, m.Content[:min(30, len(m.Content))])
	}

	fmt.Println("\n--- Get(2) 最近 2 条 ---")
	for _, m := range mem.Get(2) {
		fmt.Printf("  [%s] %s\n", m.CauseBy, truncate(m.Content, 30))
	}

	fmt.Println("\n--- GetByAction(\"WritePRD\") ---")
	for _, m := range mem.GetByAction("WritePRD") {
		fmt.Printf("  %s\n", truncate(m.Content, 50))
	}

	fmt.Println("\n--- GetByRole(\"Alex\") ---")
	for _, m := range mem.GetByRole("Alex") {
		fmt.Printf("  %s\n", truncate(m.Content, 50))
	}

	fmt.Println("\n--- Last() 最后一条 ---")
	last := mem.Last()
	if last != nil {
		fmt.Printf("  [%s] %s\n", last.CauseBy, last.Content)
	}

	// FindNews: 模拟 Role 拉取未读消息
	fmt.Println("\n--- FindNews（拉取未读消息） ---")
	news := mem.FindNews([]string{userMsg.ID}, 3) // 已读 userMsg，拉最多 3 条
	for _, m := range news {
		fmt.Printf("  NEW: [%s] %s\n", m.CauseBy, truncate(m.Content, 30))
	}

	// FIFO 淘汰演示
	fmt.Println("\n--- FIFO 淘汰 ---")
	smallMem := foundation.NewMemory(3)
	for i := 0; i < 5; i++ {
		smallMem.Add(foundation.NewSystemMessage(
			fmt.Sprintf("消息 #%d", i+1), "Test", "System",
		))
	}
	fmt.Printf("容量=3，写入5条后还剩: %d 条\n", smallMem.Count())
	for _, m := range smallMem.Get(0) {
		fmt.Printf("  %s\n", m.Content) // 只剩 #3, #4, #5
	}

	// ================================================================
	fmt.Println("\n========== 4. Logger 用法 ==========")
	foundation.Logger.Info("demo 启动", "provider", cfg.LLM.Provider)
	foundation.Logger.Debug("这条默认不显示（Info 级别）")
	foundation.Logger.Warn("这是一条警告", "reason", "演示")

	// 切换到 Debug 模式
	fmt.Println("(切换到 Debug 模式)")
	foundation.SetDebug()
	foundation.Logger.Debug("现在 Debug 信息可见了！", "module", "demo")
	foundation.Logger.Info("Info 仍然可见", "module", "demo")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
