// Telegram Bot 命令处理函数
package tgbot

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gmailbot/internal/platform"
)

var digestTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

// registerHandlers 注册所有命令处理函数
func (a *App) registerHandlers() error {
	commands := []platform.Command{
		{Name: "start", Description: "初始化机器人", Handler: a.handleStart},
		{Name: "help", Description: "帮助", Handler: a.handleHelp},
		{Name: "auth", Description: "获取 Google 授权链接", Handler: a.handleAuth},
		{Name: "code", Description: "提交 /code <redirect_url> 完成授权", Handler: a.handleCode},
		{Name: "revoke", Description: "撤销 Gmail 授权", Handler: a.handleRevoke},
		{Name: "mymail", Description: "查看已绑定的邮箱", Handler: a.handleMyMail},
		{Name: "inbox", Description: "查看收件箱 /inbox [n]", Handler: a.handleInbox},
		{Name: "unread", Description: "查看未读邮件", Handler: a.handleUnread},
		{Name: "read", Description: "查看邮件正文 /read <id>", Handler: a.handleRead},
		{Name: "search", Description: "搜索邮件 /search <query>", Handler: a.handleSearch},
		{Name: "labels", Description: "查看标签", Handler: a.handleLabels},
		{Name: "digest", Description: "立即生成每日摘要", Handler: a.handleDigest},
		{Name: "setdigest", Description: "/setdigest 08:00,12:00 支持多时间点", Handler: a.handleSetDigest},
		{Name: "canceldigest", Description: "取消每日自动摘要", Handler: a.handleCancelDigest},
		{Name: "setcheck", Description: "设置检查间隔 /setcheck <minutes>", Handler: a.handleSetCheck},
		{Name: "cancelcheck", Description: "停止新邮件自动检查", Handler: a.handleCancelCheck},
		{Name: "aipush", Description: "/aipush on|off 开启AI智能过滤推送", Handler: a.handleAIPush},
		{Name: "schedule", Description: "查看定时任务", Handler: a.handleSchedule},
		{Name: "status", Description: "查看Bot运行状态", Handler: a.handleStatus},
		{Name: "new", Description: "新建 AI 会话", Handler: a.handleNewSession},
		{Name: "sessions", Description: "会话列表", Handler: a.handleSessions},
		{Name: "switch", Description: "切换会话 /switch <id前缀>", Handler: a.handleSwitchSession},
		{Name: "clear", Description: "清空当前会话", Handler: a.handleClearSession},
		{Name: "persona", Description: "查看或切换人格 /persona [name|list]", Handler: a.handlePersona},
		{Name: "config", Description: "热修改配置项（AI模型/API/超时）", Handler: a.handleConfig},
		{Name: "memory", Description: "查看记忆文件列表和大小", Handler: a.handleMemory},
		{Name: "memoryclean", Description: "清理会话记录 /memoryclean [文件名]", Handler: a.handleMemoryClean},
	}
	for _, command := range commands {
		if err := a.router.Register(command); err != nil {
			return err
		}
	}
	return nil
}

// handleStart 初始化用户并展示欢迎指引
func (a *App) handleStart(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if _, err := a.resolveUserKey(ctx, msg); err != nil {
		return platform.UnifiedResponse{Text: "初始化用户失败，请稍后重试。"}, nil
	}
	return platform.UnifiedResponse{Text: "欢迎使用 Gmail 助手机器人。\n1) 先执行 /auth 完成 Gmail 授权\n2) 授权后可用 /inbox /unread /search /digest\n3) 直接发送文本可与 AI 助手对话"}, nil
}

// handleHelp 返回帮助文本
func (a *App) handleHelp(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	helpText := "📬 *邮件操作*\n" +
		"/inbox \\[n] — 查看收件箱（默认10封）\n" +
		"/unread — 查看未读邮件\n" +
		"/read <id> — 查看邮件正文\n" +
		"/search <query> — 搜索邮件\n" +
		"/labels — 查看标签列表\n" +
		"/digest — 立即生成每日摘要\n" +
		"\n🗓 *定时任务*\n" +
		"/setdigest 08:00,12:00 — 设置定时摘要\n" +
		"/canceldigest — 取消定时摘要\n" +
		"/setcheck <分钟> — 设置新邮件检查间隔\n" +
		"/cancelcheck — 停止自动检查\n" +
		"/aipush on|off — AI智能推送开关\n" +
		"/schedule — 查看定时任务配置\n" +
		"\n🤖 *AI 对话*\n" +
		"直接发送文本即可与 AI 对话\n" +
		"AI 可自动调用工具：查信/发信/回信/转发/标签管理/网页搜索/读网页/数学计算/设提醒/记忆读写\n" +
		"/new \\[标题] — 新建会话\n" +
		"/sessions — 会话列表\n" +
		"/switch <id> — 切换会话\n" +
		"/clear — 清空当前会话\n" +
		"\n🎭 *人格切换*\n" +
		"/persona — 查看当前人格\n" +
		"/persona list — 列出所有人格\n" +
		"/persona <name> — 切换人格\n" +
		"可选：gmail / gmail-only / research / all-tools\n" +
		"\n🧠 *记忆管理*\n" +
		"/memory — 查看记忆文件列表和大小\n" +
		"/memoryclean — 清理会话记录文件\n" +
		"/memoryclean <文件名> — 删除指定记忆文件\n" +
		"\n⚙️ *系统*\n" +
		"/config — 热修改配置（AI模型/API/超时）\n" +
		"/status — 查看 Bot 运行状态\n" +
		"/auth — Gmail 授权\n" +
		"/mymail — 查看已绑定邮箱\n" +
		"/revoke — 撤销授权"
	return platform.UnifiedResponse{Text: helpText, Markdown: true}, nil
}

// handleAuth 生成 OAuth 授权链接
func (a *App) handleAuth(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "创建用户失败，请稍后再试。"}, nil
	}
	state := fmt.Sprintf("%s_%d_%d", msg.Platform, userKey, time.Now().Unix())
	url := a.gmail.AuthCodeURL(state)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return platform.UnifiedResponse{Text: url}, nil
	}
	return platform.UnifiedResponse{Text: "请按以下步骤授权：\n1. 打开链接并同意授权：\n" + url + "\n\n2. 浏览器会跳转到 localhost 并报错，这是正常的\n3. 复制地址栏完整 URL，发送：\n/code <完整URL>"}, nil
}

// handleCode 提交授权码，完成 OAuth 流程
func (a *App) handleCode(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if len(args) == 0 {
		return platform.UnifiedResponse{Text: "用法：/code <完整重定向URL>"}, nil
	}
	raw := strings.TrimSpace(strings.Join(args, " "))
	code, err := a.gmail.ParseCode(raw)
	if err != nil {
		return platform.UnifiedResponse{Text: "解析 code 失败，请确认你发送的是完整重定向 URL。"}, nil
	}
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "创建用户失败，请稍后再试。"}, nil
	}
	token, err := a.gmail.ExchangeCode(ctx, code)
	if err != nil {
		return platform.UnifiedResponse{Text: "换取令牌失败，请重新执行 /auth 再试。"}, nil
	}
	email, err := a.gmail.GetProfileEmailByToken(ctx, token)
	if err != nil {
		return platform.UnifiedResponse{Text: "获取 Gmail 地址失败，请确认授权范围后重试。"}, nil
	}
	if err = a.gmail.SaveTokenForUser(ctx, userKey, email, token); err != nil {
		return platform.UnifiedResponse{Text: "保存令牌失败，请稍后重试。"}, nil
	}
	return platform.UnifiedResponse{Text: "授权成功，已绑定邮箱：" + email}, nil
}

// handleRevoke 撤销 Gmail 授权
func (a *App) handleRevoke(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "撤销授权失败：用户不存在"}, nil
	}
	if err := a.gmail.Revoke(ctx, userKey); err != nil {
		return platform.UnifiedResponse{Text: "撤销授权失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: "授权已撤销。"}, nil
}

// handleInbox 查看收件箱
func (a *App) handleInbox(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	n := parseBoundedInt(args, 10, 1, 20)
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取收件箱失败：用户不存在"}, nil
	}
	emails, err := a.gmail.ListEmails(ctx, userKey, n, "")
	if err != nil {
		return platform.UnifiedResponse{Text: "读取收件箱失败：" + err.Error()}, nil
	}
	if len(emails) == 0 {
		return platform.UnifiedResponse{Text: "收件箱暂无邮件。"}, nil
	}
	text := formatEmailList("收件箱", emails)
	a.appendToSession(msg, fmt.Sprintf("/inbox %d", n), text)
	return platform.UnifiedResponse{Text: text, Markdown: true}, nil
}

// handleUnread 查看未读邮件
func (a *App) handleUnread(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取未读邮件失败：用户不存在"}, nil
	}
	emails, err := a.gmail.ListUnread(ctx, userKey, 10)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取未读邮件失败：" + err.Error()}, nil
	}
	if len(emails) == 0 {
		return platform.UnifiedResponse{Text: "当前没有未读邮件。"}, nil
	}
	text := formatEmailList("未读邮件", emails)
	a.appendToSession(msg, "/unread", text)
	return platform.UnifiedResponse{Text: text, Markdown: true}, nil
}

// handleRead 读取指定邮件正文
func (a *App) handleRead(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if len(args) == 0 {
		return platform.UnifiedResponse{Text: "用法：/read <id>"}, nil
	}
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取邮件失败：用户不存在"}, nil
	}
	detail, err := a.gmail.GetEmail(ctx, userKey, args[0])
	if err != nil {
		return platform.UnifiedResponse{Text: "读取邮件失败：" + err.Error()}, nil
	}
	text := fmt.Sprintf("主题: %s\n发件人: %s\n收件人: %s\n日期: %s\nID: %s\n\n%s", detail.Subject, detail.From, detail.To, detail.Date, detail.ID, trimForDisplay(detail.Body, 12000))
	a.appendToSession(msg, "/read "+args[0], text)
	return platform.UnifiedResponse{Text: text, Markdown: true}, nil
}

// handleSearch 搜索邮件
func (a *App) handleSearch(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return platform.UnifiedResponse{Text: "用法：/search <query>"}, nil
	}
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "搜索失败：用户不存在"}, nil
	}
	emails, err := a.gmail.ListEmails(ctx, userKey, 10, query)
	if err != nil {
		return platform.UnifiedResponse{Text: "搜索失败：" + err.Error()}, nil
	}
	if len(emails) == 0 {
		return platform.UnifiedResponse{Text: "没有匹配邮件。"}, nil
	}
	text := formatEmailList("搜索结果", emails)
	a.appendToSession(msg, "/search "+query, text)
	return platform.UnifiedResponse{Text: text, Markdown: true}, nil
}

// handleLabels 列出 Gmail 标签
func (a *App) handleLabels(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取标签失败：用户不存在"}, nil
	}
	labels, err := a.gmail.GetLabels(ctx, userKey)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取标签失败：" + err.Error()}, nil
	}
	if len(labels) == 0 {
		return platform.UnifiedResponse{Text: "没有标签。"}, nil
	}
	lines := []string{"标签列表："}
	for _, item := range labels {
		lines = append(lines, fmt.Sprintf("- %s (%s, %d)", item.Name, item.Type, item.MessagesTotal))
	}
	text := strings.Join(lines, "\n")
	a.appendToSession(msg, "/labels", text)
	return platform.UnifiedResponse{Text: text, Markdown: true}, nil
}

// handleDigest 立即生成每日邮件摘要
func (a *App) handleDigest(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "生成摘要失败：用户不存在"}, nil
	}
	digest, err := a.ai.GenerateDailyDigest(ctx, userKey)
	if err != nil {
		return platform.UnifiedResponse{Text: "生成摘要失败：" + err.Error()}, nil
	}
	text := "每日摘要：\n\n" + digest
	a.appendToSession(msg, "/digest", text)
	return platform.UnifiedResponse{Text: text, Markdown: true}, nil
}

// handleSetDigest 设置每日摘要定时时间
func (a *App) handleSetDigest(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if len(args) == 0 {
		return platform.UnifiedResponse{Text: "用法：/setdigest 08:00 或 /setdigest 08:00,12:00,16:00,20:00"}, nil
	}
	raw := strings.TrimSpace(strings.Join(args, " "))
	var valid []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if !digestTimePattern.MatchString(item) {
			return platform.UnifiedResponse{Text: "时间格式错误：" + item + "，示例：08:00"}, nil
		}
		valid = append(valid, item)
	}
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "设置失败：用户不存在"}, nil
	}
	if err := a.store.SetDigestTimes(ctx, userKey, valid); err != nil {
		return platform.UnifiedResponse{Text: "设置失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: "摘要时间已设置为：" + strings.Join(valid, ", ")}, nil
}

// handleSetCheck 设置新邮件检查间隔
func (a *App) handleSetCheck(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if len(args) == 0 {
		return platform.UnifiedResponse{Text: "用法：/setcheck <minutes>"}, nil
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || minutes < 1 || minutes > 1440 {
		return platform.UnifiedResponse{Text: "minutes 取值范围为 1-1440。"}, nil
	}
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "设置失败：用户不存在"}, nil
	}
	if err := a.store.SetCheckInterval(ctx, userKey, minutes); err != nil {
		return platform.UnifiedResponse{Text: "设置失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: fmt.Sprintf("新邮件检查间隔已设置为 %d 分钟。", minutes)}, nil
}

// handleCancelDigest 取消每日摘要
func (a *App) handleCancelDigest(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "取消失败：用户不存在"}, nil
	}
	if err := a.store.SetDigestTimes(ctx, userKey, nil); err != nil {
		return platform.UnifiedResponse{Text: "取消失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: "每日自动摘要已取消。"}, nil
}

// handleAIPush 开启或关闭 AI 智能邮件过滤推送
func (a *App) handleAIPush(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if len(args) == 0 {
		return platform.UnifiedResponse{Text: "用法：/aipush on 或 /aipush off"}, nil
	}
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "设置失败：用户不存在"}, nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on", "1", "true":
		if err := a.store.SetAIPushEnabled(ctx, userKey, true); err != nil {
			return platform.UnifiedResponse{Text: "设置失败：" + err.Error()}, nil
		}
		return platform.UnifiedResponse{Text: "✅ AI智能推送已开启。\n收到新邮件时，AI会判断是否重要，只推送重要邮件。"}, nil
	case "off", "0", "false":
		if err := a.store.SetAIPushEnabled(ctx, userKey, false); err != nil {
			return platform.UnifiedResponse{Text: "设置失败：" + err.Error()}, nil
		}
		return platform.UnifiedResponse{Text: "🔕 AI智能推送已关闭。\n所有新邮件都会推送通知。"}, nil
	default:
		return platform.UnifiedResponse{Text: "用法：/aipush on 或 /aipush off"}, nil
	}
}

// handleCancelCheck 停止新邮件自动检查
func (a *App) handleCancelCheck(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "取消失败：用户不存在"}, nil
	}
	if err := a.store.SetCheckInterval(ctx, userKey, 0); err != nil {
		return platform.UnifiedResponse{Text: "取消失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: "新邮件自动检查已停止。"}, nil
}

// handleSchedule 展示当前定时任务配置
func (a *App) handleSchedule(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取计划失败。"}, nil
	}
	user, err := a.store.GetUser(ctx, userKey)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取用户配置失败。"}, nil
	}
	digestStr := "(未设置)"
	if len(user.DigestTimes) > 0 {
		digestStr = strings.Join(user.DigestTimes, ", ")
	}
	checkStr := "已停止"
	if user.CheckIntervalMin > 0 {
		checkStr = fmt.Sprintf("%d 分钟", user.CheckIntervalMin)
	}
	aiPushStr := "关闭"
	if user.AIPushEnabled {
		aiPushStr = "开启（只推送重要邮件）"
	}
	auth := "未授权"
	if user.IsAuthorized() {
		auth = "已授权: " + user.GmailAddress
	}
	return platform.UnifiedResponse{Text: fmt.Sprintf("当前配置：\n- 授权状态: %s\n- 新邮件检查: %s\n- AI智能推送: %s\n- 每日摘要时间: %s", auth, checkStr, aiPushStr, digestStr)}, nil
}

// handleStatus 展示 Bot 和用户运行状态
func (a *App) handleStatus(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取状态失败。"}, nil
	}
	user, err := a.store.GetUser(ctx, userKey)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取状态失败。"}, nil
	}
	authStr := "❌ 未授权"
	if user.IsAuthorized() {
		authStr = "✅ " + user.GmailAddress
	}
	checkStr := "🔕 已停止"
	if user.CheckIntervalMin > 0 {
		checkStr = fmt.Sprintf("✅ 每 %d 分钟", user.CheckIntervalMin)
	}
	aiStr := "🔕 关闭"
	if user.AIPushEnabled {
		aiStr = "✅ 开启"
	}
	digestStr := "🔕 未设置"
	if len(user.DigestTimes) > 0 {
		digestStr = "✅ " + strings.Join(user.DigestTimes, ", ")
	}
	return platform.UnifiedResponse{Text: "*Bot 状态*\nGmail: " + authStr + "\n新邮件检查: " + checkStr + "\nAI智能推送: " + aiStr + "\n每日摘要: " + digestStr, Markdown: true}, nil
}

// handleNewSession 新建并切换到新 AI 会话
func (a *App) handleNewSession(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	title := strings.TrimSpace(strings.Join(args, " "))
	session, err := a.store.CreateSessionByIdentity(ctx, msg.Platform, msg.UserID, title)
	if err != nil {
		return platform.UnifiedResponse{Text: "创建会话失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: fmt.Sprintf("已创建并切换到新会话：%s (%s)", session.Title, session.ID[:8])}, nil
}

// handleSessions 列出用户会话列表
func (a *App) handleSessions(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取会话失败：用户不存在"}, nil
	}
	sessions, err := a.store.ListSessions(ctx, userKey, 20)
	if err != nil {
		return platform.UnifiedResponse{Text: "读取会话失败：" + err.Error()}, nil
	}
	if len(sessions) == 0 {
		return platform.UnifiedResponse{Text: "暂无会话，发送 /new 创建。"}, nil
	}
	lines := []string{"会话列表："}
	for _, session := range sessions {
		flag := " "
		if session.IsActive {
			flag = "*"
		}
		last := "-"
		if !session.LastActive.IsZero() {
			last = session.LastActive.Local().Format("01-02 15:04")
		}
		lines = append(lines, fmt.Sprintf("%s %s | %s | %s", flag, session.ID[:8], session.Title, last))
	}
	lines = append(lines, "切换示例：/switch <id前缀>")
	return platform.UnifiedResponse{Text: strings.Join(lines, "\n"), Markdown: true}, nil
}

// handleSwitchSession 切换活跃会话
func (a *App) handleSwitchSession(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if len(args) == 0 {
		return platform.UnifiedResponse{Text: "用法：/switch <id前缀>"}, nil
	}
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "切换会话失败。"}, nil
	}
	sessionID, err := a.store.ResolveSessionID(ctx, userKey, args[0])
	if err != nil {
		if err == sql.ErrNoRows {
			return platform.UnifiedResponse{Text: "没有找到匹配会话。"}, nil
		}
		return platform.UnifiedResponse{Text: "会话前缀匹配不唯一，请输入更长前缀。"}, nil
	}
	if err := a.store.SwitchActiveSession(ctx, userKey, sessionID); err != nil {
		return platform.UnifiedResponse{Text: "切换会话失败。"}, nil
	}
	return platform.UnifiedResponse{Text: "已切换会话：" + sessionID[:8]}, nil
}

// handleClearSession 清空当前会话的消息历史
func (a *App) handleClearSession(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "清理会话失败：用户不存在"}, nil
	}
	if err := a.store.ClearActiveSessionMessages(ctx, userKey); err != nil {
		return platform.UnifiedResponse{Text: "清理会话失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: "当前会话上下文已清空。"}, nil
}

// handlePersona 查看或切换人设
func (a *App) handlePersona(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	if a.personaMgr == nil {
		return platform.UnifiedResponse{Text: "人格系统未启用。"}, nil
	}
	if len(args) == 0 {
		active, err := a.personaMgr.ActivePersona(ctx, msg.Platform, msg.UserID)
		if err != nil {
			return platform.UnifiedResponse{Text: "读取人格失败：" + err.Error()}, nil
		}
		return platform.UnifiedResponse{Text: fmt.Sprintf("当前人格：%s\n默认人格：%s\n使用 /persona list 查看所有人格，使用 /persona <name> 切换。", active.Name, a.personaMgr.Default().Name)}, nil
	}
	if args[0] == "list" {
		lines := []string{"人格列表："}
		for _, item := range a.personaMgr.List() {
			lines = append(lines, "- "+item.Name)
		}
		return platform.UnifiedResponse{Text: strings.Join(lines, "\n")}, nil
	}
	selected, err := a.personaMgr.SetActiveSessionPersona(ctx, msg.Platform, msg.UserID, strings.TrimSpace(args[0]))
	if err != nil {
		return platform.UnifiedResponse{Text: "切换人格失败：" + err.Error()}, nil
	}
	return platform.UnifiedResponse{Text: "已切换人格：" + selected.Name}, nil
}

// handleConfig 返回配置入口提示，具体配置由内联键盘处理
func (a *App) handleConfig(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	return platform.UnifiedResponse{Text: "⚙️ 点击要修改的配置项："}, nil
}

// appendToSession 异步将命令和结果记入当前会话
func (a *App) appendToSession(msg platform.UnifiedMessage, userMsg, assistantMsg string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = a.store.AppendActiveSessionMessageByIdentity(ctx, msg.Platform, msg.UserID, "user", userMsg)
		_, _ = a.store.AppendActiveSessionMessageByIdentity(ctx, msg.Platform, msg.UserID, "assistant", assistantMsg)
	}()
}

// handleMyMail 查看已绑定的 Gmail 地址
func (a *App) handleMyMail(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "查询失败：用户不存在"}, nil
	}
	user, err := a.store.GetUser(ctx, userKey)
	if err != nil {
		return platform.UnifiedResponse{Text: "查询失败：" + err.Error()}, nil
	}
	if !user.IsAuthorized() {
		return platform.UnifiedResponse{Text: "尚未绑定邮箱，请先执行 /auth 授权。"}, nil
	}
	return platform.UnifiedResponse{Text: "已绑定邮箱：" + user.GmailAddress}, nil
}

// handleMemory 列出用户记忆文件列表和大小
func (a *App) handleMemory(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "查询失败：用户不存在"}, nil
	}
	files, err := a.memory.ListFilesWithSize(userKey)
	if err != nil {
		return platform.UnifiedResponse{Text: "查询失败：" + err.Error()}, nil
	}
	if len(files) == 0 {
		return platform.UnifiedResponse{Text: "暂无记忆文件。"}, nil
	}
	var total int64
	lines := []string{"🧠 *记忆文件列表*"}
	for _, f := range files {
		size := formatSize(f.Size)
		lines = append(lines, fmt.Sprintf("- `%s` (%s)", f.Name, size))
		total += f.Size
	}
	lines = append(lines, fmt.Sprintf("\n总计 %d 个文件，%s", len(files), formatSize(total)))
	lines = append(lines, "\n删除指定文件：/memoryclean <文件名>")
	lines = append(lines, "清理全部会话记录：/memoryclean")
	return platform.UnifiedResponse{Text: strings.Join(lines, "\n"), Markdown: true}, nil
}

// handleMemoryClean 清理会话记录或删除指定记忆文件
func (a *App) handleMemoryClean(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
	userKey, err := a.resolveUserKey(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{Text: "操作失败：用户不存在"}, nil
	}
	if len(args) > 0 {
		fileName := strings.TrimSpace(strings.Join(args, " "))
		if err := a.memory.DeleteFile(userKey, fileName); err != nil {
			return platform.UnifiedResponse{Text: "删除失败：" + err.Error()}, nil
		}
		return platform.UnifiedResponse{Text: "已删除记忆文件：" + fileName}, nil
	}
	count, err := a.memory.ClearSessionTranscripts(userKey)
	if err != nil {
		return platform.UnifiedResponse{Text: "清理失败：" + err.Error()}, nil
	}
	if count == 0 {
		return platform.UnifiedResponse{Text: "没有需要清理的会话记录文件。"}, nil
	}
	return platform.UnifiedResponse{Text: fmt.Sprintf("已清理 %d 个会话记录文件。", count)}, nil
}

// formatSize 将字节数转换为可读大小字符串
func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}
