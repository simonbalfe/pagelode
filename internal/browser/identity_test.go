package browser

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/internal/page"
)

type generations map[string]int

func (g generations) Generation(name string) (int, error) { return g[name], nil }

func (g generations) SaveGeneration(name string, generation int) error {
	g[name] = generation
	return nil
}

func TestRepeatedBlocksRotateToNewIdentityAndSession(t *testing.T) {
	server, requests := fakeService(t, http.StatusOK, `{"status_code":403}`)
	store := generations{}
	identities, err := NewIdentities("http://user-{session}:secret@proxy.example:1000", store)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(server.URL, "", 0, identities)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	fetch(t, client, ctx)
	first := <-requests
	firstKey, firstProxy := first.body["identity"].(string), first.body["proxy_url"].(string)
	if firstKey == "" || strings.Contains(firstProxy, "{session}") || !strings.HasPrefix(firstProxy, "http://user-") {
		t.Fatalf("first render identity = %q, proxy = %q; want a key and a concrete session proxy", firstKey, firstProxy)
	}

	fetch(t, client, ctx)
	if second := <-requests; second.body["identity"] != firstKey {
		t.Fatalf("second render identity = %v, want %q before rotation", second.body["identity"], firstKey)
	}
	if rotation := <-requests; rotation.method != http.MethodDelete || rotation.path != "/v1/identities/"+firstKey {
		t.Fatalf("after two blocks request = %s %s, want DELETE /v1/identities/%s", rotation.method, rotation.path, firstKey)
	}

	fetch(t, client, ctx)
	third := <-requests
	if third.body["identity"] == firstKey || third.body["proxy_url"] == firstProxy {
		t.Errorf("after rotation identity = %v, proxy = %v; want a new key and session", third.body["identity"], third.body["proxy_url"])
	}
	if len(store) != 1 {
		t.Errorf("stored generations = %v, want the rotation persisted", store)
	}

	restarted, err := NewIdentities("http://user-{session}:secret@proxy.example:1000", store)
	if err != nil {
		t.Fatal(err)
	}
	if key, _ := restarted.current(); key != third.body["identity"] {
		t.Errorf("identity after restart = %q, want %v so a blocked session is not reused", key, third.body["identity"])
	}
}

func TestBlocksWithoutStickyProxyKeepIdentity(t *testing.T) {
	server, requests := fakeService(t, http.StatusOK, `{"status_code":403}`)
	client := newClient(t, server.URL, "", "http://user:secret@proxy.example:1000")
	ctx := context.Background()
	for range 3 {
		fetch(t, client, ctx)
	}
	keys := map[any]bool{}
	for range 3 {
		record := <-requests
		if record.method != http.MethodPost {
			t.Fatalf("request = %s %s; want only renders when the proxy cannot change session", record.method, record.path)
		}
		keys[record.body["identity"]] = true
	}
	if len(keys) != 1 {
		t.Errorf("identities used = %v, want one", keys)
	}
}

func fetch(t *testing.T, client *Client, ctx context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := client.Fetch(ctx, "https://shop.example/item", page.Session{}); err != nil {
		t.Fatal(err)
	}
}
