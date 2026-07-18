package main

import (
	"fmt"
	"os"

	"cohert/internal/cli"
)

// main 是构建 ./cohert 二进制时使用的入口。
// 具体命令解析和执行逻辑统一放在 internal/cli，避免多个入口重复实现。
func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
