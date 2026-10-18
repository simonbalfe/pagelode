package browsertest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

const startTimeout = 20 * time.Second

func Start(t testing.TB) string {
	t.Helper()
	root := repositoryRoot(t)
	port := freePort(t)
	command := exec.Command("bun", filepath.Join(root, "browser-service", "src", "server.ts"))
	command.Dir = root
	command.Env = append(os.Environ(),
		"BROWSER_SERVICE_PORT="+strconv.Itoa(port),
		"BROWSER_SERVICE_HOST=127.0.0.1",
		"BROWSER_SERVICE_HEADLESS=true",
		"BROWSER_SERVICE_CHANNEL=",
		"BROWSER_SERVICE_TOKEN=",
		"BROWSER_SERVICE_DATA_DIR="+t.TempDir(),
		"BROWSER_SERVICE_PROFILES_DIR="+t.TempDir(),
	)
	output := &lockedBuffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatalf("start browser service: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	t.Cleanup(func() {
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("stop browser service: %v", err)
		}
		<-exited
	})
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitHealthy(baseURL, exited); err != nil {
		t.Fatalf("browser service: %v\n%s", err, output.String())
	}
	return baseURL
}

func waitHealthy(baseURL string, exited <-chan error) error {
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return err
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			healthy := response.StatusCode == http.StatusOK
			if closeErr := response.Body.Close(); closeErr != nil {
				return closeErr
			}
			if healthy {
				return nil
			}
		}
		select {
		case err := <-exited:
			return fmt.Errorf("exited before becoming healthy: %v", err)
		case <-ctx.Done():
			return errors.New("not healthy within " + startTimeout.String())
		case <-ticker.C:
		}
	}
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("go.mod not found above working directory")
		}
		directory = parent
	}
}

func freePort(t testing.TB) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
