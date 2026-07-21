# Cohert 自我进化能力技术方案

本文档描述 Cohert 后续实现“自我进化能力”的技术方案。

这里的“自我进化”不是让 Agent 无约束地修改自身代码，而是让 Agent 在完成任务后，能够把经过验证的经验沉淀为可复用资产，并在后续任务中自动检索、注入和执行。核心资产包括：

- 短期工作记忆
- 长期记忆
- 项目记忆
- SOP
- 历史会话归档
- 后台反射任务报告

## 1. GA 调研结论

GA 的自我进化能力由几类机制共同组成，不是单一模块。

### 1.1 短期工作记忆

GA 通过 `update_working_checkpoint` 保存当前任务的关键约束、进度和下一步。

关键实现位置：

- `ga.py`：`GenericAgentHandler.do_update_working_checkpoint`
- `ga.py`：`GenericAgentHandler._get_anchor_prompt`
- `ga.py`：`GenericAgentHandler.turn_end_callback`
- `assets/tools_schema.json`：`update_working_checkpoint` 工具描述

数据流：

```text
模型调用 update_working_checkpoint
  -> handler.working["key_info"] / handler.working["related_sop"]
  -> 后续 turn 通过 _get_anchor_prompt 注入
  -> 长任务中防止遗忘 SOP、用户约束和阶段性发现
```

特点：

- 只保留当前任务需要的少量高价值信息。
- 每轮或隔轮注入，不落入长期记忆。
- 适合保存“当前要避开什么坑”“下一步要做什么”。
- 多轮失败时会强制提示更新 checkpoint。

### 1.2 长期记忆沉淀

GA 通过 `start_long_term_update` 启动任务收尾阶段的经验提炼。

关键实现位置：

- `ga.py`：`GenericAgentHandler.do_start_long_term_update`
- `memory/memory_management_sop.md`
- `memory/global_mem_insight.txt`
- `memory/global_mem.txt`
- `memory/*.md`

GA 的长期记忆分层：

```text
L1: global_mem_insight.txt
    极简索引，只保存关键词和指针

L2: global_mem.txt
    稳定事实库，例如环境事实、配置、用户偏好

L3: memory/*.md / *.py
    专项 SOP、复杂任务经验、可复用脚本

L4: memory/L4_raw_sessions/
    历史会话归档，用于后续检索和挖掘
```

最重要的规则：

```text
No Execution, No Memory.
未经工具验证的信息不能写入长期记忆。
```

也就是说，模型推理、计划、猜测不能直接进入长期记忆。只有来自成功工具调用、已读取文件、已运行测试、已验证页面状态的信息，才可以沉淀。

### 1.3 后台反射机制

GA 支持通过 `agentmain.py --reflect <script>` 启动后台反射模式。

关键实现位置：

- `agentmain.py`：`--reflect` 分支
- `reflect/autonomous.py`
- `reflect/scheduler.py`
- `reflect/goal_mode.py`

数据流：

```text
reflect script 定期 check()
  -> 返回 prompt
  -> agent.put_task(prompt, source="reflect")
  -> Agent 执行任务
  -> 结果写入 reflect log 或 done report
  -> reflect script 可通过 on_done(result) 更新状态
```

GA 的典型反射任务：

- 定时任务执行
- 长目标持续推进
- 用户离开后的自主任务
- 会话日志归档
- 历史经验整理

### 1.4 历史会话归档

GA 的 `reflect/scheduler.py` 会周期性触发 L4 历史归档。

关键实现位置：

- `reflect/scheduler.py`
- `memory/L4_raw_sessions/compress_session.py`

归档流程：

```text
temp/model_responses/*.txt
  -> compress_session.py 压缩
  -> 提取 <history> 中的 [USER] / [Agent] 摘要
  -> 写入 memory/L4_raw_sessions/all_histories.txt
  -> 原始会话按月份压缩归档
```

它的价值不是每轮都注入旧历史，而是让 Agent 后续可以按需检索历史会话，发现重复问题、长期偏好和高频失败模式。

### 1.5 项目记忆注入

GA 的 `plugins/project_mode.py` 使用 hook 机制实现项目模式。

关键点：

- 每轮只注入项目记忆的轻量指针。
- 不把 `project_memory.md` 全文塞进上下文。
- 模型根据当前任务自行判断是否读取详细记忆。
- 收尾时要求判断本轮是否产生值得写入项目记忆的经验。

这对 Cohert 很有参考价值：项目级记忆应该“可发现、可读取、少注入”，而不是每轮强行塞满上下文。

## 2. Cohert 当前基础

Cohert 已经具备一些可复用基础：

- `Runner.history`：完整内存历史。
- `history.jsonl`：本地会话历史落盘。
- `contextmgr`：请求前上下文构造、工具结果压缩、session memory 注入。
- `memory.md`：当前 session 的稳定事实摘要。
- `compact.md`：长历史摘要。
- `update_working_checkpoint`：短期工作记忆工具。
- `sops/index.md`：SOP 路由入口。
- `temp/model_responses/`：模型原始响应日志。

所以 Cohert 不需要从零开始复制 GA，而应该在现有架构上补齐：

```text
任务中：工作记忆
任务后：长期记忆候选
跨会话：项目/全局记忆
后台：反射与归档
安全：验证来源和写入边界
```

## 3. 目标架构

建议增加一个受控的 Evolution Manager。

```text
Runner
  -> Tool calls
  -> WorkingCheckpoint
  -> SessionStore(history.jsonl)
  -> ContextManager
  -> EvolutionManager
       -> CandidateExtractor
       -> MemoryClassifier
       -> EvidenceValidator
       -> MemoryWriter
       -> ReflectScheduler
```

模块职责：

| 模块 | 职责 |
| --- | --- |
| `EvolutionManager` | 统一编排经验提取、分类、验证、写入和报告 |
| `CandidateExtractor` | 从会话历史、工具结果、checkpoint 中提取候选经验 |
| `MemoryClassifier` | 判断候选经验应该进入 session/project/global/SOP 哪一层 |
| `EvidenceValidator` | 校验候选经验是否来自工具验证，禁止猜测入库 |
| `MemoryWriter` | 执行最小化写入，优先 append 或小 patch |
| `ReflectScheduler` | 后台定时触发归档、整理和建议生成 |

## 4. 记忆分层设计

建议 Cohert 使用四层记忆结构。

```text
memory/
  index.md
  global.md
  projects/
    <project_id>/
      project.md
      lessons.md
  raw_sessions/
    all_histories.md
    archives/

sops/
  index.md
  browser_sop.md
  code_run_sop.md
  ...
```

### 4.1 L1：记忆索引

路径：

```text
memory/index.md
```

职责：

- 每轮轻量注入。
- 只保存关键词到记忆文件的映射。
- 不写细节。
- 帮助模型知道“有哪些记忆可以读”。

示例：

```text
# Memory Index

项目约定: memory/projects/current/project.md
用户偏好: memory/global.md#user-preferences
浏览器操作: sops/browser_sop.md
命令执行: sops/code_run_sop.md
历史会话: memory/raw_sessions/all_histories.md
```

### 4.2 L2：全局记忆

路径：

```text
memory/global.md
```

职责：

- 用户稳定偏好。
- 跨项目通用约束。
- 环境级事实。
- 高频失败经验。

禁止内容：

- 临时路径。
- 当前 PID。
- 一次性错误输出。
- 未验证推理。
- secret、token、cookie、API key。

### 4.3 L3：项目记忆与专项 SOP

路径：

```text
memory/projects/<project_id>/project.md
memory/projects/<project_id>/lessons.md
sops/*.md
```

职责：

- 项目结构认知。
- 项目约定。
- 难以快速重建的坑点。
- 特定任务的 SOP。
- 可复用脚本或操作流程。

写入原则：

- 只写未来会重复节省认知成本的信息。
- 如果重新探测几步就能得到，不写。
- 如果只是本轮任务过程日志，不写。

### 4.4 L4：历史会话归档

路径：

```text
memory/raw_sessions/
```

职责：

- 保存历史会话摘要。
- 支持后续检索。
- 为反射任务挖掘高频失败模式提供材料。

建议从 `temp/sessions/*/history.jsonl` 和 `temp/model_responses/*` 归档，而不是直接保存完整大日志。

## 5. 核心流程

### 5.1 任务中：短期工作记忆

沿用当前 `update_working_checkpoint`，但强化触发规则。

触发场景：

- 读完 SOP 后。
- 子任务切换前。
- 连续失败两次后。
- 工具结果中出现关键新事实后。
- 上下文即将变长时。

注入内容：

```text
[WORKING CHECKPOINT]
[任务] ...
[关键约束] ...
[禁止事项] ...
[当前进度] ...
[下一步] ...
[相关 SOP] ...
```

### 5.2 任务完成：长期记忆候选提取

新增工具：

```text
start_long_term_update
```

工具行为：

1. 读取当前 session 的 history 和 checkpoint。
2. 要求模型列出“候选记忆”。
3. 每条候选必须包含：
   - 内容
   - 类型
   - 来源证据
   - 建议落点
   - 是否需要用户确认
4. 没有值得沉淀的信息时明确返回 skip。

候选格式：

```text
## Candidate

- type: project_lesson
- target: memory/projects/current/lessons.md
- evidence: history.jsonl turn 8, tool code_run exit_code=0
- content: ...
- risk: low
- action: append
```

### 5.3 验证：Evidence Validator

写入前必须检查来源。

允许来源：

- `file_read` 读取到的文件内容。
- `code_run` 成功执行结果。
- `go test` / 构建命令通过。
- 浏览器工具确认到的页面状态。
- 用户明确表达的稳定偏好。
- 已存在记忆文件中的事实。

禁止来源：

- 模型猜测。
- 未执行计划。
- 失败命令中的未经确认结论。
- 临时观察。
- 仅为了本轮任务存在的信息。

### 5.4 写入：最小化修改

写入策略：

- 优先 append。
- 已有同类条目时小范围 patch。
- 禁止整体 overwrite 记忆文件。
- 大范围整理必须生成 proposal。
- 写入后重新读取目标文件确认。

写入结果应保存审计记录：

```text
memory/audit.jsonl
```

字段：

```json
{
  "target": "memory/projects/current/lessons.md",
  "action": "append",
  "source_session": "...",
  "evidence": "...",
  "summary": "..."
}
```

## 6. 新增工具设计

### 6.1 start_long_term_update

用途：

任务完成后启动长期记忆提炼流程。

Schema：

```json
{
  "name": "start_long_term_update",
  "description": "Start distilling verified long-term memory after a task is complete.",
  "parameters": {
    "type": "object",
    "properties": {
      "reason": {
        "type": "string",
        "description": "Why this task may contain long-term reusable knowledge."
      }
    }
  }
}
```

返回：

```json
{
  "status": "success",
  "next_step": "read memory management policy and propose candidates"
}
```

### 6.2 memory_propose_update

用途：

生成待写入的记忆候选，不直接写文件。

返回：

```json
{
  "status": "success",
  "candidates": [
    {
      "type": "project_lesson",
      "target": "memory/projects/current/lessons.md",
      "content": "...",
      "evidence": "...",
      "risk": "low"
    }
  ]
}
```

### 6.3 memory_apply_update

用途：

应用已验证的低风险记忆更新。

约束：

- 只能写 `memory/` 和 `sops/`。
- 默认只允许 append。
- 修改 SOP 需要更严格证据。
- 修改核心代码不允许走这个工具。

### 6.4 reflect_run

用途：

后台反射任务入口。

第一版可以先不做常驻 daemon，只做命令：

```bash
cohert reflect once
cohert reflect scheduler
```

## 7. 后台反射设计

第一版后台反射不直接改记忆，只生成报告。

任务类型：

| 任务 | 产物 |
| --- | --- |
| 归档历史会话 | `memory/raw_sessions/all_histories.md` |
| 挖掘高频失败 | `memory/reflection/failure_patterns.md` |
| 发现可沉淀 SOP | `memory/reflection/sop_candidates.md` |
| 清理过期记忆 | `memory/reflection/cleanup_proposals.md` |
| 项目记忆整理 | `memory/projects/<project_id>/review.md` |

反射任务流程：

```text
reflect trigger
  -> 读取历史摘要和审计日志
  -> 找重复失败、重复询问、重复搜索、重复修复
  -> 生成 proposal
  -> 用户确认后再应用
```

## 8. 安全边界

自我进化能力必须默认保守。

### 8.1 自动允许

- 读取历史。
- 生成候选记忆。
- 写入当前 session 的 `memory.md`。
- append 低风险项目经验。
- 生成反射报告。

### 8.2 需要用户确认

- 修改 `sops/*.md`。
- 修改 `memory/index.md`。
- 删除或合并长期记忆。
- 把反射建议应用到全局记忆。
- 任何影响多个项目的规则变更。

### 8.3 禁止自动执行

- 修改 `internal/`、`cmd/` 等核心代码。
- 删除非临时文件。
- 写入 secret。
- 保存 cookie、token、API key。
- 把失败假设当事实写入。
- 覆盖整个记忆文件。

## 9. 与现有 Cohert 架构的接入点

### 9.1 Runner

修改点：

- 在 `Runner.Run` 完成时，根据轮数、失败次数、工具类型判断是否提示模型调用 `start_long_term_update`。
- 在 `turn` 达到阈值时提示更新 checkpoint。
- 在 `RunResult` 中保留可选的 evolution summary。

### 9.2 Context Manager

修改点：

- 在现有 `memory.md` / `compact.md` 注入前增加 `memory/index.md`。
- 项目记忆只注入指针，不全文注入。
- 对长期记忆设置独立预算。

建议注入顺序：

```text
global memory index
project memory pointer
session memory.md
compact.md
recent history
```

### 9.3 Tools

新增工具：

- `start_long_term_update`
- `memory_propose_update`
- `memory_apply_update`

增强工具：

- `update_working_checkpoint`：schema 中补充更多触发规则。
- `file_patch`：用于记忆更新时强制唯一匹配。
- `file_read`：读取 memory/SOP 后提示必须 checkpoint。

### 9.4 Session Store

新增能力：

- 列出最近 session。
- 导出 session 摘要。
- 归档旧 session 到 `memory/raw_sessions/`。
- 给每条 memory audit 关联 session ID。

## 10. 分阶段落地计划

### P0：受控长期记忆

目标：任务完成后能产生并写入经过验证的经验。

任务：

1. 新增 `memory/` 目录结构。
2. 新增 `memory/index.md`、`memory/global.md`。
3. 新增 `start_long_term_update` 工具。
4. 新增 memory management prompt。
5. 实现候选记忆提取。
6. 只允许写 session memory 和 project memory。
7. 增加单元测试：未验证信息不能入库。

### P1：项目记忆与按需读取

目标：让 Cohert 在项目内持续积累经验，但不撑爆上下文。

任务：

1. 增加 `memory/projects/<project_id>/project.md`。
2. Context Manager 注入项目记忆指针。
3. 收尾时自动判断是否有项目经验值得追加。
4. 写入审计日志。
5. 增加 `/memory` 或 `/project memory` 查看命令。

### P2：后台反射与历史归档

目标：让 Cohert 能从历史会话中发现重复模式。

任务：

1. 新增 `cohert reflect once`。
2. 实现 history 归档到 `memory/raw_sessions/`。
3. 生成失败模式报告。
4. 生成 SOP 候选报告。
5. 默认不自动应用报告。

### P3：SOP 自动建议

目标：从多次重复任务中沉淀 SOP。

任务：

1. 对重复成功路径生成 SOP 候选。
2. 对重复失败路径生成避坑候选。
3. 用户确认后写入 `sops/*.md`。
4. 更新 `sops/index.md`。
5. 给 SOP 更新增加回归测试或人工审核清单。

### P4：代码级自我改进建议

目标：让 Cohert 能提出自身代码改进方案，但不自动改核心代码。

任务：

1. 反射发现工具缺陷或 prompt 缺陷。
2. 生成技术方案和 patch proposal。
3. 用户确认后才进入实现。
4. 必须跑测试。
5. 必须保留回滚路径。

## 11. 第一版建议实现范围

第一版建议只做 P0。

不要一开始做后台 daemon，也不要让 Agent 自动改代码。原因：

- 记忆写入边界比功能本身更重要。
- 一旦长期记忆污染，后续每轮都会被错误信息影响。
- 自动改代码风险更高，需要先有 proposal、测试和审核机制。

第一版完成标准：

- 任务结束后可以触发长期记忆提炼。
- 能区分“值得记忆”和“不值得记忆”。
- 只写经过工具验证的信息。
- 写入动作可审计。
- 后续任务能通过 `memory/index.md` 发现并读取记忆。

## 12. 关键风险

| 风险 | 表现 | 缓解 |
| --- | --- | --- |
| 记忆污染 | 猜测被写成事实 | Evidence Validator 强制证据 |
| 上下文膨胀 | 每轮注入太多记忆 | L1 只注入索引，L2/L3 按需读取 |
| 旧经验误导 | 过期规则持续影响任务 | 审计日志、反射清理 proposal |
| 自动修改失控 | Agent 改坏自身代码 | 禁止自动改核心代码 |
| 重复写入 | 同一经验多次追加 | 写入前检索相似条目 |
| 用户隐私泄漏 | secret 被写入 memory | secret 扫描和禁止规则 |

## 13. 结论

Cohert 的自我进化应该走“记忆和 SOP 进化优先，代码自改延后”的路线。

推荐路线：

```text
短期 checkpoint
  -> 任务收尾提炼
  -> 验证候选记忆
  -> 写入项目/全局记忆
  -> 后续按需读取
  -> 后台反射生成改进建议
  -> 用户批准后更新 SOP 或代码
```

这个方案能够复用 GA 的核心经验，同时更适合 Cohert 当前 Go 架构：先把经验沉淀、检索和注入做好，再逐步加入后台反射和代码级改进建议。
