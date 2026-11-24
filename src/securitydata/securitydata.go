package securitydata

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/oschwald/maxminddb-golang"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

const (
	geoIPBaseURL         = "https://github.com/sapics/ip-location-db/releases/download/latest/"
	geoIPHashURL         = "https://github.com/sapics/ip-location-db/releases/download/checksum/"
	fireholURL           = "https://raw.githubusercontent.com/firehol/blocklist-ipsets/master/firehol_level1.netset"
	nvdURL               = "https://services.nvd.nist.gov/rest/json/cves/2.0"
	maxFeedBytes         = 256 << 20
	trivyLayerMediaType  = "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"
	maxTrivyExpandedSize = int64(4 << 30)
)

var geoIPFiles = []string{
	"iptoasn-asn.mmdb",
	"iptoasn-country.mmdb",
	"dbip-city-ipv4.mmdb",
	"dbip-city-ipv6.mmdb",
}

var sourceIDPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// Manager downloads and validates operational security datasets.
type Manager struct {
	Dir    string
	Client *http.Client
	Now    func() time.Time
}

// Source describes one operator-configured blocklist.
type Source struct {
	ID      string
	Type    string
	URL     string
	Enabled bool
}

// BlocklistResult reports the accepted and rejected entries for one source.
type BlocklistResult struct {
	ValidEntries   int
	SkippedEntries int
	UpdatedAt      time.Time
}

// IsIPBlocked matches an address against the active built-in and configured IP lists.
func (m *Manager) IsIPBlocked(address netip.Addr, extra []Source) (string, error) {
	if !address.IsValid() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return "", nil
	}
	sources := []Source{{ID: "firehol_level1", Type: "ip", Enabled: true}}
	for _, source := range extra {
		if source.Enabled && source.Type == "ip" {
			sources = append(sources, source)
		}
	}
	for _, source := range sources {
		entries, err := os.ReadFile(filepath.Join(m.Dir, "blocklists", source.ID+".txt"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("read IP blocklist %s: %w", source.ID, err)
		}
		for _, line := range strings.Split(string(entries), "\n") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(line))
			if err == nil && prefix.Contains(address) {
				return source.ID, nil
			}
		}
	}
	return "", nil
}

// IsDomainBlocked matches a normalized hostname against configured domain lists.
func (m *Manager) IsDomainBlocked(host string, extra []Source) (string, error) {
	domain, ok := normalizeDomain(host)
	if !ok {
		return "", nil
	}
	for _, source := range extra {
		if !source.Enabled || source.Type != "domain" {
			continue
		}
		entries, err := os.ReadFile(filepath.Join(m.Dir, "blocklists", source.ID+".txt"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("read domain blocklist %s: %w", source.ID, err)
		}
		for _, entry := range strings.Split(string(entries), "\n") {
			entry = strings.TrimSpace(entry)
			if entry == domain || strings.HasPrefix(entry, "*.") && strings.HasSuffix(domain, strings.TrimPrefix(entry, "*")) {
				return source.ID, nil
			}
		}
	}
	return "", nil
}

func normalizeDomain(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Hostname()
	} else if at := strings.LastIndexByte(value, '@'); at >= 0 {
		value = value[at+1:]
	}
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if address, err := netip.ParseAddr(value); err == nil && address.IsValid() {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil || ascii == "" {
		return "", false
	}
	return strings.ToLower(ascii), true
}

// New creates a manager rooted in the security data directory.
func New(directory string) *Manager {
	return &Manager{Dir: directory, Client: &http.Client{Timeout: 45 * time.Second}, Now: time.Now}
}

// UpdateGeoIP downloads the four canonical ip-location-db databases and verifies each release checksum.
func (m *Manager) UpdateGeoIP(ctx context.Context) error {
	directory := filepath.Join(m.Dir, "geoip")
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return fmt.Errorf("create security data directory: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(directory), ".geoip-update-")
	if err != nil {
		return fmt.Errorf("create GeoIP staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			log.Printf("remove GeoIP staging directory: %v", err)
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return err
	}
	for _, name := range geoIPFiles {
		contents, err := m.get(ctx, geoIPBaseURL+url.PathEscape(name), maxFeedBytes)
		if err != nil {
			return fmt.Errorf("download GeoIP database %s: %w", name, err)
		}
		hashText, err := m.get(ctx, geoIPHashURL+url.PathEscape(name)+".sha256", 4096)
		if err != nil {
			return fmt.Errorf("download GeoIP checksum %s: %w", name, err)
		}
		want, err := parseSHA256(hashText)
		if err != nil {
			return fmt.Errorf("parse GeoIP checksum %s: %w", name, err)
		}
		got := sha256.Sum256(contents)
		if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
			return fmt.Errorf("GeoIP checksum mismatch for %s", name)
		}
		stagedPath := filepath.Join(staging, name)
		if err := atomicWrite(stagedPath, contents); err != nil {
			return fmt.Errorf("store GeoIP database %s: %w", name, err)
		}
		if err := verifyMMDB(stagedPath); err != nil {
			return fmt.Errorf("validate GeoIP database %s: %w", name, err)
		}
	}
	if err := m.writeUpdated(staging); err != nil {
		return err
	}
	if err := activateDirectory(staging, directory); err != nil {
		return fmt.Errorf("activate GeoIP database: %w", err)
	}
	return nil
}

// GeoIP holds verified readers for the canonical ASN, country and city databases.
type GeoIP struct {
	ASN       *maxminddb.Reader
	Country   *maxminddb.Reader
	CityIPv4  *maxminddb.Reader
	CityIPv6  *maxminddb.Reader
	closeOnce sync.Once
}

// ASNRecord contains the ip-location-db ASN fields.
type ASNRecord struct {
	Number       uint32 `maxminddb:"autonomous_system_number"`
	Organization string `maxminddb:"autonomous_system_organization"`
}

// CountryRecord contains the ip-location-db country field.
type CountryRecord struct {
	Code string `maxminddb:"country_code"`
}

// CityRecord contains the DB-IP city fields used by ip-location-db.
type CityRecord struct {
	City        string  `maxminddb:"city"`
	CountryCode string  `maxminddb:"country_code"`
	State1      string  `maxminddb:"state1"`
	State2      string  `maxminddb:"state2"`
	Postcode    string  `maxminddb:"postcode"`
	Latitude    float64 `maxminddb:"latitude"`
	Longitude   float64 `maxminddb:"longitude"`
	Timezone    string  `maxminddb:"timezone"`
}

// GeoIPRecord combines the independent database lookups for one IP address.
type GeoIPRecord struct {
	ASN     ASNRecord
	Country CountryRecord
	City    CityRecord
}

// OpenGeoIP opens and verifies all four databases in a previously published directory.
func OpenGeoIP(directory string) (*GeoIP, error) {
	readers := make([]*maxminddb.Reader, 0, len(geoIPFiles))
	for _, filename := range geoIPFiles {
		reader, err := maxminddb.Open(filepath.Join(directory, filename))
		if err != nil {
			closeReaders(readers)
			return nil, fmt.Errorf("open %s: %w", filename, err)
		}
		if err := reader.Verify(); err != nil {
			_ = reader.Close()
			closeReaders(readers)
			return nil, fmt.Errorf("verify %s: %w", filename, err)
		}
		readers = append(readers, reader)
	}
	return &GeoIP{ASN: readers[0], Country: readers[1], CityIPv4: readers[2], CityIPv6: readers[3]}, nil
}

// Lookup resolves the available ASN, country and city records for an IP address.
func (g *GeoIP) Lookup(address net.IP) (GeoIPRecord, error) {
	if g == nil || g.ASN == nil || g.Country == nil || g.CityIPv4 == nil || g.CityIPv6 == nil {
		return GeoIPRecord{}, errors.New("GeoIP databases are not open")
	}
	if address == nil {
		return GeoIPRecord{}, errors.New("IP address is required")
	}
	var record GeoIPRecord
	if err := g.ASN.Lookup(address, &record.ASN); err != nil {
		return GeoIPRecord{}, fmt.Errorf("lookup ASN: %w", err)
	}
	if err := g.Country.Lookup(address, &record.Country); err != nil {
		return GeoIPRecord{}, fmt.Errorf("lookup country: %w", err)
	}
	city := g.CityIPv6
	if address.To4() != nil {
		city = g.CityIPv4
	}
	if err := city.Lookup(address, &record.City); err != nil {
		return GeoIPRecord{}, fmt.Errorf("lookup city: %w", err)
	}
	return record, nil
}

// Close releases all memory maps exactly once.
func (g *GeoIP) Close() error {
	if g == nil {
		return nil
	}
	var first error
	g.closeOnce.Do(func() {
		for _, reader := range []*maxminddb.Reader{g.ASN, g.Country, g.CityIPv4, g.CityIPv6} {
			if reader != nil {
				if err := reader.Close(); err != nil && first == nil {
					first = err
				}
			}
		}
	})
	return first
}

// UpdateBlocklists refreshes the built-in FireHOL list and enabled configured sources.
func (m *Manager) UpdateBlocklists(ctx context.Context, extra []Source, trusted []netip.Prefix) (map[string]BlocklistResult, error) {
	directory := filepath.Join(m.Dir, "blocklists")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create blocklist directory: %w", err)
	}
	sources := []Source{{ID: "firehol_level1", Type: "ip", URL: fireholURL, Enabled: true}}
	for _, source := range extra {
		if source.ID == "firehol_level1" {
			return nil, errors.New("firehol_level1 is reserved for the built-in source")
		}
		if source.Enabled {
			sources = append(sources, source)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	var failures []string
	results := make(map[string]BlocklistResult, len(sources))
	for _, source := range sources {
		if !sourceIDPattern.MatchString(source.ID) {
			failures = append(failures, fmt.Sprintf("%s: invalid source id", source.ID))
			continue
		}
		if source.Type != "ip" && source.Type != "domain" {
			failures = append(failures, fmt.Sprintf("%s: type must be ip or domain", source.ID))
			continue
		}
		if err := requireHTTPS(source.URL); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", source.ID, err))
			continue
		}
		body, err := m.get(ctx, source.URL, 32<<20)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: download failed: %v", source.ID, err))
			continue
		}
		entries, skipped, err := parseBlocklist(body, source.Type, trusted)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", source.ID, err))
			continue
		}
		if err := atomicWrite(filepath.Join(directory, source.ID+".txt"), []byte(strings.Join(entries, "\n")+"\n")); err != nil {
			failures = append(failures, fmt.Sprintf("%s: store failed: %v", source.ID, err))
			continue
		}
		updatedAt := m.now().UTC()
		results[source.ID] = BlocklistResult{ValidEntries: len(entries), SkippedEntries: skipped, UpdatedAt: updatedAt}
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("blocklist update incomplete: %s", strings.Join(failures, "; "))
	}
	if err := m.writeUpdated(directory); err != nil {
		return results, err
	}
	return results, nil
}

func parseBlocklist(data []byte, kind string, trusted []netip.Prefix) ([]string, int, error) {
	entries := make([]string, 0)
	skipped := 0
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if index := strings.IndexAny(line, " \t;"); index >= 0 {
			line = line[:index]
		}
		if line == "" {
			continue
		}
		var normalized string
		switch kind {
		case "ip":
			network, err := netip.ParsePrefix(line)
			if err != nil {
				address, addressErr := netip.ParseAddr(line)
				if addressErr != nil {
					skipped++
					continue
				}
				network = netip.PrefixFrom(address, address.BitLen())
			}
			network = network.Masked()
			if overlapsProtected(network, trusted) {
				skipped++
				continue
			}
			normalized = network.String()
		case "domain":
			wildcard := strings.HasPrefix(line, "*.")
			host := strings.TrimPrefix(line, "*.")
			host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
			ascii, err := idna.Lookup.ToASCII(host)
			if err != nil || ascii == "" || strings.ContainsAny(ascii, "/:@") {
				skipped++
				continue
			}
			suffix, _ := publicsuffix.PublicSuffix(ascii)
			if ascii == suffix {
				skipped++
				continue
			}
			if wildcard {
				normalized = "*." + ascii
			} else {
				normalized = ascii
			}
		default:
			return nil, skipped, fmt.Errorf("unsupported blocklist type %q", kind)
		}
		if _, exists := seen[normalized]; !exists {
			entries = append(entries, normalized)
			seen[normalized] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, err
	}
	if len(entries) == 0 {
		return nil, skipped, errors.New("feed contains no valid entries")
	}
	sort.Strings(entries)
	return entries, skipped, nil
}

func overlapsProtected(candidate netip.Prefix, trusted []netip.Prefix) bool {
	protected := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
		"169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "::1/128",
		"fc00::/7", "fe80::/10",
	}
	for _, value := range protected {
		if prefix, err := netip.ParsePrefix(value); err == nil && prefixesOverlap(candidate, prefix) {
			return true
		}
	}
	for _, prefix := range trusted {
		if prefix.IsValid() && prefixesOverlap(candidate, prefix) {
			return true
		}
	}
	return false
}

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Addr().Is4() == b.Addr().Is4() && (a.Contains(b.Addr()) || b.Contains(a.Addr()))
}

// UpdateNVD downloads all CVEs on first run and then walks the modification window in bounded 120-day intervals.
func (m *Manager) UpdateNVD(ctx context.Context, apiKey string) error {
	directory := filepath.Join(m.Dir, "cve")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create CVE directory: %w", err)
	}
	path := filepath.Join(directory, "nvd.json")
	stored := make(map[string]json.RawMessage)
	if contents, err := os.ReadFile(path); err == nil {
		var records []json.RawMessage
		if err := json.Unmarshal(contents, &records); err != nil {
			return fmt.Errorf("read stored NVD records: %w", err)
		}
		for _, record := range records {
			var item struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(record, &item) == nil && item.ID != "" {
				stored[item.ID] = record
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read stored NVD records: %w", err)
	}
	last, err := readUpdated(directory)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	end := m.now().UTC()
	start := time.Time{}
	if !last.IsZero() {
		start = last.UTC()
	}
	windows := [][2]time.Time{{start, end}}
	if !start.IsZero() && end.Sub(start) > 120*24*time.Hour {
		windows = nil
		for cursor := start; cursor.Before(end); {
			limit := cursor.Add(120 * 24 * time.Hour)
			if limit.After(end) {
				limit = end
			}
			windows = append(windows, [2]time.Time{cursor, limit})
			cursor = limit
		}
	}
	for _, window := range windows {
		if err := m.fetchNVDWindow(ctx, window[0], window[1], apiKey, stored); err != nil {
			return err
		}
	}
	ids := make([]string, 0, len(stored))
	for id := range stored {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	records := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		records = append(records, stored[id])
	}
	contents, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("encode NVD records: %w", err)
	}
	if len(records) == 0 {
		return errors.New("NVD response contained no CVE records")
	}
	if err := atomicWrite(path, contents); err != nil {
		return fmt.Errorf("store NVD records: %w", err)
	}
	return m.writeUpdated(directory)
}

// UpdateTrivy pulls the configured OCI database layer and atomically publishes its validated files.
// The bool reports whether a new database was installed; false means NextUpdate is still in the future.
func (m *Manager) UpdateTrivy(ctx context.Context, source string) (bool, error) {
	directory := filepath.Join(m.Dir, "trivy")
	if metadata, err := readTrivyMetadata(filepath.Join(directory, "metadata.json")); err == nil && m.now().Before(metadata.NextUpdate) {
		return false, nil
	}
	reference, err := name.ParseReference(strings.TrimSpace(source))
	if err != nil {
		return false, fmt.Errorf("parse Trivy OCI reference: %w", err)
	}
	image, err := remote.Image(reference, remote.WithContext(ctx), remote.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}))
	if err != nil {
		return false, fmt.Errorf("pull Trivy OCI image: %w", err)
	}
	layers, err := image.Layers()
	if err != nil {
		return false, fmt.Errorf("read Trivy OCI layers: %w", err)
	}
	var databaseLayer v1.Layer
	for _, layer := range layers {
		mediaType, mediaErr := layer.MediaType()
		if mediaErr != nil {
			return false, fmt.Errorf("read Trivy layer media type: %w", mediaErr)
		}
		if string(mediaType) == trivyLayerMediaType {
			databaseLayer = layer
			break
		}
	}
	if databaseLayer == nil {
		return false, fmt.Errorf("trivy OCI image has no %s layer", trivyLayerMediaType)
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return false, fmt.Errorf("create Trivy data parent: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(directory), ".trivy-update-")
	if err != nil {
		return false, fmt.Errorf("create Trivy staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			log.Printf("remove Trivy staging directory: %v", err)
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return false, err
	}
	compressed, err := databaseLayer.Compressed()
	if err != nil {
		return false, fmt.Errorf("open Trivy OCI layer: %w", err)
	}
	defer func() {
		if err := compressed.Close(); err != nil {
			log.Printf("close Trivy OCI layer: %v", err)
		}
	}()
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return false, fmt.Errorf("open Trivy gzip layer: %w", err)
	}
	archive := tar.NewReader(reader)
	var expanded int64
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = reader.Close()
			return false, fmt.Errorf("read Trivy archive: %w", nextErr)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if name != "trivy.db" && name != "metadata.json" {
			continue
		}
		if header.Size <= 0 || header.Size > maxTrivyExpandedSize-expanded {
			_ = reader.Close()
			return false, errors.New("trivy database layer exceeds the expanded size limit")
		}
		output, createErr := os.OpenFile(filepath.Join(staging, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			_ = reader.Close()
			return false, fmt.Errorf("create staged Trivy file: %w", createErr)
		}
		written, copyErr := io.CopyN(output, archive, header.Size)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			_ = reader.Close()
			return false, errors.Join(copyErr, closeErr, errors.New("incomplete Trivy archive file"))
		}
		expanded += written
	}
	if err := reader.Close(); err != nil {
		return false, fmt.Errorf("finish Trivy gzip layer: %w", err)
	}
	metadata, err := readTrivyMetadata(filepath.Join(staging, "metadata.json"))
	if err != nil {
		return false, err
	}
	if !m.now().Before(metadata.NextUpdate) {
		return false, errors.New("trivy metadata NextUpdate is not in the future")
	}
	if err := validateTrivyDatabase(filepath.Join(staging, "trivy.db")); err != nil {
		return false, err
	}
	if err := m.writeUpdated(staging); err != nil {
		return false, err
	}
	if err := activateDirectory(staging, directory); err != nil {
		return false, fmt.Errorf("activate Trivy database: %w", err)
	}
	return true, nil
}

// TrivyMetadata contains the OCI database freshness fields.
type TrivyMetadata struct {
	Version      int       `json:"Version"`
	NextUpdate   time.Time `json:"NextUpdate"`
	UpdatedAt    time.Time `json:"UpdatedAt"`
	DownloadedAt time.Time `json:"DownloadedAt"`
}

func readTrivyMetadata(path string) (TrivyMetadata, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return TrivyMetadata{}, err
	}
	if len(contents) == 0 || len(contents) > 1<<20 {
		return TrivyMetadata{}, errors.New("trivy metadata is empty or exceeds 1 MiB")
	}
	var metadata TrivyMetadata
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return TrivyMetadata{}, fmt.Errorf("parse Trivy metadata: %w", err)
	}
	if metadata.Version <= 0 || metadata.NextUpdate.IsZero() || metadata.UpdatedAt.IsZero() {
		return TrivyMetadata{}, errors.New("trivy metadata is missing version or update timestamps")
	}
	return metadata, nil
}

func validateTrivyDatabase(path string) error {
	database, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: 2 * time.Second})
	if err != nil {
		return fmt.Errorf("open Trivy database: %w", err)
	}
	validBucket := false
	viewErr := database.View(func(transaction *bolt.Tx) error {
		return transaction.ForEach(func(name []byte, _ *bolt.Bucket) error {
			if len(name) > 0 {
				validBucket = true
			}
			return nil
		})
	})
	closeErr := database.Close()
	if err := errors.Join(viewErr, closeErr); err != nil {
		return fmt.Errorf("verify trivy database: %w", err)
	}
	if !validBucket {
		return errors.New("trivy database contains no buckets")
	}
	return nil
}

func activateDirectory(staging, destination string) error {
	previous := staging + ".previous"
	if err := os.Rename(destination, previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, statErr := os.Stat(previous); statErr == nil {
			_ = os.Rename(previous, destination)
		}
		return err
	}
	if _, err := os.Stat(previous); err == nil {
		return os.RemoveAll(previous)
	}
	return nil
}

func (m *Manager) fetchNVDWindow(ctx context.Context, start, end time.Time, apiKey string, records map[string]json.RawMessage) error {
	const pageSize = 2000
	for offset := 0; ; offset += pageSize {
		if offset > 0 {
			delay := 6 * time.Second
			if apiKey != "" {
				delay = 600 * time.Millisecond
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		endpoint, _ := url.Parse(nvdURL)
		query := endpoint.Query()
		query.Set("resultsPerPage", fmt.Sprint(pageSize))
		query.Set("startIndex", fmt.Sprint(offset))
		if !start.IsZero() {
			query.Set("lastModStartDate", start.Format(time.RFC3339))
			query.Set("lastModEndDate", end.Format(time.RFC3339))
		}
		endpoint.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return err
		}
		if apiKey != "" {
			request.Header.Set("apiKey", apiKey)
		}
		body, err := m.do(request, 32<<20)
		if err != nil {
			return fmt.Errorf("NVD request at offset %d: %w", offset, err)
		}
		var page struct {
			Total int `json:"totalResults"`
			Items []struct {
				CVE json.RawMessage `json:"cve"`
			} `json:"vulnerabilities"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("decode NVD response at offset %d: %w", offset, err)
		}
		for _, item := range page.Items {
			var metadata struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(item.CVE, &metadata) == nil && metadata.ID != "" {
				records[metadata.ID] = append(json.RawMessage(nil), item.CVE...)
			}
		}
		if offset+len(page.Items) >= page.Total || len(page.Items) == 0 {
			return nil
		}
	}
}

// Check reports a dataset's loaded/fresh status using its .last_updated marker.
func (m *Manager) Check(name string, maxAge time.Duration) (string, time.Time) {
	path := filepath.Join(m.Dir, name, ".last_updated")
	updated, err := readUpdatedFile(path)
	if err != nil {
		return "error", time.Time{}
	}
	if maxAge > 0 && m.now().Sub(updated) > maxAge {
		return "degraded", updated
	}
	return "ok", updated
}

// CheckBlocklist reports one source's state and age using its data file modification time.
func (m *Manager) CheckBlocklist(id string, maxAge time.Duration) (string, time.Time) {
	if !sourceIDPattern.MatchString(id) {
		return "error", time.Time{}
	}
	path := filepath.Join(m.Dir, "blocklists", id+".txt")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "error", time.Time{}
	}
	updated := info.ModTime().UTC()
	if maxAge > 0 && m.now().Sub(updated) > maxAge {
		return "degraded", updated
	}
	return "ok", updated
}

func (m *Manager) get(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	if err := requireHTTPS(endpoint); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	return m.do(request, limit)
}

func (m *Manager) do(request *http.Request, limit int64) ([]byte, error) {
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	originalRedirect := client.CheckRedirect
	clientCopy := *client
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("feed redirected away from HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("too many feed redirects")
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}
	response, err := clientCopy.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Printf("close security feed response: %v", err)
		}
	}()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

func requireHTTPS(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("source URL must be HTTPS")
	}
	return nil
}

func verifyMMDB(path string) error {
	reader, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	verifyErr := reader.Verify()
	closeErr := reader.Close()
	return errors.Join(verifyErr, closeErr)
}

func closeReaders(readers []*maxminddb.Reader) {
	for _, reader := range readers {
		_ = reader.Close()
	}
}

func parseSHA256(contents []byte) (string, error) {
	fields := strings.Fields(string(contents))
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return "", errors.New("expected a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return "", errors.New("checksum is not hexadecimal")
	}
	return strings.ToLower(fields[0]), nil
}

func atomicWrite(path string, contents []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".security-data-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("remove temporary security data file: %v", err)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (m *Manager) writeUpdated(directory string) error {
	return atomicWrite(filepath.Join(directory, ".last_updated"), []byte(m.now().UTC().Format(time.RFC3339)+"\n"))
}

func readUpdated(directory string) (time.Time, error) {
	return readUpdatedFile(filepath.Join(directory, ".last_updated"))
}

func readUpdatedFile(path string) (time.Time, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	updated, err := time.Parse(time.RFC3339, strings.TrimSpace(string(contents)))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse last-updated timestamp: %w", err)
	}
	return updated.UTC(), nil
}

func (m *Manager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}
