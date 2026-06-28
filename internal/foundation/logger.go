package foundation

import (
	"log/slog"
	"os"
)

// Logger 是框架的结构化日志封装。
// 当前阶段基于标准库 log/slog（Go 1.21+），后续可替换为 zerolog 等高性能方案。
var Logger *slog.Logger

func init() {
	// 默认输出到 stdout，格式为 JSON（方便后续接入日志采集）
	Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// SetDebug 切换到 Debug 级别（开发调试用）。
func SetDebug() {
	Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}
