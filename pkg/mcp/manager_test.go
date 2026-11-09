package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/obot-platform/obot/pkg/safehttp"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	storagescheme "github.com/obot-platform/obot/pkg/storage/scheme"
	"github.com/obot-platform/obot/pkg/system"
	"github.com/obot-platform/obot/pkg/tunnel"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEffectiveKubernetesResourceMaximums(t *testing.T) {
	maximum := resource.MustParse("500m")
	storageClient := fake.NewClientBuilder().
		WithScheme(storagescheme.Scheme).
		WithObjects(&v1.K8sSettings{
			Name:      system.K8sSettingsName,
			Namespace: system.DefaultNamespace,
			Spec:      v1.K8sSettingsSpec{MaxCPURequest: &maximum},
		}).
		Build()

	t.Run("Kubernetes backend uses persisted maximum", func(t *testing.T) {
		manager := &SessionManager{runtimeBackend: RuntimeBackendKubernetes}
		got, err := manager.EffectiveKubernetesResourceMaximums(t.Context(), storageClient)
		require.NoError(t, err)
		require.NotNil(t, got.CPURequest)
		require.Zero(t, got.CPURequest.Cmp(maximum))
	})

	t.Run("runtime ignores startup maximum", func(t *testing.T) {
		startupMaximum := resource.MustParse("100m")
		manager := &SessionManager{
			runtimeBackend:   RuntimeBackendKubernetes,
			resourceMaximums: ResourceMaximums{CPURequest: &startupMaximum},
		}
		got, err := manager.EffectiveKubernetesResourceMaximums(t.Context(), storageClient)
		require.NoError(t, err)
		require.NotNil(t, got.CPURequest)
		require.Zero(t, got.CPURequest.Cmp(maximum))
	})

	t.Run("startup uses strictest persisted and configured maximum", func(t *testing.T) {
		startupMaximum := resource.MustParse("100m")
		manager := &SessionManager{
			runtimeBackend:   RuntimeBackendKubernetes,
			resourceMaximums: ResourceMaximums{CPURequest: &startupMaximum},
		}
		got, err := manager.StartupKubernetesResourceMaximums(t.Context(), storageClient)
		require.NoError(t, err)
		require.NotNil(t, got.CPURequest)
		require.Zero(t, got.CPURequest.Cmp(startupMaximum))
	})

	t.Run("startup uses configured maximum before settings exist", func(t *testing.T) {
		startupMaximum := resource.MustParse("100m")
		manager := &SessionManager{
			runtimeBackend:   RuntimeBackendKubernetes,
			resourceMaximums: ResourceMaximums{CPURequest: &startupMaximum},
		}
		emptyStorageClient := fake.NewClientBuilder().WithScheme(storagescheme.Scheme).Build()
		got, err := manager.StartupKubernetesResourceMaximums(t.Context(), emptyStorageClient)
		require.NoError(t, err)
		require.NotNil(t, got.CPURequest)
		require.Zero(t, got.CPURequest.Cmp(startupMaximum))
	})

	t.Run("non-Kubernetes backend ignores persisted maximum", func(t *testing.T) {
		manager := &SessionManager{runtimeBackend: runtimeBackendDocker}
		got, err := manager.EffectiveKubernetesResourceMaximums(t.Context(), nil)
		require.NoError(t, err)
		require.True(t, got.Empty())
	})
}

func TestHTTPClientForServer(t *testing.T) {
	const timeout = 3 * time.Second
	backend := &kubernetesBackend{
		httpListenPort:   8080,
		mcpNamespace:     "obot-mcp",
		mcpClusterDomain: "cluster.local",
		serviceFQDN:      "obot.obot-system.svc.cluster.local",
	}

	t.Run("Kubernetes service URL dial retries", func(t *testing.T) {
		if runMCPTestInSubprocess(t) {
			return
		}
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close() // Refuse connections so retrying clients reach the request deadline.
		serverURL, err := url.Parse(server.URL)
		require.NoError(t, err)
		localBackend := &kubernetesBackend{
			mcpNamespace:     backend.mcpNamespace,
			mcpClusterDomain: backend.mcpClusterDomain,
			serviceFQDN:      serverURL.Host,
		}

		for _, tt := range []struct {
			name      string
			url       string
			wantRetry bool
		}{
			{
				name:      "service URL",
				url:       "http://test-server.obot-mcp.svc.cluster.local",
				wantRetry: true,
			},
			{
				name:      "service URL with path",
				url:       "http://test-server.obot-mcp.svc.cluster.local/mcp",
				wantRetry: true,
			},
			{
				name: "remote URL",
				url:  "https://example.com/mcp",
			},
			{
				name: "remote URL sharing service prefix",
				url:  "http://test-server.obot-mcp.svc.cluster.local.example.com/mcp",
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				client, err := (&SessionManager{backend: localBackend}).HTTPClientForServer(ServerConfig{
					MCPServerName: "test-server",
					URL:           tt.url,
				}, HTTPClientOptions{})
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
				defer cancel()
				// Exercise the configured client against a local closed port without cluster DNS.
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
				require.NoError(t, err)
				resp, err := client.Do(req)
				if resp != nil {
					resp.Body.Close()
				}
				if tt.wantRetry {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				} else {
					require.ErrorIs(t, err, syscall.ECONNREFUSED)
					require.NotErrorIs(t, err, context.DeadlineExceeded)
				}
			})
		}
	})

	t.Run("direct server", func(t *testing.T) {
		httpClient, err := (&SessionManager{backend: backend}).HTTPClientForServer(ServerConfig{}, HTTPClientOptions{Timeout: timeout})
		if err != nil {
			t.Fatal(err)
		}
		if httpClient.Timeout != timeout {
			t.Fatalf("client timeout = %v, want %v", httpClient.Timeout, timeout)
		}
		if httpClient.Transport == nil {
			t.Fatal("direct client uses the default transport")
		}
	})

	t.Run("direct server options", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("X-MCP-Test") != "injected" {
				t.Errorf("injected header = %q, want injected", req.Header.Get("X-MCP-Test"))
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		serverURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		httpClient, err := (&SessionManager{backend: &kubernetesBackend{serviceFQDN: serverURL.Host}}).HTTPClientForServer(
			ServerConfig{Headers: []string{"X-MCP-Test=injected"}},
			HTTPClientOptions{Timeout: timeout},
		)
		if err != nil {
			t.Fatal(err)
		}

		response, err := httpClient.Get(server.URL)
		if err != nil {
			t.Fatalf("allow-listed direct request failed: %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("response status = %d, want %d", response.StatusCode, http.StatusNoContent)
		}
	})

	t.Run("direct server uses Kubernetes backend allow list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		serverURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}

		httpClient, err := (&SessionManager{
			backend: &kubernetesBackend{
				serviceFQDN: serverURL.Host,
			},
		}).HTTPClientForServer(ServerConfig{}, HTTPClientOptions{Timeout: timeout})
		if err != nil {
			t.Fatal(err)
		}

		response, err := httpClient.Get(server.URL)
		if err != nil {
			t.Fatalf("request to backend-allowlisted server failed: %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("response status = %d, want %d", response.StatusCode, http.StatusNoContent)
		}
	})

	t.Run("tunnel manager required", func(t *testing.T) {
		_, err := (&SessionManager{}).HTTPClientForServer(ServerConfig{TunnelName: "office"}, HTTPClientOptions{Timeout: timeout, DirectConnect: true})
		if err == nil {
			t.Fatal("tunneled server without tunnel manager returned no error")
		}
	})

	t.Run("tunneled server", func(t *testing.T) {
		tunnelManager, err := tunnel.NewManager(t.Context(), "http://127.0.0.1:8080", fake.NewClientBuilder().Build(), tunnel.PeerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		defer tunnelManager.Close()

		httpClient, err := (&SessionManager{tunnelManager: tunnelManager}).HTTPClientForServer(
			ServerConfig{TunnelName: "office"},
			HTTPClientOptions{Timeout: timeout, DirectConnect: true},
		)
		if err != nil {
			t.Fatal(err)
		}
		if httpClient.Timeout != timeout {
			t.Fatalf("client timeout = %v, want %v", httpClient.Timeout, timeout)
		}
		if httpClient.Transport == nil {
			t.Fatal("tunneled client uses the default transport")
		}
	})

	t.Run("invalid tunnel name", func(t *testing.T) {
		tunnelManager, err := tunnel.NewManager(t.Context(), "http://127.0.0.1:8080", fake.NewClientBuilder().Build(), tunnel.PeerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		defer tunnelManager.Close()

		_, err = (&SessionManager{tunnelManager: tunnelManager}).HTTPClientForServer(
			ServerConfig{TunnelName: "Office"},
			HTTPClientOptions{Timeout: timeout, DirectConnect: true},
		)
		if err == nil {
			t.Fatal("invalid tunnel name returned no error")
		}
	})
}

func TestOAuthClientForServerUsesBackendAllowListWithoutConfiguredHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("configured Authorization header was forwarded: %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client, err := (&SessionManager{
		backend: &kubernetesBackend{serviceFQDN: serverURL.Host},
	}).OAuthClientForServer(ServerConfig{
		URL:     server.URL,
		Headers: []string{"Authorization=Bearer secret"},
	})
	require.NoError(t, err)

	response, err := client.Get(server.URL)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
}

func TestOAuthServerAllowListIsLimitedToConfiguredBackendServer(t *testing.T) {
	_, backendAllowedHosts := (&cloudFoundryBackend{}).remoteConfig(RemoteMCPURLValidationConfig{})

	require.Equal(t,
		[]string{"mcp-cdc-places.apps.internal:8080"},
		oauthServerAllowList("http://mcp-cdc-places.apps.internal:8080/mcp", backendAllowedHosts),
	)
	require.Equal(t,
		[]string{"mcp-diu-solutions.apps.internal:8001"},
		oauthServerAllowList("http://mcp-diu-solutions.apps.internal:8001/mcp", backendAllowedHosts),
	)
	for _, rawURL := range []string{
		"http://mcp-cdc-places.apps.internal:8001/mcp",
		"http://mcp-diu-solutions.apps.internal:8002/mcp",
		"http://mcp-cdc-places.apps.internal/mcp",
		"http://10.255.232.69:8080/mcp",
		"http://apps.internal:8080/mcp",
		"http://apps.internal.evil.example:8080/mcp",
		"http://127.0.0.1:8080/mcp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			require.Empty(t, oauthServerAllowList(rawURL, backendAllowedHosts))
		})
	}
}

func TestValidationOptionsPreservesGlobalPolicyAndBackendHosts(t *testing.T) {
	tests := []struct {
		name    string
		backend backend
	}{
		{
			name:    "Cloud Foundry",
			backend: &cloudFoundryBackend{},
		},
		{
			name: "Docker container",
			backend: &dockerBackend{
				containerEnv:        true,
				httpListenPort:      8080,
				hostBaseURLWithPort: "http://obot:8080",
			},
		},
		{
			name: "native Docker",
			backend: &dockerBackend{
				httpListenPort:      8080,
				hostBaseURLWithPort: "http://host.docker.internal:8080",
			},
		},
		{
			name: "Kubernetes",
			backend: &kubernetesBackend{
				httpListenPort:   8080,
				serviceFQDN:      "obot.default.svc.cluster.local",
				mcpNamespace:     "obot-mcp",
				mcpClusterDomain: "cluster.local",
			},
		},
		{
			name: "no backend",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for flags := range 8 {
				global := RemoteMCPURLValidationConfig{
					AllowLocalhostMCP: flags&1 != 0,
					AllowPrivateIPMCP: flags&2 != 0,
					AllowLinkLocalMCP: flags&4 != 0,
				}
				manager := &SessionManager{backend: tt.backend, remoteURLValidationConfig: global, devMode: flags&1 != 0}
				want := global
				if tt.backend != nil {
					_, want.AllowedHosts = tt.backend.remoteConfig(global)
				}
				options := manager.ValidationOptions()
				require.Equal(t, want, options.RemoteMCPURLValidationConfig)
				require.Equal(t, manager.devMode, options.DevMode)
				require.Equal(t, global, manager.remoteURLValidationConfig, "startup policy must not mutate")
			}
		})
	}
}

func TestValidationOptionsDoesNotRelaxDockerAdmission(t *testing.T) {
	for _, containerEnv := range []bool{false, true} {
		manager := &SessionManager{backend: &dockerBackend{
			containerEnv:        containerEnv,
			httpListenPort:      8080,
			hostBaseURLWithPort: "http://obot:8080",
		}}
		policy, _ := manager.RemoteConfigForBackend()
		require.Equal(t, containerEnv, policy.AllowPrivateIPMCP)
		require.Equal(t, !containerEnv, policy.AllowLocalhostMCP)
		admission := manager.ValidationOptions().RemoteMCPURLValidationConfig
		require.Error(t, ValidateRemoteMCPURL(t.Context(), "http://10.0.0.5:8001/mcp", admission))
		require.Error(t, ValidateRemoteMCPURL(t.Context(), "http://localhost:8001/mcp", admission))
		require.NoError(t, ValidateRemoteMCPURL(t.Context(), "http://localhost:8080/mcp", admission))
	}
}

func TestValidationOptionsRetainsKubernetesAdmissionScope(t *testing.T) {
	if runMCPTestInSubprocess(t) {
		return
	}
	useCloudFoundryPrivateDNS(t)
	manager := &SessionManager{backend: &kubernetesBackend{
		httpListenPort:   8080,
		serviceFQDN:      "obot.default.svc.cluster.local",
		mcpNamespace:     "obot-mcp",
		mcpClusterDomain: "cluster.local",
	}}
	policy := manager.ValidationOptions().RemoteMCPURLValidationConfig
	require.NoError(t, ValidateRemoteMCPURL(t.Context(), "http://server.obot-mcp.svc.cluster.local:8080/mcp", policy))
	require.ErrorContains(t, ValidateRemoteMCPURL(t.Context(), "http://server.other.svc.cluster.local:8080/mcp", policy), "private IP")
	require.ErrorContains(t, ValidateRemoteMCPURL(t.Context(), "http://mcp-diu-solutions.apps.internal:8001/mcp", policy), "private IP")
	require.Error(t, ValidateRemoteMCPURL(t.Context(), "http://10.0.0.5:8080/mcp", policy))
}

// An in-process proxy observes requests only after the real safe transport has
// checked the controlled private DNS result. No private backend is contacted.
func TestCloudFoundryHTTPAndOAuthExceptionsAreScoped(t *testing.T) {
	if runMCPTestInSubprocess(t) {
		return
	}
	useCloudFoundryPrivateDNS(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			t.Error("OAuth forwarded configured MCP authorization")
		}
		if req.URL.Path == "/redirect" {
			http.Redirect(w, req, "http://mcp-cdc-places.apps.internal:8080/oauth/token", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	manager := &SessionManager{backend: &cloudFoundryBackend{}}
	const diuURL = "http://mcp-diu-solutions.apps.internal:8001/mcp"
	server := ServerConfig{URL: diuURL, Headers: []string{"Authorization=Bearer test-only"}}
	// Ordinary MCP HTTP uses the full port-scoped backend allowlist.
	client, err := manager.HTTPClientForServer(ServerConfig{URL: diuURL}, HTTPClientOptions{Timeout: time.Second})
	require.NoError(t, err)
	assertCloudFoundryHTTPStatus(t, client, diuURL)
	assertCloudFoundryHTTPStatus(t, client, "http://mcp-cdc-places.apps.internal:8080/mcp")
	assertCloudFoundryHTTPBlocked(t, client, "http://mcp-cdc-places.apps.internal:8001/mcp")
	assertCloudFoundryHTTPBlocked(t, client, "http://mcp-diu-solutions.apps.internal:8002/mcp")
	assertCloudFoundryHTTPBlocked(t, client, "http://10.0.0.5:8080/mcp")
	assertCloudFoundryHTTPBlocked(t, client, "http://127.0.0.1:8080/mcp")
	assertCloudFoundryHTTPBlocked(t, client, "http://169.254.169.254/latest/meta-data/")
	// OAuth discovery/token URLs cannot borrow a different backend exception.
	oauthClient, err := manager.OAuthClientForServer(server)
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, oauthClient.Timeout)
	assertCloudFoundryHTTPStatus(t, oauthClient, diuURL)
	assertCloudFoundryHTTPBlocked(t, oauthClient, "http://mcp-diu-solutions.apps.internal:8001/redirect")
	assertCloudFoundryHTTPBlocked(t, oauthClient, "http://mcp-cdc-places.apps.internal:8080/oauth/token")
	assertCloudFoundryHTTPBlocked(t, oauthClient, "http://mcp-diu-solutions.apps.internal:8080/oauth/token")
	assertCloudFoundryHTTPBlocked(t, oauthClient, "http://100.100.100.200/latest/meta-data/")
	// Generic fetchers (catalog/OpenAPI/client metadata) do not receive exceptions.
	options := manager.ValidationOptions().RemoteMCPURLValidationConfig.SafeHTTPOptions()
	require.Empty(t, options.AllowList)
	assertCloudFoundryHTTPBlocked(t, safehttp.NewClient(options), diuURL)
	require.Empty(t, openAPINetworkOptions(manager.ValidationOptions().RemoteMCPURLValidationConfig).AllowList)
}

func assertCloudFoundryHTTPStatus(t *testing.T, client *http.Client, rawURL string) {
	t.Helper()
	response, err := client.Get(rawURL)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
}

func assertCloudFoundryHTTPBlocked(t *testing.T, client *http.Client, rawURL string) {
	t.Helper()
	response, err := client.Get(rawURL)
	if response != nil {
		defer response.Body.Close()
	}
	var blocked *safehttp.BlockedAddressError
	require.ErrorAs(t, err, &blocked)
}
