package overlay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/cretz/bine/control"
	"github.com/pires/go-proxyproto"
)

type TorStatus struct {
	Enabled            bool   `json:"enabled"`
	Running            bool   `json:"running"`
	CircuitEstablished bool   `json:"circuit_established"`
	RequestsTotal      uint64 `json:"requests_total"`
	Status             string `json:"status"`
	Hostname           string `json:"hostname"`
	Backend            string `json:"-"`
	Error              string `json:"-"`
}

type TorManager struct {
	mu          sync.RWMutex
	configDir   string
	dataDir     string
	logsDir     string
	config      config.TorConfig
	status      TorStatus
	command     *exec.Cmd
	listener    net.Listener
	server      *http.Server
	logFile     *os.File
	processDone chan error
	controller  *control.Conn
	ctx         context.Context
	cancel      context.CancelFunc
}

var onionAddressPattern = regexp.MustCompile(`^[a-z2-7]{56}\.onion$`)

func NewTorManager(configDir, dataDir, logsDir string, settings config.TorConfig, handler http.Handler) *TorManager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &TorManager{
		configDir: configDir, dataDir: dataDir, logsDir: logsDir, config: settings,
		status: TorStatus{Status: "disabled"}, ctx: ctx, cancel: cancel,
	}
	manager.server = &http.Server{Handler: manager.trackTorRequests(handler), ReadHeaderTimeout: 10 * time.Second}
	return manager
}

func (manager *TorManager) trackTorRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manager.mu.Lock()
		manager.status.RequestsTotal++
		manager.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (manager *TorManager) Start() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.command != nil {
		return nil
	}
	binary, err := manager.resolveBinary()
	if err != nil {
		manager.status = TorStatus{Status: "disabled"}
		if manager.config.Binary != "" {
			return fmt.Errorf("configured Tor binary is unavailable: %w", err)
		}
		log.Printf("Tor not available")
		return nil
	}
	for _, directory := range []string{filepath.Join(manager.configDir, "tor"), filepath.Join(manager.dataDir, "tor"), filepath.Join(manager.dataDir, "tor", "site"), manager.logsDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create Tor directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure Tor directory: %w", err)
		}
	}
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("allocate Tor backend listener: %w", err)
	}
	manager.listener = &proxyproto.Listener{
		Listener:          backend,
		ReadHeaderTimeout: 10 * time.Second,
		ConnPolicy:        func(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) { return proxyproto.REQUIRE, nil },
	}
	manager.status.Backend = backend.Addr().String()
	torrcPath := filepath.Join(manager.configDir, "tor", "torrc")
	controlPath := filepath.Join(manager.dataDir, "tor", "control_port")
	_ = os.Remove(controlPath)
	torrc := manager.torrc(binary, controlPath)
	if err := os.WriteFile(torrcPath, []byte(torrc), 0o600); err != nil {
		_ = manager.listener.Close()
		return fmt.Errorf("write Tor configuration: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(manager.logsDir, "tor.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_ = manager.listener.Close()
		return fmt.Errorf("open Tor log: %w", err)
	}
	manager.logFile = logFile
	command := exec.Command(binary, "-f", torrcPath)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = manager.listener.Close()
		_ = logFile.Close()
		manager.logFile = nil
		return fmt.Errorf("start dedicated Tor process: %w", err)
	}
	manager.command = command
	manager.processDone = make(chan error, 1)
	manager.status = TorStatus{Enabled: true, Running: true, Status: "starting", Backend: backend.Addr().String()}
	go manager.serveBackend()
	go manager.watchProcess(command)
	go manager.initializeControl(controlPath)
	return nil
}

// Restart replaces the process and its canceled lifetime context.
func (manager *TorManager) Restart() error {
	if err := manager.Close(); err != nil {
		return err
	}
	manager.mu.Lock()
	manager.ctx, manager.cancel = context.WithCancel(context.Background())
	manager.status.Enabled = true
	manager.mu.Unlock()
	return manager.Start()
}

func (manager *TorManager) resolveBinary() (string, error) {
	if binary := strings.TrimSpace(manager.config.Binary); binary != "" {
		path, err := exec.LookPath(binary)
		if err != nil {
			return "", fmt.Errorf("find configured Tor binary: %w", err)
		}
		return path, nil
	}
	return exec.LookPath("tor")
}

func (manager *TorManager) torrc(binary, controlPath string) string {
	socks := "SocksPort 0"
	if manager.config.UseNetwork {
		socks = "SocksPort 127.0.0.1:auto"
	}
	safeLogging := 0
	if manager.config.SafeLogging {
		safeLogging = 1
	}
	siteDirectory := filepath.Join(manager.dataDir, "tor", "site")
	backendPort := manager.listener.Addr().(*net.TCPAddr).Port
	lines := []string{
		"DataDirectory " + torrcPath(filepath.Join(manager.dataDir, "tor")),
		"Log notice file " + torrcPath(filepath.Join(manager.logsDir, "tor.log")),
		socks,
		"ControlPort 127.0.0.1:auto",
		"ControlPortWriteToFile " + torrcPath(controlPath),
		"CookieAuthentication 1",
		"CookieAuthFile " + torrcPath(filepath.Join(manager.dataDir, "tor", "control_auth_cookie")),
		fmt.Sprintf("__OwningControllerProcess %d", os.Getpid()),
		fmt.Sprintf("SafeLogging %d", safeLogging),
		"ExitRelay 0", "ExitPolicy reject *:*", "DirPort 0",
		"MaxCircuitDirtiness 600",
		fmt.Sprintf("MaxClientCircuitsPending %d", manager.config.MaxCircuits),
		fmt.Sprintf("CircuitBuildTimeout %d", manager.config.CircuitTimeout),
		fmt.Sprintf("BandwidthRate %s", manager.config.BandwidthRate),
		fmt.Sprintf("BandwidthBurst %s", manager.config.BandwidthBurst),
		fmt.Sprintf("AccountingMax %s", manager.config.MaxMonthlyBandwidth),
		"AccountingStart month 1 00:00",
		"VanguardsLiteEnabled 1",
		"HiddenServiceDir " + torrcPath(siteDirectory),
		"HiddenServiceVersion 3",
		fmt.Sprintf("HiddenServiceNumIntroductionPoints %d", manager.config.NumIntroPoints),
		fmt.Sprintf("HiddenServicePort %d 127.0.0.1:%d", manager.config.VirtualPort, backendPort),
		"HiddenServiceExportCircuitID haproxy",
		"# Tor binary: " + binary,
	}
	return strings.Join(lines, "\n") + "\n"
}

func torrcPath(path string) string {
	return "\"" + strings.ReplaceAll(path, "\"", "\\\"") + "\""
}

func waitTorControlFile(ctx context.Context, path string, timeout time.Duration) (string, string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var port, cookie string
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "PORT=127.0.0.1:") {
					port = strings.TrimPrefix(line, "PORT=127.0.0.1:")
				}
				if strings.HasPrefix(line, "COOKIEFILE=") {
					cookie = strings.Trim(strings.TrimPrefix(line, "COOKIEFILE="), `"`)
				}
			}
			if portNumber, parseErr := strconv.Atoi(port); parseErr == nil && portNumber > 0 && portNumber < 65536 && cookie != "" {
				return "127.0.0.1:" + port, cookie, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-deadline.C:
			return "", "", fmt.Errorf("tor control port was not ready within %s", timeout)
		case <-ticker.C:
		}
	}
}

func (manager *TorManager) initializeControl(path string) {
	address, _, err := waitTorControlFile(manager.ctx, path, time.Duration(manager.config.StartupTimeout)*time.Second)
	if err != nil {
		manager.fail(err)
		return
	}
	connection, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		manager.fail(fmt.Errorf("connect Tor control port: %w", err))
		return
	}
	controller := control.NewConn(textproto.NewConn(connection))
	if err := controller.Authenticate(""); err != nil {
		_ = controller.Close()
		manager.fail(fmt.Errorf("authenticate Tor control connection: %w", err))
		return
	}
	if err := controller.TakeOwnership(); err != nil {
		_ = controller.Close()
		manager.fail(fmt.Errorf("take ownership of Tor process: %w", err))
		return
	}
	manager.mu.Lock()
	if manager.ctx.Err() != nil {
		manager.mu.Unlock()
		_ = controller.Close()
		return
	}
	manager.controller = controller
	manager.mu.Unlock()
	manager.watchBootstrap(controller, filepath.Join(manager.dataDir, "tor", "site", "hostname"), time.Duration(manager.config.BootstrapTimeout)*time.Second)
}

func (manager *TorManager) watchBootstrap(controller *control.Conn, hostnamePath string, timeout time.Duration) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		information, err := controller.GetInfo("status/bootstrap-phase")
		if err == nil && len(information) > 0 && strings.Contains(information[0].Val, "PROGRESS=100") {
			data, readErr := os.ReadFile(hostnamePath)
			if readErr == nil {
				hostname := strings.TrimSpace(string(data))
				if onionAddressPattern.MatchString(hostname) {
					circuitEstablished, circuitKnown := torCircuitEstablished(controller)
					manager.mu.Lock()
					manager.status.Status = "healthy"
					manager.status.Hostname = hostname
					if circuitKnown {
						manager.status.CircuitEstablished = circuitEstablished
					}
					manager.mu.Unlock()
					go manager.watchCircuitStatus(controller)
					log.Printf("Tor hidden service healthy: %s", hostname)
					return
				}
			}
		}
		select {
		case <-manager.ctx.Done():
			return
		case <-deadline.C:
			manager.fail(fmt.Errorf("tor hidden service did not bootstrap within %s", timeout))
			return
		case <-ticker.C:
		}
	}
}

func torCircuitEstablished(controller *control.Conn) (bool, bool) {
	information, err := controller.GetInfo("status/circuit-established")
	if err != nil || len(information) == 0 {
		return false, false
	}
	return strings.TrimSpace(information[0].Val) == "1", true
}

func (manager *TorManager) watchCircuitStatus(controller *control.Conn) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-manager.ctx.Done():
			return
		case <-ticker.C:
			if established, ok := torCircuitEstablished(controller); ok {
				manager.mu.Lock()
				manager.status.CircuitEstablished = established
				manager.mu.Unlock()
			}
		}
	}
}

func (manager *TorManager) serveBackend() {
	if err := manager.server.Serve(manager.listener); err != nil && err != http.ErrServerClosed {
		manager.fail(fmt.Errorf("serve Tor backend: %w", err))
	}
}

func (manager *TorManager) fail(err error) {
	manager.mu.Lock()
	manager.status.Status = "unavailable"
	manager.status.Error = err.Error()
	command := manager.command
	server := manager.server
	listener := manager.listener
	manager.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
}

func (manager *TorManager) watchProcess(command *exec.Cmd) {
	err := command.Wait()
	manager.processDone <- err
	manager.mu.Lock()
	if manager.command != command {
		manager.mu.Unlock()
		return
	}
	manager.command = nil
	wasActive := manager.status.Status == "healthy" || manager.status.Status == "starting"
	manager.status.Running = false
	manager.status.CircuitEstablished = false
	manager.status.Status = "disabled"
	if manager.ctx.Err() == nil {
		manager.status.Status = "unavailable"
		if err != nil {
			manager.status.Error = "Tor process exited"
		}
	}
	shouldRestart := wasActive && manager.ctx.Err() == nil
	manager.mu.Unlock()
	if shouldRestart {
		go func() {
			log.Printf("WARN Tor process exited after becoming healthy; monitor restarting Tor")
			if restartErr := manager.Restart(); restartErr != nil {
				log.Printf("WARN Tor monitor restart failed: %v", restartErr)
			}
		}()
	}
}

func (manager *TorManager) Status() TorStatus {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.status
}

func (manager *TorManager) Close() error {
	manager.cancel()
	manager.mu.Lock()
	command := manager.command
	done := manager.processDone
	controller := manager.controller
	manager.controller = nil
	manager.command = nil
	listener := manager.listener
	server := manager.server
	logFile := manager.logFile
	manager.status.Running = false
	manager.status.CircuitEstablished = false
	manager.mu.Unlock()
	var result error
	if controller != nil {
		if err := controller.Close(); err != nil {
			result = err
		}
	}
	if command != nil && command.Process != nil {
		if controller == nil {
			_ = command.Process.Signal(os.Interrupt)
		}
		select {
		case err := <-done:
			if err != nil && !strings.Contains(err.Error(), "signal: interrupt") {
				result = err
			}
		case <-time.After(10 * time.Second):
			result = command.Process.Kill()
			if waitErr := <-done; waitErr != nil {
				result = errors.Join(result, waitErr)
			}
		}
	}
	if server != nil {
		_ = server.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if logFile != nil {
		if err := logFile.Close(); result == nil {
			result = err
		}
	}
	manager.mu.Lock()
	manager.status.Status = "disabled"
	manager.status.Enabled = false
	manager.status.Hostname = ""
	manager.mu.Unlock()
	return result
}
