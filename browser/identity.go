package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/simonbalfe/pagelode/proxy"
)

const rotateAfterBlocks = 2

type GenerationStore interface {
	Generation(name string) (int, error)
	SaveGeneration(name string, generation int) error
}

type Identities struct {
	template string
	store    GenerationStore
	slots    []identitySlot
}

type identitySlot struct {
	mu         sync.Mutex
	name       string
	generation int
	blocks     map[string]int
}

func NewIdentities(proxyTemplate string, slots int, store GenerationStore) (*Identities, error) {
	if slots < 1 {
		return nil, fmt.Errorf("browser identities: slots must be at least 1, got %d", slots)
	}
	base := "pl-" + shortHash(proxyTemplate)
	identities := &Identities{template: proxyTemplate, store: store, slots: make([]identitySlot, slots)}
	for index := range identities.slots {
		slot := &identities.slots[index]
		slot.name = base
		if index > 0 {
			slot.name = fmt.Sprintf("%s-s%d", base, index)
		}
		slot.blocks = make(map[string]int)
		if store == nil {
			continue
		}
		generation, err := store.Generation(slot.name)
		if err != nil {
			return nil, fmt.Errorf("browser identities: %w", err)
		}
		slot.generation = generation
	}
	return identities, nil
}

func (i *Identities) ProxyFor(host string) string {
	_, _, proxyURL := i.identity(host)
	return proxyURL
}

func (i *Identities) identity(host string) (int, string, string) {
	index := i.slotFor(host)
	slot := &i.slots[index]
	slot.mu.Lock()
	key := slot.keyLocked()
	slot.mu.Unlock()
	return index, key, i.seededProxy(key)
}

func (i *Identities) slotFor(host string) int {
	return bucket(strings.ToLower(host), len(i.slots))
}

func (i *Identities) profileProxy(profile string) string {
	return i.seededProxy("profile:" + profile)
}

func (i *Identities) seededProxy(seed string) string {
	if i.template == "" {
		return ""
	}
	return proxy.WithSession(i.template, shortHash(seed))
}

func (i *Identities) observe(key string, host string, blocked bool) (string, bool, error) {
	slot := &i.slots[i.slotFor(host)]
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if key != slot.keyLocked() {
		return "", false, nil
	}
	if !blocked {
		delete(slot.blocks, host)
		return "", false, nil
	}
	slot.blocks[host]++
	if slot.blocks[host] < rotateAfterBlocks || !proxy.Sticky(i.template) {
		return "", false, nil
	}
	retired := key
	slot.generation++
	clear(slot.blocks)
	if i.store == nil {
		return retired, true, nil
	}
	if err := i.store.SaveGeneration(slot.name, slot.generation); err != nil {
		return retired, true, fmt.Errorf("browser identities: %w", err)
	}
	return retired, true, nil
}

func (s *identitySlot) keyLocked() string {
	return fmt.Sprintf("%s-g%d", s.name, s.generation)
}

func bucket(value string, n int) int {
	hash := fnv.New32a()
	hash.Write([]byte(value))
	return int(hash.Sum32() % uint32(n))
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
