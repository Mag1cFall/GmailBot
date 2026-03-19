package gmail

import (
	"strings"
	"testing"

	"gmailbot/config"
)

func TestAuthCodeURLRequiresConfiguredOAuth(t *testing.T) {
	service := NewService(config.Config{}, nil)
	url := service.AuthCodeURL("state")
	if !strings.Contains(url, "GOOGLE_CLIENT_ID") || !strings.Contains(url, "GOOGLE_CLIENT_SECRET") {
		t.Fatalf("expected configuration guidance, got %q", url)
	}
}

func TestAuthCodeURLBuildsURLFromConfiguredOAuth(t *testing.T) {
	service := NewService(config.Config{
		GoogleClientID:     "client-id",
		GoogleClientSecret: "client-secret",
		OAuthRedirectURL:   "http://localhost/callback",
	}, nil)
	url := service.AuthCodeURL("state")
	if !strings.Contains(url, "client_id=client-id") {
		t.Fatalf("expected client id in auth url, got %q", url)
	}
	if !strings.Contains(url, "redirect_uri=http%3A%2F%2Flocalhost%2Fcallback") {
		t.Fatalf("expected redirect uri in auth url, got %q", url)
	}
}
