package version

import "strings"

// 这些变量会在 release 构建时通过 -ldflags -X 注入。
// 本地源码运行或普通 go build 未注入时，保留 dev/unknown 语义，避免硬编码过期版本。
var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

type Info struct {
	Version string
	Commit  string
	BuiltAt string
}

func Current() Info {
	return Info{
		Version: clean(Version, "dev"),
		Commit:  clean(Commit, "unknown"),
		BuiltAt: clean(BuiltAt, "unknown"),
	}
}

func clean(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
