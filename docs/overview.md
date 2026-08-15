# Eval-Go 总览:它测什么,怎么用

README 是参考手册,查具体 flag 和指标看它。这篇讲的是**心智模型**——这个项目分几层、什么问题该用哪一层、以及每种测法的边界在哪。

## 一句话

给 AI 打分的工具。底层是评分引擎(Go 库 + `evalgo` CLI),上层是用这个引擎搭的现成 benchmark(`agentbench/`)。

## 两个层次,回答两种不同的问题

|  | 问题 | 你要准备什么 | 入口 |
|---|---|---|---|
| **层一** | 「这个回答好不好?」 | 一个 JSON 数据集:问题 + 模型的回答 | `evalgo -d dataset.json` |
| **层二** | 「这个 agent 会不会办事?」 | 一个任务集 + 被测 agent 的启动命令 | `evalgo bench` |

区别不在指标,在**数据从哪来**。层一你把回答喂进来;层二 eval-go 自己去驱动 agent 产出回答、工具调用和执行轨迹,再走同一套评分。

底座是共用的:`Suite` / `Report` / 裁判 / 缓存 / 成本追踪 / CI 闸门。

---

## 层一:测输出质量

准备一个 `Sample` 数组(JSON / JSONL / CSV 都行):

```json
[{
  "name": "case-1",
  "input": "储蓄账户利率是多少?",
  "output": "利率是分档的:5万以下 0.30%,以上 0.55% [KB-001]。",
  "context": ["储蓄账户 5 万以下年利率 0.30%,5 万以上 0.55%。"],
  "rubric": "准确说出两档利率并给出出处。"
}]
```

跑:

```bash
# 确定论指标:免费、离线、秒出
evalgo -d dataset.json -m nonempty,citation,valid_json

# 加 LLM 裁判
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...
evalgo -d dataset.json --judge env -m faithfulness,answer_relevancy,rubric --cache .cache
```

### 27 个指标,按类别

| 类别 | 指标 | 说明 |
|---|---|---|
| 确定论(不花钱) | `valid_json` `citation` `exact_match` `nonempty` `tool_correctness` `refusal` | 纯代码判定,当廉价前置闸门用 |
| RAG | `faithfulness` `answer_relevancy` `context_precision` `contextual_recall` `contextual_relevancy` | 拆成原子命题逐条验证(RAGAS 式),不是笼统打一个分 |
| 安全 | `hallucination` `bias` `toxicity` `pii_leakage` | |
| 多轮 | `conversation_completeness` `knowledge_retention` `role_adherence` `conversation_relevancy` | 读 `Sample.Turns` |
| 红队 | `attack_resistance` `refusal` | 配 `evalgo redteam` 生成的攻击题用 |
| Agent | `task_completion` `step_efficiency` `plan_quality` `plan_adherence` `argument_correctness` | 读执行轨迹而非最终文本 |
| 通用 | `rubric` | G-Eval:按你写的标准打分 |
| 库级 | `DAG` `JSONHasFields` `MatchesRegex` `ForbidsRegex` `Contains` | 需要构造参数,不在 CLI 注册表里 |

**`faithfulness` 是招牌**:`examples/golden.json` 里故意埋了一条幻觉答案,还配了个能骗过确定论检查的假引用 `[KB-001]`。真实模型实测判 **0.00**——它看穿了"统一 1.5% 利率"在上下文里没有依据。

---

## 层二:测 agent 会不会办事

Agent 指标读的是**执行轨迹**,不是最终那段话。`Sample` 上带这些字段:

```json
{
  "plan": "1) 查天气 2) 算小费 3) 发邮件",
  "trajectory": ["查了罗马天气", "算出 20% = 30", "发送邮件"],
  "tool_calls": [{"name": "get_weather", "args": {"city": "Rome"}}],
  "expected_tools": ["get_weather", "calculator", "send_email"]
}
```

这是 **record-then-evaluate**:先跑完录下来再评分,不像 DeepEval 那样要在运行时插桩(`@observe`)。代价是被测系统必须能吐出工具调用记录;好处是任何框架、任何语言都能接。

`evalgo bench` 会替你完成"驱动 agent"这一步:

```bash
evalgo bench --tasks agentbench/tasks/core-30.json --targets targets.json \
  -m tool_correctness,task_completion,step_efficiency \
  --gate tool_correctness,task_completion --cache .cache
```

输出是 **任务 × Agent 的 PASS/FAIL 网格**。`targets.json` 里每个 agent 就是一条启动命令,eval-go 用环境变量 `EVAL_INPUT` 把任务喂进去,读它 stdout 上的 JSON。

### 一个踩过的坑:gate 别用 `rubric`

`rubric` 只看最终那段文本。工具调用类任务里,很多 agent 调完工具就停了、最终文本是空的——用 `rubric` 当闸门会让所有 agent 全军覆没,而那是测量假象不是真实表现。**测 agent 就 gate 在 `tool_correctness,task_completion`**,这两个会读轨迹。

---

## AgentBench:现成的工具调用擂台

`agentbench/` 是用上面这套引擎搭的一个完整 benchmark,不是 example。

**30 道题**(`tasks/core-30.json`),6 个领域各 5 道(家庭/公司/健康/旅行/财务/个人),按"最能拉开差距"加权:

| 类型 | 题数 | 考什么 |
|---|---|---|
| `restraint` | 8 | **没有合适工具时,敢不敢说"我做不到"** |
| `conditional` 系(含 `-chain` / `-restraint`) | 10 | 算出条件后走对分支 |
| `chain` | 6 | 3-5 步依赖调用不掉链子 |
| `distractor` 系 | 4 | 在易混工具里选对 |
| `parallel` 系 | 2 | 一次要调多个 |

有些条件题**正确答案是少调工具**(比如 BMI 没超标就不该设提醒),专门抓"过度行动"。

**10 个工具**在 `tools/` 里定义一次,两种方式供给:

| 前端 | 用于 | 方式 |
|---|---|---|
| `cmd/benchagent` | 原始模型 | 挂到 OpenAI 兼容请求上 |
| `cmd/benchmcp` | 自带运行时的 agent | **MCP stdio 服务** |

MCP 那条路是为公平性存在的:goose、dsh 这类 agent 自带工具集,不给它们同一套工具,`tool_correctness` 比的就不是一回事——分不清"选错了工具"和"手里根本是另一套工具"。

```bash
go build -o bin/benchmcp ./cmd/benchmcp
goose run --with-extension "$PWD/bin/benchmcp" --text "..."
```

其中 4 个是**故意的干扰工具**:`unit_convert` vs `convert_currency`(物理单位 vs 货币)、`get_stock_price` vs `web_search`、`set_reminder` vs `create_calendar_event`。

---

## 其它子命令

| 命令 | 干什么 |
|---|---|
| `evalgo gen` | 从文档/上下文用 LLM **自动生成测试集**,不用手写 |
| `evalgo redteam` | 离线生成越狱/注入/PII 套取类攻击题 |
| `evalgo diff old.json new.json` | 比对两次报告,**有回归就退出 1**(CI 闸门) |
| `evalgo metrics` | 列出全部指标名 |

## 省钱的三件套

- **`--cache`**:按 prompt 的 SHA-256 缓存裁判响应,存磁盘。**重跑不花钱**——实测第一次 116 秒 20 次调用,第二次 0.014 秒 0 次调用。
- **`--cost-in/--cost-out`**:报告末尾出 token 数和估算美元。
- **`--rps`**:令牌桶限流,防 429。

顺序有讲究:meter 装在 cache **内层**,所以缓存命中不计费,统计反映的是真实付费调用。

---

## 已知边界

- **测 agent 需要工具调用记录**。像 dsh 这种不输出机器可读轨迹的,只有 `task_completion` 有意义,`tool_correctness` 是空的——除非让它加载 `benchmcp`。
- **裁判会挂**。跑之前最好先探一下裁判模型可用性:实测遇到过网关把某个模型下线,导致整轮 30 题的 judge 全部报错判 0,分数看起来像"这个 agent 一塌糊涂"。发现异常低分时,先查 `report.json` 里 result 有没有 `error` 字段。
- **token 数是估算**(~4 字符/token),不是 provider 的分词器,用于相对比较和粗略成本,不是账单。
- 核心包是 **stdlib-only**(零第三方依赖),LLM 耦合全在 `llmjudge/`;`agentbench/` 是独立 module,它引入的 MCP SDK 不会污染核心。

---

## 实测记录(2026-08)

| 做了什么 | 结果 |
|---|---|
| 12 个模型(9 云 + 3 本地)× 50 题 | gemini 系扫榜;本地 `ornith`(5.6GB)打进云端前沿梯队 |
| superai(agent-go) vs superleo(harness-rs),锁同一模型 | 73% vs 53%——把框架能力和模型能力分离开 |
| 诊断反馈给 agent-go | 被采纳修复,commit 里直接引用 "On agentbench (30 tasks, paired…)" |

**跨模型最一致的发现**:所有 agent 的共同短板是**克制力**。没有对应能力时(打电话、订机票、转账),绝大多数宁可硬编一个工具假装完成,也不肯说"我做不到"。
