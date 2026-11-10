package ghapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func server(t *testing.T) *Client {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/o/r/actions/runners/registration-token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"message":"Bad credentials"}`, 401)
			return
		}
		w.Write([]byte(`{"token":"REG"}`))
	})
	mux.HandleFunc("POST /orgs/acme/actions/runners/remove-token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"RM"}`))
	})
	mux.HandleFunc("GET /repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"private":true}`))
	})
	mux.HandleFunc("GET /repos/actions/runner/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v2.330.0","body":"b","assets":[{"name":"actions-runner-linux-x64-2.330.0.tar.gz","browser_download_url":"u","digest":"sha256:abc"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{API: srv.URL, Token: "tok", HTTP: srv.Client()}
}

func TestTokens(t *testing.T) {
	c := server(t)
	if tok, err := c.RegistrationToken("o/r"); err != nil || tok != "REG" {
		t.Fatalf("registration: %q %v", tok, err)
	}
	if tok, err := c.RemovalToken("acme"); err != nil || tok != "RM" {
		t.Fatalf("removal: %q %v", tok, err)
	}
	c.Token = "wrong"
	_, err := c.RegistrationToken("o/r")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected a 401 error, got %v", err)
	}
}

func TestVisibilityAndRelease(t *testing.T) {
	c := server(t)
	if private, err := c.IsPrivate("o/r"); err != nil || !private {
		t.Fatalf("private: %v %v", private, err)
	}
	rel, err := c.LatestRunner()
	if err != nil || rel.Version != "2.330.0" || len(rel.Assets) != 1 || rel.Assets[0].Digest != "sha256:abc" {
		t.Fatalf("release: %+v %v", rel, err)
	}
}

func TestNotFoundMentionsPermissions(t *testing.T) {
	c := server(t)
	_, err := c.IsPrivate("o/missing")
	if err == nil || !strings.Contains(err.Error(), "administration") {
		t.Fatalf("expected a permissions hint, got %v", err)
	}
}

func TestValidScope(t *testing.T) {
	for _, ok := range []string{"o/r", "my-org", "a.b/c_d"} {
		if ValidScope(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "a/b/c", "/r", "o/", "a b", "o/../x"} {
		if ValidScope(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestResolveTokenPrefersFlag(t *testing.T) {
	t.Setenv("GH_TOKEN", "env")
	if tok, _ := ResolveToken("flag"); tok != "flag" {
		t.Fatal("flag must win")
	}
	if tok, _ := ResolveToken(""); tok != "env" {
		t.Fatal("env must be used")
	}
}
