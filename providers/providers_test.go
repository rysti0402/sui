package providers

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseDomains(t *testing.T) {
	cases := map[string][]string{
		"Host(`sui.example.com`)":                        {"sui.example.com"},
		"Host(`a.example.com`, `b.example.com`)":         {"a.example.com", "b.example.com"},
		"Host(`a.example.com`) || Host(`B.example.com`)": {"a.example.com", "b.example.com"},
		"Host(`x.example.com`) && Headers(`k`, `v`)":     {"x.example.com"},
		"HostRegexp(`{sub:[a-z]+}.example.com`)":         nil,
		"HostSNI(`*`)":                                   nil,
		"PathPrefix(`/api`) && Host(\"q.example.com\")":  {"q.example.com"},
		"": nil,
	}
	for rule, want := range cases {
		if got := parseDomains(rule); !reflect.DeepEqual(got, want) {
			t.Errorf("parseDomains(%q) = %v, want %v", rule, got, want)
		}
	}
}

// countingServer counts distinct TCP connections the server accepts
func countingServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *int64) {
	var conns int64
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			atomic.AddInt64(&conns, 1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &conns
}

func TestTraefikReusesConnections(t *testing.T) {
	srv, conns := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			fmt.Fprint(w, `{"Version":"3.6.1","Codename":"ramequin"}`)
		case "/api/http/routers":
			fmt.Fprint(w, `[{"service":"dozzle-svc","rule":"Host(`+"`dozzle.example.com`"+`)","status":"enabled"},
			                {"service":"sui-svc","rule":"Host(`+"`sui.example.com`"+`)","status":"enabled"}]`)
		default:
			http.NotFound(w, r)
		}
	})
	tr := &Traefik{URL: srv.URL, Dockers: map[string]*Docker{}}
	if !tr.TestConnection(false) {
		t.Fatal("version check failed")
	}
	for i := 0; i < 500; i++ {
		apps := tr.GetApps()
		if len(apps) != 2 || apps["dozzle-svc"].URL != "https://dozzle.example.com" {
			t.Fatalf("unexpected apps on iteration %d: %+v", i, apps)
		}
	}
	if n := atomic.LoadInt64(conns); n > 2 {
		t.Fatalf("500 refreshes opened %d connections; responses are leaking", n)
	}
}

func TestTraefikErrorStatusDoesNotLeak(t *testing.T) {
	srv, conns := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	})
	tr := &Traefik{URL: srv.URL}
	for i := 0; i < 200; i++ {
		if apps := tr.GetApps(); apps != nil {
			t.Fatal("expected nil apps on error")
		}
	}
	if n := atomic.LoadInt64(conns); n > 2 {
		t.Fatalf("200 failed refreshes opened %d connections", n)
	}
}

func TestDockerProvider(t *testing.T) {
	var listCalls int64
	srv, conns := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1.") {
			t.Errorf("request used a pinned api version: %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"Version":"29.7.1","ApiVersion":"1.55"}`)
		case "/containers/json":
			atomic.AddInt64(&listCalls, 1)
			fmt.Fprint(w, `[{"Id":"1","Names":["/sui"],"Labels":{"sui.enabled":"true","sui.name":"dashboard","sui.icon":"home"}},
			                {"Id":"2","Names":["/plex"],"Labels":{}}]`)
		default:
			http.NotFound(w, r)
		}
	})
	host := strings.TrimPrefix(srv.URL, "http://")
	_, port, _ := net.SplitHostPort(host)

	// hostnames are accepted now, not just IPs
	for _, url := range []string{host, "localhost:" + port} {
		cnf := &DockerConfig{ConnType: "tcp", ConnURL: url}
		client, base, err := cnf.createClient()
		if err != nil {
			t.Fatalf("createClient(%s): %v", url, err)
		}
		dkr := &Docker{Client: client, BaseURL: base}
		if !dkr.TestConnection(false) {
			t.Fatalf("version check failed for %s", url)
		}
		for i := 0; i < 100; i++ {
			apps := dkr.GetApps()
			if len(apps) != 1 || apps["dashboard"] == nil || apps["dashboard"].Icon != "home" {
				t.Fatalf("unexpected apps: %+v", apps)
			}
		}
	}
	if n := atomic.LoadInt64(&listCalls); n > 4 {
		t.Errorf("container list fetched %d times; cache not working", n)
	}
	if n := atomic.LoadInt64(conns); n > 4 {
		t.Errorf("opened %d connections", n)
	}
}

func TestDockerTCPValidation(t *testing.T) {
	good := []string{"172.16.2.254:2375", "socket-proxy:2375", "[::1]:2375"}
	bad := []string{"socket-proxy", "socket-proxy:", ":2375", "host:99999", "host:abc"}
	for _, u := range good {
		if !(&DockerConfig{ConnURL: u}).dockerTCPOkay() {
			t.Errorf("%q should be valid", u)
		}
	}
	for _, u := range bad {
		if (&DockerConfig{ConnURL: u}).dockerTCPOkay() {
			t.Errorf("%q should be invalid", u)
		}
	}
}
