package main

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

func startupPublicURLs(fqdn, baseURL string, listeners []serverListener) []string {
	host := strings.Trim(strings.TrimSpace(fqdn), "[]")
	if host == "" {
		return nil
	}
	urls := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		scheme := "http"
		defaultPort := 80
		if listener.tls {
			scheme = "https"
			defaultPort = 443
		}
		urlHost := host
		if listener.port != defaultPort {
			urlHost = net.JoinHostPort(host, strconv.Itoa(listener.port))
		} else if strings.Contains(host, ":") {
			urlHost = "[" + host + "]"
		}
		urls = append(urls, (&url.URL{Scheme: scheme, Host: urlHost, Path: baseURL}).String())
	}
	return urls
}

func startupOverlayURL(host, baseURL string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: "http", Host: host, Path: baseURL}).String()
}
