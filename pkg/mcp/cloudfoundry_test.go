package mcp

import (
	"errors"
	"testing"

	otypes "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/safehttp"
)

var (
	// gatewayServedRuntimes are the runtimes Obot's own process serves, which
	// need no workload deployment and therefore work unchanged on Cloud Foundry.
	gatewayServedRuntimes = []otypes.Runtime{
		otypes.RuntimeRemote,
		otypes.RuntimeVMCP,
	}

	// deployedRuntimes require Obot to start a workload on an orchestrator it
	// controls. Cloud Foundry gives the app no such orchestrator, so every one
	// of these must report ErrNotSupportedByBackend rather than failing some
	// other way.
	deployedRuntimes = []otypes.Runtime{
		otypes.RuntimeUVX,
		otypes.RuntimeNPX,
		otypes.RuntimeContainerized,
	}

	// The concrete type must satisfy the backend interface. If upstream adds a
	// method to that interface, this fails to compile -- which is the point.
	_ backend = (*cloudFoundryBackend)(nil)
)

// requireNotSupported asserts the error is an ErrNotSupportedByBackend naming
// this backend. The concrete type matters: the API layer type-asserts on it to
// return 404 instead of 500, so a plain error here would surface as a server
// error to the client.
func requireNotSupported(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected ErrNotSupportedByBackend, got nil")
	}

	var notSupported *ErrNotSupportedByBackend
	if !errors.As(err, &notSupported) {
		t.Fatalf("expected *ErrNotSupportedByBackend, got %T: %v", err, err)
	}
	if notSupported.Backend != RuntimeBackendCloudFoundry {
		t.Fatalf("expected backend %q, got %q", RuntimeBackendCloudFoundry, notSupported.Backend)
	}
}

func TestCloudFoundryRuntimeIsGatewayServed(t *testing.T) {
	for _, runtime := range gatewayServedRuntimes {
		t.Run(string(runtime)+" is gateway served", func(t *testing.T) {
			if !runtimeIsGatewayServed(runtime) {
				t.Fatalf("expected %q to be gateway served", runtime)
			}
		})
	}

	for _, runtime := range deployedRuntimes {
		t.Run(string(runtime)+" is not gateway served", func(t *testing.T) {
			if runtimeIsGatewayServed(runtime) {
				t.Fatalf("expected %q to require deployment", runtime)
			}
		})
	}
}

func TestCloudFoundryEnsureServerDeploymentAllowsGatewayServedRuntimes(t *testing.T) {
	for _, runtime := range gatewayServedRuntimes {
		t.Run(string(runtime), func(t *testing.T) {
			backend := &cloudFoundryBackend{}
			server := ServerConfig{Runtime: runtime, URL: "https://mcp.example.gov/mcp"}

			got, err := backend.ensureServerDeployment(t.Context(), server)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.URL != server.URL {
				t.Fatalf("URL = %q, want %q", got.URL, server.URL)
			}
		})
	}
}

func TestCloudFoundryEnsureServerDeploymentRejectsDeployedRuntimes(t *testing.T) {
	for _, runtime := range deployedRuntimes {
		t.Run(string(runtime), func(t *testing.T) {
			backend := &cloudFoundryBackend{}

			if _, err := backend.ensureServerDeployment(t.Context(), ServerConfig{Runtime: runtime}); err == nil {
				t.Fatal("expected error for runtime requiring deployment")
			} else {
				requireNotSupported(t, err)
			}
		})
	}
}

// ensureServerDeployment rewrites component and webhook URLs before the runtime
// check, exactly as the docker backend does. transformObotHostname is the
// identity function here, so the URLs must survive untouched -- this pins that
// the rewrite loop does not drop or corrupt them.
func TestCloudFoundryEnsureServerDeploymentPreservesComponentAndWebhookURLs(t *testing.T) {
	backend := &cloudFoundryBackend{}
	server := ServerConfig{
		Runtime:    otypes.RuntimeVMCP,
		Components: []ComponentServer{{URL: "https://component.apps.internal/mcp"}},
		Webhooks:   []Webhook{{URL: "https://webhook.apps.internal/hook"}},
	}

	got, err := backend.ensureServerDeployment(t.Context(), server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.Components) != 1 || got.Components[0].URL != "https://component.apps.internal/mcp" {
		t.Fatalf("component URL not preserved: %+v", got.Components)
	}
	if len(got.Webhooks) != 1 || got.Webhooks[0].URL != "https://webhook.apps.internal/hook" {
		t.Fatalf("webhook URL not preserved: %+v", got.Webhooks)
	}
}

func TestCloudFoundryDeployServer(t *testing.T) {
	for _, runtime := range gatewayServedRuntimes {
		t.Run(string(runtime)+" succeeds", func(t *testing.T) {
			backend := &cloudFoundryBackend{}
			if err := backend.deployServer(t.Context(), ServerConfig{Runtime: runtime}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	for _, runtime := range deployedRuntimes {
		t.Run(string(runtime)+" is unsupported", func(t *testing.T) {
			backend := &cloudFoundryBackend{}
			requireNotSupported(t, backend.deployServer(t.Context(), ServerConfig{Runtime: runtime}))
		})
	}
}

func TestCloudFoundryUnsupportedOperations(t *testing.T) {
	backend := &cloudFoundryBackend{}

	t.Run("getServerDetails", func(t *testing.T) {
		_, err := backend.getServerDetails(t.Context(), "any-server")
		requireNotSupported(t, err)
	})

	t.Run("streamServerLogs", func(t *testing.T) {
		readCloser, err := backend.streamServerLogs(t.Context(), "any-server")
		requireNotSupported(t, err)
		if readCloser != nil {
			t.Fatal("expected nil ReadCloser alongside the error")
		}
	})

	t.Run("restartServer", func(t *testing.T) {
		requireNotSupported(t, backend.restartServer(t.Context(), ServerConfig{Runtime: otypes.RuntimeRemote}))
	})
}

// shutdownServer must never error, for any runtime. It is called from delete and
// finalizer paths as well as the idle-disable path, and an error there wedges the
// finalizer instead of degrading. Nothing is ever deployed, so there is nothing
// to shut down. This is the single most load-bearing behavior in the backend.
func TestCloudFoundryShutdownServerNeverErrors(t *testing.T) {
	backend := &cloudFoundryBackend{}
	allRuntimes := append(append([]otypes.Runtime{}, gatewayServedRuntimes...), deployedRuntimes...)

	for _, runtime := range allRuntimes {
		for _, hardShutdown := range []bool{false, true} {
			name := string(runtime)
			if hardShutdown {
				name += " hard"
			}
			t.Run(name, func(t *testing.T) {
				if err := backend.shutdownServer(t.Context(), "any-server", hardShutdown); err != nil {
					t.Fatalf("shutdownServer must not error, got: %v", err)
				}
			})
		}
	}
}

// transformObotHostname is the identity function: Obot is reachable at its
// external Cloud Foundry route and no MCP workload runs anywhere needing a
// rewritten hostname. Routing over the CF internal domain would change this.
func TestCloudFoundryTransformObotHostnameIsIdentity(t *testing.T) {
	backend := &cloudFoundryBackend{}

	for _, url := range []string{
		"https://obot.app.cloud.gov/oauth/token",
		"http://localhost:8080/oauth/token",
		"https://cdc-places.apps.internal:8080/mcp",
		"not-a-url",
		"",
	} {
		if got := backend.transformObotHostname(url); got != url {
			t.Fatalf("transformObotHostname(%q) = %q, want identity", url, got)
		}
	}
}

// remoteConfig must return the operator's global validation config untouched and
// grant no network exceptions. The docker backend has to relax private-IP
// blocking to reach bridge-network containers; this backend talks to nothing
// internal, so weakening the operator's posture here would be a regression.
// remoteConfig must not widen the operator's address-range flags. Only the
// allow list is the backend's contribution. Widening the flags here would
// relax blocking for EVERY remote server, including operator- and
// partner-supplied URLs, which is the hole this design exists to avoid.
func TestCloudFoundryRemoteConfigDoesNotWidenOperatorFlags(t *testing.T) {
	backend := &cloudFoundryBackend{}

	for _, global := range []RemoteMCPURLValidationConfig{
		{},
		{AllowLocalhostMCP: true, AllowPrivateIPMCP: true, AllowLinkLocalMCP: true},
		{AllowPrivateIPMCP: true},
		{AllowLocalhostMCP: true},
	} {
		got, _ := backend.remoteConfig(global)

		if got.AllowLocalhostMCP != global.AllowLocalhostMCP ||
			got.AllowPrivateIPMCP != global.AllowPrivateIPMCP ||
			got.AllowLinkLocalMCP != global.AllowLinkLocalMCP {
			t.Fatalf("remoteConfig(%+v) changed the operator flags: %+v", global, got)
		}
	}
}

// The allow list must contain exactly the Cloud Foundry internal domain and MCP
// listener port.
//
// MCP servers this backend cannot deploy are instead run as their own CF apps
// on apps.internal, which resolves into 10.255.0.0/16 -- RFC1918. Without this
// entry, Obot's default DisallowPrivateIPMCP rejects them at admission, before
// any network plumbing is exercised.
func TestCloudFoundryRemoteConfigAllowsInternalDomain(t *testing.T) {
	backend := &cloudFoundryBackend{}

	_, allowList := backend.remoteConfig(RemoteMCPURLValidationConfig{})

	if len(allowList) != 1 {
		t.Fatalf("expected exactly one allow-list entry, got %v", allowList)
	}
	if allowList[0] != "*.apps.internal:8080" {
		t.Fatalf("allow list = %q, want %q", allowList[0], "*.apps.internal:8080")
	}
}

// The allow list is only useful if it admits real internal routes and nothing
// else. Assert against safehttp.HostAllowed -- the same function the dialer
// uses -- rather than re-deriving the matching rules here.
func TestCloudFoundryAllowListScope(t *testing.T) {
	backend := &cloudFoundryBackend{}
	_, allowList := backend.remoteConfig(RemoteMCPURLValidationConfig{})

	tests := []struct {
		name string
		host string
		port string
		want bool
	}{
		{name: "internal MCP route", host: "mcp-cdc-places.apps.internal", port: "8080", want: true},
		{name: "another internal route", host: "mcp-nih-reporter.apps.internal", port: "8080", want: true},
		{name: "internal route on another port", host: "mcp-cdc-places.apps.internal", port: "80", want: false},
		{name: "internal apex is not a route", host: "apps.internal", port: "8080", want: false},
		{name: "lookalike domain", host: "evil-apps.internal", port: "8080", want: false},
		{name: "attacker-controlled suffix", host: "apps.internal.evil.com", port: "443", want: false},
		{name: "public route", host: "mcp-server-hub.app.cloud.gov", port: "443", want: false},
		{name: "cloud metadata endpoint", host: "169.254.169.254", port: "80", want: false},
		{name: "loopback", host: "127.0.0.1", port: "8080", want: false},
		{name: "arbitrary private IP", host: "10.0.0.5", port: "8080", want: false},
		{name: "kubernetes service domain", host: "svc.cluster.local", port: "443", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safehttp.HostAllowed(allowList, tt.host, tt.port); got != tt.want {
				t.Errorf("HostAllowed(%q, %q) = %v, want %v", tt.host, tt.port, got, tt.want)
			}
		})
	}
}

// The compile-time interface assertion lives in the var block at the top of this
// file. This test covers the constructor instead: it must hand back a usable
// implementation rather than a typed nil, which would satisfy the interface at
// compile time but panic on first use.
func TestNewCloudFoundryBackendReturnsUsableBackend(t *testing.T) {
	b := newCloudFoundryBackend(true, 8080, Options{})
	if b == nil {
		t.Fatal("expected a backend implementation, got nil")
	}

	cf, ok := b.(*cloudFoundryBackend)
	if !ok {
		t.Fatalf("expected *cloudFoundryBackend, got %T", b)
	}
	if cf == nil {
		t.Fatal("expected a non-nil *cloudFoundryBackend")
	}
	if !cf.authEnabled {
		t.Error("authEnabled = false, want true (constructor argument ignored)")
	}
	if cf.httpListenPort != 8080 {
		t.Errorf("httpListenPort = %d, want 8080 (constructor argument ignored)", cf.httpListenPort)
	}
}

// End-to-end admission check for the Tier 2 topology: an MCP server deployed as
// its own Cloud Foundry app on apps.internal, registered as a remote server.
//
// This is the case that motivated the allow list. apps.internal resolves into
// 10.255.0.0/16, so with Obot's default DisallowPrivateIPMCP the URL is
// rejected at admission -- in the admin UI, before any network policy or C2C
// path is exercised. The test asserts the allow list reaches
// ValidateRemoteMCPURL, and that it opens nothing wider than that one domain.
//
// Hostnames here never resolve, which is the point: the allow list must be
// consulted BEFORE DNS, exactly as safeDialer.checkHost does. A blocked host
// therefore fails on resolution rather than on address range, so the assertion
// is only that it fails.
func TestCloudFoundryInternalURLPassesRemoteValidation(t *testing.T) {
	backend := &cloudFoundryBackend{}
	// The operator's strictest posture, which is also the default.
	strict := RemoteMCPURLValidationConfig{
		AllowLocalhostMCP: false,
		AllowPrivateIPMCP: false,
		AllowLinkLocalMCP: false,
	}
	_, allowList := backend.remoteConfig(strict)
	strict.AllowedHosts = allowList

	t.Run("internal MCP URL is admitted", func(t *testing.T) {
		for _, rawURL := range []string{
			"http://mcp-cdc-places.apps.internal:8080/mcp",
			"http://mcp-nih-reporter.apps.internal:8080/mcp",
			"http://some-server.apps.internal:8080/mcp",
		} {
			if err := ValidateRemoteMCPURL(t.Context(), rawURL, strict); err != nil {
				t.Errorf("ValidateRemoteMCPURL(%q) = %v, want nil", rawURL, err)
			}
		}
	})

	t.Run("the allow list does not admit anything else", func(t *testing.T) {
		for _, rawURL := range []string{
			"http://localhost:8080/mcp",
			"http://127.0.0.1:8080/mcp",
			"http://169.254.169.254/latest/meta-data/",
			"http://10.0.0.5:8080/mcp",
			"http://mcp-cdc-places.apps.internal/mcp",
			"http://apps.internal/mcp",
			"http://evil-apps.internal/mcp",
			"http://apps.internal.attacker.example/mcp",
		} {
			if err := ValidateRemoteMCPURL(t.Context(), rawURL, strict); err == nil {
				t.Errorf("ValidateRemoteMCPURL(%q) = nil, want an error", rawURL)
			}
		}
	})

	t.Run("without the allow list the internal URL is rejected", func(t *testing.T) {
		// Pins the regression this fixes: the same URL under the same operator
		// policy, minus the backend's contribution, must fail.
		noAllowList := strict
		noAllowList.AllowedHosts = nil

		if err := ValidateRemoteMCPURL(t.Context(), "http://mcp-cdc-places.apps.internal:8080/mcp", noAllowList); err == nil {
			t.Error("expected rejection without the backend allow list")
		}
	})
}
