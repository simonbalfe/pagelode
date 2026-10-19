package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/simonbalfe/pagelode/internal/proxy"
)

const rotateAfterBlocks = 2

type GenerationStore interface {
	Generation(name string) (int, error)
	SaveGeneration(name string, generation int) error
}

type Identities struct {
	mu         sync.Mutex
	template   string
	name       string
	generation int
	blocks     map[string]int
	store      GenerationStore
}

func NewIdentities(proxyTemplate string, store GenerationStore) (*Identities, error) {
	name := "pl-" + shortHash(proxyTemplate)
	generation := 0
	if store != nil {
		stored, err := store.Generation(name)
		if err != nil {
			return nil, fmt.Errorf("browser identities: %w", err)
		}
		generation = stored
	}
	return &Identities{template: proxyTemplate, name: name, generation: generation, blocks: make(map[string]int), store: store}, nil
}

func (i *Identities) current() (string, string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	key := i.keyLocked()
	return key, i.proxyFor(key)
}

func (i *Identities) CurrentProxy() string {
	_, proxyURL := i.current()
	return proxyURL
}
func (i *Identities) profileProxy(profile string) string {
	return i.proxyFor("profile:" + profile)
}

func (i *Identities) proxyFor(seed string) string {
	if i.template == "" {
		return ""
	}
	return proxy.WithSession(i.template, shortHash(seed))
}

func (i *Identities) observe(key string, host string, blocked bool) (string, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if key != i.keyLocked() {
		return "", false, nil
	}
	if !blocked {
		delete(i.blocks, host)
		return "", false, nil
	}
	i.blocks[host]++
	if i.blocks[host] < rotateAfterBlocks || !proxy.Sticky(i.template) {
		return "", false, nil
	}
	retired := key
	i.generation++
	clear(i.blocks)
	if i.store == nil {
		return retired, true, nil
	}
	if err := i.store.SaveGeneration(i.name, i.generation); err != nil {
		return retired, true, fmt.Errorf("browser identities: %w", err)
	}
	return retired, true, nil
}

func (i *Identities) keyLocked() string {
	return fmt.Sprintf("%s-g%d", i.name, i.generation)
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
