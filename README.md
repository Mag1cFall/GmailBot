# GmailBot

Telegram Bot，通过 AI 管理你的 Gmail 收件箱。

## 功能

- 📬 收件箱浏览、未读邮件、邮件搜索、标签管理
- 🤖 AI 对话：自然语言操作 Gmail（查信、读信、摘要），支持 function calling
- 🗓 定时每日摘要：可设置多个时间点自动生成邮件摘要
- 🔔 新邮件检测 + AI 智能过滤推送（只推送重要邮件）
- 💬 多会话管理：支持创建、切换、清空 AI 会话
- ⚙️ 配置热重载：通过 Telegram 按钮直接修改模型、API、超时等配置，无需重启

## 技术栈

- Go 1.21+
- Telegram Bot API（[telebot v3](https://github.com/tucnak/telebot)）
- Gmail API（OAuth2）
- OpenAI 兼容 API（支持任意 OpenAI 格式的后端）
- SQLite（数据存储）

## 快速开始

### 1. 准备工作

- 创建 Telegram Bot（[@BotFather](https://t.me/BotFather)），获取 Bot Token
- 在 [Google Cloud Console](https://console.cloud.google.com/) 创建 OAuth 2.0 凭据，启用 Gmail API
- 准备一个兼容 OpenAI API 格式的服务端点和 API Key

### 2. 配置

```bash
cp .env.example .env
```

编辑 `.env`，填入真实的凭据：

| 变量 | 说明 |
|---|---|
| `BOT_TOKEN` | Telegram Bot Token |
| `GOOGLE_CLIENT_ID` | Google OAuth Client ID |
| `GOOGLE_CLIENT_SECRET` | Google OAuth Client Secret |
| `OAUTH_REDIRECT_URL` | OAuth 回调地址，本地部署填 `http://localhost` |
| `AI_BASE_URL` | OpenAI Compatible API 端点，如 `https://api.openai.com/v1` |
| `AI_API_KEY` | AI API Key |
| `AI_MODEL` | 模型名称，如 `gpt-5.3-codex(xhigh)` |
| `DB_PATH` | SQLite 数据库路径，默认 `./data/gmailbot.db` |
| `TELEGRAM_TIMEOUT_SEC` | Telegram 请求超时（秒） |
| `AI_TIMEOUT_SEC` | AI 请求超时（秒） |

### 3. 编译运行

```bash
go build -o gmailbot ./cmd/bot/
./gmailbot
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

## 命令列表

| 命令 | 说明 |
|---|---|
| `/inbox [n]` | 查看收件箱（默认 10 封） |
| `/unread` | 查看未读邮件 |
| `/read <id>` | 查看邮件正文 |
| `/search <query>` | 搜索邮件 |
| `/labels` | 查看标签列表 |
| `/digest` | 立即生成每日摘要 |
| `/setdigest 08:00,12:00` | 设置定时摘要（多时间点逗号分隔） |
| `/canceldigest` | 取消定时摘要 |
| `/setcheck <minutes>` | 设置新邮件检查间隔 |
| `/cancelcheck` | 停止自动检查 |
| `/aipush on/off` | AI 智能推送开关 |
| `/schedule` | 查看定时任务配置 |
| `/status` | 查看 Bot 运行状态 |
| `/new [title]` | 新建 AI 会话 |
| `/sessions` | 查看会话列表 |
| `/switch <id>` | 切换会话 |
| `/clear` | 清空当前会话 |
| `/config` | 热修改配置（模型/API/超时），InlineKeyboard 按钮式 |

直接发送文本即可与 AI 助手自由对话，AI 可通过 function calling 代你操作 Gmail。

## 项目结构

```
gmailbot/
├── cmd/bot/          # 入口
├── config/           # 配置加载（从 .env / 环境变量）
├── internal/
│   ├── ai/           # AI Agent（提示词、function calling、工具调用）
│   ├── gmail/        # Gmail API 封装（OAuth、邮件读取）
│   ├── store/        # SQLite 存储（用户、会话、定时任务）
│   └── tgbot/        # Telegram Bot（命令处理、调度器）
├── .env.example      # 环境变量示例
├── .gitignore
├── go.mod
└── go.sum
```


