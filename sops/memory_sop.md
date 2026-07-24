# Memory SOP

## 触发场景

- 任务结束前出现长期记忆提示或 final review 提醒。
- 需要判断是否调用 `start_long_term_update`。
- 需要写入 `memory/global.md`、项目记忆或 `memory/reflection/sop_candidates.md`。
- 需要把稳定流程升级为正式 SOP。
- 用户提到记忆、长期记忆、项目记忆、经验沉淀、SOP candidate、skill、技能、能力等级。

## 核心原则

- No Execution, No Memory：没有工具证据，不写长期记忆。
- 上层只放指针：`memory/index.md` 和 `sops/index.md` 只做导航，不写操作细节。
- 默认不写：一次性事实、过程日志、临时页面内容、联系人、消息正文、失败猜测都应 skip。
- 默认 append：P0 只允许低风险追加；覆盖、合并、删除和索引更新都需要显式确认。
- 先候选后晋级：稳定流程先进 `sop_candidates.md`，再由人工确认升级到 `sops/*.md`。

## 能力等级

| 等级 | 资产 | 作用 | 晋级条件 |
| --- | --- | --- | --- |
| C0 | 原子工具 | 文件、命令、浏览器、询问等基础执行能力 | 工具 schema 已注册且测试通过 |
| C1 | SOP 约束 | 对特定场景给出执行顺序、禁区和验收标准 | 任务命中场景后读取 SOP 并写 checkpoint |
| C2 | 工作记忆 | 当前任务的约束、进度和下一步 | 读 SOP、切换子任务或多次失败后更新 |
| C3 | 长期记忆 entry | 经过验证的项目/全局经验 | 有 verified evidence，未来能减少重复摸索 |
| C4 | SOP candidate | 重复稳定流程的候选 Skill | 推荐步骤明确、触发词清晰、可复用且非一次性 |
| C5 | 正式 SOP / Skill | 已审查并进入 `sops/index.md` 的主动路由能力 | 人工确认升级，必要时更新索引和测试 |

## 判断流程

```text
1. 这条信息是否来自已验证证据？
   否 -> skip
2. 未来重新接手时缺了它是否会重复付出认知代价？
   否 -> skip
3. 它是稳定偏好/环境事实/项目约定？
   是 -> memory/global.md 或 memory/projects/<project_id>/project.md
4. 它是可重复执行的流程？
   是 -> memory/reflection/sop_candidates.md
5. 是否需要后续任务主动路由？
   是 -> 通过 /sop promote <id> 人工升级到 sops/*.md
```

## 工具流程

长期记忆收尾：

```text
start_long_term_update
memory_propose_update
memory_apply_update
```

规则：

- `start_long_term_update` 只初始化结构并返回证据列表，不写经验。
- `memory_propose_update` 可以 `skip=true`，这是正常结论，不是失败。
- `memory_apply_update` 只能应用低风险、已验证、非重复的 append。
- 每个 candidate 必须引用 `available_evidence` 中 `verified=true` 的 `evidence_ids`。

SOP candidate 晋级：

```text
/sop candidates
/sop promote <candidate_id>
/sop promote <candidate_id> --confirm-index
```

规则：

- 默认 promote 只生成 `sops/*.md`。
- 更新 `sops/index.md` 必须显式确认。
- 晋级后的 SOP 必须保留触发场景、执行流程、禁止事项和验收标准。

## Candidate 字段

```text
type
target
scene
trigger_keywords
lesson
recommended_steps
evidence_ids
risk
action
promote_to_sop
sop_title
sop_path
```

字段要求：

- `scene`：一句话描述可复用场景。
- `trigger_keywords`：后续检索会用到的短词，不写长句。
- `lesson`：稳定经验，不写本轮过程流水账。
- `recommended_steps`：能复跑的步骤，不能是抽象愿望。
- `risk`：不确定、跨项目或可能影响行为边界时标 medium/high，并要求确认。

## 禁止事项

- 保存 secret、token、cookie、API key。
- 保存当前 PID、一次性 URL、临时 session ID、时间戳等易变状态。
- 把模型推理、未执行计划、失败命令里的猜测写成事实。
- 把用户本轮让你处理的消息正文、联系人、审批内容等业务数据写入长期记忆。
- 因为 final review 提醒就强行写记忆；没有价值时必须 skip。

## 验收标准

- 写入前能指出证据 ID。
- 写入后工具返回成功，并能在目标文件中读到新增 entry。
- `memory/audit.jsonl` 记录了 apply 或 SOP index 更新。
- SOP 晋级后，`/sop candidates` 可追溯来源；若更新索引，`sops/index.md` 有对应路由项。
