package overlay

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

func TestCreateI2PSessionPersistsKeyAndForwards(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	private := i2pBase64.EncodeToString(make([]byte, 387))
	commands := make(chan string, 5)
	go func() {
		for accepted := 0; accepted < 2; accepted++ {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(connection net.Conn) {
				defer func() { _ = connection.Close() }()
				reader := bufio.NewReader(connection)
				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil {
						return
					}
					command := strings.TrimSpace(line)
					commands <- command
					switch {
					case strings.HasPrefix(command, "HELLO "):
						_, _ = fmt.Fprintln(connection, "HELLO REPLY RESULT=OK VERSION=3.3")
					case strings.HasPrefix(command, "DEST GENERATE"):
						_, _ = fmt.Fprintf(connection, "DEST REPLY PUB=public PRIV=%s\n", private)
					case strings.HasPrefix(command, "SESSION CREATE"):
						_, _ = fmt.Fprintln(connection, "SESSION STATUS RESULT=OK")
					case strings.HasPrefix(command, "STREAM FORWARD"):
						_, _ = fmt.Fprintln(connection, "STREAM STATUS RESULT=OK")
						return
					}
				}
			}(connection)
		}
	}()

	directory := projectTestDir(t)
	service := &i2pService{}
	settings := config.DefaultConfig().Server.I2P
	address, err := createI2PSession(context.Background(), listener.Addr().String(), filepath.Join(directory, "site-keys.dat"), 12345, settings, service)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(address, ".b32.i2p") || service.session == nil || service.forward == nil {
		t.Fatalf("I2P session = address %q, session %v, forward %v", address, service.session, service.forward)
	}
	info, err := os.Stat(filepath.Join(directory, "site-keys.dat"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("destination key mode/stat = %v, %v", info, err)
	}
	_ = service.forward.Close()
	_ = service.session.Close()
	_ = listener.Close()
	seen := map[string]bool{}
	for range 5 {
		seen[<-commands] = true
	}
	if !seen["HELLO VERSION MIN=3.1 MAX=3.3"] || !seen["SESSION CREATE STYLE=STREAM ID=gitmessages-"+fmt.Sprint(os.Getpid())+" DESTINATION="+private+" i2cp.leaseSetEncType=4 inbound.length=3 outbound.length=3 inbound.quantity=5 outbound.quantity=5"] {
		t.Fatalf("SAM commands were incomplete: %#v", seen)
	}
	forwardFound := false
	for command := range seen {
		if strings.HasPrefix(command, "STREAM FORWARD") && strings.Contains(command, "SILENT=true") {
			forwardFound = true
		}
	}
	if !forwardFound {
		t.Fatalf("SAM forward command missing SILENT=true: %#v", seen)
	}
}

func TestParseSAMReply(t *testing.T) {
	reply := parseSAM(`SESSION STATUS RESULT=I2P_ERROR MESSAGE="destination unavailable"`)
	if reply.values["RESULT"] != "I2P_ERROR" {
		t.Fatalf("RESULT = %q, want I2P_ERROR", reply.values["RESULT"])
	}
	if reply.values["MESSAGE"] != "destination unavailable" {
		t.Fatalf("MESSAGE = %q, want full message", reply.values["MESSAGE"])
	}
}

func TestI2PAddressUsesDestinationHash(t *testing.T) {
	address := i2pAddress([]byte("destination"))
	if !strings.HasSuffix(address, ".b32.i2p") {
		t.Fatalf("address %q has no I2P suffix", address)
	}
	if len(strings.TrimSuffix(address, ".b32.i2p")) != 52 {
		t.Fatalf("base32 address length = %d, want 52", len(strings.TrimSuffix(address, ".b32.i2p")))
	}
	if strings.ToLower(address) != address {
		t.Fatalf("address %q is not lowercase", address)
	}
}

func TestI2PManagerDisabledDoesNotAllocateResources(t *testing.T) {
	manager := NewI2PManager(projectTestDir(t), projectTestDir(t), projectTestDir(t), config.I2PConfig{Enabled: false}, nil)
	manager.Start()
	status := manager.Status()
	if status.Status != "disabled" || status.Enabled || status.Running {
		t.Fatalf("disabled I2P status = %#v", status)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close disabled manager: %v", err)
	}
}

func TestI2PProviderResolutionDoesNotAllocateBackendWithoutProvider(t *testing.T) {
	manager := NewI2PManager(projectTestDir(t), projectTestDir(t), projectTestDir(t), config.I2PConfig{
		Enabled: true, Binary: "/missing/i2pd", SAMAddress: "127.0.0.1:1",
	}, nil)
	_, _, err := manager.startService()
	if err == nil {
		t.Fatal("expected unavailable provider error")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func TestI2PStartReportsProviderFailureAndCloses(t *testing.T) {
	manager := NewI2PManager(projectTestDir(t), projectTestDir(t), projectTestDir(t), config.I2PConfig{
		Enabled: true, Binary: "/missing/i2pd", SAMAddress: "127.0.0.1:1",
	}, nil)
	manager.Start()
	manager.mu.RLock()
	done := manager.done
	manager.mu.RUnlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("I2P startup did not finish after provider failure")
	}
	if status := manager.Status(); status.Status != "unavailable" || status.Running {
		t.Fatalf("provider failure status = %#v", status)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestI2PDestinationRejectsMalformedKey(t *testing.T) {
	if _, err := i2pDestination("invalid"); err == nil {
		t.Fatal("expected malformed destination key error")
	}
}

func TestSAMDialRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := samDial(ctx, "127.0.0.1:7656"); err == nil {
		t.Fatal("expected cancelled SAM dial to fail")
	}
}

func TestI2PKeyParsingAtomicStorageAndErrors(t *testing.T) {
	private := make([]byte, 389)
	binary.BigEndian.PutUint16(private[385:387], 2)
	encoded := i2pBase64.EncodeToString(private)
	destination, err := i2pDestination(encoded)
	if err != nil || len(destination) != 389 {
		t.Fatalf("parsed destination length=%d err=%v", len(destination), err)
	}
	if _, err := i2pDestination(i2pBase64.EncodeToString(make([]byte, 387))); err != nil {
		t.Fatalf("zero-certificate destination rejected: %v", err)
	}
	truncated := make([]byte, 387)
	binary.BigEndian.PutUint16(truncated[385:387], 4)
	if _, err := i2pDestination(i2pBase64.EncodeToString(truncated)); err == nil {
		t.Fatal("truncated destination key was accepted")
	}
	path := filepath.Join(projectTestDir(t), "private", "site-keys.dat")
	if err := atomicPrivateFile(path, []byte("private destination")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode/stat = %v, %v", info, err)
	}
	if err := atomicPrivateFile(path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "replacement" {
		t.Fatalf("atomic replacement = %q, err=%v", contents, err)
	}
	if got := safeSAMError(nil, nil); got != "empty SAM response" {
		t.Fatalf("empty SAM error = %q", got)
	}
	if got := safeSAMError(nil, errors.New("network failed")); got != "network failed" {
		t.Fatalf("SAM transport error = %q", got)
	}
}

func TestSendSAMParsesReplyAndHandlesClosedConnection(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() {
		defer func() { _ = server.Close() }()
		reader := bufio.NewReader(server)
		_, _ = reader.ReadString('\n')
		_, _ = fmt.Fprintln(server, `SESSION STATUS RESULT=OK MESSAGE="ready"`)
	}()
	reply, err := sendSAM(client, bufio.NewReader(client), time.Second, "SESSION STATUS")
	if err != nil || reply.values["RESULT"] != "OK" || reply.values["MESSAGE"] != "ready" {
		t.Fatalf("SAM reply=%#v err=%v", reply, err)
	}
	closed, reader := net.Pipe()
	_ = reader.Close()
	if _, err := sendSAM(closed, bufio.NewReader(closed), time.Second, "SESSION STATUS"); err == nil {
		t.Fatal("write to a closed SAM socket succeeded")
	}
	_ = closed.Close()
}
