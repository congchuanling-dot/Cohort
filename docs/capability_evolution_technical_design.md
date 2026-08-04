# Cohort 能力边界拓展技术方案

> 状态：部分完成。当前已完成 P0/P4 的核心闭环：本地 capability registry、手动 gap/proposal CLI、Runner no-tool 能力缺口记录、`CapabilityGapRecorded` 观测事件、项目级 Skill scaffold、doctor 诊断、依赖安装 plan/approve/install 审计、smoke test、`verify/promote/disable` 状态流转、available capability 轻量索引注入、重复 gap CLI 建议。Tool/MCP adapter 生成和离线反思汇总仍为后续规划。

## 1. 背景

Cohort 当前已经具备本地文件、Shell、浏览器、桌面、MCP、Skill、长期记忆和可观测性基础。但当用户提出一个当前工具链无法覆盖的新任务时，例如“处理某种新文件格式”“接入某个新平台”“调用某类专业工具”“自动化一个从未支持过的软件”，Agent 仍然容易走向两种不理想结果：

- 直接回答“做不到”，没有给出可执行的补能力路径。
- 临时用 `code_run` 写脚本或安装依赖，任务可能跑通，但没有把能力沉淀成可发现、可验证、可复用的系统资产。

GenericAgent 的经验说明，能力边界不应该靠一次性内置所有工具解决，而应该靠“探测、临时实现、验证、固化、下次路由”的闭环持续扩展。

Cohort 需要把这套软机制工程化，形成受控的 Capability Evolution System。

## 2. GenericAgent 当前做法

GA 目前不是通过一个独立的“能力拓展模块”完成自进化，而是由几层机制叠加实现。

| 层 | GA 做法 | 作用 |
| --- | --- | --- |
| 系统提示 | 不允许轻易说做不到，失败后先 probe，再换策略 | 抑制过早拒绝 |
| 基础工具 | `file_read`、`file_write`、`file_patch`、`code_run`、浏览器、桌面 | 用少量通用工具临时造能力 |
| 依赖安装 | 遇到缺 Python 包时允许用 `pip` 等方式补依赖 | 让运行环境随任务增长 |
| 工作记忆 | 每轮摘要、checkpoint、失败后重读 SOP | 防止长任务失控 |
| 长期记忆 | L1/L2/L3/L4 分层记忆 | 让新能力下次可发现 |
| SOP / 脚本 | 将成功路径写为 `memory/*_sop.md` 或 `memory/*.py` | 固化可复用流程 |
| Morphling | 从外部项目抽目标、测例和组件，选择调用/重写/舍弃 | 吸收外部能力 |

这套机制有效，但边界比较软：

- 能力缺口没有结构化记录。
- 是否安装依赖、是否写入 SOP，主要依赖模型自觉。
- 新能力是否真的可用，缺少统一 smoke test。
- 能力发现依赖 L1/L2/L3 文本索引，缺少机器可读 registry。

Cohort 应吸收 GA 的增长思想，但不能照搬它的松散实现。

## 3. 当前 Cohort 能不能 `pip install`

可以，但要准确描述边界。

Cohort 当前的 `code_run` 是一个工作区内 Shell 执行器：

- macOS / Linux 下使用 `bash -c`。
- Windows 下使用 PowerShell。
- 默认超时 60 秒，最大 120 秒。
- stdout / stderr 会被截断后返回给模型。
- 成功的 `code_run` 结果可以作为长期记忆证据。

因此，如果本机有 Python、pip、网络和写权限，Agent 可以通过 `code_run` 执行类似命令：

```bash
python3 -m pip install --user some-package
```

但当前这只是“Shell 能执行 pip 命令”，不是完整能力边界拓展系统。

当前缺口：

- 没有专门的 dependency policy。
- 没有安装前风险确认。
- 没有依赖安装清单和回滚记录。
- 没有把“缺依赖”自动转成 capability gap。
- 没有安装后 smoke test 和能力注册。
- 没有区分一次性临时依赖与可长期复用依赖。

所以对外表达应为：

> Cohort 当前可以通过 `code_run` 在受控工作区内执行 shell 命令，因此技术上可以运行 pip/npm/brew 等安装命令；但依赖安装还没有产品化为受控能力拓展闭环。正式自进化应在用户确认、审计、验证和 registry 之后启用。

## 4. 目标

能力边界拓展不是让模型“声称自己学会了”，而是把“现在做不到”转成可验证的工程闭环。

目标流程：

```text
用户任务
  -> 当前 Tool / Skill / MCP 能力评估
  -> 发现能力缺口
  -> 记录 Capability Gap
  -> 生成补能力方案
  -> 用户确认风险和依赖
  -> 生成或安装 Tool / Skill / MCP Adapter
  -> 运行 smoke test
  -> 通过后注册 Capability
  -> 下次任务自动路由
```

核心原则：

- 能力必须可验证，不能只靠模型总结。
- 依赖安装必须可审计，不能静默修改系统。
- 新能力默认先进入候选态，验证通过后才能 promote。
- 所有能力都要有触发词、依赖、风险等级和验收方式。
- 失败也要沉淀为 capability gap，避免下次重复撞墙。

## 5. 系统架构

```text
┌──────────────────────────┐
│ User Task                │
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│ Capability Router        │
│ - tool / skill / mcp      │
│ - registry lookup         │
└────────────┬─────────────┘
             │
    available│missing
             ▼
┌──────────────────────────┐
│ Capability Gap Detector  │
│ - no tool used            │
│ - repeated tool failure   │
│ - explicit can't do       │
│ - missing dependency      │
└────────────┬─────────────┘
             ▼
┌──────────────────────────┐
│ Proposal Generator       │
│ - plan                    │
│ - dependencies            │
│ - risk                    │
│ - verification            │
└────────────┬─────────────┘
             ▼
┌──────────────────────────┐
│ User Approval Gate       │
└────────────┬─────────────┘
             ▼
┌──────────────────────────┐
│ Scaffolder / Installer   │
│ - Skill                   │
│ - Tool                    │
│ - MCP adapter             │
│ - dependency manifest     │
└────────────┬─────────────┘
             ▼
┌──────────────────────────┐
│ Verification Runner      │
│ - smoke test              │
│ - evidence                │
│ - rollback notes          │
└────────────┬─────────────┘
             ▼
┌──────────────────────────┐
│ Capability Registry      │
│ - candidate / available   │
│ - triggers / dependencies │
│ - risk / verification     │
└──────────────────────────┘
```

## 6. 核心模块

### 6.1 Capability Registry

机器可读能力注册表，建议放在：

```text
.cohort/capabilities/registry.yaml
~/.cohort/capabilities/registry.yaml
```

示例：

```yaml
capabilities:
  local_pdf_analysis:
    status: available
    type: skill
    entry: .cohort/skills/local_pdf_analysis/SKILL.md
    triggers:
      - PDF
      - 扫描件
      - 提取表格
    requires:
      commands:
        - python3
      python:
        - pymupdf
        - pillow
    risk: local_file_processing
    verification:
      command: cohort capability verify local_pdf_analysis
      last_passed_at: "2026-07-31T00:00:00Z"
```

状态：

| 状态 | 含义 |
| --- | --- |
| `missing` | 已发现缺口，但未生成方案 |
| `proposed` | 已生成方案，等待用户确认 |
| `candidate` | 已生成 Skill / Tool，但未验证 |
| `available` | 验证通过，可参与路由 |
| `disabled` | 用户禁用或验证失效 |
| `failed` | 构建或验证失败，保留证据 |

### 6.2 Capability Gap Detector

检测“不是任务失败，而是能力缺失”的场景。

触发信号：

- 模型回复包含“做不到 / 无法处理 / 没有工具 / 缺少能力”且没有调用工具。
- 同一任务多轮工具失败，错误集中在缺依赖、格式不支持、命令不存在、认证缺失。
- 工具返回结构化错误，例如 `command_not_found`、`unsupported_format`、`missing_helper`。
- 用户明确要求“以后能不能处理这类任务”。
- `NoToolPolicy` 拦截到模型过早结束。

记录事件：

```json
{
  "type": "capability_gap",
  "task": "分析一类新的本地文件",
  "missing_capability": "local_file_format_analysis",
  "evidence": [
    "tool:code_run:exit_code=127",
    "error: command not found"
  ],
  "suggested_actions": [
    "probe file metadata",
    "find parser dependency",
    "generate skill wrapper",
    "run smoke test"
  ]
}
```

### 6.3 Proposal Generator

把 gap 转成补能力方案。

输出必须包含：

- 能力名称和触发场景。
- 需要新增的依赖、工具、Skill 或 MCP。
- 风险等级。
- 安装范围：临时、项目级、用户级。
- smoke test。
- 回滚方式。

示例输出结构：

```yaml
id: local_file_format_analysis
proposal:
  summary: Add a project-level skill for parsing a new local file format.
  install_scope: project
  dependencies:
    python:
      - some-parser
  artifacts:
    - .cohort/skills/local_file_format_analysis/SKILL.md
    - .cohort/skills/local_file_format_analysis/scripts/parse_file.py
  risk:
    level: R2
    reason: installs Python dependency and processes local files
  verification:
    sample_task: "解析 samples/example.xxx 并输出摘要"
```

### 6.4 User Approval Gate

能力拓展可能修改环境，必须比普通工具调用更严格。

需要确认的动作：

- 安装依赖：`pip`、`npm`、`brew`、系统包管理器。
- 写入 `~/.cohort` 用户级能力。
- 新增或启用 MCP server。
- 启动长期后台服务。
- 访问外部 API 或上传本地文件。

确认内容应明确：

```text
Cohort wants to extend capability: local_file_format_analysis

Will do:
- install python dependency: some-parser
- create project skill: .cohort/skills/local_file_format_analysis
- run smoke test on samples/example.xxx

Risk:
- local dependency installation
- reads local files only

Approve? [yes/no]
```

### 6.5 Scaffolder / Installer

根据 proposal 生成候选能力。

产物类型：

| 类型 | 适合场景 |
| --- | --- |
| Skill | 有明确流程、可由现有工具组合完成 |
| Tool | 需要稳定结构化输入输出、高频复用 |
| MCP Adapter | 需要连接外部系统或长期服务 |
| SOP | 只是流程约束，不需要代码 |
| Dependency Manifest | 只需声明依赖和验证命令 |

Skill 目录示例：

```text
.cohort/skills/local_file_format_analysis/
  SKILL.md
  cohort-capability.yaml
  scripts/
    parse_file.py
  tests/
    smoke.sh
```

### 6.6 Verification Runner

能力不能靠“生成完成”即启用，必须验证。

验证要求：

- 依赖可用。
- smoke test 通过。
- 输出结构可被 Agent 消费。
- 日志和证据进入 `run.log.jsonl`。
- 失败时保留原因，不 promote。

命令：

```bash
cohort capability verify local_file_format_analysis
```

### 6.7 Capability Router

Runner 构造上下文前，根据用户任务检索 registry：

- 可用能力：注入简短摘要和入口。
- 候选能力：提示需要先 verify。
- 缺失能力：提示可进入 propose 流程。

Router 不应把所有能力全文塞进 prompt，只注入极简索引；具体内容仍按需读取 Skill / SOP。

## 7. CLI 设计

```bash
cohort capability list
cohort capability gaps
cohort capability show <id>
cohort capability propose "<task or gap>"
cohort capability build <proposal_id>
cohort capability verify <capability_id>
cohort capability promote <capability_id>
cohort capability disable <capability_id>
```

交互模式可增加 slash command：

```text
/capabilities
/capability gaps
/capability propose <task>
```

## 8. 与现有系统的关系

| 现有模块 | 关系 |
| --- | --- |
| `code_run` | 临时探测、安装和 smoke test 执行器 |
| Skill Runtime | 推荐的能力沉淀载体 |
| MCP Runtime | 外部系统能力的接入载体 |
| Long-term Memory | 记录经验，但不替代 registry |
| Observability | 记录 gap、proposal、verification 的证据 |
| `reflect once` | 可离线挖掘重复 gap 和高频失败能力 |
| R1/R2/R3 风险模型 | 复用到依赖安装、外部 API 和后台服务 |

## 9. 安全边界

能力拓展默认比普通任务更危险，因为它会改变未来 Agent 的行为边界。

硬规则：

- 不静默安装依赖。
- 不静默启用新 MCP server。
- 不把未验证能力写成 `available`。
- 不把 API key、token、password 写入 registry 或 Skill。
- 不把一次任务中的猜测写成能力事实。
- 不允许通过 `code_run` 绕过桌面 R2/R3 或文件安全边界。

依赖安装建议分级：

| 安装方式 | 默认风险 |
| --- | --- |
| 项目虚拟环境内 `pip install` | R2 |
| 用户级 `pip install --user` | R2 |
| 全局系统包安装 | R3 |
| 执行远程 shell installer | R3 |
| 启动长期后台服务 | R2/R3，取决于网络与权限 |

## 10. 分阶段落地计划

### P0：记录缺口

- 新增 `CapabilityGap` 事件类型。
- 在 Runner 中识别 no-tool 失败、重复工具失败、缺依赖错误。
- 写入 `run.log.jsonl` 和本地 gap 文件。

验收：

```bash
cohort capability gaps
```

能列出最近任务中的能力缺口。

### P1：Registry 与手动提案

- 新增 registry 文件。
- 新增 `cohort capability list/show/propose`。
- proposal 只生成 Markdown/YAML，不自动安装。

验收：

```bash
cohort capability propose "处理一种新的本地文件格式"
```

能生成包含依赖、风险、smoke test 的方案。

### P2：Skill Scaffolder

- 根据 proposal 生成 `.cohort/skills/<name>/`。
- 生成 `SKILL.md`、脚本骨架和 smoke test。
- 生成 `cohort-capability.json`，记录 proposal、entry 和验证命令。

验收：

```bash
cohort capability build <proposal_id>
cohort skill list
```

状态：已完成项目级 Skill scaffold。当前不覆盖已有 `SKILL.md`，避免破坏用户手工编辑；Tool/MCP adapter scaffold 仍是后续项。

### P3：Verification 与 Promote

- 新增 verify runner。
- 新增 doctor 诊断，检查候选能力的文件、依赖和验证状态。
- 新增依赖安装审核链路：`deps plan` 只生成计划，`deps approve` 显式批准，`deps install` 执行 Cohort 生成的 pip/npm/brew 命令并写入审计记录。
- 验证通过后将状态改为 `available`。
- 失败时将 capability 标记为 `failed`，保留候选文件供用户修复后重跑 verify。
- 新增 `disable`，允许显式关闭已注册能力。

验收：

```bash
cohort capability doctor <id>
cohort capability deps plan <proposal_id>
cohort capability deps approve <plan_id>
cohort capability deps install <plan_id>
cohort capability verify <id>
cohort capability promote <id>
cohort capability disable <id>
```

状态：已完成。`doctor` 会检查 Skill entry、manifest、smoke test、命令/env 依赖、依赖安装审计记录和最近 verify 状态；`deps` 链路会把计划与安装记录写入 `.cohort/capabilities/deps.json`，默认不自动安装，必须显式 approve/install。`promote` 要求存在最近一次成功 verify 的 `last_passed_at`，且 failed 状态必须重新 verify 后才能 promote。

### P4：自动路由与离线反思

- Runner 根据 registry 注入能力索引。
- `reflect once` 汇总高频 gap。
- 对重复 gap 自动建议 build/propose。

验收：

- 相似任务能自动发现已有能力。
- 高频失败能生成能力建设建议。

状态：部分完成。系统提示词现在会注入 `[Capability Index]`，且只包含 `status=available` 的能力；skill 类型能力会暴露 `skill_id`，模型仍需先调用 `skill_read` 再执行。候选、失败、禁用和缺失能力不会进入路由索引。`cohort capability suggestions` 已能从重复 unresolved gaps 中给出 propose 建议；`reflect once` 的离线汇总和自动生成更完整 proposal 仍是后续项。

## 11. 对外表达

当前版本可以这样描述：

> Cohort 当前具备通过 `code_run` 探测环境、运行脚本和执行依赖安装命令的基础能力，也具备 Skill、MCP、长期记忆和可观测性基础。当前版本已经能把“做不到”结构化为 capability gap，并通过 proposal、项目级 Skill scaffold、smoke test 和 registry promote 形成受控的候选能力闭环。依赖安装审核、Tool/MCP adapter 生成和自动路由仍在后续规划中。

不建议这样描述：

> Cohort 已经可以自动自我进化出任何能力。

原因：

- 没有验证的能力不是能力。
- 没有审核的依赖安装不是安全自进化。
- 没有 registry 的脚本沉淀无法稳定路由。

## 12. 关键结论

GA 的能力增长来自“少量通用工具 + 依赖安装 + SOP/脚本记忆 + 反射任务”的软闭环。Cohort 应保留这种增长方向，但升级为硬机制：

```text
Capability Gap
  -> Proposal
  -> Approval
  -> Scaffold
  -> Verify
  -> Promote
  -> Route
```

这才是适合 Cohort 的能力边界拓展方式。
