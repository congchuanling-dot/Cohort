package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Risk 表示外部 MCP 工具的副作用等级。
//
// 风险等级只决定 Cohort 默认如何处理调用，不影响 MCP Server 自身的权限范围。
// 未被用户显式描述的外部工具一律按 R2 处理，避免因为工具名看起来像“读取”
// 就在没有确认的情况下执行实际副作用。
type Risk string

const (
	// RiskR1 是已验证的只读或可恢复操作，可以由显式规则直接放行。
	RiskR1 Risk = "R1"
	// RiskR2 是可能产生外部副作用的操作，默认需要用户确认。
	RiskR2 Risk = "R2"
	// RiskR3 是删除、审批、授权、支付等高风险操作，Cohort 不自动放行。
	RiskR3 Risk = "R3"
)

// PermissionDecision 是 MCP 调用进入策略层后的处理结果。
type PermissionDecision string

const (
	// PermissionAllow 表示当前规则允许调用。
	PermissionAllow PermissionDecision = "allow"
	// PermissionAsk 表示必须向用户展示参数摘要并取得确认。
	PermissionAsk PermissionDecision = "ask"
	// PermissionDeny 表示不向外部 Server 发起调用。
	PermissionDeny PermissionDecision = "deny"
)

// ArgsPolicy 约束一份授权可以覆盖哪些参数。
type ArgsPolicy string

const (
	// ArgsPolicyExact 只匹配同一份规范化 JSON 参数，适合消息发送和远程写入。
	ArgsPolicyExact ArgsPolicy = "exact_args"
	// ArgsPolicyToolScope 允许同一工具的后续参数；只应通过项目显式规则配置，
	// 不能由一次临时确认自动扩大。
	ArgsPolicyToolScope ArgsPolicy = "tool_scope"
)

// ToolPermissionRule 是用户针对一个 MCP 工具声明的本地策略。
//
// Rule 不携带连接信息，因此在没有对应 .mcp.json Server 时不会让任何 Server
// 自动安装或自动启动。它只会在用户已经显式装配同名 Server 后参与授权判断。
type ToolPermissionRule struct {
	// Risk 是工具的已知风险等级。空值表示保守的 R2。
	Risk Risk `json:"risk,omitempty"`
	// Decision 覆盖风险等级的默认处理方式；R3 仍会被强制拒绝。
	Decision PermissionDecision `json:"decision,omitempty"`
	// ArgsPolicy 仅在 Decision=allow 时生效。空值默认 exact_args。
	ArgsPolicy ArgsPolicy `json:"args_policy,omitempty"`
}

// PermissionGrant 是用户在交互式提示中授予的一份精确授权。
//
// 当前项目级 grant 总是绑定 ArgsHash；用户一次确认“发送 A”绝不能被模型
// 复用于“发送 B”。更宽的 tool_scope 只能通过人工编辑的显式规则开启。
type PermissionGrant struct {
	Server    string    `json:"server"`
	Tool      string    `json:"tool"`
	ArgsHash  string    `json:"args_hash"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"created_at"`
}

// PermissionConfig 存储 Cohort 私有 MCP 治理信息。
//
// 它刻意与 Claude Code 兼容的 .mcp.json 分离：
//   - .mcp.json 只描述“连接哪个 Server”；
//   - 本文件只描述“已装配 Server 的哪些调用可以执行”。
//
// 因此写入授权或规则永远不会引入默认飞书、GitHub 或其他 MCP Server。
type PermissionConfig struct {
	// Rules 使用 "server/tool" 作为键。它只匹配对应 Server 实际发现到的工具。
	Rules map[string]ToolPermissionRule `json:"rules,omitempty"`
	// Grants 保存来自 allow project 的精确参数授权。
	Grants []PermissionGrant `json:"grants,omitempty"`
}

// DefaultPermissionConfig 创建零规则、零授权的安全默认值。
func DefaultPermissionConfig() PermissionConfig {
	return PermissionConfig{Rules: map[string]ToolPermissionRule{}}
}

// NormalizeRisk 返回规则指定的风险等级；未知值退化为 R2 而不是 R1。
func NormalizeRisk(risk Risk) Risk {
	switch risk {
	case RiskR1, RiskR2, RiskR3:
		return risk
	default:
		return RiskR2
	}
}

// Resolve 返回指定工具的有效规则。没有规则时默认 R2 + ask。
func (c PermissionConfig) Resolve(server, tool string) ToolPermissionRule {
	rule := ToolPermissionRule{
		Risk:       RiskR2,
		Decision:   PermissionAsk,
		ArgsPolicy: ArgsPolicyExact,
	}
	if configured, ok := c.Rules[permissionRuleKey(server, tool)]; ok {
		rule = configured
	} else if looksLikeIrreversibleTool(tool) {
		// 关键词只用于识别显而易见的 R3 下限，绝不用于把未知工具降为
		// R1。真正的 R1/R2 分类仍应由用户的显式项目规则完成。
		rule.Risk = RiskR3
	}
	rule.Risk = NormalizeRisk(rule.Risk)
	if rule.ArgsPolicy != ArgsPolicyToolScope {
		rule.ArgsPolicy = ArgsPolicyExact
	}
	if rule.Risk == RiskR3 {
		rule.Decision = PermissionDeny
		return rule
	}
	switch rule.Decision {
	case PermissionAllow, PermissionAsk, PermissionDeny:
	default:
		if rule.Risk == RiskR1 {
			rule.Decision = PermissionAllow
		} else {
			rule.Decision = PermissionAsk
		}
	}
	return rule
}

// looksLikeIrreversibleTool 为没有显式规则的 Server 保留最小高风险防线。
// 这不是完整风险分类器：不匹配的工具仍按 R2 询问，而不是自动放行。
func looksLikeIrreversibleTool(tool string) bool {
	lower := strings.ToLower(tool)
	for _, keyword := range []string{
		"delete", "remove", "destroy", "approve", "payment", "pay",
		"authorize", "permission", "export_sensitive",
	} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// HasExactGrant 判断项目授权是否覆盖当前参数。只有 allow project 写出的
// exact_args grant 会命中，确保持久授权不因参数变化而扩大副作用。
func (c PermissionConfig) HasExactGrant(server, tool, argsHash string) bool {
	for _, grant := range c.Grants {
		if strings.EqualFold(strings.TrimSpace(grant.Server), strings.TrimSpace(server)) &&
			strings.EqualFold(strings.TrimSpace(grant.Tool), strings.TrimSpace(tool)) &&
			grant.ArgsHash == argsHash &&
			grant.Scope == "project" {
			return true
		}
	}
	return false
}

// AddExactProjectGrant 幂等地新增一份项目级精确授权。
func (c *PermissionConfig) AddExactProjectGrant(server, tool, argsHash string) {
	if c.Rules == nil {
		c.Rules = map[string]ToolPermissionRule{}
	}
	if c.HasExactGrant(server, tool, argsHash) {
		return
	}
	c.Grants = append(c.Grants, PermissionGrant{
		Server:    strings.TrimSpace(server),
		Tool:      strings.TrimSpace(tool),
		ArgsHash:  argsHash,
		Scope:     "project",
		CreatedAt: time.Now().UTC(),
	})
	sort.Slice(c.Grants, func(i, j int) bool {
		left := c.Grants[i]
		right := c.Grants[j]
		return permissionRuleKey(left.Server, left.Tool)+"\x00"+left.ArgsHash <
			permissionRuleKey(right.Server, right.Tool)+"\x00"+right.ArgsHash
	})
}

// PermissionPath 返回项目级 MCP 授权文件路径。
//
// 该文件只保存哈希、风险和策略，不保存 token、env、HTTP header 或 MCP 返回正文。
func (s Store) PermissionPath() string {
	return filepath.Join(s.ProjectRoot, ".cohort", "mcp.permissions.json")
}

// LoadPermissions 读取项目级授权配置；文件不存在表示尚未做任何永久授权。
func (s Store) LoadPermissions() (PermissionConfig, error) {
	path := s.PermissionPath()
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultPermissionConfig(), nil
	}
	if err != nil {
		return PermissionConfig{}, err
	}
	var config PermissionConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return PermissionConfig{}, fmt.Errorf("parse MCP permissions %s: %w", path, err)
	}
	if config.Rules == nil {
		config.Rules = map[string]ToolPermissionRule{}
	}
	return config, nil
}

// SavePermissions 使用原子替换写入项目策略，避免进程中断留下半个 JSON 文件。
func (s Store) SavePermissions(config PermissionConfig) error {
	if config.Rules == nil {
		config.Rules = map[string]ToolPermissionRule{}
	}
	content, marshalErr := json.MarshalIndent(config, "", "  ")
	if marshalErr != nil {
		return marshalErr
	}
	path := s.PermissionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".mcp-permissions-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if chmodErr := temp.Chmod(0600); chmodErr != nil {
		_ = temp.Close()
		return chmodErr
	}
	if _, err := temp.Write(append(content, '\n')); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// AddExactProjectGrant 读取、更新并写回一条项目级精确授权。
func (s Store) AddExactProjectGrant(server, tool, argsHash string) (PermissionConfig, error) {
	config, err := s.LoadPermissions()
	if err != nil {
		return PermissionConfig{}, err
	}
	config.AddExactProjectGrant(server, tool, argsHash)
	if err := s.SavePermissions(config); err != nil {
		return PermissionConfig{}, err
	}
	return config, nil
}

// ArgsHash 对参数进行稳定 JSON 编码并计算 SHA-256。encoding/json 会稳定排序 map
// 的字符串键，因此相同语义的 map 即使构造顺序不同也会得到同一授权键。
func ArgsHash(args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	content, err := json.Marshal(args)
	if err != nil {
		// 动态参数无法序列化时不给它可复用授权；调用仍可按 once 继续。
		return ""
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// permissionRuleKey 统一规则查找和排序使用的 server/tool 键。
func permissionRuleKey(server, tool string) string {
	return strings.ToLower(strings.TrimSpace(server)) + "/" +
		strings.ToLower(strings.TrimSpace(tool))
}
