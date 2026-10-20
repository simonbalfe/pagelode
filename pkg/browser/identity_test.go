package browser

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/simonbalfe/pagelode/pkg/page"
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
	identities, err := NewIdentities("http://user-{session}:secret@proxy.example:1000", 1, store)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New([]string{server.URL}, "", 0, 4, identities)
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

	restarted, err := NewIdentities("http://user-{session}:secret@proxy.example:1000", 1, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, key, _ := restarted.identity("shop.example"); key != third.body["identity"] {
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

const stickyTemplate = "http://user-{session}:secret@proxy.example:1000"

func TestIdentitiesMapHostsToStableSlots(t *testing.T) {
	identities, err := NewIdentities(stickyTemplate, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxies := map[int]string{}
	for index := range 64 {
		host := fmt.Sprintf("host-%d.example", index)
		slot, key, proxyURL := identities.identity(host)
		againSlot, againKey, againProxy := identities.identity(strings.ToUpper(host))
		if againSlot != slot || againKey != key || againProxy != proxyURL {
			t.Errorf("identity(%q) = %d %s %s, identity(upper) = %d %s %s; want the same identity", host, slot, key, proxyURL, againSlot, againKey, againProxy)
		}
		if got := identities.ProxyFor(host); got != proxyURL {
			t.Errorf("ProxyFor(%q) = %q, want the browser identity's proxy %q", host, got, proxyURL)
		}
		proxies[slot] = proxyURL
	}
	if got, want := len(proxies), 4; got != want {
		t.Fatalf("slots used by 64 hosts = %d, want %d", got, want)
	}
	if got := slices.Compact(slices.Sorted(maps.Values(proxies))); len(got) != 4 {
		t.Errorf("distinct slot proxies = %v, want one session per slot", got)
	}
}

func TestRotationOnlyMovesBlockedSlot(t *testing.T) {
	base := "pl-" + shortHash(stickyTemplate)
	legacyKeys := []string{base + "-g0", base + "-s1-g0", base + "-s2-g0"}
	for _, test := range []struct {
		name      string
		blocked   int
		wantKeys  []string
		wantStore generations
	}{
		{name: "slot 0 keeps legacy name", blocked: 0, wantKeys: []string{base + "-g1", base + "-s1-g0", base + "-s2-g0"}, wantStore: generations{base: 1}},
		{name: "slot 2", blocked: 2, wantKeys: []string{base + "-g0", base + "-s1-g0", base + "-s2-g1"}, wantStore: generations{base + "-s2": 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := generations{}
			identities, err := NewIdentities(stickyTemplate, 3, store)
			if err != nil {
				t.Fatal(err)
			}
			hosts := hostPerSlot(t, identities)
			if got := slotKeys(identities, hosts); !slices.Equal(got, legacyKeys) {
				t.Fatalf("initial keys = %v, want %v", got, legacyKeys)
			}
			host := hosts[test.blocked]
			var retired string
			for range rotateAfterBlocks {
				_, key, _ := identities.identity(host)
				retired, _, err = identities.observe(key, host, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			if retired != legacyKeys[test.blocked] {
				t.Errorf("retired = %q, want %q", retired, legacyKeys[test.blocked])
			}
			if got := slotKeys(identities, hosts); !slices.Equal(got, test.wantKeys) {
				t.Errorf("keys after rotation = %v, want %v", got, test.wantKeys)
			}
			if !maps.Equal(store, test.wantStore) {
				t.Errorf("stored generations = %v, want %v", store, test.wantStore)
			}
		})
	}
}

func TestSingleSlotResumesLegacyGeneration(t *testing.T) {
	base := "pl-" + shortHash(stickyTemplate)
	identities, err := NewIdentities(stickyTemplate, 1, generations{base: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, got, _ := identities.identity("shop.example"); got != base+"-g3" {
		t.Errorf("key = %q, want %q", got, base+"-g3")
	}
}

func hostPerSlot(t *testing.T, identities *Identities) []string {
	t.Helper()
	hosts := make([]string, len(identities.slots))
	for index := 0; index < 1000 && slices.Contains(hosts, ""); index++ {
		host := fmt.Sprintf("site-%d.example", index)
		if slot := identities.slotFor(host); hosts[slot] == "" {
			hosts[slot] = host
		}
	}
	if slices.Contains(hosts, "") {
		t.Fatalf("hosts per slot = %q, want every slot covered", hosts)
	}
	return hosts
}

func slotKeys(identities *Identities, hosts []string) []string {
	keys := make([]string, len(hosts))
	for index, host := range hosts {
		_, keys[index], _ = identities.identity(host)
	}
	return keys
}
