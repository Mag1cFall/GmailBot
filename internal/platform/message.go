package platform

type UnifiedMessage struct {
	Platform    string
	UserID      string
	SessionID   string
	Text        string
	Attachments []Attachment
	Extra       map[string]any
}

type UnifiedResponse struct {
	Text        string
	Markdown    bool
	Attachments []Attachment
}

type Attachment struct {
	Type string
	URL  string
	Data []byte
	Name string
}
