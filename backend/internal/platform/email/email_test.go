package email

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSMTPSendsToMailpit sends a real message through Mailpit (docker compose up -d) and reads it
// back through Mailpit's API.
func TestSMTPSendsToMailpit(t *testing.T) {
	subject := "NMS test " + time.Now().Format(time.RFC3339Nano)
	sender := SMTP{Addr: "localhost:1025", From: "NMS <no-reply@localhost>"}

	err := sender.Send(t.Context(), Message{To: "owner@example.com", Subject: subject, Text: "Hello\nfrom NMS"})
	if err != nil {
		t.Fatalf("send (is Mailpit running? docker compose up -d): %v", err)
	}

	response, err := http.Get("http://localhost:8025/api/v1/search?query=" + strings.ReplaceAll(`subject:"`+subject+`"`, " ", "%20"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var found struct {
		Messages []struct {
			Subject string
			To      []struct{ Address string }
		} `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&found); err != nil {
		t.Fatal(err)
	}
	if len(found.Messages) != 1 || found.Messages[0].To[0].Address != "owner@example.com" {
		t.Errorf("Mailpit has %+v, want one message to owner@example.com", found.Messages)
	}
}
