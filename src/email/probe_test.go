package email

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProbeCompletesEHLOWithoutSendingMail(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	commands := make(chan string, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = fmt.Fprint(connection, "220 relay.example.test ESMTP\r\n")
		line, _ := bufio.NewReader(connection).ReadString('\n')
		commands <- strings.TrimSpace(line)
		_, _ = fmt.Fprint(connection, "250 relay.example.test\r\n")
	}()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Probe(ctx, SMTPConfig{Host: "127.0.0.1", Port: port, TLS: "none"}, time.Second); err != nil {
		t.Fatalf("SMTP probe failed: %v", err)
	}
	select {
	case command := <-commands:
		if !strings.HasPrefix(strings.ToUpper(command), "EHLO ") && !strings.HasPrefix(strings.ToUpper(command), "HELO ") {
			t.Fatalf("first SMTP command = %q, want EHLO/HELO", command)
		}
	case <-ctx.Done():
		t.Fatal("SMTP probe did not send its greeting command")
	}
}

func TestSMTPCandidatePriority(t *testing.T) {
	hosts := smtpCandidateHosts(context.Background(), "mailhost.example.test")
	if len(hosts) < 4 || hosts[0] != "127.0.0.1" || hosts[1] != "172.17.0.1" {
		t.Fatalf("candidate order starts with %v", hosts)
	}
	if hosts[len(hosts)-2] != "mail.mailhost.example.test" || hosts[len(hosts)-1] != "smtp.mailhost.example.test" {
		t.Fatalf("mail subdomain candidates are not last: %v", hosts)
	}
}
