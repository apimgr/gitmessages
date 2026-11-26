package overlay

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

type I2PStatus struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	State    string `json:"state"`
	Hostname string `json:"hostname"`
	Provider string `json:"provider"`
	Address  string `json:"address,omitempty"`
	Error    string `json:"-"`
}

type I2PManager struct {
	mu        sync.RWMutex
	configDir string
	dataDir   string
	logsDir   string
	settings  config.I2PConfig
	handler   http.Handler
	status    I2PStatus
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	started   bool
	service   *i2pService
	listener  net.Listener
	server    *http.Server
}

type i2pService struct {
	address     string
	provider    string
	process     *exec.Cmd
	processDone chan struct{}
	samAddress  string
	session     net.Conn
	forward     net.Conn
}

type samReply struct {
	values map[string]string
}

var i2pBase64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~")

var errNoI2PProvider = errors.New("no I2P provider is available")

func NewI2PManager(configDir, dataDir, logsDir string, settings config.I2PConfig, handler http.Handler) *I2PManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &I2PManager{configDir: configDir, dataDir: dataDir, logsDir: logsDir, settings: settings, handler: handler, status: I2PStatus{Status: "disabled", State: "disabled", Provider: "none"}, ctx: ctx, cancel: cancel}
}

func (manager *I2PManager) Start() {
	if !manager.settings.Enabled {
		return
	}
	manager.mu.Lock()
	if manager.started || manager.ctx.Err() != nil {
		manager.mu.Unlock()
		return
	}
	manager.started = true
	manager.done = make(chan struct{})
	manager.status = I2PStatus{Enabled: true, Status: "starting", State: "starting", Provider: "none"}
	manager.mu.Unlock()
	go func() {
		defer close(manager.done)
		manager.run()
	}()
}

// Restart replaces the service lifetime context and starts a fresh provider session.
func (manager *I2PManager) Restart() error {
	if err := manager.Close(); err != nil {
		return err
	}
	manager.mu.Lock()
	manager.ctx, manager.cancel = context.WithCancel(context.Background())
	manager.started = false
	manager.done = nil
	manager.mu.Unlock()
	manager.Start()
	return nil
}

func (manager *I2PManager) run() {
	service, listener, err := manager.startService()
	if err != nil {
		log.Printf("I2P eepsite unavailable: %v", err)
		manager.mu.Lock()
		if errors.Is(err, errNoI2PProvider) {
			manager.status = I2PStatus{Status: "disabled", State: "no_provider", Provider: "none"}
		} else {
			manager.status.Status = "unavailable"
			manager.status.State = "error"
			manager.status.Error = err.Error()
		}
		manager.mu.Unlock()
		return
	}
	if manager.ctx.Err() != nil {
		_ = listener.Close()
		service.close()
		return
	}
	manager.mu.Lock()
	manager.service = service
	manager.listener = listener
	manager.server = &http.Server{Handler: manager.handler, ReadHeaderTimeout: 10 * time.Second}
	manager.status = I2PStatus{Enabled: true, Running: true, Status: "healthy", State: "running", Hostname: service.address, Provider: service.provider, Address: service.address}
	server := manager.server
	manager.mu.Unlock()
	log.Printf("I2P eepsite healthy: %s", service.address)

	serverResult := make(chan error, 1)
	go func() { serverResult <- server.Serve(listener) }()
	monitor := time.NewTicker(15 * time.Second)
	defer monitor.Stop()
	for {
		select {
		case <-manager.ctx.Done():
			return
		case <-service.processDone:
			if service.process != nil {
				manager.fail(fmt.Errorf("managed i2pd process exited: %v", service.process.ProcessState))
			}
			return
		case err := <-serverResult:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				manager.fail(fmt.Errorf("serve I2P backend: %w", err))
			}
			return
		case <-monitor.C:
			ctx, cancel := context.WithTimeout(manager.ctx, 5*time.Second)
			connection, _, err := samDial(ctx, service.samAddress)
			cancel()
			if err != nil {
				manager.fail(fmt.Errorf("I2P SAM provider became unavailable: %w", err))
				return
			}
			_ = connection.Close()
		}
	}
}

func (manager *I2PManager) startService() (*i2pService, net.Listener, error) {
	provider, binary, err := manager.resolveProvider()
	if err != nil {
		return nil, nil, err
	}
	for _, directory := range []string{filepath.Join(manager.configDir, "i2p"), filepath.Join(manager.dataDir, "i2p"), filepath.Join(manager.dataDir, "i2p", "site")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, nil, fmt.Errorf("create I2P directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, nil, fmt.Errorf("secure I2P directory: %w", err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("allocate I2P backend listener: %w", err)
	}
	service := &i2pService{provider: provider}
	manager.mu.Lock()
	manager.status.Provider = provider
	manager.mu.Unlock()
	samAddress := manager.settings.SAMAddress
	if provider == "i2pd" {
		samAddress, err = manager.startI2PD(binary, service)
		if err != nil {
			_ = listener.Close()
			service.close()
			return nil, nil, err
		}
	}
	service.samAddress = samAddress
	keyPath := filepath.Join(manager.dataDir, "i2p", "site", "site-keys.dat")
	service.address, err = createI2PSession(manager.ctx, samAddress, keyPath, listener.Addr().(*net.TCPAddr).Port, manager.settings, service)
	if err != nil {
		_ = listener.Close()
		service.close()
		return nil, nil, err
	}
	return service, listener, nil
}

func (manager *I2PManager) resolveProvider() (string, string, error) {
	if manager.settings.Binary != "" {
		binary, err := exec.LookPath(manager.settings.Binary)
		if err != nil {
			return "", "", fmt.Errorf("configured i2pd binary is unavailable: %w", err)
		}
		return "i2pd", binary, nil
	}
	if binary, err := exec.LookPath("i2pd"); err == nil {
		return "i2pd", binary, nil
	}
	ctx, cancel := context.WithTimeout(manager.ctx, 5*time.Second)
	defer cancel()
	connection, reader, err := samDial(ctx, manager.settings.SAMAddress)
	if err != nil {
		return "", "", errNoI2PProvider
	}
	_ = reader
	_ = connection.Close()
	return "sam", "", nil
}

func (manager *I2PManager) startI2PD(binary string, service *i2pService) (string, error) {
	configDirectory := filepath.Join(manager.configDir, "i2p")
	dataDirectory := filepath.Join(manager.dataDir, "i2p")
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate private I2P SAM port: %w", err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	logPath := filepath.Join(manager.logsDir, "i2pd.log")
	confPath := filepath.Join(configDirectory, "i2pd.conf")
	content := fmt.Sprintf("daemon = false\nipv4 = true\nipv6 = false\nnotransit = true\nfloodfill = false\nlog = file\nlogfile = %s\nloglevel = warn\n[http]\nenabled = false\n[httpproxy]\nenabled = false\n[socksproxy]\nenabled = false\n[sam]\nenabled = true\naddress = 127.0.0.1\nport = %d\n[bob]\nenabled = false\n[i2cp]\nenabled = false\n[i2pcontrol]\nenabled = false\n[upnp]\nenabled = false\n", strconv.Quote(logPath), port)
	if err := atomicPrivateFile(confPath, []byte(content)); err != nil {
		return "", fmt.Errorf("write dedicated i2pd configuration: %w", err)
	}
	tunnelConfig := filepath.Join(configDirectory, "tunnels.conf")
	if err := atomicPrivateFile(tunnelConfig, nil); err != nil {
		return "", fmt.Errorf("write empty I2P tunnel configuration: %w", err)
	}
	tunnelDirectory := filepath.Join(configDirectory, "tunnels.d")
	if err := os.MkdirAll(tunnelDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create empty I2P tunnel directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("open I2P log: %w", err)
	}
	command := exec.Command(binary, "--conf", confPath, "--tunconf", tunnelConfig, "--tunnelsdir", tunnelDirectory, "--datadir", dataDirectory)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("start dedicated i2pd process: %w", err)
	}
	service.process = command
	service.processDone = make(chan struct{})
	go func() {
		_ = command.Wait()
		_ = logFile.Close()
		close(service.processDone)
	}()
	address := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	for {
		ctx, cancel := context.WithTimeout(manager.ctx, 3*time.Second)
		connection, _, dialErr := samDial(ctx, address)
		cancel()
		if dialErr == nil {
			_ = connection.Close()
			return address, nil
		}
		select {
		case <-manager.ctx.Done():
			return "", manager.ctx.Err()
		case <-service.processDone:
			return "", fmt.Errorf("i2pd exited before its SAM bridge became ready: %v", command.ProcessState)
		case <-deadline.C:
			return "", fmt.Errorf("i2pd SAM bridge did not start within 60 seconds")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func atomicPrivateFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".i2p-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (service *i2pService) close() {
	if service.forward != nil {
		_ = service.forward.Close()
	}
	if service.session != nil {
		_ = service.session.Close()
	}
	if service.process != nil && service.process.Process != nil {
		_ = service.process.Process.Signal(os.Interrupt)
		select {
		case <-service.processDone:
		case <-time.After(10 * time.Second):
			_ = service.process.Process.Kill()
			<-service.processDone
		}
	}
}

func (manager *I2PManager) fail(err error) {
	manager.mu.Lock()
	manager.status.Status = "unavailable"
	manager.status.State = "error"
	manager.status.Running = false
	manager.status.Error = err.Error()
	service, listener, server := manager.service, manager.listener, manager.server
	manager.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if service != nil {
		service.close()
	}
}

func (manager *I2PManager) Status() I2PStatus {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.status
}

func (manager *I2PManager) Close() error {
	manager.cancel()
	manager.mu.Lock()
	done := manager.done
	manager.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			return fmt.Errorf("I2P shutdown did not finish within 15 seconds")
		}
	}
	manager.mu.Lock()
	service, listener, server := manager.service, manager.listener, manager.server
	manager.service = nil
	manager.status = I2PStatus{Status: "disabled", State: "disabled", Provider: "none"}
	manager.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if service != nil {
		service.close()
	}
	return nil
}

func createI2PSession(ctx context.Context, address, keyPath string, port int, settings config.I2PConfig, service *i2pService) (string, error) {
	connection, reader, err := samDial(ctx, address)
	if err != nil {
		return "", fmt.Errorf("connect to I2P SAM bridge: %w", err)
	}
	stopConnection := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopConnection()
	privateKey, err := loadOrCreateI2PKey(connection, reader, keyPath, settings.SignatureType)
	if err != nil {
		_ = connection.Close()
		return "", err
	}
	destination, err := i2pDestination(privateKey)
	if err != nil {
		_ = connection.Close()
		return "", err
	}
	identifier := fmt.Sprintf("gitmessages-%d", os.Getpid())
	command := fmt.Sprintf("SESSION CREATE STYLE=STREAM ID=%s DESTINATION=%s i2cp.leaseSetEncType=4 inbound.length=%d outbound.length=%d inbound.quantity=%d outbound.quantity=%d", identifier, privateKey, settings.InboundLength, settings.OutboundLength, settings.InboundQuantity, settings.OutboundQuantity)
	reply, err := sendSAM(connection, reader, time.Duration(settings.BootstrapTimeout)*time.Second, command)
	if err != nil || reply == nil || reply.values["RESULT"] != "OK" {
		_ = connection.Close()
		return "", fmt.Errorf("create I2P stream session: %s", safeSAMError(reply, err))
	}
	forward, forwardReader, err := samDial(ctx, address)
	if err != nil {
		_ = connection.Close()
		return "", fmt.Errorf("connect I2P stream forward: %w", err)
	}
	stopForward := context.AfterFunc(ctx, func() { _ = forward.Close() })
	defer stopForward()
	reply, err = sendSAM(forward, forwardReader, 10*time.Second, fmt.Sprintf("STREAM FORWARD ID=%s PORT=%d HOST=127.0.0.1 SILENT=true", identifier, port))
	if err != nil || reply == nil || reply.values["RESULT"] != "OK" {
		_ = forward.Close()
		_ = connection.Close()
		return "", fmt.Errorf("forward I2P streams: %s", safeSAMError(reply, err))
	}
	service.session = connection
	service.forward = forward
	return i2pAddress(destination), nil
}

func loadOrCreateI2PKey(connection net.Conn, reader *bufio.Reader, path string, signatureType int) (string, error) {
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("I2P destination key is not a regular file")
		}
		if info.Size() > 1<<20 {
			return "", fmt.Errorf("I2P destination key file is too large")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("secure I2P destination key: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect I2P destination key: %w", statErr)
	}
	contents, err := os.ReadFile(path)
	if err == nil {
		key := strings.TrimSpace(string(contents))
		if _, err := i2pDestination(key); err != nil {
			return "", fmt.Errorf("I2P destination key is corrupt: %w", err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read I2P destination key: %w", err)
	}
	reply, err := sendSAM(connection, reader, 10*time.Second, fmt.Sprintf("DEST GENERATE SIGNATURE_TYPE=%d", signatureType))
	if err != nil || reply == nil || reply.values["PRIV"] == "" {
		return "", fmt.Errorf("generate I2P destination key: %s", safeSAMError(reply, err))
	}
	key := reply.values["PRIV"]
	if _, err := i2pDestination(key); err != nil {
		return "", fmt.Errorf("SAM returned an invalid I2P destination key")
	}
	if err := atomicPrivateFile(path, []byte(key)); err != nil {
		return "", fmt.Errorf("persist I2P destination key: %w", err)
	}
	return key, nil
}

func i2pDestination(privateKey string) ([]byte, error) {
	raw, err := i2pBase64.DecodeString(privateKey)
	if err != nil || len(raw) < 387 {
		return nil, fmt.Errorf("invalid I2P destination key")
	}
	end := 387 + int(binary.BigEndian.Uint16(raw[385:387]))
	if len(raw) < end {
		return nil, fmt.Errorf("truncated I2P destination key")
	}
	return raw[:end], nil
}

func i2pAddress(destination []byte) string {
	digest := sha256.Sum256(destination)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:])) + ".b32.i2p"
}

func samDial(ctx context.Context, address string) (net.Conn, *bufio.Reader, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, nil, err
	}
	stopConnection := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopConnection()
	reader := bufio.NewReader(connection)
	reply, err := sendSAM(connection, reader, 10*time.Second, "HELLO VERSION MIN=3.1 MAX=3.3")
	if err != nil || reply == nil || reply.values["RESULT"] != "OK" {
		_ = connection.Close()
		return nil, nil, fmt.Errorf("I2P SAM handshake failed: %s", safeSAMError(reply, err))
	}
	return connection, reader, nil
}

func sendSAM(connection net.Conn, reader *bufio.Reader, timeout time.Duration, command string) (*samReply, error) {
	_ = connection.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = connection.SetDeadline(time.Time{}) }()
	if _, err := fmt.Fprintln(connection, command); err != nil {
		return nil, fmt.Errorf("SAM command write failed")
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("SAM command response failed: %w", err)
	}
	return parseSAM(line), nil
}

func parseSAM(line string) *samReply {
	reply := &samReply{values: map[string]string{}}
	line = strings.TrimSpace(line)
	if index := strings.Index(line, `MESSAGE="`); index >= 0 {
		reply.values["MESSAGE"] = strings.Trim(line[index+len(`MESSAGE=`):], `"`)
		line = strings.TrimSpace(line[:index])
	}
	for _, field := range strings.Fields(line) {
		if key, value, ok := strings.Cut(field, "="); ok {
			reply.values[key] = value
		}
	}
	return reply
}

func safeSAMError(reply *samReply, err error) string {
	if err != nil {
		return err.Error()
	}
	if reply == nil {
		return "empty SAM response"
	}
	message := strings.TrimSpace(reply.values["MESSAGE"])
	if message != "" {
		return message
	}
	return reply.values["RESULT"]
}
