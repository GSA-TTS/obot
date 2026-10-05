package mcp

import (
	"errors"
	"testing"

	otypes "github.com/obot-platform/obot/apiclient/types"
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
func TestCloudFoundryRemoteConfigGrantsNoExceptions(t *testing.T) {
	backend := &cloudFoundryBackend{}

	for _, global := range []RemoteMCPURLValidationConfig{
		{},
		{AllowLocalhostMCP: true, AllowPrivateIPMCP: true, AllowLinkLocalMCP: true},
		{AllowPrivateIPMCP: true},
	} {
		got, allowlist := backend.remoteConfig(global)
		if got != global {
			t.Fatalf("remoteConfig(%+v) = %+v, want unchanged", global, got)
		}
		if len(allowlist) != 0 {
			t.Fatalf("expected empty allowlist, got %v", allowlist)
		}
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
