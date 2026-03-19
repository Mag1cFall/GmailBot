package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gmailbot/config"
	"gmailbot/internal/store"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

type Service struct {
	oauthConfig *oauth2.Config
	store       *store.Store
	httpClient  *http.Client
}

type EmailSummary struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	Subject  string `json:"subject"`
	From     string `json:"from"`
	Date     string `json:"date"`
	Snippet  string `json:"snippet"`
}

type EmailDetail struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"thread_id"`
	Subject  string   `json:"subject"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Date     string   `json:"date"`
	Snippet  string   `json:"snippet"`
	LabelIDs []string `json:"label_ids"`
	Body     string   `json:"body"`
}

type Label struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	MessagesTotal int64  `json:"messages_total"`
}

func NewService(cfg config.Config, st *store.Store) *Service {
	return &Service{
		oauthConfig: &oauth2.Config{
			ClientID:     strings.TrimSpace(cfg.GoogleClientID),
			ClientSecret: strings.TrimSpace(cfg.GoogleClientSecret),
			Endpoint: oauth2.Endpoint{
				AuthURL:   "https://accounts.google.com/o/oauth2/auth",
				TokenURL:  "https://oauth2.googleapis.com/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
			RedirectURL: cfg.OAuthRedirectURL,
			Scopes: []string{
				gmail.GmailModifyScope,
				gmail.GmailSendScope,
			},
		},
		store: st,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (s *Service) ensureOAuthConfigured() error {
	if strings.TrimSpace(s.oauthConfig.ClientID) == "" || strings.TrimSpace(s.oauthConfig.ClientSecret) == "" {
		return errors.New("gmail oauth is not configured, please set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET")
	}
	return nil
}

func (s *Service) AuthCodeURL(state string) string {
	if err := s.ensureOAuthConfigured(); err != nil {
		return err.Error()
	}
	return s.oauthConfig.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
	)
}

func (s *Service) ParseCode(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty code")
	}
	if strings.Contains(raw, "code=") {
		parsedURL, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("invalid redirect url: %w", err)
		}
		code := strings.TrimSpace(parsedURL.Query().Get("code"))
		if code == "" {
			return "", errors.New("code not found in redirect url")
		}
		return code, nil
	}
	return raw, nil
}

func (s *Service) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	if err := s.ensureOAuthConfigured(); err != nil {
		return nil, err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("empty oauth code")
	}
	token, err := s.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange oauth code failed: %w", err)
	}
	return token, nil
}

func (s *Service) SaveTokenForUser(
	ctx context.Context,
	tgUserID int64,
	email string,
	token *oauth2.Token,
) error {
	if token == nil {
		return errors.New("token is nil")
	}
	return s.store.SaveUserTokens(
		ctx,
		tgUserID,
		email,
		token.AccessToken,
		token.RefreshToken,
		token.Expiry,
	)
}

func (s *Service) GetProfileEmailByToken(ctx context.Context, token *oauth2.Token) (string, error) {
	if token == nil {
		return "", errors.New("token is nil")
	}
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	gmailSvc, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("init gmail service failed: %w", err)
	}
	profile, err := gmailSvc.Users.GetProfile("me").Do()
	if err != nil {
		return "", fmt.Errorf("get gmail profile failed: %w", err)
	}
	return strings.TrimSpace(profile.EmailAddress), nil
}

func (s *Service) ListEmails(ctx context.Context, tgUserID int64, limit int, query string) ([]EmailSummary, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	gmailSvc, _, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return nil, err
	}

	listCall := gmailSvc.Users.Messages.List("me").MaxResults(int64(limit))
	if strings.TrimSpace(query) != "" {
		listCall = listCall.Q(strings.TrimSpace(query))
	}
	listResp, err := listCall.Do()
	if err != nil {
		return nil, fmt.Errorf("list gmail messages failed: %w", err)
	}
	if len(listResp.Messages) == 0 {
		return []EmailSummary{}, nil
	}

	summaries := make([]EmailSummary, 0, len(listResp.Messages))
	for _, item := range listResp.Messages {
		msg, getErr := gmailSvc.Users.Messages.Get("me", item.Id).
			Format("metadata").
			MetadataHeaders("Subject", "From", "Date").
			Do()
		if getErr != nil {
			continue
		}
		headers := toHeaderMap(msg.Payload)
		summaries = append(summaries, EmailSummary{
			ID:       msg.Id,
			ThreadID: msg.ThreadId,
			Subject:  headers["subject"],
			From:     headers["from"],
			Date:     headers["date"],
			Snippet:  strings.TrimSpace(msg.Snippet),
		})
	}
	return summaries, nil
}

func (s *Service) ListUnread(ctx context.Context, tgUserID int64, limit int) ([]EmailSummary, error) {
	return s.ListEmails(ctx, tgUserID, limit, "is:unread")
}

func (s *Service) GetEmail(ctx context.Context, tgUserID int64, emailID string) (EmailDetail, error) {
	emailID = strings.TrimSpace(emailID)
	if emailID == "" {
		return EmailDetail{}, errors.New("email id is required")
	}
	gmailSvc, _, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return EmailDetail{}, err
	}
	msg, err := gmailSvc.Users.Messages.Get("me", emailID).Format("full").Do()
	if err != nil {
		return EmailDetail{}, fmt.Errorf("get gmail message failed: %w", err)
	}
	headers := toHeaderMap(msg.Payload)
	body := extractMessageBody(msg.Payload)
	if body == "" {
		body = strings.TrimSpace(msg.Snippet)
	}
	return EmailDetail{
		ID:       msg.Id,
		ThreadID: msg.ThreadId,
		Subject:  headers["subject"],
		From:     headers["from"],
		To:       headers["to"],
		Date:     headers["date"],
		Snippet:  strings.TrimSpace(msg.Snippet),
		LabelIDs: msg.LabelIds,
		Body:     strings.TrimSpace(body),
	}, nil
}

func (s *Service) GetLabels(ctx context.Context, tgUserID int64) ([]Label, error) {
	gmailSvc, _, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return nil, err
	}
	resp, err := gmailSvc.Users.Labels.List("me").Do()
	if err != nil {
		return nil, fmt.Errorf("list labels failed: %w", err)
	}
	out := make([]Label, 0, len(resp.Labels))
	for _, item := range resp.Labels {
		out = append(out, Label{
			ID:            item.Id,
			Name:          item.Name,
			Type:          item.Type,
			MessagesTotal: item.MessagesTotal,
		})
	}
	return out, nil
}

func (s *Service) Revoke(ctx context.Context, tgUserID int64) error {
	user, err := s.store.GetUser(ctx, tgUserID)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(user.RefreshToken)
	if token == "" {
		token = strings.TrimSpace(user.AccessToken)
	}
	if token == "" {
		return s.store.ClearUserTokens(ctx, tgUserID)
	}

	form := url.Values{}
	form.Set("token", token)
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://oauth2.googleapis.com/revoke",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("revoke failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return s.store.ClearUserTokens(ctx, tgUserID)
}

func (s *Service) MarshalEmailSummaries(emails []EmailSummary) string {
	data, err := json.Marshal(emails)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func (s *Service) gmailClientForUser(ctx context.Context, tgUserID int64) (*gmail.Service, store.User, error) {
	user, err := s.store.GetUser(ctx, tgUserID)
	if err != nil {
		return nil, store.User{}, err
	}
	if !user.IsAuthorized() {
		return nil, store.User{}, errors.New("user is not authorized, please run /auth first")
	}
	if err := s.ensureOAuthConfigured(); err != nil {
		return nil, store.User{}, err
	}

	token := &oauth2.Token{
		AccessToken:  user.AccessToken,
		RefreshToken: user.RefreshToken,
		Expiry:       user.TokenExpiry,
		TokenType:    "Bearer",
	}
	ts := s.oauthConfig.TokenSource(ctx, token)
	freshToken, err := ts.Token()
	if err != nil {
		return nil, store.User{}, fmt.Errorf("refresh token failed: %w", err)
	}

	if shouldPersistRefreshedToken(user, freshToken) {
		refresh := strings.TrimSpace(freshToken.RefreshToken)
		if refresh == "" {
			refresh = user.RefreshToken
		}
		if saveErr := s.store.SaveUserTokens(
			ctx,
			user.TgUserID,
			user.GmailAddress,
			freshToken.AccessToken,
			refresh,
			freshToken.Expiry,
		); saveErr != nil {
			return nil, store.User{}, saveErr
		}
	}

	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(freshToken))
	gmailSvc, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, store.User{}, fmt.Errorf("init gmail service failed: %w", err)
	}
	return gmailSvc, user, nil
}

func shouldPersistRefreshedToken(user store.User, token *oauth2.Token) bool {
	if token == nil {
		return false
	}
	if strings.TrimSpace(token.AccessToken) != strings.TrimSpace(user.AccessToken) {
		return true
	}
	if strings.TrimSpace(token.RefreshToken) != "" &&
		strings.TrimSpace(token.RefreshToken) != strings.TrimSpace(user.RefreshToken) {
		return true
	}
	if !token.Expiry.Equal(user.TokenExpiry) {
		return true
	}
	return false
}

func toHeaderMap(payload *gmail.MessagePart) map[string]string {
	headers := map[string]string{
		"subject": "(无主题)",
		"from":    "(未知发件人)",
		"to":      "",
		"date":    "",
	}
	if payload == nil {
		return headers
	}
	for _, h := range payload.Headers {
		key := strings.ToLower(strings.TrimSpace(h.Name))
		val := strings.TrimSpace(h.Value)
		if val == "" {
			continue
		}
		switch key {
		case "subject":
			headers["subject"] = val
		case "from":
			headers["from"] = val
		case "to":
			headers["to"] = val
		case "date":
			headers["date"] = val
		}
	}
	return headers
}

func extractMessageBody(part *gmail.MessagePart) string {
	if part == nil {
		return ""
	}

	if len(part.Parts) == 0 {
		return decodePartBody(part.MimeType, part.Body)
	}

	var plainText, htmlText string
	for _, child := range part.Parts {
		body := extractMessageBody(child)
		if strings.TrimSpace(body) == "" {
			continue
		}
		if strings.Contains(strings.ToLower(child.MimeType), "text/plain") {
			plainText += "\n" + body
			continue
		}
		htmlText += "\n" + body
	}
	if strings.TrimSpace(plainText) != "" {
		return strings.TrimSpace(plainText)
	}
	return strings.TrimSpace(htmlText)
}

func decodePartBody(mimeType string, body *gmail.MessagePartBody) string {
	if body == nil || strings.TrimSpace(body.Data) == "" {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(body.Data)
	if err != nil {
		return ""
	}
	content := string(raw)
	if strings.Contains(strings.ToLower(mimeType), "html") {
		content = html.UnescapeString(content)
		content = htmlTagPattern.ReplaceAllString(content, " ")
	}
	return strings.TrimSpace(content)
}

func (s *Service) SendEmail(ctx context.Context, tgUserID int64, to, subject, body string) (string, error) {
	gmailSvc, user, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return "", err
	}
	from := strings.TrimSpace(user.GmailAddress)
	raw := buildRawEmail(from, to, subject, "", "", body)
	msg := &gmail.Message{
		Raw: base64.URLEncoding.EncodeToString([]byte(raw)),
	}
	sent, err := gmailSvc.Users.Messages.Send("me", msg).Do()
	if err != nil {
		return "", fmt.Errorf("send email failed: %w", err)
	}
	return sent.Id, nil
}

func (s *Service) ReplyEmail(ctx context.Context, tgUserID int64, emailID, body string) (string, error) {
	gmailSvc, user, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return "", err
	}
	original, err := gmailSvc.Users.Messages.Get("me", emailID).Format("metadata").MetadataHeaders("Subject", "From", "To", "Message-ID").Do()
	if err != nil {
		return "", fmt.Errorf("get original email failed: %w", err)
	}
	headers := toHeaderMap(original.Payload)
	origFrom := headers["from"]
	origSubject := headers["subject"]
	messageID := ""
	for _, h := range original.Payload.Headers {
		if strings.EqualFold(h.Name, "Message-ID") {
			messageID = strings.TrimSpace(h.Value)
		}
	}

	replyTo := origFrom
	subject := origSubject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	from := strings.TrimSpace(user.GmailAddress)
	raw := buildRawEmail(from, replyTo, subject, messageID, "", body)

	msg := &gmail.Message{
		Raw:      base64.URLEncoding.EncodeToString([]byte(raw)),
		ThreadId: original.ThreadId,
	}
	sent, err := gmailSvc.Users.Messages.Send("me", msg).Do()
	if err != nil {
		return "", fmt.Errorf("reply email failed: %w", err)
	}
	return sent.Id, nil
}

func (s *Service) ForwardEmail(ctx context.Context, tgUserID int64, emailID, to string) (string, error) {
	gmailSvc, user, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return "", err
	}
	original, err := gmailSvc.Users.Messages.Get("me", emailID).Format("full").Do()
	if err != nil {
		return "", fmt.Errorf("get original email failed: %w", err)
	}
	headers := toHeaderMap(original.Payload)
	origSubject := headers["subject"]
	origFrom := headers["from"]
	origDate := headers["date"]
	origBody := extractMessageBody(original.Payload)
	if origBody == "" {
		origBody = strings.TrimSpace(original.Snippet)
	}

	subject := origSubject
	if !strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		subject = "Fwd: " + subject
	}

	body := fmt.Sprintf("---------- Forwarded message ----------\nFrom: %s\nDate: %s\nSubject: %s\n\n%s",
		origFrom, origDate, origSubject, origBody)

	from := strings.TrimSpace(user.GmailAddress)
	raw := buildRawEmail(from, to, subject, "", "", body)

	msg := &gmail.Message{
		Raw: base64.URLEncoding.EncodeToString([]byte(raw)),
	}
	sent, err := gmailSvc.Users.Messages.Send("me", msg).Do()
	if err != nil {
		return "", fmt.Errorf("forward email failed: %w", err)
	}
	return sent.Id, nil
}

func (s *Service) CreateLabel(ctx context.Context, tgUserID int64, name string) (*Label, error) {
	gmailSvc, _, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return nil, err
	}
	label, err := gmailSvc.Users.Labels.Create("me", &gmail.Label{
		Name:                  strings.TrimSpace(name),
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}).Do()
	if err != nil {
		return nil, fmt.Errorf("create label failed: %w", err)
	}
	return &Label{
		ID:   label.Id,
		Name: label.Name,
		Type: label.Type,
	}, nil
}

func (s *Service) DeleteLabel(ctx context.Context, tgUserID int64, labelID string) error {
	gmailSvc, _, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return err
	}
	if err := gmailSvc.Users.Labels.Delete("me", strings.TrimSpace(labelID)).Do(); err != nil {
		return fmt.Errorf("delete label failed: %w", err)
	}
	return nil
}

func (s *Service) ModifyMessageLabels(ctx context.Context, tgUserID int64, emailID string, addLabels, removeLabels []string) error {
	gmailSvc, _, err := s.gmailClientForUser(ctx, tgUserID)
	if err != nil {
		return err
	}
	req := &gmail.ModifyMessageRequest{
		AddLabelIds:    addLabels,
		RemoveLabelIds: removeLabels,
	}
	if _, err := gmailSvc.Users.Messages.Modify("me", strings.TrimSpace(emailID), req).Do(); err != nil {
		return fmt.Errorf("modify labels failed: %w", err)
	}
	return nil
}

func buildRawEmail(from, to, subject, inReplyTo, references, body string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	if inReplyTo != "" {
		sb.WriteString("In-Reply-To: " + inReplyTo + "\r\n")
		sb.WriteString("References: " + inReplyTo + "\r\n")
	}
	if references != "" {
		sb.WriteString("References: " + references + "\r\n")
	}
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}
