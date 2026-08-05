# Cohort Eval Stability

- generated_at: `2026-08-05T07:39:22Z`
- runs: `7`
- suites: `2`
- cases: `11`
- average_pass_rate: `63.1%`
- average_score: `95.1`
- average_stability: `16.7%`
- flaky_cases: `5`
- regressions: `0`

## Suites

| suite | runs | pass rate | score | stability | regressions |
| --- | ---: | ---: | ---: | ---: | ---: |
| `core` | 5 | 75.0% | 94.6 | 0.0% | 0 |
| `stateful` | 2 | 33.3% | 96.4 | 58.3% | 0 |

## Cases

| case | suite | pass rate | stability | flaky | regressions | latest |
| --- | --- | ---: | ---: | --- | ---: | --- |
| `locate_runner` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `read_active_model` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `read_go_version` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `read_only_boundary` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `summarize_observability` | `core` | 33.3% | 0.0% | true | 0 | `eval_20260805T051929.501060000` |
| `create_config` | `stateful` | 0.0% | 25.0% | false | 0 | `eval_20260805T070358.449676000` |
| `repair_go_test` | `stateful` | 0.0% | 50.0% | false | 0 | `eval_20260805T070358.449676000` |
| `honest_unknown` | `core` | 100.0% | 0.0% | false | 0 | `eval_20260805T051929.501060000` |
| `instruction_exact` | `core` | 100.0% | 0.0% | false | 0 | `eval_20260805T051929.501060000` |
| `no_unnecessary_question` | `core` | 100.0% | 0.0% | false | 0 | `eval_20260805T051929.501060000` |
| `patch_status` | `stateful` | 100.0% | 100.0% | false | 0 | `eval_20260805T070358.449676000` |

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
