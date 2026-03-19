package platform

import (
	"context"
	"reflect"
	"testing"
)

func TestCommandRouterHandleParsesSlashCommand(t *testing.T) {
	router := NewCommandRouter()
	router.Register(Command{
		Name:        "hello",
		Description: "say hello",
		Handler: func(ctx context.Context, msg UnifiedMessage, args []string) (UnifiedResponse, error) {
			if msg.Platform != "telegram" {
				t.Fatalf("unexpected platform: %s", msg.Platform)
			}
			if !reflect.DeepEqual(args, []string{"one", "two"}) {
				t.Fatalf("unexpected args: %#v", args)
			}
			return UnifiedResponse{Text: "hi"}, nil
		},
	})

	resp, handled, err := router.Handle(context.Background(), UnifiedMessage{
		Platform: "telegram",
		UserID:   "42",
		Text:     "/hello one two",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("expected command to be handled")
	}
	if resp.Text != "hi" {
		t.Fatalf("unexpected response text: %q", resp.Text)
	}
}

func TestCommandRouterIgnorePlainText(t *testing.T) {
	router := NewCommandRouter()
	resp, handled, err := router.Handle(context.Background(), UnifiedMessage{Text: "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handled {
		t.Fatal("expected plain text to skip command router")
	}
	if resp.Text != "" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}
