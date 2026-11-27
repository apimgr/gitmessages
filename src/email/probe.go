package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/smtp"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Probe checks relay reachability and its SMTP greeting without sending mail.
func Probe(ctx context.Context, configuration SMTPConfig, timeout time.Duration) error {
	if strings.TrimSpace(configuration.Host) == "" || configuration.Port < 1 || configuration.Port > 65535 {
		return errors.New("SMTP is not configured")
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	address := net.JoinHostPort(configuration.Host, strconv.Itoa(configuration.Port))
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(timeout))
	if configuration.TLS == "tls" || configuration.TLS == "auto" && configuration.Port == 465 {
		tlsConnection := tls.Client(connection, tlsConfig(configuration.Host))
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("SMTP implicit TLS failed: %w", err)
		}
		connection = tlsConnection
	}
	client, err := smtp.NewClient(connection, configuration.Host)
	if err != nil {
		return fmt.Errorf("SMTP EHLO failed: %w", err)
	}
	defer func() { _ = client.Close() }()
	clientName, _ := os.Hostname()
	if clientName == "" {
		clientName = "gitmessages.invalid"
	}
	if err := client.Hello(clientName); err != nil {
		return fmt.Errorf("SMTP EHLO failed: %w", err)
	}
	mode := configuration.TLS
	if mode == "" {
		mode = "auto"
	}
	if mode == "auto" || mode == "starttls" {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(tlsConfig(configuration.Host)); err != nil {
				return fmt.Errorf("SMTP STARTTLS failed: %w", err)
			}
		} else if mode == "starttls" || mode == "auto" && (configuration.Username != "" || configuration.Password != "") {
			return errors.New("SMTP server does not offer required STARTTLS")
		}
	} else if mode != "tls" && mode != "none" {
		return errors.New("invalid SMTP TLS mode")
	}
	return nil
}

// DetectSMTP tries the AI.md host and port priority order and returns the first
// relay that completes an SMTP greeting and TLS negotiation when available.
func DetectSMTP(ctx context.Context, fqdn string, configuration SMTPConfig) (SMTPConfig, error) {
	hosts := smtpCandidateHosts(ctx, fqdn)
	for _, host := range hosts {
		for _, port := range []int{25, 465, 587} {
			candidate := configuration
			candidate.Host = host
			candidate.Port = port
			if err := Probe(ctx, candidate, 750*time.Millisecond); err == nil {
				return candidate, nil
			}
		}
	}
	return SMTPConfig{}, errors.New("no SMTP relay found")
}

func smtpCandidateHosts(ctx context.Context, fqdn string) []string {
	hostname := strings.TrimSuffix(strings.TrimSpace(fqdn), ".")
	if hostname == "" {
		hostname, _ = os.Hostname()
		lookupContext, cancel := context.WithTimeout(ctx, time.Second)
		canonical, err := net.DefaultResolver.LookupCNAME(lookupContext, hostname)
		cancel()
		if err == nil {
			hostname = strings.TrimSuffix(canonical, ".")
		}
	}
	ordered := []string{"127.0.0.1", "172.17.0.1", defaultGatewayIPv4(), hostname, globalIPv4(), prefixHost("mail", hostname), prefixHost("smtp", hostname)}
	seen := make(map[string]bool, len(ordered))
	result := make([]string, 0, len(ordered))
	for _, host := range ordered {
		host = strings.TrimSpace(host)
		if host != "" && !seen[strings.ToLower(host)] {
			seen[strings.ToLower(host)] = true
			result = append(result, host)
		}
	}
	return result
}

func prefixHost(prefix, host string) string {
	if host == "" {
		return ""
	}
	return prefix + "." + host
}

func globalIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	sort.SliceStable(interfaces, func(i, j int) bool {
		return interfacePriority(interfaces[i].Name) < interfacePriority(interfaces[j].Name)
	})
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 || interfacePriority(networkInterface.Name) == 3 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			if ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
				return ip.String()
			}
		}
	}
	return ""
}

func interfacePriority(name string) int {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"lo", "docker", "br-", "veth", "incus", "lxc", "lxd", "virbr", "vnet", "tun", "tap", "wg", "tailscale", "utun", "podman", "cni", "flannel", "calico"} {
		if strings.HasPrefix(lower, prefix) {
			return 3
		}
	}
	if strings.HasPrefix(lower, "eth") || strings.HasPrefix(lower, "en") || strings.HasPrefix(lower, "em") {
		return 0
	}
	if strings.HasPrefix(lower, "wlan") || strings.HasPrefix(lower, "wl") || strings.HasPrefix(lower, "wifi") {
		return 1
	}
	return 2
}

func defaultGatewayIPv4() string {
	if runtime.GOOS == "linux" {
		file, err := os.Open("/proc/net/route")
		if err != nil {
			return ""
		}
		defer func() { _ = file.Close() }()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 3 || fields[1] != "00000000" || fields[2] == "00000000" {
				continue
			}
			var octets [4]byte
			if _, err := fmt.Sscanf(fields[2], "%02x%02x%02x%02x", &octets[3], &octets[2], &octets[1], &octets[0]); err == nil {
				return net.IPv4(octets[0], octets[1], octets[2], octets[3]).String()
			}
		}
		return ""
	}
	var command string
	var arguments []string
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd":
		command, arguments = "route", []string{"-n", "get", "default"}
	case "windows":
		command, arguments = "route", []string{"print", "-4"}
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, command, arguments...).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if runtime.GOOS == "windows" && len(fields) >= 3 && fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" {
			if net.ParseIP(fields[2]) != nil {
				return fields[2]
			}
		}
		if len(fields) == 2 && fields[0] == "gateway:" && net.ParseIP(fields[1]) != nil {
			return fields[1]
		}
	}
	return ""
}
