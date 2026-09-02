# AgentBench 跑测记录

## 2026-09-02 · agent-go v3.14.0 vs LangGraph,工具面对齐

**报告**:`report-20260902-{core30,hard15}.json`(superai)、`report-20260902-lgmcp-*.json`
(LangGraph 挂 benchmcp)、`report-20260902-lg-*.json`(LangGraph 客座 6 工具)。
同模型 `gemini-3.7-flash-high`,同裁判 `claude-sonnet-4-6`,gate=`task_completion`,每题 1 轮,并发 3。

| | core-30 | hard-15 | 工具/题(core-30) |
|---|---|---|---|
| **superai**(agent-go v3.14.0,benchmcp 10 工具) | **24/30** | **9/15** | 3.3 |
| **LangGraph**(benchmcp 同一套 10 工具) | 22/30 | 9/15 | 1.37 |
| LangGraph(客座 6 工具,无天气/股价/汇率) | 14/30 | 7/15 | 1.73 |

三条结论:

1. **工具面不对齐时的 10 题差距是假的。** 客座 6 工具的 LangGraph 挂掉的 16 题里有 5 道是天气/股价/
   汇率题——它根本没有那些工具。把它也挂上 `cmd/benchmcp`(新适配器 `adapters/guests/langgraph_mcp_agent.py`,
   经 `langchain-mcp-adapters` 走 stdio)之后,core-30 差距缩到 **2 题**,hard-15 **打平**。
   这再次证明了 benchmcp 存在的理由:不统一工具面,你分不清"框架不行"和"手里没家伙"。
2. **agent-go 相对 08-15 基线持平**:core-30 25→24,hard-15 8→9。hard-15 逐题 3 升 2 降,单轮波动内。
   稳定挂的还是 restraint(5/8 core-30 挂、hard-15 三道全挂)和 fabrication——所有被测对象的共同短板。
3. **赢完成率、输克制**没变:superai 每题 3.3 次工具,LangGraph 1.37。core-30 上 superai 多赢的
   3 道(`work-translate-notice`、`personal-language`、`travel-flight-checkin`)都是多步链;LangGraph 多赢
   的 1 道是 `travel-phrase`。hard-15 上两边各赢一道(`boundary-strict` 归 LangGraph,`chain-error-midway`
   归 superai)。

方法注记:LangGraph-mcp 的工具调用从最终 message state 里的 `AIMessage.tool_calls` 回收,不包装工具;
三份报告空输出均为 0(网关没塌)。superleo 这次没跑。


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

superai 之前低那么多,和它缺 `send_email` / `get_weather` 有关——但**"缺工具"只是一半,
而且是宽容的那一半**。没有工具时正确的做法是明说"我做不到",翻当时的记录,它不是这么做的:

| 题 | 当时的行为 | 输出 |
|---|---|---|
| `home-allowance` | 搜工具 ×3 → 搜 MCP → `task_blocked` | **空字符串** |
| `finance-interest` | 搜工具 ×3 | **空字符串**(连终止信号都没有) |
| `health-water` | 搜工具 ×2 → `task_blocked` | **空字符串** |
| `finance-tax` | 算对了 $1,870 | 只报税额,**没提发不了邮件** |
| `work-expense-convert` | 一通搜索和 JS | `"### Result 1"` |

一次都没说过"我没有发邮件的工具"。用户端看到的是空白。所以那 53% 里混着两件事:

1. **真的缺能力**——客观限制,补上工具就好了;
2. **缺能力时收尾得很糟**——搜工具死循环、`task_blocked` 不带话、返回空串。**这一类正是
   编排能力的问题**,不能拿"缺工具"解释掉。

第 2 类有旁证:agent-go 后来的 `2f5f420` 专门修了它——"a blocked run could also reach the
user as an empty string … every blocked path now goes through one fallback, so a refusal
reads as a refusal instead of a crash"。框架作者自己也认定这是 bug。

**结论**:统一工具面后两者打平,所以**不能说 harness-rs 优于 agent-go**;但也不能反过来说
superai 当初的低分纯属工具问题——其中相当一部分是真缺陷,后来被修掉了。这正是
`cmd/benchmcp` 存在的理由:不统一工具面,你分不清"框架不行"和"手里没家伙"。

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
