package email

import (
	"context"
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"
)

func TestEncodeMessageIncludesSafeMIMEParts(t *testing.T) {
	message, err := encodeMessage(Sender{Name: "Git Messages", Address: "sender@example.test", FQDN: "example.test"}, Message{
		To: "recipient@example.test", ReplyTo: "researcher@example.test", Subject: "Security report: café",
		Text: "The encrypted report is attached.", Attachments: []Attachment{{Name: "report.enc", ContentType: "application/octet-stream", Data: []byte("ciphertext")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(message)
	for _, header := range []string{"From: \"Git Messages\" <sender@example.test>", "To: recipient@example.test", "Reply-To: researcher@example.test", "MIME-Version: 1.0", "Auto-Submitted: auto-generated", "filename=\"report.enc\""} {
		if !strings.Contains(text, header) {
			t.Errorf("message does not contain %q", header)
		}
	}
	if !strings.Contains(text, base64.StdEncoding.EncodeToString([]byte("ciphertext"))) {
		t.Fatal("attachment payload is missing")
	}
}

func TestEncodeMessageRejectsHeaderInjection(t *testing.T) {
	if _, err := encodeMessage(Sender{Name: "sender", Address: "sender@example.test"}, Message{To: "to@example.test", Subject: "ok\r\nBcc: victim@example.test", Text: "body"}); err == nil {
		t.Fatal("header injection subject was accepted")
	}
}

func TestMailerConfigurationAndValidation(t *testing.T) {
	if (Mailer{}).Configured() {
		t.Fatal("empty mailer is configured")
	}
	mailer := Mailer{SMTP: SMTPConfig{Host: "mail.example.test", Port: 25}, Sender: Sender{Name: "Git Messages", Address: "sender@example.test"}}
	if !mailer.Configured() {
		t.Fatal("valid SMTP host and port are not configured")
	}
	for _, test := range []struct {
		name    string
		mailer  Mailer
		message Message
		wantErr string
	}{
		{"missing SMTP", Mailer{}, Message{To: "person@example.test", Subject: "Hello", Text: "body"}, "not configured"},
		{"bad recipient", mailer, Message{To: "bad", Subject: "Hello", Text: "body"}, "recipient"},
		{"bad reply-to", mailer, Message{To: "person@example.test", ReplyTo: "bad", Subject: "Hello", Text: "body"}, "reply-to"},
		{"bad subject", mailer, Message{To: "person@example.test", Subject: "Hello\r\nBcc: x@example.test", Text: "body"}, "subject"},
		{"bad sender", Mailer{SMTP: mailer.SMTP, Sender: Sender{Name: "sender", Address: "bad"}}, Message{To: "person@example.test", Subject: "Hello", Text: "body"}, "sender"},
		{"missing text", mailer, Message{To: "person@example.test", Subject: "Hello"}, "plain-text"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.mailer.Send(context.Background(), test.message); err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantErr) {
				t.Fatalf("Send error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestEmailAddressAndTLSHelpers(t *testing.T) {
	if _, err := parseAddress("Name <person@example.test>"); err != nil {
		t.Fatalf("valid mailbox rejected: %v", err)
	}
	for _, value := range []string{"", "missing-at", "person@example.test\r\nBcc: victim@example.test"} {
		if _, err := parseAddress(value); err == nil {
			t.Errorf("invalid mailbox %q was accepted", value)
		}
	}
	private := tlsConfig("127.0.0.1")
	public := tlsConfig("mail.example.test")
	if private.MinVersion != 0x0303 || !private.InsecureSkipVerify {
		t.Fatalf("private relay TLS config = %+v", private)
	}
	if public.MinVersion != 0x0303 || public.InsecureSkipVerify {
		t.Fatalf("public relay TLS config = %+v", public)
	}
	for _, value := range []string{"10.0.0.1", "127.0.0.1", "169.254.1.1", "100.64.1.1"} {
		if !isPrivateAddress(netip.MustParseAddr(value)) {
			t.Errorf("%s should be private or local", value)
		}
	}
	if isPrivateAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address was classified as private")
	}
}
