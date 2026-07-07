package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// browserIdleTTL: a warm Chromium with no active stream is reaped after this
// long idle. Long enough that reopening the same box/worktree within a work
// session reuses the warm instance (instant frames, no respawn); short enough
// that abandoned instances don't accumulate. An instance with a live stream is
// never reaped regardless of TTL (streams counter, not frame cadence — a static
// page emits no frames but is still being watched).
const browserIdleTTL = 15 * time.Minute

// Embedded browser ("general browser pane"): the daemon runs a headless Chromium
// and bridges Chrome DevTools Protocol (CDP) screencast frames + input to the
// Agora app over a WebSocket. The app renders the frames and forwards
// clicks/keys, so a human gets an interactive browser inside the editor that can
// (a) load the dev-server preview URL or any URL, and (b) be the Chromium an
// automation script attaches to via `connectOverCDP(<cdp_url>)`, so you watch
// the automation run. Self-host only (browser ↔ daemon on the same host).

type chromeInstance struct {
	dbgPort     int
	cmd         *exec.Cmd
	pageWSURL   string
	userDataDir string
	done        chan struct{}
	// lastUsed + streams gate the idle reaper (both guarded by browserManager.mu).
	// streams is the count of live screencast connections; while > 0 the instance
	// is in use and never reaped. lastUsed marks the last connect/disconnect so a
	// zero-stream instance is reaped only after browserIdleTTL of true idleness.
	lastUsed time.Time
	streams  int
}

func (c *chromeInstance) running() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

type browserManager struct {
	mu        sync.Mutex
	instances map[string]*chromeInstance // key = workdir
	logger    *slog.Logger
}

func newBrowserManager(logger *slog.Logger) *browserManager {
	bm := &browserManager{instances: make(map[string]*chromeInstance), logger: logger}
	go bm.reapIdle()
	return bm
}

// reapIdle kills warm Chromium instances that have had no active stream for
// browserIdleTTL, so abandoned browsers don't leak. Runs for the daemon's life.
func (bm *browserManager) reapIdle() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		bm.mu.Lock()
		for key, inst := range bm.instances {
			if !inst.running() {
				delete(bm.instances, key)
				continue
			}
			if inst.streams == 0 && now.Sub(inst.lastUsed) > browserIdleTTL {
				killProcessGroup(inst.cmd)
				delete(bm.instances, key)
				if bm.logger != nil {
					bm.logger.Info("reaped idle embedded browser", "key", key, "idle", now.Sub(inst.lastUsed).String())
				}
			}
		}
		bm.mu.Unlock()
	}
}

var browserUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		return o == "" || strings.HasPrefix(o, "http://localhost") || strings.HasPrefix(o, "http://127.0.0.1")
	},
}

func browserCORS(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}

// detectChromium finds a Chromium/Chrome executable, preferring a Playwright
// chromium (the common automation build), then the system browsers.
func detectChromium() string {
	// Roots holding Playwright browser installs: the per-user cache, plus an
	// explicit PLAYWRIGHT_BROWSERS_PATH (the cloud daemon image bakes browsers
	// into a shared path outside HOME, since HOME is a mounted volume there).
	var globs []string
	if root := strings.TrimSpace(os.Getenv("PLAYWRIGHT_BROWSERS_PATH")); root != "" && root != "0" {
		globs = append(globs,
			filepath.Join(root, "chromium-*/chrome-linux/chrome"),
			filepath.Join(root, "chromium_headless_shell-*/chrome-linux/headless_shell"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil {
		globs = append(globs,
			filepath.Join(home, "Library/Caches/ms-playwright/chromium-*/chrome-mac/Chromium.app/Contents/MacOS/Chromium"),
			filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux/chrome"),
			filepath.Join(home, ".cache/puppeteer/chrome/*/chrome-*/Google Chrome for Testing"),
			// run_test_cases installs chromium-headless-shell (not full chromium)
			// per box — it drives CDP + screencast the same, so accept it as the
			// live-browser binary rather than failing "no Chromium found".
			filepath.Join(home, "Library/Caches/ms-playwright/chromium_headless_shell-*/chrome-mac/headless_shell"),
			filepath.Join(home, ".cache/ms-playwright/chromium_headless_shell-*/chrome-linux/headless_shell"),
		)
	}
	for _, g := range globs {
		if m, _ := filepath.Glob(g); len(m) > 0 {
			sort.Strings(m)
			return m[len(m)-1]
		}
	}
	for _, c := range []string{
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// ensureChrome launches (or reuses) a headless Chromium for key and returns it.
func (bm *browserManager) ensureChrome(key string) (*chromeInstance, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if inst, ok := bm.instances[key]; ok && inst.running() {
		inst.lastUsed = time.Now()
		return inst, nil
	}
	bin := detectChromium()
	if bin == "" {
		return nil, fmt.Errorf("no Chromium/Chrome found on this host")
	}
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := pl.Addr().(*net.TCPAddr).Port
	pl.Close()
	udd, err := os.MkdirTemp("", "agora-browser-")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--headless=new",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + udd,
		"--no-first-run",
		"--no-default-browser-check",
		"--hide-scrollbars",
		"--disable-gpu",
		"--window-size=1280,800",
	}
	// Containerized daemon (Fly cloud image) runs as root with a tiny /dev/shm;
	// Chromium refuses to start as root with the sandbox on, so relax both ONLY
	// there — a developer's own machine keeps the sandbox.
	if os.Geteuid() == 0 {
		args = append(args, "--no-sandbox", "--disable-dev-shm-usage")
	}
	args = append(args, "about:blank")
	cmd := exec.Command(bin, args...)
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(udd)
		return nil, err
	}
	inst := &chromeInstance{dbgPort: port, cmd: cmd, userDataDir: udd, done: make(chan struct{}), lastUsed: time.Now()}
	go func() {
		_ = cmd.Wait()
		close(inst.done)
		os.RemoveAll(udd)
	}()
	pageWS, err := waitForPageWS(port, 12*time.Second)
	if err != nil {
		killProcessGroup(cmd)
		return nil, err
	}
	inst.pageWSURL = pageWS
	bm.instances[key] = inst
	if bm.logger != nil {
		bm.logger.Info("launched embedded browser", "key", key, "dbg_port", port, "bin", bin)
	}
	return inst, nil
}

// waitForPageWS polls the CDP /json endpoint until a page target's debugger
// WebSocket URL is available.
func waitForPageWS(dbgPort int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/json", dbgPort)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			var targets []struct {
				Type                 string `json:"type"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			for _, t := range targets {
				if t.Type == "page" && t.WebSocketDebuggerURL != "" {
					return t.WebSocketDebuggerURL, nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("chromium devtools did not become ready")
}

func (bm *browserManager) cdpURL(key string) string {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if inst, ok := bm.instances[key]; ok && inst.running() {
		return fmt.Sprintf("http://127.0.0.1:%d", inst.dbgPort)
	}
	return ""
}

func (bm *browserManager) handleStart(w http.ResponseWriter, r *http.Request) {
	if !browserCORS(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		WorkDir string `json:"workdir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(req.WorkDir)
	if key == "" {
		http.Error(w, "workdir is required", http.StatusBadRequest)
		return
	}
	inst, err := bm.ensureChrome(key)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ready":   true,
		"cdp_url": fmt.Sprintf("http://127.0.0.1:%d", inst.dbgPort),
	})
}

func (bm *browserManager) handleStop(w http.ResponseWriter, r *http.Request) {
	if !browserCORS(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		WorkDir string `json:"workdir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(req.WorkDir)
	bm.mu.Lock()
	if inst, ok := bm.instances[key]; ok {
		killProcessGroup(inst.cmd)
		delete(bm.instances, key)
	}
	bm.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"stopped": true})
}

// handleStream upgrades to a WebSocket and bridges CDP screencast ⇄ the app:
// frames flow daemon→app, input + navigation flow app→daemon.
func (bm *browserManager) handleStream(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("workdir"))
	if key == "" {
		http.Error(w, "workdir is required", http.StatusBadRequest)
		return
	}
	inst, err := bm.ensureChrome(key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	client, err := browserUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close()

	// Mark the instance in-use for the life of this stream so the idle reaper
	// never kills a browser someone is actively watching (a static page emits no
	// frames, so frame cadence alone can't tell "idle" from "watching").
	bm.mu.Lock()
	inst.streams++
	inst.lastUsed = time.Now()
	bm.mu.Unlock()
	defer func() {
		bm.mu.Lock()
		inst.streams--
		inst.lastUsed = time.Now()
		bm.mu.Unlock()
	}()

	cdp, _, err := websocket.DefaultDialer.Dial(inst.pageWSURL, nil)
	if err != nil {
		_ = client.WriteJSON(map[string]any{"type": "error", "message": "cdp dial failed: " + err.Error()})
		return
	}
	defer cdp.Close()

	var cdpWriteMu sync.Mutex
	var idCounter int64
	sendCDP := func(method string, params map[string]any) {
		cdpWriteMu.Lock()
		defer cdpWriteMu.Unlock()
		idCounter++
		_ = cdp.WriteJSON(map[string]any{"id": idCounter, "method": method, "params": params})
	}

	sendCDP("Page.enable", nil)
	sendCDP("Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 1280, "height": 800, "deviceScaleFactor": 1, "mobile": false,
	})
	sendCDP("Page.startScreencast", map[string]any{
		"format": "jpeg", "quality": 60, "maxWidth": 1280, "maxHeight": 800, "everyNthFrame": 1,
	})

	// CDP → app: relay screencast frames, ack each.
	go func() {
		for {
			var msg struct {
				Method string `json:"method"`
				Params struct {
					Data      string `json:"data"`
					SessionID int    `json:"sessionId"`
					Metadata  struct {
						DeviceWidth  float64 `json:"deviceWidth"`
						DeviceHeight float64 `json:"deviceHeight"`
					} `json:"metadata"`
				} `json:"params"`
			}
			if err := cdp.ReadJSON(&msg); err != nil {
				client.Close()
				return
			}
			if msg.Method == "Page.screencastFrame" {
				// Relay the JPEG as a BINARY frame, not base64-in-JSON: ~33%
				// fewer bytes and the browser decodes it natively from a Blob
				// URL (no base64→dataURL string alloc per frame). The client is
				// the only writer on this goroutine, so no write mutex is needed.
				// Frame w/h are omitted — the app maps clicks off the fixed
				// 1280×800 device metrics, not per-frame dimensions.
				if raw, derr := base64.StdEncoding.DecodeString(msg.Params.Data); derr == nil {
					_ = client.WriteMessage(websocket.BinaryMessage, raw)
				}
				sendCDP("Page.screencastFrameAck", map[string]any{"sessionId": msg.Params.SessionID})
			}
		}
	}()

	// App → CDP: input + navigation.
	for {
		var in struct {
			Type       string  `json:"type"`
			URL        string  `json:"url"`
			X          float64 `json:"x"`
			Y          float64 `json:"y"`
			Button     string  `json:"button"`
			ClickCount int     `json:"clickCount"`
			DeltaX     float64 `json:"deltaX"`
			DeltaY     float64 `json:"deltaY"`
			CdpType    string  `json:"cdpType"`
			Text       string  `json:"text"`
			Key        string  `json:"key"`
			Code       string  `json:"code"`
			KeyCode    int     `json:"keyCode"`
		}
		if err := client.ReadJSON(&in); err != nil {
			return
		}
		switch in.Type {
		case "navigate":
			if u := strings.TrimSpace(in.URL); u != "" {
				sendCDP("Page.navigate", map[string]any{"url": u})
			}
		case "reload":
			sendCDP("Page.reload", nil)
		case "mouse":
			button := in.Button
			if button == "" {
				button = "none"
			}
			sendCDP("Input.dispatchMouseEvent", map[string]any{
				"type": in.CdpType, "x": in.X, "y": in.Y,
				"button": button, "clickCount": in.ClickCount,
			})
		case "wheel":
			sendCDP("Input.dispatchMouseEvent", map[string]any{
				"type": "mouseWheel", "x": in.X, "y": in.Y,
				"deltaX": in.DeltaX, "deltaY": in.DeltaY,
			})
		case "key":
			params := map[string]any{"type": in.CdpType}
			if in.Text != "" {
				params["text"] = in.Text
			}
			if in.Key != "" {
				params["key"] = in.Key
			}
			if in.Code != "" {
				params["code"] = in.Code
			}
			if in.KeyCode != 0 {
				params["windowsVirtualKeyCode"] = in.KeyCode
				params["nativeVirtualKeyCode"] = in.KeyCode
			}
			sendCDP("Input.dispatchKeyEvent", params)
		}
	}
}

func (bm *browserManager) shutdown() {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	for _, inst := range bm.instances {
		killProcessGroup(inst.cmd)
	}
}
