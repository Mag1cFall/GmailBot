// 统一消息格式
package platform

// UnifiedMessage 统一入站消息
type UnifiedMessage struct {
	Platform    string
	UserID      string
	SessionID   string
	Text        string
	Attachments []Attachment
	Extra       map[string]any
}

// UnifiedResponse 统一出站响应
type UnifiedResponse struct {
	Text        string       `json:"text"`
	Markdown    bool         `json:"markdown,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Actions     []Action     `json:"actions,omitempty"`
}

type Action struct {
	Label  string `json:"label"`
	Action string `json:"action"`
}

// Attachment 附件
type Attachment struct {
	Type string
	URL  string
	Data []byte
	Name string
}
