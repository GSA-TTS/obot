package mcp

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/obot-platform/obot/pkg/safehttp"
	"github.com/obot-platform/obot/pkg/tunnel"
	"golang.org/x/oauth2"
)

type HTTPClientOptions struct {
	TokenSource   oauth2.TokenSource
	Timeout       time.Duration
	DirectConnect bool
}

// HTTPClientForServer returns an HTTP client that follows the server's
// configured network path, including its tunnel when present.
func (sm *SessionManager) HTTPClientForServer(server ServerConfig, opts HTTPClientOptions) (*http.Client, error) {
	headers := serverConfigHeaders(server)
	if (opts.DirectConnect || server.UserID == "system") && server.TunnelName != "" {
		if sm.tunnelManager == nil {
			return nil, fmt.Errorf("tunnel manager is not configured")
		}

		client, err := sm.tunnelManager.HTTPClient(server.TunnelName, tunnel.HTTPClientOptions{
			Headers:     headers,
			Timeout:     opts.Timeout,
			TokenSource: opts.TokenSource,
		})
		return client, err
	}

	remoteValidationConfig, allowedHosts := sm.RemoteConfigForBackend()

	clientOptions := safehttp.Options{
		BlockLoopback:  !remoteValidationConfig.AllowLocalhostMCP,
		BlockPrivateIP: !remoteValidationConfig.AllowPrivateIPMCP,
		BlockLinkLocal: !remoteValidationConfig.AllowLinkLocalMCP,
		AllowList:      allowedHosts,
		Timeout:        opts.Timeout,
		Headers:        headers,
		TokenSource:    opts.TokenSource,
	}
	if k, ok := sm.backend.(*kubernetesBackend); ok && server.MCPServerName != "" {
		serviceURL := fmt.Sprintf("http://%s.%s.svc.%s", server.MCPServerName, k.mcpNamespace, k.mcpClusterDomain)
		if server.URL == serviceURL || strings.HasPrefix(server.URL, serviceURL+"/") {
			// Endpoint routing can lag deployment readiness. Retry only connection establishment.
			clientOptions.DialRetryTimeout = 10 * time.Second
		}
	}
	return safehttp.NewClient(clientOptions), nil
}

// OAuthClientForServer returns the client for the server's OAuth requests: metadata
// discovery, dynamic client registration, and token exchange and refresh. Those URLs come
// from the server's own metadata, so the client follows the server's tunnel when present
// and otherwise applies the admin's egress policy. If the configured server uses a backend
// exception, only that exact host and port are allowed; server-supplied metadata cannot use
// the backend wildcard to reach a different internal service. Unlike HTTPClientForServer it
// carries no configured headers and has a timeout.
func (sm *SessionManager) OAuthClientForServer(server ServerConfig) (*http.Client, error) {
	const timeout = 30 * time.Second
	if server.TunnelName != "" {
		if sm.tunnelManager == nil {
			return nil, fmt.Errorf("tunnel manager is not configured")
		}
		return sm.tunnelManager.HTTPClient(server.TunnelName, tunnel.HTTPClientOptions{Timeout: timeout})
	}

	policy, backendAllowedHosts := sm.RemoteConfigForBackend()
	return safehttp.NewClient(safehttp.Options{
		BlockLoopback:  !policy.AllowLocalhostMCP,
		BlockPrivateIP: !policy.AllowPrivateIPMCP,
		BlockLinkLocal: !policy.AllowLinkLocalMCP,
		AllowList:      oauthServerAllowList(server.URL, backendAllowedHosts),
		Timeout:        timeout,
	}), nil
}

func oauthServerAllowList(rawURL string, backendAllowedHosts []string) []string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || !safehttp.HostAllowed(backendAllowedHosts, u.Hostname(), safehttp.PortForURL(u)) {
		return nil
	}
	return []string{u.Host}
}
