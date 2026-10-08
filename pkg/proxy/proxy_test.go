package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/obot-platform/obot/pkg/auth"
)

// The staged provider is allowed to beat the session's provider only for a browser carrying an
// open verification. This covers the gate that decides that, before any provider lookup happens:
// without the cookie the answer must be "no staged provider" regardless of what is staged, so an
// ordinary request can never be routed to a replacement that is not serving logins yet.
//
// The remaining conditions -- that something is staged, and that the cookie names an open
// verification -- run through the dispatcher, and LoginableAuthProvider is tested against them in
// its own package.
func TestStagedVerificationProviderRequiresTheVerifyCookie(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		cookie string
	}{
		{
			name: "no cookie at all",
			path: "/oauth2/start",
		},
		{
			name:   "some other cookie",
			path:   "/oauth2/start",
			cookie: CurrentAuthProviderCookie,
		},
	}

	// A nil dispatcher makes the assertion sharper than an equality check: reaching a provider
	// lookup at all would panic, so passing proves the gate returned first.
	pm := &Manager{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: tt.cookie, Value: "default/google"})
			}
			if got := pm.stagedVerificationProvider(req); got != "" {
				t.Fatalf("stagedVerificationProvider() = %q, want %q", got, "")
			}
		})
	}
}

// An empty verify cookie is a cookie the browser still sends after it has been cleared, so it must
// be treated as absent rather than as an open verification.
func TestStagedVerificationProviderIgnoresAnEmptyVerifyCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start", nil)
	req.AddCookie(&http.Cookie{Name: auth.AuthProviderVerifyCookie, Value: ""})

	pm := &Manager{}
	if got := pm.stagedVerificationProvider(req); got != "" {
		t.Fatalf("stagedVerificationProvider() = %q, want %q", got, "")
	}
}

func TestValidateSerializableState(t *testing.T) {
	tests := []struct {
		name    string
		state   serializableState
		wantErr bool
	}{
		{name: "valid", state: serializableState{User: "subject", Email: " User@GSA.GOV "}},
		{name: "missing user ID", state: serializableState{Email: "user@gsa.gov"}, wantErr: true},
		{name: "missing email", state: serializableState{User: "subject"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSerializableState(&tt.state)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSerializableState() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && tt.state.Email != "user@gsa.gov" {
				t.Fatalf("normalized email = %q, want user@gsa.gov", tt.state.Email)
			}
		})
	}
}
