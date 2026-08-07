# Cohort Eval Stability

- generated_at: `2026-08-07T08:58:44Z`
- runs: `20`
- suites: `1`
- cases: `4`
- average_pass_rate: `15.0%`
- average_score: `76.4`
- average_stability: `15.0%`
- flaky_cases: `3`
- regressions: `0`

## Suites

| suite | runs | pass rate | score | stability | regressions |
| --- | ---: | ---: | ---: | ---: | ---: |
| `computer-use-real` | 20 | 15.0% | 76.4 | 15.0% | 0 |

## Cases

| case | suite | pass rate | stability | flaky | regressions | latest |
| --- | --- | ---: | ---: | --- | ---: | --- |
| `macos_menu_dialog_safety` | `computer-use-real` | 14.3% | 14.3% | true | 0 | `eval_20260807T085733.866765000` |
| `browser_dom_form_roundtrip` | `computer-use-real` | 16.7% | 16.7% | true | 0 | `eval_20260807T083832.119240000` |
| `browser_ocr_canvas_fallback` | `computer-use-real` | 33.3% | 33.3% | true | 0 | `eval_20260807T064022.721583000` |
| `macos_textedit_draft_verify` | `computer-use-real` | 0.0% | 0.0% | false | 0 | `eval_20260807T064046.358014000` |

## Failure Signatures

| signature | count | cases |
| --- | ---: | --- |
| `computer-use-real::macos_menu_dialog_safety::max_tool_failures::0` | 6 | macos_menu_dialog_safety |
| `computer-use-real::browser_dom_form_roundtrip::judge_score::>= 80.0` | 5 | browser_dom_form_roundtrip |
| `computer-use-real::macos_menu_dialog_safety::judge_score::>= 75.0` | 4 | macos_menu_dialog_safety |
| `computer-use-real::browser_dom_form_roundtrip::max_tool_calls::9` | 3 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::max_turns::8` | 3 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::output_contains::COHORT_DOM_READY` | 3 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::required_tool::browser_type_element` | 3 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::tool_sequence::browser_open -> browser_wait_for_load -> browser_dom_summary -> browser_type_ele` | 3 | browser_dom_form_roundtrip |
| `computer-use-real::macos_menu_dialog_safety::max_turns::12` | 3 | macos_menu_dialog_safety |
| `computer-use-real::browser_dom_form_roundtrip::required_tool::browser_dom_summary` | 2 | browser_dom_form_roundtrip |
| `computer-use-real::macos_menu_dialog_safety::max_tool_calls::12` | 2 | macos_menu_dialog_safety |
| `computer-use-real::macos_menu_dialog_safety::max_turns::10` | 2 | macos_menu_dialog_safety |
| `computer-use-real::browser_dom_form_roundtrip::execution_error::none` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::forbidden_tool::browser_execute_js` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::required_tool::browser_open` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::required_tool::browser_wait_for_load` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_dom_form_roundtrip::status::done` | 1 | browser_dom_form_roundtrip |
| `computer-use-real::browser_ocr_canvas_fallback::judge_score::>= 75.0` | 1 | browser_ocr_canvas_fallback |
| `computer-use-real::browser_ocr_canvas_fallback::max_tool_failures::0` | 1 | browser_ocr_canvas_fallback |
| `computer-use-real::macos_menu_dialog_safety::max_tool_calls::14` | 1 | macos_menu_dialog_safety |
| `computer-use-real::macos_textedit_draft_verify::judge_score::>= 80.0` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::max_turns::10` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::required_tool::computer_find` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::required_tool::computer_type` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::tool_sequence::desktop_permissions -> computer_see -> computer_find -> computer_type -> compute` | 1 | macos_textedit_draft_verify |
| `computer-use-real::macos_textedit_draft_verify::tool_sequence::desktop_permissions -> computer_see -> computer_type -> computer_check` | 1 | macos_textedit_draft_verify |

## Action Items

| severity | category | title | evidence |
| --- | --- | --- | --- |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=3772ms` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="8" actual="13"` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 80.0" actual="35.0" message=verbose output: 477 chars > 200; tool overuse: 13 calls > 9` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 75.0" actual="70.0" message=verbose output: 288 chars > 120` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=7165ms` |
| `high` | `tool_routing` | 修正工具路由策略 | `required_tool expected="computer_type" actual="file_read,desktop_permissions,desktop_windows,code_run,computer_see,desktop_windows,desktop_windows,computer_see,computer_check"` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="10" actual="11"` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 80.0" actual="70.0" message=verbose output: 632 chars > 200` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=2` |
| `medium` | `trace_warning` | 清理失败路径中的 warning 事件 | `warnings=3` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=6055ms` |
| `medium` | `trajectory` | 收敛 Agent 执行轨迹 | `max_turns expected="12" actual="15"` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="3"` |
| `medium` | `judge_quality` | 提升 Judge 质量评分 | `judge_score expected=">= 75.0" actual="70.0" message=tool failures: 3` |
| `high` | `tool_failure` | 消除工具失败 | `tool_failures=1` |
| `medium` | `trace_warning` | 清理失败路径中的 warning 事件 | `warnings=1` |
| `medium` | `latency` | 压缩慢事件间隔 | `LLMRequestStarted -> LLMResponseFinished gap=3215ms` |
| `high` | `tool_failure` | 修复工具失败路径 | `max_tool_failures expected="0" actual="1"` |
| `medium` | `answer_quality` | 修正最终回答质量 | `output_contains expected="COHORT_DOM_READY" actual=""` |
| `high` | `tool_routing` | 修正工具路由策略 | `required_tool expected="browser_type_element" actual="file_read,browser_open,browser_wait_for_load,browser_wait_for_stable,browser_scan,browser_dom_summary,browser_open,browser_wait_for_load,browser_execute_js,browser_wait_for_load,code_run,browser_open,code_run"` |
| `critical` | `runtime` | 修复运行时失败 | `status expected="done" actual="timeout"` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=2 pass_rate=33.3%` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=6 pass_rate=14.3%` |
| `high` | `flaky` | 治理跨 run 不稳定 case | `passes=1 failures=5 pass_rate=16.7%` |
| `medium` | `failure_signature` | 合并处理重复失败签名 | `count=3 cases=browser_dom_form_roundtrip` |
