# Dandelion 研发中心（RDC）第 5 菜单「AI模型」· P1 增量 PRD

| 项 | 内容 |
| --- | --- |
| Language | 中文（与用户原始需求一致） |
| 技术栈 | 沿用现有：Go 1.24 + chi + pgx + PostgreSQL 16；React 18 + Vite + TS；`@seakoi/console-kit` 根入口。**不引入新技术栈、不引入第二个 HTTP 客户端** |
| Project Name | `rdc_ai_model_p1` |
| 文档类型 | **增量 PRD**（P0 已完整交付并通过 QA，本文只定义 P1 增量，不重复 P0 内容） |
| 上游文档 | `ai-model-prd.md`（P0 PRD）、`ai-model-design.md`（P0 设计）、`ai-interaction-prd.md`（下游消费方） |
| 强制规范 | `AGENTS.md`（优先级：当前代码与测试 > `api/openapi.yaml` > AGENTS.md > docs 设计稿） |
| 现状基线 | 见 §0.1（已核对代码，非文档推断） |

---

## 0. 本次拍板与现状基线

### 0.1 已交付能力（P0，本文不改动其对外行为）

**入口与权限**
- 菜单 `/rdc/models`（研发中心第 5 项），读点 `rdc.model.read` 守菜单 / 列表 / 详情，写点 `rdc.model.write` 守全部写操作与实测；菜单 `Actions` 挂写点，`AutoGrantBuiltin=false`。
- 仅 team 模式（`RequireTeamMode`，solo 侧栏不出现、直连 403 且不发请求）；按 `tenant_id` 隔离；超管 `scope.All` 必须带 `?tenantId=` 投影到单一公司，未指定返回 `ErrAITenantRequired`（列表空态 / 写操作 400）。

**配置实体** `team.rdc_ai_model`
`name` / `provider` / `protocol` / `base_url` / `model_id` / `api_key_ciphertext`+`api_key_nonce`+`api_key_mask`（AES-GCM，主密钥 env `RDC_MODEL_ENCRYPTION_KEY`，缺失即启动失败）/ `is_default` / `thinking_enabled` / `context_tokens`(1024–393216，默认 8192) / `extra_config` JSONB（`temperature` / `topP` / `reasoningEffort` / `timeoutMs`） / `capabilities`（文本生成 / 深度思考 / 视觉理解） / `enabled` / 时间戳。每公司至多一条 `is_default`（部分唯一索引 + 事务先清后置）。

**统一调用** `ModelChatService.Chat(ctx, ChatRequest{TenantID, Scene, Messages})`
- 与「测试连接」`Test` 共用唯一私有函数 `chatOnce`（`server/internal/service/rdc/model_chat.go:194`），是全仓**唯一** HTTP 出口：`POST {base_url}/chat/completions`，共享 `http.Client`（120s 硬超时）+ per-call `ctx` 超时（`extra_config.timeoutMs`，上限被硬超时夹紧）。
- 不注册前端可直调路由；响应内容经 `modelkey.Redact` 脱敏；6 类错误 `config_missing` / `auth_failed` / `parameter_error` / `rate_limited` / `network_error` / `response_error`。

**当前调度逻辑（本次要改的核心）**
`AIModelRepository.FindDefaultOrFirstEnabled`（`ai_model_repository.go:273`）：
```sql
WHERE tenant_id=? AND enabled=true ORDER BY is_default DESC, created_at ASC LIMIT 1
```
→ **完全不看** `capabilities` / `thinking_enabled` / `context_tokens` / `protocol`。`ChatRequest` 只有 `TenantID` / `Scene` / `Messages` 三个字段。

**当前协议与参数注入**
- `AIProtocols = []string{"openai"}`（`contract/ai_model.go:49`），只有一个值。
- `contract.AIModelRequestParams`：关思考 → `reasoning_effort=none`；开思考 → `extra_config.reasoningEffort`（默认 `high`）；`max_tokens = context_tokens`。
- `capabilities` 只在列表 / 表单展示，**不参与任何调度**。

### 0.2 本次用户拍板（三项，不可违背）

1. **多协议适配**：在现有 OpenAI 兼容之外新增 **Anthropic**、**OpenAI Responses** 两种协议；模型配置里选协议，调用时按协议构造不同请求体、解析不同响应；UI 上传协议选择、列表显示协议。
2. **能力标签参与调度**（用户原话："按模型思考模式强度选择和上下文窗口选择后参与"）——按此顺序分层：
   - 调用方可声明**所需能力**与**所需思考强度**；
   - **先**按「思考强度 + 上下文窗口」筛掉不满足的模型（上下文不足直接排除；思考强度按 `thinking_enabled` 与 `extra_config.reasoningEffort` 档位匹配），**再**叠加能力标签匹配；
   - 无匹配 → **明确错误**（不静默降级）；调用方不声明任何要求 → 行为与现在**完全一致**（向后兼容）。
3. **调用统计 + 软限流**：新增调用记录（公司 / 模型 / 协议 / prompt tokens / completion tokens / 耗时 / 成败 / 错误分类 / 场景 / 时间），按公司可查统计；限流做**阈值可配的软限流**（动作可选「告警」或「拒绝」，不做复杂配额窗口 / 计费），阈值按公司配置，**配置结构要留扩展口子**。

### 0.3 本次不做（明确排除，避免范围蔓延）

| 排除项 | 原因 |
| --- | --- |
| **场景级独立配置（原 R12）** | 场景本次只作为**统计维度**与请求标识落库，不参与选模；不为场景建配置表、不做场景→模型映射 |
| **Key 轮换提醒 / 到期管理（原 R14）** | 仍为手工编辑替换 |
| **计费 / 账单 / 配额周期重置** | 用户明确只做软限流，不做计费 |
| Agent 执行、模型训练 / 微调、模型自动故障转移 | 属 P2 及以后 |
| 前端直调 LLM 的任何路由 | P0 硬约束，P1 不变 |
| 多供应商模板库（scnet 之外的模板按钮） | 仅补 Anthropic / Responses 的协议选择，不做模板商店 |

---

## 1. 增量目标与范围

### 1.1 三条正交目标

1. **接入面变宽而调用面不变**：平台能配三类协议（OpenAI 兼容 / Anthropic / OpenAI Responses），但下游模块仍只调 `Chat` 一个函数、只传业务语义（要什么能力、要多长上下文、要多强思考），协议差异全部收在 `chatOnce` 内部。
2. **选模从"随机取第一个"变成"按需求匹配"**：能力 / 思考强度 / 上下文窗口成为**可选硬约束**；声明了就严格匹配、匹配不到明确报错，不声明就保持今天的行为——升级不改变任何存量调用方的结果。
3. **调用可计量、可限额**：每次出站调用落一条记录，公司维度可看用量与成败分布；按公司配阈值，超阈值可只告警或直接拒绝，为后续配额/计费留数据结构口子。

### 1.2 三块范围对照

| 能力块 | 本次做什么 | 本次不做什么 |
| --- | --- | --- |
| **A. 多协议** | 协议枚举加 `anthropic` / `openai_responses`；协议 → 端点后缀 / 认证头 / 请求体 / 响应字段的映射唯一；列表显示协议、表单可选协议；按协议解析 usage 与 content | 不做协议自动探测；不做自定义端点后缀；不支持同一模型多协议并存（一行一协议） |
| **B. 能力调度** | `ChatRequest` 增加可选需求字段；先筛思考强度 + 上下文窗口，再筛能力标签；无匹配返回新错误类别；不声明 = 原逻辑 | 不做场景级配置；不做权重打分 / 排序算法（只做硬筛 + 稳定排序）；不做模型自动降级重试 |
| **C. 统计 + 软限流** | 调用记录表 + 按公司统计接口 + 统计页；阈值配置（次数维度）+ 动作（告警 / 拒绝）；统计与限流在 `Chat` 内部完成 | 不做 token 计费；不做配额周期重置；不做按用户维度限流；不做实时告警通道（邮件/IM）；不做数据导出 |

### 1.3 关键兼容性口径

- **`ChatRequest` 新增字段必须全部是可零值 = "未声明"**：`Capabilities []string` 为空、`MinContextTokens` 为 0、`ReasoningEffort` 为空串 → 等价 P0 行为（`FindDefaultOrFirstEnabled` 同一 SQL / 同一排序）。
- **现有端到端断言需同步更新（提醒架构师 / 工程师）**：`server/internal/app/ai_model_qa_e2e_test.go:576` 用反射把 `ChatRequest` 锁成 `["TenantID","Scene","Messages"]` 三个字段，新增字段后该断言必然失败。这不属"放宽契约"，而是**有意的结构变更**：断言应改为"字段集合 = 白名单（新增字段均不得是 Key / base_url / 明文凭据）"，继续保证调用方无法注入敏感配置。

---

## 2. 用户故事

1. **作为公司管理员**，我希望在同一个模型表单里把协议选成 Anthropic 或 OpenAI Responses（而不仅是 OpenAI 兼容），并且列表能看出每条模型走的是哪种协议，以便公司用上非 OpenAI 形态的模型供应商，而不用改代码。
2. **作为公司管理员**，我希望给模型打上能力标签、设定思考强度与上下文窗口，并且这些配置**真的会决定调用命中哪个模型**，以便"视觉理解"类任务不会落到纯文本模型上、长上下文任务不会落到 8K 模型上。
3. **作为内部模块开发者（如 AI 交互层）**，我希望调用时只声明"我要视觉理解 + 至少 32K 上下文 + 高思考强度"，由模型层自动选出满足条件的模型，以便我不必知道公司配了哪些模型；**没有能满足的模型时请直接报错告诉我**，而不是偷偷用一个不完全满足的模型。
4. **作为存量调用方**，我希望不声明任何需求时调用行为与升级前完全一致，以便这次升级不需要我改一行代码、也不会突然换模型。
5. **作为公司管理员**，我希望按时间范围看到本公司每个模型 / 每种协议 / 每个场景的调用次数、token 用量、耗时与失败分布，以便判断哪个模型该换、哪个 Key 快失效、哪类错误最常出现。
6. **作为公司管理员**，我希望给公司设一个调用次数阈值，并选择"超了只告警"还是"超了直接拒绝"，以便在成本失控或 Key 被滥用时有个可预期的闸门，而不必等到账单出来才发现。
7. **作为超管**，我希望跨公司查看调用统计、代配某公司的限流阈值，以便发现异常用量并统一运维，同时不破坏公司间隔离（不选公司时看的是汇总，不是某家的明细）。

---

## 3. 需求池（P0 / P1 / P2）

> 本次 P1 增量内的 P0 = 必须交付；P1 = 本次可延后但不影响主体验收；P2 = 远期。**每项都写验收口径**，验收口径以"可观察行为"为准，不写"已实现"。

### 3.1 块 A · 多协议适配

| 编号 | 优先级 | 需求 | 验收口径 |
| --- | --- | --- | --- |
| A1 | **P0** | 协议枚举扩展为 `openai` / `anthropic` / `openai_responses`；`x-rdc-ai-protocols`、`AIProtocol` schema、`contract.AIProtocols`、前端 `AI_PROTOCOL_OPTIONS` / `AI_PROTOCOL_LABELS` 五处同序同集合 | 契约测试把 YAML 枚举与 `contract.AIProtocols` 逐项比对通过；前端下拉三项、列表 `aiProtocolLabel` 三项均正确显示中文（`OpenAI 兼容` / `Anthropic` / `OpenAI Responses`），历史脏值回显原值不显示空白 |
| A2 | **P0** | 协议 → 调用形态映射收在**唯一**入口：端点后缀、鉴权头、请求体、响应解析四处按协议分支 | 三种协议各有一条 mock 单测：断言请求 URL 后缀、鉴权头名与前缀、请求体关键字段、响应 content / reasoning / usage 提取位置均符合 §5.2 映射表；**`chatOnce` 仍是全仓唯一 LLM HTTP 出口**（不新增第二个 client、不新增第二个出口函数） |
| A3 | **P0** | Anthropic 协议：`x-api-key` 头 + `anthropic-version` 头；system 消息提为顶层 `system`；`max_tokens` 必填；思考参数走 `thinking` 结构 | mock 上游校验：不带 `Authorization`；有 `x-api-key`；有 `anthropic-version`；`system` 在顶层且 messages 内无 `system` role；`max_tokens` 存在且为正；响应 `content[]` 中 `text` / `thinking` 块分别落到 `Content` / `ReasoningContent` |
| A4 | **P0** | OpenAI Responses 协议：`{base_url}/responses`；`input` 而非 `messages`；`max_output_tokens`；响应 `output_text` / `output[]` 提取 | mock 上游校验路径以 `/responses` 结尾、请求体含 `input` 不含 `messages`、含 `max_output_tokens`；响应解析出文本与 usage（`input_tokens` / `output_tokens` → `PromptTokens` / `CompletionTokens`） |
| A5 | **P0** | 「测试连接」对三种协议都可用，且复用同一 `chatOnce` | 三条模型各点一次实测，成功分支 `category=ok` 且 `requestSummary` 体现协议名；失败分支仍是 6 类分类之一，摘要不含 Key / 密文 / 上游原文 |
| A6 | P1 | 协议变更保护：编辑已存在模型时改 `protocol`，`base_url` 与 `model_id` 是否需同步确认 | 见 §8 4；本次建议**不阻断**，仅表单提示"切换协议后请确认地址与模型标识仍匹配" |

### 3.2 块 B · 能力标签参与调度

| 编号 | 优先级 | 需求 | 验收口径 |
| --- | --- | --- | --- |
| B1 | **P0** | `ChatRequest` 新增三个**可选**需求字段：`Capabilities []string`、`MinContextTokens int`、`ReasoningEffort string`（名称由架构师定，语义见 §4.2） | 三个字段全零值时，`Chat` 走 P0 原路径（同一 SQL、同一排序），结果与升级前一致；单测用同一 fixture 断言"零值请求命中模型"与 P0 断言相同 |
| B2 | **P0** | **两层筛选，顺序不可交换**：① 先按思考强度 + 上下文窗口筛；② 再叠加能力标签 | 单测构造 4 个模型（A: 8K/无思考/文本；B: 32K/高思考/视觉；C: 128K/低思考/文本；D: 32K/高思考/文本），请求"视觉 + ≥16K + high"→ 只命中 B；若把能力筛选放前面仍会命中 B，故再加一组"能力满足但上下文不足"的用例，断言该模型被**排除**而不是胜出 |
| B3 | **P0** | 无匹配模型返回**新错误类别** `capability_mismatch`（语义：配置不满足请求），文案说明缺什么，不静默降级 | 请求"视觉 + ≥200K"而公司最高只有 128K → 返回 `capability_mismatch`，`message` 含"未找到满足条件（视觉理解 / 上下文 ≥ 204800）的启用模型"；**不得**回落到默认模型 |
| B4 | **P0** | 能力标签匹配语义 = **AND**（请求的每个能力，模型都必须具备）；能力标签集合本身来自 `contract.AICapabilities` 白名单 | 请求 `["视觉理解","深度思考"]`，模型只有 `["视觉理解"]` → 不匹配；模型 `["文本生成","视觉理解","深度思考"]` → 匹配。超集模型可命中（不受多余标签影响） |
| B5 | **P0** | 思考强度按**档位序**匹配：`none < low < high < max`，请求档位 = 模型**可提供**档位的最低要求 | 模型 `thinking_enabled=true` + `extra_config.reasoningEffort=high` → 可提供 `high`；请求 `low` 命中、请求 `max` 不命中。模型 `thinking_enabled=false` → 只能提供 `none`，请求 `low/high/max` 一律不命中 |
| B6 | **P0** | 上下文窗口匹配：模型 `context_tokens` ≥ 请求 `MinContextTokens` 才算满足 | 模型 8192、请求 16384 → 排除；请求 8192 → 命中（**含等于**） |
| B7 | **P0** | 默认模型**不豁免**硬约束：默认模型不满足声明条件时不得被选中 | 把不满足条件的模型设为默认，再用带条件的请求调用 → 命中其他满足条件的模型（或报 `capability_mismatch`），默认标记不构成越过筛选的理由 |
| B8 | P1 | 多命中时的**稳定排序**：满足条件的模型按现有 `is_default DESC, created_at ASC` 取第一条 | 单测断言：两个满足条件的模型中默认者胜出；都非默认则创建早的胜出（可复现、不随机） |
| B9 | P1 | 需求字段的校验：`Capabilities` 中出现白名单外的值、`ReasoningEffort` 非四档、`MinContextTokens` 为负数 → 视为**调用方 bug**，返回 `parameter_error` | 单测三条非法输入均返回 `parameter_error`，不落库、不发起 HTTP |

### 3.3 块 C · 调用统计 + 软限流

| 编号 | 优先级 | 需求 | 验收口径 |
| --- | --- | --- | --- |
| C1 | **P0** | 每次**出站调用**落一条调用记录（字段见 §4.3），成功与失败都写 | 成功调用后库里有 1 行，`success=true`、token 与耗时非零；失败调用后 1 行 `success=false` + 正确 `error_category`；**本地配置类失败**（无模型 / 无匹配 / 限流拒绝）是否落库见 §5.4 口径表，按表实现 |
| C2 | **P0** | 写记录**不得**影响调用结果：记录失败只记日志，不把成功翻成失败 | 单测注入写库失败的 fake repo，`Chat` 返回成功结果；日志有告警 |
| C3 | **P0** | 记录中**不含** Key、密文、完整请求体 / 响应体；`scene` 与 `model_name` 只存非敏感标识 | 记录表全字段扫描断言无 Key 明文与密文；`request/response` 无原文列；脱敏与 P0 同一函数（`modelkey.Redact`） |
| C4 | **P0** | 按公司统计查询接口（读点 `rdc.model.read`）：按时间范围 + 可选分组维度（模型 / 协议 / 场景 / 日）返回次数、成功数、失败数、失败分类分布、prompt / completion tokens 合计、平均耗时 | 造 10 条记录（含 3 条失败、2 条 token 缺失）→ 合计与分组值逐项与手算一致；token 缺失按 §5.3 口径处理 |
| C5 | **P0** | 统计页（`/rdc/models/usage` 或列表页内 tab，见 §7）展示 C4 数据，三态齐全、时间范围进 URL | 空数据为空态卡片不挂分页条；错误为可重试错误态；时间范围切换刷新且刷新后保持 |
| C6 | **P0** | 限流阈值按公司配置：`enabled` / `max_calls`（次数）/ `window`（统计窗口）/ `action`(`warn`\|`reject`)；配置持久化且结构可扩展 | 保存后重新读取值一致；`action=reject` 且用满阈值 → 下一次 `Chat` 返回 `rate_limited`（软限流专用文案，区别于上游 429）；`action=warn` 且用满 → 调用**照常发出**，结果与告警标记见 §5.4 |
| C7 | **P0** | 限流判定**先于**出站调用，且不消耗上游配额 | `reject` 命中时断言 mock 上游**未收到任何请求**；调用记录里有被拒的失败行（`error_category=rate_limited`） |
| C8 | **P0** | 阈值配置的读写权限与审计：读点看、写点改；写操作写审计（不记敏感值，只记动作与目标公司） | 无 `rdc.model.write` 的账号调写接口 403；审计 detail 含目标 `tenant_id` 与新阈值，不含 Key |
| C9 | P1 | 统计与限流对「实测」也生效（实测是真实出站调用，应计入） | 点一次实测后统计计数 +1；限流 `reject` 状态下实测被拒 |
| C10 | P1 | 统计保留策略：超过保留期的记录清理（保留期见 §8 5） | 保留期配置生效后，超期记录不再出现在统计中 |

### 3.4 横切

| 编号 | 优先级 | 需求 | 验收口径 |
| --- | --- | --- | --- |
| X1 | **P0** | 契约链路完整：`api/openapi.yaml` → `contract` → `APIRoutes` → `make gen:api` → 前端 api 文件 → 路由 → 测试 | `make gen:api` 后 `git diff --exit-code -- web/src/api/schema.d.ts` 无残留；`make test` 与 `npm --prefix web run build` 通过 |
| X2 | **P0** | 全链路中文四要素注释（职责 / 输入 / 输出 / 边界）；无 TODO、无假数据、无假按钮、无占位菜单 | 人工检查；新增文件无占位实现 |
| X3 | **P0** | Key 安全不退化：任何新接口（统计 / 限流配置）响应都**不含** Key 或其密文；日志不含 Key |
| X4 | **P0** | 分层不破：限流与统计的 SQL 在 repository、规则在 service、HTTP 语义在 handler；不新建第二套 HTTP 客户端 / 第二套配置系统 / 第二份权限目录 | 代码审查 + 现有架构测试 |

### 3.5 P2（远期，本次明确不做，不建路由不建表不放假入口）

| 编号 | 需求 | 触发前提 |
| --- | --- | --- |
| P2-1 | 场景级独立配置（不同业务场景配不同模型 / 参数） | 需先有真实的场景枚举与调用方，本次场景只落统计 |
| P2-2 | Key 轮换提醒 / 到期管理 | 需要 Key 签发日期与到期元数据 |
| P2-3 | 计费 / 配额周期重置 / 按 token 限额 | 需先有稳定的 token 上报率（当前部分上游不报 usage） |
| P2-4 | 多规则限流（按模型 / 按用户 / 多窗口叠加） | 需先把单规则的窗口与阈值跑准 |
| P2-5 | 模型健康探测与自动故障转移 | 需要多模型同能力前提，本次刚建立能力标签 |
| P2-6 | 告警通道（邮件 / IM / Webhook） | 需先确认平台的告警基础设施 |
| P2-7 | 统计导出与趋势图 | 需先确认图表依赖的引入方式（避免为统计新增前端库） |

---

## 4. 数据模型增量（PRD 层，非 DDL）

> 只定义实体、字段语义与约束；表名 / 列类型 / 索引 / 幂等 DDL 由架构师在 `schema.sql` 落实。沿用 `team` 分区、`rdc_` 前缀、`snake_case`、UUID 主键、`created_at` / `updated_at`。

### 4.1 协议枚举扩展（改既有实体，不加表）

`team.rdc_ai_model.protocol` 取值从 `{openai}` 扩为 `{openai, anthropic, openai_responses}`。

| 注意点 | 口径 |
| --- | --- |
| 存量数据 | 全部是 `openai`，**无需迁移**；列不加 CHECK（枚举唯一来源是 `api/openapi.yaml`，与项目惯例一致） |
| 默认值 | 新建默认仍为 `openai` |
| 一对一关系 | 一行模型 = 一种协议；不做"同一 Key / 地址多协议并存" |
| `provider` 与 `protocol` 的关系 | **正交**：`provider` 是接入源标识（当前只有 `scnet`），`protocol` 是报文形态。是否要为 Anthropic / Responses 扩 `provider` 见 §8 3 |

### 4.2 调度请求参数（`ChatRequest` 新增）

| 字段（语义名） | 类型 | 零值 = 未声明 | 语义 |
| --- | --- | --- | --- |
| 所需能力 | 字符串数组（白名单内） | 空 | **AND** 语义：模型 `capabilities` 必须包含其中每一项 |
| 所需最小上下文 | 整数（tokens） | 0 | 模型 `context_tokens` ≥ 该值才满足 |
| 所需思考强度 | 字符串（`none`/`low`/`high`/`max`） | 空串 | 模型**可提供**档位 ≥ 请求档位才满足（档位序比较，见 §5.1） |

**不变的部分（安全边界必须锁定）**：不得新增 Key / base_url / 明文凭据 / 温度 / topP 这类**绕过公司配置**的字段——P0 的原则是"参数由服务端配置决定，调用方不逐次覆盖"。三档需求是"选择条件"，不是"调用参数"。

### 4.3 调用记录（新表，建议 `team.rdc_ai_model_call`）

| 字段（语义） | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | UUID |
| `tenant_id` | 是 | 公司隔离，统计与限流的第一维度；从 `ChatRequest.TenantID` 取 |
| `model_id`（外键语义） | 是 | 指向 `rdc_ai_model.id`；**不设物理外键级联删除**（记录是事实，删模型不应抹掉历史；建议 `ON DELETE SET NULL` 或存冗余快照，见下） |
| `model_name` / `model_identifier`（快照） | 是 | 冗余存当次使用的显示名与模型标识，保证模型改名 / 删除后历史仍可读（沿用审计 `actor_name` 冗余惯例） |
| `protocol` | 是 | 当次使用的协议（快照，防止后续改协议导致历史统计错位） |
| `scene` | 否 | 调用方传入的场景标识，空串表示未声明 |
| `success` | 是 | 布尔 |
| `error_category` | 否 | 成功为空；失败为 6 类之一 + 新增 `capability_mismatch` + 软限流拒绝（见 §5.4） |
| `status_code` | 否 | 上游 HTTP 状态码，本地失败为 0 |
| `prompt_tokens` / `completion_tokens` | 否 | 上游返回才有；缺失记 0（**不冒充真实值**），同时置 `tokens_reported=false` 供统计区分 |
| `tokens_reported` | 是 | 上游是否上报了 usage；默认 `false`，解析到非零 usage 时置 `true` |
| `latency_ms` | 是 | 端到端耗时（从进入 `Chat` 到拿到结果，不含写记录本身的耗时）；本地拒绝为 0 |
| `used_upstream` | 是 | 是否真正发出了上游请求；用于区分「上游 429」与「本地软限流拒绝」（同属 `rate_limited`） |
| `used_default` / `used_fallback` | 否 | 保留 P0 语义，便于排查"为什么用了这个模型" |
| `requested_capabilities` / `requested_min_context` / `requested_effort` | 否 | 记录当次声明的需求，便于回答"哪些请求经常匹配不到" |
| `created_at` | 是 | 调用发生时间（UTC） |

**索引建议（给架构师）**：`(tenant_id, created_at DESC)` 支撑统计与限流窗口查询；`(tenant_id, model_id, created_at DESC)` 支撑按模型分组。限流窗口查询只需要 `count(*)`，不建物化聚合表。

### 4.4 限流阈值配置存放（建议 + 理由）

**建议：新建独立表 `team.rdc_ai_model_quota`（每公司一行），不复用 `system_settings`。**

| 方案 | 优点 | 缺点 | 结论 |
| --- | --- | --- | --- |
| 扩展 `system_settings` 加列 | 少一张表 | `system_settings` 已经是"Webhook + 外部 API Key + Git 仓库路径"的集合体，再塞限流会变成第三个关注点共用一行，读写互相牵连（改 Webhook 的 UPDATE 要小心不覆盖限流字段）；且 `system_settings` 是 `public` 全局表，而本模块是 `team` 分区 | ❌ |
| 塞进 `system_settings` 的 JSON 列 | 灵活 | 语义更糊，且同样有分区错位问题 | ❌ |
| **新建 `team.rdc_ai_model_quota`** | 关注点单一；跟随 `team` 分区与 `rdc_` 前缀；每公司一行、可用唯一索引保证；将来扩字段只加列 | 多一张表 | ✅ **推荐** |

**建议字段**：`tenant_id`（唯一）/ `enabled` / `max_calls`（窗口内允许次数）/ `window_minutes`（统计窗口，分钟）/ `action`（`warn`\|`reject`）/ `extra_config` JSONB（预留：将来加 token 维度、按模型维度、多窗口、告警通道等）/ `created_at` / `updated_at`。

**留口子的具体做法**（呼应用户"后续还要能扩展"）：
- 阈值判定只认 `(metric, window, limit, action)` 这个四元组；**当前 metric 只有 `calls` 一个值**，但规则层按"可枚举的 metric"写，将来加 `tokens` 不改调用方。
- `extra_config` 读取时**未知键忽略、缺失键补默认**（沿用 `extra_config` 的既有做法），因此新增配置项不需要迁移历史行。
- **不做**多规则合集（一个公司多条阈值）——本次每公司一条规则，避免一上来就造规则引擎。

### 4.5 是否需要新增权限点（建议）

| 权限点 | 建议 | 理由 |
| --- | --- | --- |
| 看统计 `rdc.model.read` | **复用，不加新点** | 统计是模型配置的只读视图，与"看模型"同一关注点；单独加点会让"能看模型不能看用量"这种配置失去意义 |
| 改限流阈值 `rdc.model.write` | **复用，不加新点** | 与"配模型 / Key / 实测"同属高敏感写操作，语义一致 |
| 跨公司看统计（超管） | **不设权限点，按身份** | 与 P0 的"超管 `?tenantId=` 代配"完全同构：超管身份 + 必须指定目标公司；不引入新的身份门槛 |

---

## 5. 产品规则

### 5.1 调度优先级完整规则（本次最关键的一节）

**总体顺序**：`参数校验` → `软限流判定` → `候选集筛选（思考强度 → 上下文 → 能力）` → `稳定排序取首` → `出站调用` → `写记录`。

**Step 1 · 需求归一**

| 输入 | 归一结果 |
| --- | --- |
| 三项全零值 | `selection = nil`（不筛，走 P0 原逻辑） |
| 任一非零 | `selection` 生效；**只声明一项时其余项不构成约束**（例如只声明能力，则不看上下文与思考强度） |

**Step 2 · 候选集（SQL 层）**
- 基础条件不变：`tenant_id = ?` AND `enabled = true`。
- 有 `selection` 时在同一 SQL 追加：
  - 上下文：`context_tokens >= ?`（**含等于**）
  - 思考强度：见 Step 3 的档位换算，落成 SQL 条件或取回后在 service 过滤（由架构师定，但**筛选先后顺序在语义上不可交换**）
  - 能力：模型 `capabilities` 必须包含请求的**每一项**（AND）
- 若三个约束都加上后**候选集为空** → `capability_mismatch`，**不回落默认**。

**Step 3 · 思考强度档位匹配**

| 模型配置 | 模型**可提供**的最高档位 |
| --- | --- |
| `thinking_enabled = false` | `none` |
| `thinking_enabled = true` 且 `extra_config.reasoningEffort = none` | `none`（配置矛盾：开了思考却把档位设为 none → 以 `extra_config` 为准，视为 none，并在表单给出提示，见 §8 6） |
| `thinking_enabled = true` 且 `extra_config.reasoningEffort = low\|high\|max` | 对应档位 |

匹配规则：`档位序(模型可提供) >= 档位序(请求)`，序为 `none=0 < low=1 < high=2 < max=3`。

**为什么是"≥"而不是"=="**：请求 `low` 表示"至少要能思考一点"，一个能 `max` 的模型当然满足；若用"=="，管理员把某模型档位调高就会让原本满足的低档请求突然匹配不到，属于脆弱的隐式耦合。
**"必须精确 high"这种需求本次不支持**（需要新增 `exactEffort` 语义），列为 §8 7。

**Step 4 · 多命中排序**（应用在满足全部硬约束的集合上）

| 优先级 | 排序键 | 说明 |
| --- | --- | --- |
| 1 | `is_default DESC` | 默认模型在**满足条件者之中**优先。默认模型不满足条件时**不参与**（B7） |
| 2 | `created_at ASC` | 与 P0 完全一致，保证同条件下可复现 |

> **为什么保留"默认优先"**：默认模型是管理员显式表达的偏好，在有多个都合规的模型时尊重它，比"每次都取最老那条"更符合直觉；而它只在硬约束通过后才起作用，不会破坏能力/上下文/思考的正确性。此点仍需用户确认，见 §8 Q1。

**Step 5 · 无匹配的错误语义**

| 场景 | 错误类别 | 文案口径（中文、可上屏、不含敏感信息） |
| --- | --- | --- |
| 公司无启用模型 | `config_missing`（沿用 P0） | "该公司尚未配置可用模型，请前往 AI 模型页配置" |
| 有启用模型但**无一条满足声明条件** | `capability_mismatch`（新增） | "未找到满足条件的启用模型（能力：视觉理解；上下文 ≥ 16384；思考强度 ≥ high）" —— 只列**声明了**的维度 |
| 声明了能力但公司内**没有任何模型配过该标签** | `capability_mismatch` | 同上，另加一句建议："请为模型补充能力标签或调整需求" |

**禁止行为**（验收要能反证）：
- ❌ 悄悄用默认模型代替（静默降级）→ 会导致"以为是视觉模型，其实是纯文本"的隐性 bug。
- ❌ 放宽上下文约束取最接近的模型 → 依赖方会拿到被截断的上下文。
- ❌ 把 `capability_mismatch` 报成 `config_missing` → 管理员会去配模型，而不是去看能力标签。

### 5.2 三种协议的差异点（请求字段映射）

> 下表是**产品口径的字段映射**，不写具体代码；架构师按此在 `chatOnce` 内做唯一的协议分支。`{base_url}` 已由 P0 校验为无 query 的 http(s) 绝对地址、无尾斜杠。

**端点与认证**

| 协议 | 端点后缀 | 认证头 | 额外必需头 | 响应结构 |
| --- | --- | --- | --- | --- |
| `openai`（P0，不变） | `/chat/completions` | `Authorization: Bearer <key>` | — | `choices[0].message.content` + `reasoning_content`，`usage.prompt_tokens/completion_tokens` |
| `anthropic` | `/messages` | `x-api-key: <key>` | `anthropic-version: 2023-06-01`（固定值，由后端写死，不暴露给用户配置） | `content[]`（块数组：`type=text` → 正文；`type=thinking` → 思考内容），`usage.input_tokens/output_tokens` |
| `openai_responses` | `/responses` | `Authorization: Bearer <key>` | — | `output_text`（便捷字段）或 `output[]` 中 `type=message` 的 `content[].text`；`usage.input_tokens/output_tokens` |

**请求体字段映射**

| 语义 | `openai` | `anthropic` | `openai_responses` |
| --- | --- | --- | --- |
| 模型标识 | `model` | `model` | `model` |
| 对话内容 | `messages[]`（`role` = system/user/assistant） | `messages[]`（**只有 user/assistant**）+ 顶层 `system` 字符串 | `input`（字符串或消息数组，由架构师按上游稳定性选一种并锁定） |
| 输出上限 | `max_tokens` | `max_tokens`（**必填**） | `max_output_tokens` |
| 温度 | `temperature` | `temperature`（0–1 区间，与 P0 的 0–2 不同，见下） | `temperature` |
| 核采样 | `top_p` | `top_p` | `top_p` |
| 思考开关 / 强度 | `reasoning_effort`（`none`/`low`/`high`/`max`） | `thinking: {type: "enabled", budget_tokens: N}`；关闭时不传该字段 | `reasoning: {effort: "low"\|"medium"\|"high"}`；关闭时不传 |

**思考参数的三协议差异（必须在实现里显式折算，不能直接透传）**

| 平台统一档位 | `openai` 传值 | `anthropic` 折算 | `openai_responses` 折算 |
| --- | --- | --- | --- |
| `none` | `reasoning_effort: none` | 不传 `thinking` 字段 | 不传 `reasoning` 字段 |
| `low` | `low` | `budget_tokens` = 1024 | `effort: low` |
| `high` | `high` | `budget_tokens` = 4096 | `effort: medium` |
| `max` | `max` | `budget_tokens` = 8192 | `effort: high` |

> **为什么用折算表而不是直接映射**：三个协议对"思考强度"的表达不是一个维度——OpenAI 是档位字符串，Anthropic 是 token 预算，Responses 是三档枚举。平台内部的四档是**统一词汇**，落到各协议必须翻译；因此 `AIModelRequestParams` 的语义保持不变（四档），翻译发生在协议分支内。折算数值**需实测校准**，见 §8 8。
> **`temperature` 区间差异**：P0 允许 0–2（OpenAI 口径）；Anthropic 是 0–1。产品规则：**校验按协议走**——保存 `anthropic` 协议模型时 `temperature > 1` 直接 400 提示区间，而不是运行时被上游拒。这是"配置期失败优于调用期失败"的原则。

**响应字段提取（写进 `ChatResult` 的唯一映射）**

| 输出 | `openai` | `anthropic` | `openai_responses` |
| --- | --- | --- | --- |
| 正文 `Content` | `choices[0].message.content` | `content[]` 中 `type=text` 的 `text` 顺序拼接 | `output_text`；缺失时取 `output[]` 的 message 文本块拼接 |
| 思考 `ReasoningContent` | `choices[0].message.reasoning_content` | `content[]` 中 `type=thinking` 的 `thinking` 拼接 | `output[]` 中 `type=reasoning` 的摘要文本（若无则空） |
| prompt tokens | `usage.prompt_tokens` | `usage.input_tokens` | `usage.input_tokens` |
| completion tokens | `usage.completion_tokens` | `usage.output_tokens` | `usage.output_tokens` |
| 空响应判定 | choices 为空 / content 与 reasoning 皆空 → `response_error` | `content[]` 为空或全是未知 type → `response_error` | `output_text` 与 `output[]` 皆空 → `response_error` |

**错误分类不因协议变化**：6 类分类仍按 HTTP 状态码判定（同一 `classifyUpstreamStatus`）；协议差异只影响"请求怎么发、响应怎么读"。新增两个语义不同的类别见 §5.1（`capability_mismatch`）与 §5.4（软限流拒绝）。

### 5.3 统计口径

| 问题 | 口径 |
| --- | --- |
| **什么算一次调用** | 每一次**真正发出上游 HTTP 请求**的调用算 1 次（含「测试连接」实测）。被软限流拒绝、无模型可用、无匹配模型这些**未出站**的调用，按 §5.4 口径处理（建议记录但可单独筛除，见下） |
| **失败是否计入** | **计入**。统计同时给"总次数 / 成功数 / 失败数"，失败再按 `error_category` 分组。只看成功次数无法发现 Key 失效或参数错误 |
| **token 缺失怎么办** | 上游未返回 usage 时 `prompt_tokens`/`completion_tokens` 记 0，并置 `tokens_reported = false`。统计里分别给"token 合计（仅统计已上报的调用）"与"未上报 token 的调用数"，**不用 0 冒充真实值**，避免平均值被系统性拉低 |
| **平均耗时怎么算** | 只对**成功且已出站**的调用求平均（失败的耗时反映的是超时 / 报错路径，混在一起会误导容量判断）；失败耗时单独给"平均失败耗时"，用于识别超时型故障 |
| **时间口径** | 全部按 `created_at`（UTC 存储），页面按用户时区展示；时间范围筛选使用**半开区间** `[start, end)`，避免边界那一秒被算两次或漏算 |
| **分组维度** | 模型 / 协议 / 场景 / 日，四者可自由组合（至少支持两两组合：模型 × 日、协议 × 场景） |
| **超管跨公司** | 未指定公司 → 只返回**汇总**（各公司明细不出现在非目标公司的视图里）；指定 `?tenantId=` → 按该公司维度返回，与 P0 代配一致 |

**统计指标清单（页面与接口的最小集合）**：总调用次数、成功次数、失败次数、失败分类 Top（6+2 类）、prompt tokens 合计、completion tokens 合计、平均成功耗时、P95 成功耗时（若实现成本高可延后到 P1）。

### 5.4 软限流规则

**判定位置**：`Chat` 内、**候选集筛选之前还是之后**？——**在筛选之前**。理由：限流保护的是公司成本与上游 Key，与"选哪个模型"无关；先判定可以避免无谓的 SQL 筛选，也让"超限时错误是 `rate_limited`"这一语义稳定（不会因为同时不满足能力条件而报成 `capability_mismatch`，掩盖真正的限流问题）。

**判定顺序（唯一顺序，不可交换）**

1. 读公司阈值配置。`enabled=false` 或整行不存在 → **不限流**，直接进入选模。
2. 取窗口内已发生次数：`count(*)` where `tenant_id=?` and `created_at >= now() - window`，**只统计已出站的调用**（不含被拒绝的记录，否则拒绝会产生"自激"计数、永远无法恢复）。
3. `已用次数 >= max_calls` → 超限：
   - `action = warn` → **照常调用**，结果中带 `quota_state=exceeded` 标记，调用记录额外可查（页面在统计区显示"已超阈值，当前为告警模式"）。
   - `action = reject` → **不出站**，返回 `rate_limited`，文案"该公司本窗口调用次数已达上限（max_calls 次 / window 分钟），请联系管理员调整"，与上游 429 的"供应商繁忙或限流"文案**明确区分**（否则管理员会去查供应商而不是查自己的阈值）。
4. 未超限 → 正常调用。**不做**令牌桶 / 滑动窗口平滑 / 预留配额。

**阈值单位与默认值**

| 配置项 | 单位 | 建议默认 | 说明 |
| --- | --- | --- | --- |
| `enabled` | 布尔 | `false` | 默认关闭，避免升级后突然开始拒绝调用 |
| `max_calls` | 次 | 1000 | 见 §8 5 待确认 |
| `window_minutes` | 分钟 | 60 | 窗口长度；判定方式为「`now()` 往前推一个窗口取 count」，见下方窗口口径 |
| `action` | `warn` / `reject` | `warn` | 默认只告警，与"软"限流一致 |

**窗口口径**：**固定长度滑动窗口**（每次判定都以 `now()` 往前推一个窗口取 count），不是对齐到整点的时间桶。理由：整点对齐会让"59 分打满、00 分重置"产生突发双倍流量；滑动窗口只需要一条索引 range scan，成本可接受。

**超限错误码与类别**

| 项 | 值 |
| --- | --- |
| 业务错误类别 | `rate_limited`（复用既有类别，语义 = "被告知要等"） |
| 与上游 429 的区分 | 调用记录里 `status_code = 0` 且 `used_upstream = false`；上游 429 则有真实 `status_code = 429`。两者同类别但可区分，页面文案不同 |
| 是否落库 | **落库**（`success=false`，`model_id` 为空、`latency_ms=0`），便于统计"被拦了多少次"。统计接口默认**不含**这些行，需显式开启"含本地拒绝"筛选 |

**什么算"未出站"的本地失败（统一口径）**

| 情况 | 是否写调用记录 | 计入限流窗口 | `error_category` |
| --- | --- | --- | --- |
| 无启用模型 `config_missing` | 是 | 否 | `config_missing` |
| 无匹配模型 `capability_mismatch` | 是 | 否 | `capability_mismatch` |
| 软限流拒绝 | 是 | **否**（否则自激） | `rate_limited` |
| Key 缺失 / 解密失败 | 是 | 否 | `config_missing` |
| 请求参数非法（B9） | **否**（调用方 bug，不是调用事实） | 否 | — |
| 已出站成功 / 失败 | 是 | **是** | 空 / 6 类之一 |

**限流的可观测性要求**：`action=warn` 时用户必须**看得见**已超阈值（统计页顶部提示条 + 调用记录里可筛出 `quota_state=exceeded` 的行）；否则"告警"退化成无人知晓的日志，等于没做。

### 5.5 关键取舍与理由（给架构师的"为什么"）

| 决策点 | 选择 | 被否的方案与原因 |
| --- | --- | --- |
| 需求声明放在 `ChatRequest` 而不是新建 `ChatOptions` 结构 | 平铺三字段 | 嵌套结构会让"零值 = 未声明"变得含糊（`Options != nil` 但字段全零怎么办），平铺零值语义最清晰 |
| 能力多标签 AND | AND | OR 会让"要视觉+思考"的请求落到只有视觉的模型上，静默能力缺失比报错更难查 |
| 思考强度用档位序 ≥ | ≥ | 精确相等会让管理员调高模型档位后低档请求突然失配（隐式耦合） |
| 无匹配直接报错 | 报错 | 回落默认会产出"看起来成功但能力不对"的结果，违背用户"明确错误"的拍板 |
| 限流先于选模 | 先限流 | 先选模会让超限错误被能力错误掩盖；且限流是成本保护，应最先生效 |
| 限流窗口用滑动而非整点桶 | 滑动 | 整点桶会造成边界突发，且需要额外的时间取整逻辑 |
| 阈值配置独立成表 | 独立表 | 塞 `system_settings` 会让三个关注点共用一行 UPDATE，且有分区错位（`public` vs `team`） |
| 调用记录不删（默认保留待定） | 见 §8 5 | 物理删会让历史统计不可复现；保留期太短无法看月度趋势 |

---

## 6. 权限与隔离

### 6.1 权限点（建议，见 §4.5）

- **看统计 / 看限流配置**：复用 `rdc.model.read`（守菜单 + 统计 tab + 只读展示阈值）。
- **改限流阈值**：复用 `rdc.model.write`（后端 `RequirePermission` + 前端 `can(me,'rdc.model.write')` 隐藏入口）。
- **不新增权限点**。若用户希望"统计只给管理员看、模型配置给运维看"，才需要拆点——列为 §8 9。

### 6.2 tenant 隔离（不变量）

- 调用记录与阈值配置**都必须带 `tenant_id`**，且一切读写先过 `ScopeFilter`（沿用 P0 的 `aiModelFilter` 同款"空范围失败关闭"：`1=0` 兜底）；即便误传 `scope.All` 也**不得**跨公司返回明细。
- 跨公司资源一律按 **404**（不泄漏存在性）。
- 归属字段从 `scope` 取，**请求体不接受 `tenantId`**（P0 既有白名单惯例，本次不破）。

### 6.3 超管跨公司

| 场景 | 规则 |
| --- | --- |
| 看统计 | 未带 `?tenantId=` → 只给**平台汇总**（次数 / token / 失败分布），不给任一公司的明细行；带上 → 按该公司返回 |
| 配阈值 | **必须**带 `?tenantId=`，未带返回 `ErrAITenantRequired`（与 P0 写操作一致，400） |
| 隔离底线 | 超管能跨公司是"代配"能力，不是"混合查看"能力；任何响应都不得把两家公司的数据混在一个明细列表里 |
| 审计 | 代配写操作 detail 记 `target_tenant_id` 与操作者，明确"超管代某公司配置阈值" |

### 6.4 审计（增量）

| 动作 | action | detail 内容 |
| --- | --- | --- |
| 改限流阈值 | `rdc.ai_model.quota_updated` | `target_tenant_id`、`enabled` / `max_calls` / `window_minutes` / `action` 的新值（**非敏感**，可记） |
| 统计查询 | **不审计** | 读操作不写审计（沿用现有惯例：只有写操作进审计） |

审计写入失败**只记日志、不回滚业务**（沿用惯例）。

---

## 7. 前端影响

> 全部组件来自 `@seakoi/console-kit` 根入口；筛选 / 分段用 `Controls.Segmented`；页面不拼接口 URL；写入口按 `can(me,'rdc.model.write')` 隐藏；`me` 未加载完按无权限处理。

### 7.1 模型列表页（`/rdc/models`，改造 `ModelListPage.tsx`）

| 改动 | 内容 |
| --- | --- |
| 协议列 | 已存在，本次确保三协议标签齐全（`OpenAI 兼容` / `Anthropic` / `OpenAI Responses`），脏值回显原值 |
| 新增筛选 | 按协议筛（`Controls.Segmented` 或 Select，进 URL，与现有 `enabled` 筛选共存） |
| 新增入口 | 「调用统计」按钮 / tab 跳转统计视图；「限流设置」按钮（按写点隐藏） |
| 能力标签展示 | 保持只读徽标；**新增说明文案**："能力标签参与自动选模"（避免用户以为还是纯装饰） |
| 不改动 | 服务端分页、页码进 URL、三态、空态卡片不挂分页条、详情抽屉、实测面板 |

### 7.2 模型表单页（`ModelFormPage.tsx`）

| 改动 | 内容 |
| --- | --- |
| 协议选择 | 从 1 项扩到 3 项；选中后**动态提示**对应端点后缀与认证方式（只读提示，不可编辑） |
| 上下文 / 思考强度 | 语义说明补一句："上下文窗口与思考强度会参与自动选模，请按模型真实能力填写" |
| 协议相关校验 | `anthropic` 协议下 `temperature` 上限 1；`thinking_enabled=true` 且档位为 `none` 时给出"配置矛盾"提示（不阻断，见 §8 6） |
| 不改动 | Key 留空 = 不更新、掩码占位、不回填掩码作为值、不写 Key 到页面以外 |

### 7.3 统计视图（新增，页面或 tab）

| 项 | 内容 |
| --- | --- |
| 位置 | 建议 `/rdc/models/usage`（独立路由，可分享 URL）或列表页 tab；由架构师按现有路由习惯定，PRD 只要求"URL 可直达、范围进 URL" |
| 时间范围 | 预设（近 24 小时 / 近 7 天 / 近 30 天）+ 自定义；默认近 7 天；放 URL |
| 摘要卡 | 总调用 / 成功 / 失败 / prompt tokens / completion tokens / 平均成功耗时 |
| 分布 | 失败分类分布（含 `capability_mismatch`）、按模型 Top N、按协议、按场景 |
| 超阈值提示 | `action=warn` 且已超 → 顶部琥珀色提示条"本窗口调用次数已达阈值，当前为告警模式"（用琥珀色，不用主色，符合高危提示规范） |
| 三态 | loading 骨架 / 可重试错误态 / 空态卡片（不挂分页条） |
| 不做 | 图表库引入（用现有表格 + 简单条状 CSS 表达即可，避免为统计新增依赖）；导出 |

### 7.4 限流设置（新增，弹窗或表单页）

| 项 | 内容 |
| --- | --- |
| 入口 | 列表页工具栏按钮（`rdc.model.write` 才显示），弹窗表单 |
| 字段 | 启用开关 / 最大次数 / 窗口（分钟）/ 动作（`Segmented`：告警 / 拒绝） |
| 说明文案 | 必须写清影响面："`拒绝`模式下超出阈值后所有模型调用将直接失败（不影响已保存配置）"——符合"危险操作写清影响面"规范 |
| 超管 | 带 `?tenantId=` 代配；未选公司时按钮不渲染（与列表页同口径） |

---

## 8. 待确认问题（请 lead 转用户决策）

> 无硬阻塞项：本文所有条目都给了「建议方案 + 默认值」，架构师可按建议先行推进，用户后续确认若与建议不同再调整。**建议至少确认 Q1、Q2、Q5（涉及默认值与语义口径）再进入实现。**

1. **Q1｜默认模型在调度中的优先级**：产品建议"满足全部硬约束之后，`is_default` 优先，其次 `created_at ASC`"（§5.1 Step 4）。另一种选择是"完全忽略默认标记，只按创建时间"，但那会让管理员的「设为默认」在新调度路径上失去意义。请确认是否同意建议方案。
2. **Q2｜能力多标签是 AND 还是 OR**：本文按 **AND** 落地（请求的每个能力模型都必须具备，§3.2 B4）。另一选择是 OR（具备任一即可），但会让"要视觉 + 思考"的请求落到只有视觉的模型上，形成静默能力缺失。若用户希望"至少要有一个"语义，需在 `ChatRequest` 增加匹配模式字段（本次不实现）。
3. **Q3｜`provider` 是否随协议扩展**：当前 `provider` 只有 `scnet` 一个值。接 Anthropic / Responses 时，是 (a) 仍记 `scnet`（协议只是报文形态），还是 (b) 扩 `provider`（如 `anthropic` / `openai`）？产品建议：**本次只扩 `protocol`，`provider` 不动**——`provider` 表示"哪家接入源"，`protocol` 表示"哪种报文"，两者正交；扩 `provider` 会牵扯表单联动与模板，收益不明。
4. **Q4｜编辑时改协议的联动**：已保存模型把 `protocol` 从 `openai` 改成 `anthropic` 时，`base_url`（如 `https://api.scnet.cn/api/llm/v1` 与 `https://api.anthropic.com/v1`）与 `model_id` 通常也必须变。产品建议：**不阻断保存**，仅表单展示"切换协议后请确认地址与模型标识仍匹配"的提示，并允许用户点「测试连接」自证。是否要改成"切协议时清空 base_url 强制重填"？
5. **Q5｜限流窗口与阈值默认值 + 统计保留时长**：
   - 默认 `enabled=false`（不启用）/ `max_calls=1000` / `window_minutes=60` / `action=warn` 是否合适？这组默认值直接决定"升级后会不会突然影响线上调用"——产品建议**默认不启用**，由管理员显式打开。
   - 调用记录保留期建议 **90 天**（可配），到期清理。是否有合规或成本上的不同要求（如"永久保留"或"仅 7 天"）？
6. **Q6｜思考配置矛盾的处置**：`thinking_enabled=true` 但 `extra_config.reasoningEffort=none` 属自相矛盾。产品建议：**以 `extra_config` 为准**（可提供档位 = `none`），表单给出提示但不阻断保存。是否要改为保存时 400？
7. **Q7｜是否需要"精确匹配思考强度"**：本次按"≥"匹配（§5.1 Step 3）。若业务上需要"必须正好 `high`，不允许 `max` 的模型"（例如按成本控档），需要新增 `exactEffort` 语义。产品建议：本次不实现，先按 ≥ 上线。
8. **Q8｜Anthropic / Responses 的思考参数折算数值**：§5.2 的折算表（`low→1024` / `high→4096` / `max→8192` budget_tokens；Responses 的 `low`/`medium`/`high`）是**基于协议惯例的首版取值，需用真实上游校准**。建议架构师实现时以实测为准并回填本文；若某供应商不接受这些值，须明确是报错还是忽略参数（产品建议：报 `parameter_error`，不静默忽略）。
9. **Q9｜是否需要独立的"统计查看"权限点**：产品建议复用 `rdc.model.read`（§4.5 / §6.1）。若希望"能看模型但不能看用量与成本"，需新增 `rdc.model.usage.read`，这会牵动权限目录、菜单 Actions 与前端隐藏逻辑——**默认不新增**。
10. **Q10｜统计是否默认包含"本地拒绝"记录**：当前建议统计默认**不含**被软限流拒绝的记录（它们是未出站的事实），另给开关查看。若用户希望"总次数包含被拒次数"以便直观看到"有多少请求被拦"，应反转默认。这影响统计口径的第一句话（§5.3）。

---

## 附：本次增量对既有文件的预期影响面（供架构师排任务）

| 层 | 预期改动 |
| --- | --- |
| `api/openapi.yaml` | 协议枚举加 2 值；`AITestCategory` 加 `capability_mismatch`；新增统计查询 schema、限流配置 schema；`x-rdc-ai-*` 扩展同步 |
| `contract/ai_model.go` | `AIProtocols` 加 2 值；新增 `AIChatCapabilityMismatch` 类别与文案；`AIModelRequestParams` 保持四档语义；新增折算表的纯函数（协议 → 参数） |
| `service/rdc/model_chat.go` | `ChatRequest` 加 3 个可选字段；`chatOnce` 内按协议分支（端点 / 头 / 请求体 / 响应）；`Chat` 内加限流判定与写记录 |
| `repository/rdc/` | `FindDefaultOrFirstEnabled` → 新增"按条件选模"的方法（P0 方法保留给零值路径或合并，由架构师定）；新增调用记录仓储、阈值配置仓储 |
| `schema.sql` | 新表 `team.rdc_ai_model_call`、`team.rdc_ai_model_quota`；`rdc_ai_model.protocol` 无需迁移 |
| `handler/rdc/` | 新增统计查询、阈值读写的 handler；审计 action 增加 1 个 |
| `web/src/` | `format.ts` 加协议标签；列表页加协议筛选与统计入口；表单页协议 3 项 + 协议相关校验；新增统计视图与限流设置弹窗；`api/rdcModel.ts` 加统计与阈值接口 |
| 测试 | `ai_model_qa_e2e_test.go:576` 的 `ChatRequest` 字段断言必须更新（见 §1.3）；三协议 mock 单测；筛选顺序的单测；限流单测 |

---

**硬约束重申**：Key 绝不回显、绝不写日志、绝不进调用记录；隔离维度为公司（tenant）；统一调用仍只有 `chatOnce` 一个 HTTP 出口、前端仍无任何可直调 LLM 的路由；本次不做场景级配置、Key 轮换提醒、计费。
