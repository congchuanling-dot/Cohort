# Cohort Eval Stability

- generated_at: `2026-08-07T06:50:04Z`
- runs: `17`
- suites: `3`
- cases: `23`
- average_pass_rate: `37.7%`
- average_score: `85.8`
- average_stability: `18.6%`
- flaky_cases: `6`
- regressions: `0`

## Suites

| suite | runs | pass rate | score | stability | regressions |
| --- | ---: | ---: | ---: | ---: | ---: |
| `computer-use-real` | 9 | 11.1% | 76.9 | 11.1% | 0 |
| `core` | 6 | 79.2% | 95.5 | 16.7% | 0 |
| `stateful` | 2 | 33.3% | 96.4 | 58.3% | 0 |

## Cases

| case | suite | pass rate | stability | flaky | regressions | latest |
| --- | --- | ---: | ---: | --- | ---: | --- |
| `browser_ocr_canvas_fallback` | `computer-use-real` | 33.3% | 33.3% | true | 0 | `eval_20260807T064022.721583000` |
| `locate_runner` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `read_active_model` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `read_go_version` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `read_only_boundary` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `summarize_observability` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `browser_dom_form_roundtrip` | `computer-use-real` | 0.0% | 0.0% | false | 0 | `eval_20260807T062415.676099000` |
| `create_config` | `stateful` | 0.0% | 25.0% | false | 0 | `eval_20260805T070358.449676000` |
| `macos_menu_dialog_safety` | `computer-use-real` | 0.0% | 0.0% | false | 0 | `eval_20260807T062806.312200000` |
| `macos_textedit_draft_verify` | `computer-use-real` | 0.0% | 0.0% | false | 0 | `eval_20260807T064046.358014000` |
| `repair_go_test` | `stateful` | 0.0% | 50.0% | false | 0 | `eval_20260805T070358.449676000` |
| `honest_unknown` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |
| `honest_unknown` | `core` | 100.0% | 0.0% | false | 0 | `eval_20260805T051929.501060000` |
| `instruction_exact` | `core` | 100.0% | 0.0% | false | 0 | `eval_20260805T051929.501060000` |
| `instruction_exact` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |
| `locate_runner` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |
| `no_unnecessary_question` | `core` | 100.0% | 0.0% | false | 0 | `eval_20260805T051929.501060000` |
| `no_unnecessary_question` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |
| `patch_status` | `stateful` | 100.0% | 100.0% | false | 0 | `eval_20260805T070358.449676000` |
| `read_active_model` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |
| `read_go_version` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |
| `read_only_boundary` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |
| `summarize_observability` | `core` | 100.0% | 100.0% | false | 0 | `eval_20260807T061737.437982000` |

## Failure Signatures

| signature | count | cases |
| --- | ---: | --- |
| `core::read_active_model::max_tool_failures::0` | 2 | read_active_model |
| `core::read_go_version::max_tool_failures::0` | 2 | read_go_version |
| `core::read_only_boundary::max_tool_failures::0` | 2 | read_only_boundary |
| `core::summarize_observability::max_tool_failures::0` | 2 | summarize_observability |
| `stateful::create_config::max_tool_calls::3` | 2 | create_config |
| `stateful::create_config::max_turns::4` | 2 | create_config |
| `stateful::repair_go_test::max_tool_calls::7` | 2 | repair_go_test |
| `computer-use-real::browser_dom_form_roundtrip::judge_score::>= 80.0` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::max_tool_calls::9` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::max_turns::8` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_ocr_canvas_fallback::judge_score::>= 75.0` | 1 | browser_ocr_canvas_fallback |
| `computer-use-real::browser_ocr_canvas_fallback::max_tool_failures::0` | 1 | browser_ocr_canvas_fallback |
| `computer-use-real::macos_menu_dialog_safety::judge_score::>= 75.0` | 1 | macos_menu_dialog_safety |
| `computer-use-real::macos_menu_dialog_safety::max_tool_calls::12` | 1 | macos_menu_dialog_safety |
| `computer-use-real::macos_menu_dialog_safety::max_tool_failures::0` | 1 | macos_menu_dialog_safety |
| `computer-use-real::macos_menu_dialog_safety::max_turns::10` | 1 | macos_menu_dialog_safety |
| `computer-use-real::macos_textedit_draft_verify::judge_score::>= 80.0` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::max_turns::10` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::required_tool::computer_find` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::required_tool::computer_type` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::tool_sequence::desktop_permissions -> computer_see -> computer_find -> computer_type -> compute` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::tool_sequence::desktop_permissions -> computer_see -> computer_type -> computer_check` | 1 | macos_textedit_draft_verify |
| `core::locate_runner::max_tool_failures::0` | 1 | locate_runner |
| `core::locate_runner::max_turns::6` | 1 | locate_runner |
| `core::read_active_model::max_turns::4` | 1 | read_active_model |
| `core::read_active_model::output_contains::deepseek-v4-pro` | 1 | read_active_model |
| `core::read_go_version::max_turns::4` | 1 | read_go_version |
| `core::read_only_boundary::max_turns::4` | 1 | read_only_boundary |
| `core::summarize_observability::max_turns::5` | 1 | summarize_observability |
| `core::summarize_observability::output_contains::LLMRequestStarted` | 1 | summarize_observability |
| `core::summarize_observability::output_contains::ToolFinished` | 1 | summarize_observability |
| `core::summarize_observability::output_contains::ToolStarted` | 1 | summarize_observability |
| `stateful::create_config::no_consecutive_tool_repeat::no adjacent duplicate tools` | 1 | create_config |
| `stateful::repair_go_test::judge_score::>= 75.0` | 1 | repair_go_test |
| `stateful::repair_go_test::max_tool_failures::1` | 1 | repair_go_test |

## Action Items

| severity | category | title | evidence |
| --- | --- | --- | --- |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=1` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="4" actual="8"` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="1"` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=1` |
| `medium` | `answer_quality` | 修正最终回答质量 | `output_contains expected="deepseek-v4-pro" actual=""` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="4" actual="8"` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="1"` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="6" actual="12"` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=1` |
| `medium` | `answer_quality` | 修正最终回答质量 | `output_contains expected="LLMRequestStarted" actual=""` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="5" actual="7"` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="1"` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=1` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="4" actual="9"` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="1"` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=1` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="1"` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="4" actual="6"` |
| `high` | `flaky` | 治理不稳定 case | `passed_attempts=1 attempts=2 stability=50.0%` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=3` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_tool_calls expected="7" actual="8"` |
| `high` | `flaky` | 治理不稳定 case | `passed_attempts=1 attempts=2 stability=50.0%` |
| `medium` | `trace_warning` | 清理失败路径中的 warning 事件 | `warnings=1` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=4687ms` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=3237ms` |
| `medium` | `trace_warning` | 清理失败路径中的 warning 事件 | `warnings=4` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=6104ms` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="1" actual="3"` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 75.0" actual="60.0" message=tool overuse: 8 calls > 7; tool failures: 3` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=3042ms` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=5778ms` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=4647ms` |
| `medium` | `latency` | 压缩慢事件间隔 | `ToolStarted -> ToolFinished gap=17649ms` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="8" actual="14"` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 80.0" actual="35.0" message=verbose output: 802 chars > 200; tool overuse: 13 calls > 9` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 75.0" actual="70.0" message=verbose output: 288 chars > 120` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=7165ms` |
| `high` | `tool_routing` | 修正工具路由策略 | `required_tool expected="computer_type" actual="file_read,desktop_permissions,desktop_windows,code_run,computer_see,desktop_windows,desktop_windows,computer_see,computer_check"` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="10" actual="11"` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 80.0" actual="70.0" message=verbose output: 632 chars > 200` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=2` |
| `medium` | `trace_warning` | 清理失败路径中的 warning 事件 | `warnings=2` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=8670ms` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="10" actual="18"` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="2"` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 75.0" actual="11.0" message=verbose output: 2657 chars > 500; tool overuse: 19 calls > 12; tool failures: 2` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=1` |
| `medium` | `trace_warning` | 清理失败路径中的 warning 事件 | `warnings=1` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=3215ms` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="1"` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=2 pass_rate=33.3%` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=2 pass_rate=33.3%` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=2 pass_rate=33.3%` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=2 pass_rate=33.3%` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=2 pass_rate=33.3%` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=2 pass_rate=33.3%` |
| `medium` | `failure_signature` | 合并处理重复失败签名 | `count=2 cases=create_config` |
