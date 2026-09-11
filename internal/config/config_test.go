package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("PAGELODE_PATCHRIGHT_COMMAND", "")
	t.Setenv("PAGELODE_PATCHRIGHT_WORKER", "")
	t.Setenv("PAGELODE_BROWSER_CONCURRENCY", "")
	t.Setenv("PAGELODE_MAX_CONCURRENCY", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Address != ":8083" {
		t.Errorf("Load().Address = %q, want :8083", got.Address)
	}
	if !got.RodEnabled {
		t.Error("Load().RodEnabled = false, want true")
	}
	if got.PatchrightCommand != "bun" {
		t.Errorf("Load().PatchrightCommand = %q, want bun", got.PatchrightCommand)
	}
	if got.PatchrightWorker != "browser/src/worker.ts" {
		t.Errorf("Load().PatchrightWorker = %q, want browser/src/worker.ts", got.PatchrightWorker)
	}
}
