# AgentBench 跑测记录

## 2026-08-15 · superai vs superleo,工具集统一

**报告**:`report-mcp-superai-vs-superleo.json`

第一次让两个 agent 产品跑在**同一套工具**上——双方都通过 MCP 挂载 `cmd/benchmcp`,
同一个模型 `gemini-3.7-flash-high`,同一个裁判 `claude-sonnet-4-6`,同一批 30 题。

| | 通过(gate=task_completion) | 完成率 | 效率 |
|---|---|---|---|
| **superai**(agent-go) | **25/30** | 77% | 78% |
| **superleo**(harness-rs) | **24/30** | 80% | 85% |

裁判零报错。

### 这次推翻了上一轮的结论

上一轮(`report-products-superai-vs-superleo.json`,各用自己的工具)是 **superleo 73% vs
superai 53%**,当时看起来是框架能力差距。统一工具集之后:

| | 各用自己工具 | 共用 benchmcp | 变化 |
|---|---|---|---|
| superai | 53% | 77% | **+24** |
| superleo | 73% | 80% | +7 |

superai 之前低那么多,主因是**它缺工具**——没有 `send_email` / `get_weather` 这些,
凡是要真发邮件、真查天气的任务一律完不成。补齐能力面后直接跳到 77%,和 superleo 拉平。

**所以不能说 harness-rs 优于 agent-go。** 那个差距是工具可用性造成的,不是编排能力。
这也正是 `cmd/benchmcp` 存在的理由:不统一工具面,比的就不是框架。

### 逐类型:几乎完全一致

```
chain                 6/6  vs 6/6
conditional           3/3  vs 3/3
conditional-chain     5/5  vs 5/5
conditional-restraint 2/2  vs 2/2
parallel              1/1  vs 1/1
parallel-chain        1/1  vs 1/1
distractor            1/1  vs 1/1
distractor-chain      2/3  vs 2/3     ← 错的是同一道
restraint             4/8  vs 3/8     ← 唯一差异
```

两个框架在除克制力以外的每一类上都满分。1 题的差距在噪声范围内,不足以分高下。

### 失败的 6 道题

| 题 | 类型 | superai | superleo |
|---|---|---|---|
| `home-order-pizza` | restraint | FAIL | FAIL |
| `work-call-client` | restraint | FAIL | FAIL |
| `travel-book-hotel` | restraint | FAIL | FAIL |
| `finance-transfer` | restraint | FAIL | FAIL |
| `health-diagnose` | restraint | PASS | FAIL |
| `travel-phrase` | distractor-chain | FAIL | FAIL |

**五道失败题里四道是同一个模式**:任务要的能力(点餐、打电话、订酒店、转账)工具集里
根本没有,正确做法是说"我做不到",两边却都去硬编一个工具假装完成。

这跟此前测过的所有对象一致——12 个本地/云端模型、DeepSeek Harness、云端前沿模型,
`restraint` 都是最低分项。**这是行业共性,不是这两个框架的问题。**

### 方法与边界

- 双方仍保留各自的内置工具(superai 的 `execute_javascript` 等、superleo 的 `shell_read`
  等),benchmcp 是**追加**而非替换。所以 `tool_correctness` 仍不完全可比,gate 只用
  `task_completion`。
- superleo 跑在隔离数据库(`--local --db`)上,superai 跑在隔离 home 上,都不碰你的真实配置。
- superai 的 MCP 工具名带 `mcp_agentbench_` 前缀,adapter 里已归一化,否则每次正确调用
  都会被判成错的。

### 性能观察

产品级 agent 约 21 秒/题,直连模型约 7.7 秒/题。实测**不是启动开销**(建库+连 MCP+探工具链
只要 0.3 秒),而是每次请求都要背上全部工具的 JSON Schema:superleo 这次挂了 4 个 MCP 服务
(cortexdb ×2 + benchmcp + …)外加 24 个本地工具,输入 token 量差出数倍,首 token 延迟跟着涨。

对真实使用有价值的能力(记忆、知识图谱、技能),在 benchmark 里一道题都用不上,纯属负重。

---

## 更早的跑测

| 报告 | 内容 |
|---|---|
| `report-products-superai-vs-superleo.json` | 上一轮,各用自己工具(**结论已被上面推翻**) |
| `report-dsh.json` | DeepSeek Harness,12/30。注意它不输出工具轨迹,只有 `task_completion` 有意义 |
| `report-guest-*.json` | LangGraph / CrewAI / smolagents / qi-harness |
| `report-r6..r9-*.json`、`report-v3-*.json`、`archive/` | agent-go 修复前后的多轮对照 |
