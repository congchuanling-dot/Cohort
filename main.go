package main

import (
	"fmt"
	"os"

	"cohort/internal/cli"
)

// main 是 go run . 使用的入口。
// 它和 cmd/cohort/main.go 都委托给同一个 cli.Run，保证行为一致。
func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
