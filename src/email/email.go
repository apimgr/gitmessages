// Package email implements safe RFC 5322 messages over configured SMTP.
package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host, Username, Password, TLS string
	Port                          int
}

type Sender struct{ Name, Address, FQDN string }

type Message struct {
	To, ReplyTo, Subject string
	Text, HTML           string
	Attachments          []Attachment
}

type Attachment struct {
	Name, ContentType string
	Data              []byte
}

type Mailer struct {
	SMTP    SMTPConfig
	Sender  Sender
	Timeout time.Duration
}

func (m Mailer) Configured() bool { return strings.TrimSpace(m.SMTP.Host) != "" && m.SMTP.Port > 0 }

func (m Mailer) Send(ctx context.Context, message Message) error {
	if strings.TrimSpace(m.SMTP.Host) == "" || m.SMTP.Port < 1 || m.SMTP.Port > 65535 {
		return errors.New("SMTP is not configured")
	}
	recipient, err := parseAddress(message.To)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if message.ReplyTo != "" {
		if _, err := parseAddress(message.ReplyTo); err != nil {
			return fmt.Errorf("invalid reply-to: %w", err)
		}
	}
	if strings.TrimSpace(message.Subject) == "" || strings.ContainsAny(message.Subject, "\r\n") {
		return errors.New("invalid subject")
	}
	if strings.ContainsAny(m.Sender.Name, "\r\n") {
		return errors.New("invalid sender name")
	}
	sender, err := parseAddress(m.Sender.Address)
	if err != nil {
		return fmt.Errorf("invalid sender: %w", err)
	}
	if strings.TrimSpace(message.Text) == "" {
		return errors.New("plain-text message is required")
	}
	body, err := encodeMessage(m.Sender, message)
	if err != nil {
		return err
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	address := net.JoinHostPort(m.SMTP.Host, strconv.Itoa(m.SMTP.Port))
	var conn net.Conn
	if m.SMTP.TLS == "tls" || m.SMTP.TLS == "auto" && m.SMTP.Port == 465 {
		conn, err = tls.DialWithDialer(&dialer, "tcp", address, tlsConfig(m.SMTP.Host))
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	client, err := smtp.NewClient(conn, m.SMTP.Host)
	if err != nil {
		return fmt.Errorf("start SMTP: %w", err)
	}
	defer func() { _ = client.Close() }()
	mode := m.SMTP.TLS
	if mode == "" {
		mode = "auto"
	}
	if mode == "auto" || mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig(m.SMTP.Host)); err != nil {
				return fmt.Errorf("SMTP STARTTLS: %w", err)
			}
		} else if mode == "starttls" || mode == "auto" && (m.SMTP.Username != "" || m.SMTP.Password != "") {
			return errors.New("SMTP server does not offer required STARTTLS")
		}
	} else if mode != "tls" && mode != "none" {
		return errors.New("invalid SMTP TLS mode")
	}
	if m.SMTP.Username != "" || m.SMTP.Password != "" {
		if _, ok := client.TLSConnectionState(); !ok {
			return errors.New("SMTP authentication requires TLS")
		}
		if err := client.Auth(smtp.PlainAuth("", m.SMTP.Username, m.SMTP.Password, m.SMTP.Host)); err != nil {
			return fmt.Errorf("SMTP authentication: %w", err)
		}
	}
	if err := client.Mail(sender.Address); err != nil {
		return err
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func parseAddress(value string) (*mail.Address, error) {
	if strings.ContainsAny(value, "\r\n") {
		return nil, errors.New("CR/LF is not allowed")
	}
	parsed, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil || parsed.Address == "" {
		return nil, errors.New("invalid address")
	}
	return parsed, nil
}

func encodeMessage(sender Sender, message Message) ([]byte, error) {
	if strings.TrimSpace(message.Subject) == "" || strings.ContainsAny(message.Subject, "\r\n") {
		return nil, errors.New("invalid subject")
	}
	if strings.ContainsAny(sender.Name, "\r\n") || strings.ContainsAny(sender.Address, "\r\n") || strings.ContainsAny(message.To, "\r\n") || strings.ContainsAny(message.ReplyTo, "\r\n") {
		return nil, errors.New("invalid message header")
	}
	if message.HTML == "" {
		message.HTML = "<p>" + strings.ReplaceAll(strings.ReplaceAll(message.Text, "&", "&amp;"), "<", "&lt;") + "</p>"
	}
	boundaryBytes := make([]byte, 18)
	if _, err := rand.Read(boundaryBytes); err != nil {
		return nil, err
	}
	messageID := make([]byte, 16)
	if _, err := rand.Read(messageID); err != nil {
		return nil, err
	}
	fqdn := strings.TrimSpace(sender.FQDN)
	if fqdn == "" {
		fqdn = "localhost"
	}
	from := (&mail.Address{Name: sender.Name, Address: sender.Address}).String()
	subject := mime.QEncoding.Encode("UTF-8", message.Subject)
	date := time.Now().Format(time.RFC1123Z)
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nAuto-Submitted: auto-generated\r\n", from, message.To, subject, date, hex.EncodeToString(messageID), fqdn)
	if message.ReplyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", message.ReplyTo)
	}
	boundary := hex.EncodeToString(boundaryBytes)
	if len(message.Attachments) > 0 {
		outer := boundary + "-mixed"
		fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n--%s\r\n", outer, outer)
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)
	}
	if len(message.Attachments) == 0 {
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)
	}
	for _, part := range []struct{ kind, value string }{{"text/plain", message.Text}, {"text/html", message.HTML}} {
		b.WriteString("--" + boundary + "\r\nContent-Type: " + part.kind + "; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		encoded := base64.StdEncoding.EncodeToString([]byte(part.value))
		for len(encoded) > 76 {
			b.WriteString(encoded[:76] + "\r\n")
			encoded = encoded[76:]
		}
		b.WriteString(encoded + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	if len(message.Attachments) > 0 {
		outer := boundary + "-mixed"
		for _, attachment := range message.Attachments {
			name := strings.ReplaceAll(strings.ReplaceAll(attachment.Name, "\r", ""), "\n", "")
			kind := attachment.ContentType
			if kind == "" {
				kind = "application/octet-stream"
			}
			fmt.Fprintf(&b, "--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"%s\"\r\n\r\n", outer, kind, name)
			encoded := base64.StdEncoding.EncodeToString(attachment.Data)
			for len(encoded) > 76 {
				b.WriteString(encoded[:76] + "\r\n")
				encoded = encoded[76:]
			}
			b.WriteString(encoded + "\r\n")
		}
		b.WriteString("--" + outer + "--\r\n")
	}
	return []byte(b.String()), nil
}

func tlsConfig(host string) *tls.Config {
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	address, err := netip.ParseAddr(host)
	if err == nil && isPrivateAddress(address) {
		// Private relay certificates often have no IP SAN; encryption remains enabled.
		config.InsecureSkipVerify = true //nolint:gosec
	}
	return config
}

func isPrivateAddress(address netip.Addr) bool {
	shared := netip.MustParsePrefix("100.64.0.0/10")
	return address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || shared.Contains(address)
}
