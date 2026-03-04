package tgbot

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gmailbot/internal/gmail"

	tele "gopkg.in/telebot.v3"
)

var digestTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)
var headingRe = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
var boldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

func (a *App) registerHandlers() {
	a.bot.Handle("/start", a.handleStart)
	a.bot.Handle("/help", a.handleHelp)

	a.bot.Handle("/auth", a.handleAuth)
	a.bot.Handle("/code", a.handleCode)
	a.bot.Handle("/revoke", a.handleRevoke)

	a.bot.Handle("/inbox", a.handleInbox)
	a.bot.Handle("/unread", a.handleUnread)
	a.bot.Handle("/read", a.handleRead)
	a.bot.Handle("/search", a.handleSearch)
	a.bot.Handle("/labels", a.handleLabels)

	a.bot.Handle("/digest", a.handleDigest)
	a.bot.Handle("/setdigest", a.handleSetDigest)
	a.bot.Handle("/canceldigest", a.handleCancelDigest)
	a.bot.Handle("/setcheck", a.handleSetCheck)
	a.bot.Handle("/cancelcheck", a.handleCancelCheck)
	a.bot.Handle("/aipush", a.handleAIPush)
	a.bot.Handle("/schedule", a.handleSchedule)
	a.bot.Handle("/status", a.handleStatus)

	a.bot.Handle("/new", a.handleNewSession)
	a.bot.Handle("/sessions", a.handleSessions)
	a.bot.Handle("/switch", a.handleSwitchSession)
	a.bot.Handle("/clear", a.handleClearSession)

	a.bot.Handle(tele.OnText, a.handleFreeText)
}

func (a *App) registerCommands() {
	commands := []tele.Command{
		{Text: "start", Description: "初始化机器人"},
		{Text: "auth", Description: "获取 Google 授权链接"},
		{Text: "code", Description: "提交 /code <redirect_url> 完成授权"},
		{Text: "revoke", Description: "撤销 Gmail 授权"},
		{Text: "inbox", Description: "查看收件箱 /inbox [n]"},
		{Text: "unread", Description: "查看未读邮件"},
		{Text: "read", Description: "查看邮件正文 /read <id>"},
		{Text: "search", Description: "搜索邮件 /search <query>"},
		{Text: "labels", Description: "查看标签"},
		{Text: "digest", Description: "立即生成每日摘要"},
		{Text: "setdigest", Description: "/setdigest 08:00,12:00,16:00 支持多时间点"},
		{Text: "canceldigest", Description: "取消每日自动摘要"},
		{Text: "setcheck", Description: "设置检查间隔 /setcheck <minutes>"},
		{Text: "cancelcheck", Description: "停止新邮件自动检查"},
		{Text: "aipush", Description: "/aipush on|off 开启AI智能过滤推送"},
		{Text: "schedule", Description: "查看定时任务"},
		{Text: "status", Description: "查看Bot运行状态"},
		{Text: "new", Description: "新建 AI 会话"},
		{Text: "sessions", Description: "会话列表"},
		{Text: "switch", Description: "切换会话 /switch <id前缀>"},
		{Text: "clear", Description: "清空当前会话"},
		{Text: "help", Description: "帮助"},
	}
	if err := a.bot.SetCommands(commands); err != nil {
		log.Printf("set commands failed: %v", err)
	}
}

func (a *App) handleStart(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	if err := a.store.EnsureUser(ctx, c.Sender().ID); err != nil {
		return c.Send("初始化用户失败，请稍后重试。")
	}
	return c.Send(
		"欢迎使用 Gmail 助手机器人。\n" +
			"1) 先执行 /auth 完成 Gmail 授权\n" +
			"2) 授权后可用 /inbox /unread /search /digest\n" +
			"3) 直接发送文本可与 AI 助手对话",
	)
}

func (a *App) handleHelp(c tele.Context) error {
	return c.Send(
		"可用命令：\n" +
			"/start /auth /code /revoke\n" +
			"/inbox [n] /unread /read <id> /search <query> /labels\n" +
			"/digest\n" +
			"/setdigest 08:00,12:00,16:00,20:00 （多时间点逗号分隔）\n" +
			"/canceldigest\n" +
			"/setcheck <minutes> /cancelcheck\n" +
			"/aipush on|off （AI智能过滤，只推送重要邮件）\n" +
			"/schedule /status\n" +
			"/new /sessions /switch <id> /clear\n" +
			"说明：任何自由文本会自动进入 AI 会话。",
	)
}

func (a *App) handleAuth(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	if err := a.store.EnsureUser(ctx, c.Sender().ID); err != nil {
		return c.Send("创建用户失败，请稍后再试。")
	}
	state := fmt.Sprintf("tg_%d_%d", c.Sender().ID, time.Now().Unix())
	url := a.gmail.AuthCodeURL(state)
	return c.Send(
		"请按以下步骤授权：\n" +
			"1. 打开链接并同意授权：\n" + url + "\n\n" +
			"2. 浏览器会跳转到 localhost 并报错，这是正常的\n" +
			"3. 复制地址栏完整 URL，发送：\n" +
			"/code <完整URL>",
	)
}

func (a *App) handleCode(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Send("用法：/code <完整重定向URL>")
	}
	raw := strings.TrimSpace(strings.Join(args, " "))
	code, err := a.gmail.ParseCode(raw)
	if err != nil {
		return c.Send("解析 code 失败，请确认你发送的是完整重定向 URL。")
	}

	ctx, cancel := a.shortCtx()
	defer cancel()

	token, err := a.gmail.ExchangeCode(ctx, code)
	if err != nil {
		return c.Send("换取令牌失败，请重新执行 /auth 再试。")
	}
	email, err := a.gmail.GetProfileEmailByToken(ctx, token)
	if err != nil {
		return c.Send("获取 Gmail 地址失败，请确认授权范围后重试。")
	}
	if err = a.gmail.SaveTokenForUser(ctx, c.Sender().ID, email, token); err != nil {
		return c.Send("保存令牌失败，请稍后重试。")
	}
	return c.Send("授权成功，已绑定邮箱：" + email)
}

func (a *App) handleRevoke(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	if err := a.gmail.Revoke(ctx, c.Sender().ID); err != nil {
		return c.Send("撤销授权失败：" + err.Error())
	}
	return c.Send("授权已撤销。")
}

func (a *App) handleInbox(c tele.Context) error {
	n := parseBoundedInt(c.Args(), 10, 1, 20)
	ctx, cancel := a.shortCtx()
	defer cancel()

	emails, err := a.gmail.ListEmails(ctx, c.Sender().ID, n, "")
	if err != nil {
		return c.Send("读取收件箱失败：" + err.Error())
	}
	if len(emails) == 0 {
		return c.Send("收件箱暂无邮件。")
	}
	text := formatEmailList("收件箱", emails)
	if err := a.sendLong(c, text); err != nil {
		return err
	}
	userMsg := fmt.Sprintf("/inbox %d", n)
	a.appendToSession(c.Sender().ID, userMsg, text)
	return nil
}

func (a *App) handleUnread(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	emails, err := a.gmail.ListUnread(ctx, c.Sender().ID, 10)
	if err != nil {
		return c.Send("读取未读邮件失败：" + err.Error())
	}
	if len(emails) == 0 {
		return c.Send("当前没有未读邮件。")
	}
	text := formatEmailList("未读邮件", emails)
	if err := a.sendLong(c, text); err != nil {
		return err
	}
	a.appendToSession(c.Sender().ID, "/unread", text)
	return nil
}

func (a *App) handleRead(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Send("用法：/read <id>")
	}

	ctx, cancel := a.shortCtx()
	defer cancel()

	detail, err := a.gmail.GetEmail(ctx, c.Sender().ID, args[0])
	if err != nil {
		return c.Send("读取邮件失败：" + err.Error())
	}

	text := fmt.Sprintf(
		"主题: %s\n发件人: %s\n收件人: %s\n日期: %s\nID: %s\n\n%s",
		detail.Subject,
		detail.From,
		detail.To,
		detail.Date,
		detail.ID,
		trimForTelegram(detail.Body, 12000),
	)
	if err := a.sendLong(c, text); err != nil {
		return err
	}
	a.appendToSession(c.Sender().ID, "/read "+args[0], text)
	return nil
}

func (a *App) handleSearch(c tele.Context) error {
	query := strings.TrimSpace(strings.Join(c.Args(), " "))
	if query == "" {
		return c.Send("用法：/search <query>")
	}
	ctx, cancel := a.shortCtx()
	defer cancel()

	emails, err := a.gmail.ListEmails(ctx, c.Sender().ID, 10, query)
	if err != nil {
		return c.Send("搜索失败：" + err.Error())
	}
	if len(emails) == 0 {
		return c.Send("没有匹配邮件。")
	}
	text := formatEmailList("搜索结果", emails)
	if err := a.sendLong(c, text); err != nil {
		return err
	}
	a.appendToSession(c.Sender().ID, "/search "+query, text)
	return nil
}

func (a *App) handleLabels(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	labels, err := a.gmail.GetLabels(ctx, c.Sender().ID)
	if err != nil {
		return c.Send("读取标签失败：" + err.Error())
	}
	if len(labels) == 0 {
		return c.Send("没有标签。")
	}
	var lines []string
	lines = append(lines, "标签列表：")
	for _, item := range labels {
		lines = append(lines, fmt.Sprintf("- %s (%s, %d)", item.Name, item.Type, item.MessagesTotal))
	}
	return a.sendLong(c, strings.Join(lines, "\n"))
}

func (a *App) handleDigest(c tele.Context) error {
	ctx, cancel := a.aiCtx()
	defer cancel()

	digest, err := a.ai.GenerateDailyDigest(ctx, c.Sender().ID)
	if err != nil {
		return c.Send("生成摘要失败：" + err.Error())
	}
	text := "每日摘要：\n\n" + digest
	if err := a.sendLong(c, text); err != nil {
		return err
	}
	a.appendToSession(c.Sender().ID, "/digest", text)
	return nil
}

func (a *App) handleSetDigest(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Send("用法：/setdigest 08:00 或 /setdigest 08:00,12:00,16:00,20:00")
	}
	raw := strings.TrimSpace(strings.Join(args, " "))
	var valid []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if !digestTimePattern.MatchString(t) {
			return c.Send("时间格式错误：" + t + "，示例：08:00")
		}
		valid = append(valid, t)
	}
	ctx, cancel := a.shortCtx()
	defer cancel()
	if err := a.store.SetDigestTimes(ctx, c.Sender().ID, valid); err != nil {
		return c.Send("设置失败：" + err.Error())
	}
	return c.Send("摘要时间已设置为：" + strings.Join(valid, ", "))
}

func (a *App) handleSetCheck(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Send("用法：/setcheck <minutes>")
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || minutes < 1 || minutes > 1440 {
		return c.Send("minutes 取值范围为 1-1440。")
	}
	ctx, cancel := a.shortCtx()
	defer cancel()
	if err = a.store.SetCheckInterval(ctx, c.Sender().ID, minutes); err != nil {
		return c.Send("设置失败：" + err.Error())
	}
	return c.Send(fmt.Sprintf("新邮件检查间隔已设置为 %d 分钟。", minutes))
}

func (a *App) handleCancelDigest(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()
	if err := a.store.SetDigestTimes(ctx, c.Sender().ID, nil); err != nil {
		return c.Send("取消失败：" + err.Error())
	}
	return c.Send("每日自动摘要已取消。")
}

func (a *App) handleAIPush(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Send("用法：/aipush on 或 /aipush off")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on", "1", "true":
		ctx, cancel := a.shortCtx()
		defer cancel()
		if err := a.store.SetAIPushEnabled(ctx, c.Sender().ID, true); err != nil {
			return c.Send("设置失败：" + err.Error())
		}
		return c.Send("✅ AI智能推送已开启。\n收到新邮件时，AI会判断是否重要，只推送重要邮件。")
	case "off", "0", "false":
		ctx, cancel := a.shortCtx()
		defer cancel()
		if err := a.store.SetAIPushEnabled(ctx, c.Sender().ID, false); err != nil {
			return c.Send("设置失败：" + err.Error())
		}
		return c.Send("🔕 AI智能推送已关闭。\n所有新邮件都会推送通知。")
	default:
		return c.Send("用法：/aipush on 或 /aipush off")
	}
}

func (a *App) handleCancelCheck(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()
	if err := a.store.SetCheckInterval(ctx, c.Sender().ID, 0); err != nil {
		return c.Send("取消失败：" + err.Error())
	}
	return c.Send("新邮件自动检查已停止。")
}

func (a *App) handleSchedule(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	if err := a.store.EnsureUser(ctx, c.Sender().ID); err != nil {
		return c.Send("读取计划失败。")
	}
	user, err := a.store.GetUser(ctx, c.Sender().ID)
	if err != nil {
		return c.Send("读取用户配置失败。")
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
	return c.Send(
		fmt.Sprintf(
			"当前配置：\n"+
				"- 授权状态: %s\n"+
				"- 新邮件检查: %s\n"+
				"- AI智能推送: %s\n"+
				"- 每日摘要时间: %s",
			auth, checkStr, aiPushStr, digestStr,
		),
	)
}

func (a *App) handleStatus(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()
	user, err := a.store.GetUser(ctx, c.Sender().ID)
	if err != nil {
		return c.Send("读取状态失败。")
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
	return c.Send(
		"*Bot 状态*\n"+
			"Gmail: "+authStr+"\n"+
			"新邮件检查: "+checkStr+"\n"+
			"AI智能推送: "+aiStr+"\n"+
			"每日摘要: "+digestStr,
		tele.ModeMarkdown,
	)
}

func (a *App) handleNewSession(c tele.Context) error {
	title := strings.TrimSpace(strings.Join(c.Args(), " "))
	ctx, cancel := a.shortCtx()
	defer cancel()

	session, err := a.store.CreateSession(ctx, c.Sender().ID, title)
	if err != nil {
		return c.Send("创建会话失败：" + err.Error())
	}
	return c.Send(fmt.Sprintf("已创建并切换到新会话：%s (%s)", session.Title, session.ID[:8]))
}

func (a *App) handleSessions(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	sessions, err := a.store.ListSessions(ctx, c.Sender().ID, 20)
	if err != nil {
		return c.Send("读取会话失败：" + err.Error())
	}
	if len(sessions) == 0 {
		return c.Send("暂无会话，发送 /new 创建。")
	}
	var lines []string
	lines = append(lines, "会话列表：")
	for _, s := range sessions {
		flag := " "
		if s.IsActive {
			flag = "*"
		}
		last := "-"
		if !s.LastActive.IsZero() {
			last = s.LastActive.Local().Format("01-02 15:04")
		}
		lines = append(lines, fmt.Sprintf("%s %s | %s | %s", flag, s.ID[:8], s.Title, last))
	}
	lines = append(lines, "切换示例：/switch <id前缀>")
	return a.sendLong(c, strings.Join(lines, "\n"))
}

func (a *App) handleSwitchSession(c tele.Context) error {
	args := c.Args()
	if len(args) == 0 {
		return c.Send("用法：/switch <id前缀>")
	}
	ctx, cancel := a.shortCtx()
	defer cancel()

	sessionID, err := a.store.ResolveSessionID(ctx, c.Sender().ID, args[0])
	if err != nil {
		if err == sql.ErrNoRows {
			return c.Send("没有找到匹配会话。")
		}
		return c.Send("会话前缀匹配不唯一，请输入更长前缀。")
	}
	if err = a.store.SwitchActiveSession(ctx, c.Sender().ID, sessionID); err != nil {
		return c.Send("切换会话失败。")
	}
	return c.Send("已切换会话：" + sessionID[:8])
}

func (a *App) handleClearSession(c tele.Context) error {
	ctx, cancel := a.shortCtx()
	defer cancel()

	if err := a.store.ClearActiveSessionMessages(ctx, c.Sender().ID); err != nil {
		return c.Send("清理会话失败：" + err.Error())
	}
	return c.Send("当前会话上下文已清空。")
}

func (a *App) handleFreeText(c tele.Context) error {
	text := strings.TrimSpace(c.Text())
	if text == "" || strings.HasPrefix(text, "/") {
		return nil
	}
	ctx, cancel := a.aiCtx()
	defer cancel()

	if err := a.store.EnsureUser(ctx, c.Sender().ID); err != nil {
		return c.Send("初始化用户失败，请稍后重试。")
	}
	reply, err := a.ai.HandleUserMessage(ctx, c.Sender().ID, text)
	if err != nil {
		return c.Send("AI 处理失败：" + err.Error())
	}
	return a.sendLong(c, reply)
}

func (a *App) shortCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func (a *App) aiCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(a.cfg.AITimeoutSec)*time.Second)
}

// appendToSession 将指令交互异步写入 AI 会话上下文，失败只记日志不影响主流程。
func (a *App) appendToSession(tgUserID int64, userMsg, assistantMsg string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := a.store.AppendActiveSessionMessage(ctx, tgUserID, "user", userMsg); err != nil {
			log.Printf("appendToSession user msg failed (uid=%d): %v", tgUserID, err)
			return
		}
		if _, err := a.store.AppendActiveSessionMessage(ctx, tgUserID, "assistant", assistantMsg); err != nil {
			log.Printf("appendToSession assistant msg failed (uid=%d): %v", tgUserID, err)
		}
	}()
}

func (a *App) sendLong(c tele.Context, text string) error {
	text = mdToTelegram(text)
	for _, chunk := range splitBySize(text, 3500) {
		if err := c.Send(chunk, tele.ModeMarkdown); err != nil {
			if err2 := c.Send(chunk); err2 != nil {
				return err2
			}
		}
	}
	return nil
}

func mdToTelegram(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			content := strings.ReplaceAll(strings.TrimSpace(m[1]), "*", "")
			if content != "" {
				lines[i] = "*" + content + "*"
			}
			continue
		}
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			lines[i] = "———"
			continue
		}
		if strings.HasPrefix(trimmed, "> ") {
			lines[i] = "┃ " + strings.TrimPrefix(trimmed, "> ")
			continue
		}
		if strings.HasPrefix(trimmed, ">") && len(trimmed) > 1 {
			lines[i] = "┃ " + strings.TrimPrefix(trimmed, ">")
			continue
		}
	}
	result := strings.Join(lines, "\n")
	result = boldRe.ReplaceAllString(result, "*$1*")
	result = strings.ReplaceAll(result, "⇒", "→")
	return result
}

func formatEmailList(title string, emails []gmail.EmailSummary) string {
	lines := []string{title + "："}
	for i, item := range emails {
		lines = append(lines,
			fmt.Sprintf(
				"%d) %s\nFrom: %s\nDate: %s\nID: %s\nSnippet: %s",
				i+1,
				item.Subject,
				item.From,
				item.Date,
				item.ID,
				trimForTelegram(item.Snippet, 200),
			),
		)
	}
	lines = append(lines, "\n使用 /read <ID> 查看正文")
	return strings.Join(lines, "\n\n")
}

func parseBoundedInt(args []string, def, min, max int) int {
	if len(args) == 0 {
		return def
	}
	v, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func splitBySize(text string, chunkSize int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}
	var chunks []string
	lines := strings.Split(text, "\n")
	var current strings.Builder
	for _, line := range lines {
		if current.Len()+len(line)+1 > chunkSize && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func trimForTelegram(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}
