# GmailBot

Telegram Bot，通过 AI 管理你的 Gmail 收件箱。基于通用 Agent 平台架构，支持多平台接入（Telegram / WebUI / Lark / QQ）、插件化工具扩展、多 LLM 自动 Fallback、子 Agent 委派、配置热重载。

## 功能

- 📬 收件箱浏览、未读邮件、邮件搜索、标签管理（创建/删除/分配标签）
- 🤖 AI 对话：自然语言操作 Gmail（查信、读信、发信、回复、转发、摘要），支持 function calling 自动调用工具
- ✍️ 邮件撰写：AI 代写邮件，支持通过子 Agent 委派给专用邮件撰写助手
- 🗓 定时每日摘要：可设置多个时间点自动生成邮件摘要推送
- 🔔 新邮件检测 + AI 智能过滤推送（AI 判断重要性，只推送重要邮件）
- 💬 多会话管理：支持创建、切换、清空 AI 会话
- 🧠 AI 记忆系统：Markdown 持久化存储用户偏好、联系人、自定义规则，AI 可读写搜索
- 🎭 多人格切换：不同人格对应不同 System Prompt 和工具集
- 🔄 多 LLM Provider 自动 Fallback：主 Provider 失败自动切换备用
- 🔌 插件架构：ToolRegistry + Plugin Manager，工具可独立注册与启停
- 🛡 消息 Pipeline：认证检查 → 频率限制 → AI 处理 → 安全脱敏（自动过滤 AI 输出中的密钥）
- 🤝 MCP 协议支持：连接外部 MCP Server，自动发现并注册远端工具
- ⚙️ 配置热重载：修改 .env 后自动生效，也可通过 Telegram 按钮直接修改配置，无需重启
- 📊 Dashboard：Web 管理面板，查看插件/工具/Provider/指标/日志，支持在线开关
- 🌐 WebUI：独立 Web 聊天入口，支持 Bearer Token 鉴权、SSE 实时推送
- 📡 多平台适配器：Telegram / WebUI / Lark / QQ，统一消息格式，按需启用

## 技术栈

- Go 1.24+
- Telegram Bot API（[telebot v3](https://github.com/tucnak/telebot)）
- Gmail API（OAuth2, Authorization Code Grant）
- OpenAI 兼容 API（[go-openai](https://github.com/sashabaranov/go-openai)，支持 DeepSeek/Qwen/Moonshot/Ollama 等）
- MySQL / MariaDB（通过 `DB_DSN` 配置连接串）
- MCP 协议（stdio + SSE 双传输）
- 定时任务（[robfig/cron](https://github.com/robfig/cron)）

## 快速开始

### 1. 准备工作

- 创建 Telegram Bot（[@BotFather](https://t.me/BotFather)），获取 Bot Token
- 在 [Google Cloud Console](https://console.cloud.google.com/) 创建 OAuth 2.0 凭据，启用 Gmail API
- 准备一个兼容 OpenAI API 格式的服务端点和 API Key

### 2. 配置

```bash
cp .env.example .env
```

编辑 `.env`，填入真实的凭据（完整配置项见下方表格）：

**必填项：**

| 变量 | 说明 |
|---|---|
| `BOT_TOKEN` | Telegram Bot Token |
| `AI_BASE_URL` | OpenAI Compatible API 端点，如 `https://api.openai.com/v1` |
| `AI_API_KEY` | AI API Key |
| `AI_MODEL` | 模型名称，如 `gpt-4o` |

**Gmail（可选，不填则 Gmail 功能不可用）：**

| 变量 | 说明 |
|---|---|
| `GOOGLE_CLIENT_ID` | Google OAuth Client ID |
| `GOOGLE_CLIENT_SECRET` | Google OAuth Client Secret |
| `OAUTH_REDIRECT_URL` | OAuth 回调地址，本地部署填 `http://localhost` |

**全部可选配置项：**

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DB_DSN` | — | MySQL DSN，如 `root:pass@tcp(127.0.0.1:3306)/gmailbot?parseTime=true` |
| `MEMORY_ROOT` | `./data/memory` | 记忆文件存储目录 |
| `KNOWLEDGE_ROOT` | `./data/knowledge` | 知识库存储目录 |
| `TELEGRAM_TIMEOUT_SEC` | `10` | Telegram 长轮询超时（秒） |
| `AI_TIMEOUT_SEC` | `90` | AI 请求超时（秒） |
| `AI_TOOL_MAX_STEPS` | `6` | 单次对话最大工具调用轮数 |
| `AI_PROVIDER_TYPE` | `openai_compat` | Provider 类型 |
| `AI_FALLBACK_PROVIDERS` | — | 备用 Provider JSON 数组（见下方说明） |
| `MESSAGE_RATE_LIMIT_PER_MIN` | `0` | 每分钟消息频率限制（0=不限） |
| `DEFAULT_PERSONA` | `gmail` | 默认人格 |
| `CONFIG_WATCH_ENABLED` | `true` | 是否启用 .env 配置热重载 |
| `CONFIG_WATCH_DEBOUNCE_MS` | `800` | 热重载去抖间隔（毫秒） |
| `WEBHOOK_ADDR` | — | Webhook 监听地址，如 `:29110` |
| `WEBHOOK_SECRET` | — | Webhook 认证密钥 |
| `DASHBOARD_ADDR` | — | Dashboard 监听地址，如 `:29111` |
| `DASHBOARD_AUTH` | — | Dashboard / WebUI 认证 Token |
| `WEBUI_ADDR` | — | WebUI 监听地址，如 `:29112`（留空则不启用） |
| `LARK_APP_ID` | — | 飞书应用 App ID |
| `LARK_APP_SECRET` | — | 飞书应用 App Secret |
| `QQ_APP_ID` | — | QQ 机器人 App ID |
| `QQ_SECRET` | — | QQ 机器人 Secret |
| `MCP_SERVERS` | — | MCP Server 配置 JSON（见下方说明） |

### 3. 编译运行

```bash
go build -o gmailbot ./cmd/bot/
./gmailbot
```

或直接运行：

```bash
go run ./cmd/bot
```

或后台运行：

```bash
nohup ./gmailbot > bot.log 2>&1 &
```

### 4. Telegram 使用

在 Telegram 中与 Bot 对话：

1. `/start` 初始化
2. `/auth` 获取 Gmail 授权链接，完成 OAuth 授权流程
3. 授权完成后即可使用所有功能
4. 直接发送文本即可与 AI 助手对话，AI 通过 function calling 自动操作 Gmail

## 命令列表

| 命令 | 说明 |
|---|---|
| `/start` | 欢迎消息与使用引导 |
| `/auth` | 开始 Gmail OAuth 授权 |
| `/code <url>` | 提交 OAuth 回调 URL 完成邮箱绑定 |
| `/mymail` | 查看已绑定的邮箱信息 |
| `/inbox [n]` | 查看收件箱（默认 10 封） |
| `/unread` | 查看未读邮件 |
| `/read <id>` | 查看邮件正文 |
| `/search <query>` | 搜索邮件 |
| `/labels` | 查看标签列表 |
| `/digest` | 立即生成邮件摘要 |
| `/setdigest 08:00,20:00` | 设置定时摘要推送（多时间点逗号分隔） |
| `/canceldigest` | 取消定时摘要 |
| `/setcheck <minutes>` | 设置新邮件检查间隔（分钟） |
| `/cancelcheck` | 停止自动检查 |
| `/aipush on/off` | AI 智能过滤推送开关 |
| `/schedule` | 查看当前定时任务配置 |
| `/status` | 查看 Bot 运行状态 |
| `/persona [名称]` | 查看/切换 AI 人格 |
| `/new [title]` | 新建 AI 会话 |
| `/sessions` | 查看会话列表 |
| `/switch <id>` | 切换到指定会话 |
| `/clear` | 清空当前会话历史 |
| `/config` | 热修改配置（模型/API/超时），InlineKeyboard 按钮式 |

## AI 工具（function calling）

AI 助手通过以下工具自动操作，用户只需用自然语言描述需求：

### Gmail 工具
| 工具 | 说明 |
|---|---|
| `list_emails` | 列出/搜索邮件 |
| `get_email` | 读取邮件完整内容 |
| `send_email` | 发送新邮件 |
| `reply_email` | 回复邮件 |
| `forward_email` | 转发邮件 |
| `summarize_emails` | 生成邮件摘要 |
| `get_labels` | 获取标签列表 |
| `create_label` | 创建标签 |
| `delete_label` | 删除标签 |
| `modify_labels` | 给邮件添加/移除标签 |

### 记忆工具
| 工具 | 说明 |
|---|---|
| `memory_read` | 读取记忆文件 |
| `memory_write` | 写入记忆内容 |
| `memory_search` | 关键词搜索记忆 |
| `memory_save_transcript` | 保存对话历史到记忆 |

### 知识库工具
| 工具 | 说明 |
|---|---|
| `knowledge_search` | 搜索知识库 |
| `knowledge_list` | 列出知识库文件 |

### 搜索工具
| 工具 | 说明 |
|---|---|
| `web_search` | 网页搜索 |
| `read_url` | 读取网页内容 |

### 系统工具
| 工具 | 说明 |
|---|---|
| `get_current_time` | 获取当前时间 |
| `run_calculation` | 数学计算 |
| `set_reminder` | 设置提醒 |

### 子 Agent 委派工具
| 工具 | 说明 |
|---|---|
| `handoff_to_email_writer` | 委派给邮件撰写专用子 Agent |
| `handoff_to_email_searcher` | 委派给邮件搜索专用子 Agent |

## 架构

```
                              ┌─────────────────────────────────────┐
                              │            cmd/bot/main.go          │
                              │  组装所有组件、注册插件、启动服务    │
                              └──────────────┬──────────────────────┘
                                             │
              ┌──────────────────────────────┼──────────────────────────────┐
              ▼                              ▼                              ▼
     Platform Adapter              Plugin Manager                    Config Watcher
     (Telegram/...)                (Gmail/Memory/                    (.env 热重载)
              │                    Knowledge/Search/System)                 │
              ▼                              │                              ▼
     ┌─────────────┐                         ▼                     Agent.Reload()
     │ App (tgbot)  │◄── commands ── CommandRouter                 App.Reload()
     │              │                        │
     │  non-command ├──► Pipeline ───────────┼──────────────────────────┐
     │   messages   │    │ AuthCheck         │                         │
     └─────────────┘    │ RateLimit         ▼                         │
              │          │ AIProcess    ToolRegistry ◄── MCP Client   │
              │          │ SafetyFilter     │            (远端工具)    │
              │          └──────────────────┼─────────────────────────┘
              │                             │
              ▼                             ▼
         Scheduler                    Agent (ReAct)
     (定时摘要/新邮件)           ┌─────────┴─────────┐
                                │  ProviderManager   │
                                │  (多 LLM Fallback) │
                                └─────────┬──────────┘
                                          │
                                    SubAgent Orchestrator
                                    (email_writer / email_searcher)
```

### 消息处理 Pipeline

采用洋葱模型，阶段可插拔：

1. **AuthCheck** — 验证用户是否已绑定邮箱（WebUI 平台跳过此检查，由 Bearer Token 保护）
2. **RateLimit** — 按平台+用户 ID 频率限制，防刷消息
3. **AIProcess** — 调用 Agent.HandleMessage，执行 ReAct 循环
4. **SafetyFilter** — 扫描 AI 输出中的敏感信息（API Key/Token/Secret），自动替换为 `****`

### 多 Provider 自动 Fallback

```env
AI_FALLBACK_PROVIDERS=[{"name":"deepseek","base_url":"https://api.deepseek.com/v1","api_key":"sk-xxx","model":"deepseek-chat"},{"name":"qwen","base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","api_key":"sk-xxx","model":"qwen-plus"}]
```

主 Provider 请求失败时自动尝试下一个，日志记录切换过程，所有 Provider 失败才返回错误。

### 人格系统

通过 `/persona` 切换，不同人格使用不同的 System Prompt 和工具集：

| 人格 | 说明 | 工具范围 |
|---|---|---|
| `gmail` | 默认，Gmail 管理助手 | Gmail + 搜索 + 系统 |
| `gmail-only` | 纯邮件助手 | 仅 Gmail |
| `research` | 搜索与知识助手 | 搜索 + 知识库 + 记忆 |
| `all-tools` | 全能助手 | 所有已注册的工具 |

### MCP 协议支持

```env
MCP_SERVERS=[{"name":"filesystem","command":"npx","args":["-y","@anthropic/mcp-filesystem","/path"]},{"name":"remote","url":"http://localhost:3000/sse"}]
```

支持 stdio 和 SSE 两种传输协议。MCP Server 提供的工具会自动注册到 ToolRegistry，AI 可直接调用。

### Dashboard

```env
DASHBOARD_ADDR=:29111
DASHBOARD_AUTH=your-secret-token
```

REST API 端点：
- `GET /api/status` — 运行状态与指标
- `GET /api/plugins` — 插件列表与启停状态
- `POST /api/plugins/<name>/toggle` — 启用/禁用插件
- `GET /api/tools` — 工具列表与启停状态
- `POST /api/tools/<name>/toggle` — 启用/禁用工具
- `GET /api/providers` — LLM Provider 列表
- `GET /api/sessions/<user_id>` — 用户会话列表
- `GET /api/config` — 当前配置（敏感值脱敏）
- `POST /api/config/<key>` — 修改配置项
- `GET /api/logs` — 最近日志
- `GET /api/metrics` — 运行指标

### WebUI

```env
WEBUI_ADDR=:29112
DASHBOARD_AUTH=your-secret-token  # WebUI 与 Dashboard 共用此 Token
```

浏览器打开 `http://localhost:29112`，输入用户 ID 和 Token 即可聊天。API 端点由 Bearer Token 保护，SSE 实时推送 AI 回复。

## 项目结构

```
gmailbot/
├── cmd/bot/                    # 入口，组装所有组件
├── config/                     # 配置加载 + .env 热重载 Watcher
├── internal/
│   ├── agent/                  # Agent 核心
│   │   ├── agent.go            #   ReAct 循环、动态 System Prompt
│   │   ├── context.go          #   上下文窗口管理（warn/compress/trim）
│   │   ├── tool.go             #   ToolRegistry（注册/查询/启停/执行）
│   │   ├── provider.go         #   ProviderManager（多 LLM Fallback）
│   │   └── sub_agent.go        #   SubAgent 编排与 Handoff 工具
│   ├── pipeline/               # 消息处理 Pipeline（洋葱模型）
│   ├── plugin/                 # 插件管理器（生命周期、工具/命令/事件联动）
│   ├── platform/               # 平台抽象层
│   │   ├── adapter.go          #   Adapter 接口
│   │   ├── message.go          #   UnifiedMessage / UnifiedResponse
│   │   ├── telegram/           #   Telegram Adapter
│   │   ├── webui/              #   WebUI Adapter（HTTP + SSE）
│   │   ├── lark/               #   飞书 Adapter（桩代码）
│   │   └── qq/                 #   QQ Adapter（桩代码）
│   ├── gmail/                  # Gmail 服务 + Plugin（10 个 AI 工具）
│   │   ├── service.go          #   OAuth、邮件 CRUD、MIME 编码
│   │   ├── plugin.go           #   工具注册（send/reply/forward/list/...）
│   │   └── pending.go          #   草稿暂存 PendingStore
│   ├── memory/                 # Markdown 持久化记忆系统
│   ├── knowledge/              # 轻量知识库检索
│   ├── persona/                # 人格管理器
│   ├── event/                  # EventBus（异步事件发布/订阅）
│   ├── mcp/                    # MCP Client（stdio/SSE 双传输）
│   ├── dashboard/              # Web Dashboard（REST API + 静态前端）
│   ├── metrics/                # 运行指标统计
│   ├── logging/                # 结构化日志（slog + RingBuffer）
│   ├── webhook/                # Webhook Server
│   ├── store/                  # MySQL 存储层
│   ├── tgbot/                  # Telegram 业务逻辑
│   │   ├── app.go              #   消息入口、Pipeline 接入、命令路由
│   │   ├── handlers.go         #   所有 Telegram 命令处理
│   │   └── scheduler.go        #   定时摘要 + 新邮件轮询
│   ├── testutil/               # 测试辅助（MySQL testDB）
│   ├── plugins/system/         # 系统工具插件（时间/计算/提醒）
│   └── plugins/websearch/      # Web 搜索插件
├── .env.example                # 环境变量示例
├── .gitignore
├── go.mod
└── go.sum
```
