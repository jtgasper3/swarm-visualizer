package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// This file drives a headless Chrome for browser_test.go. It speaks just
// enough of the DevTools protocol to open a page, evaluate script in it and
// hear about anything that went wrong there, over the WebSocket library the
// server already depends on.

// findBrowser returns a Chrome or Chromium to run, or "" when there is none.
// CHROME_BIN names one outright.
func findBrowser() string {
	if bin := os.Getenv("CHROME_BIN"); bin != "" {
		return bin
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "microsoft-edge"} {
		if found, err := exec.LookPath(name); err == nil {
			return found
		}
	}
	for _, app := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	} {
		if _, err := os.Stat(app); err == nil {
			return app
		}
	}
	return ""
}

// page is one tab of a headless browser.
type page struct {
	conn    *websocket.Conn
	session string

	mu       sync.Mutex
	nextID   int
	pending  map[int]chan cdpMessage
	problems []string
}

type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

var devToolsAddress = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

// startBrowser launches a headless browser with one blank tab. The test is
// skipped with -short, and when no browser is installed, except under CI,
// where that would let the browser tests stop running without anyone noticing.
func startBrowser(t *testing.T) *page {
	t.Helper()
	if testing.Short() {
		t.Skip("needs a browser")
	}

	bin := findBrowser()
	if bin == "" {
		const missing = "no Chrome or Chromium found; install one or point CHROME_BIN at it"
		if os.Getenv("CI") != "" {
			t.Fatal(missing)
		}
		t.Skip(missing)
	}

	profile, err := os.MkdirTemp("", "swarm-visualizer-browser-")
	if err != nil {
		t.Fatalf("create browser profile: %v", err)
	}
	cmd := exec.Command(bin,
		"--headless=new",
		"--remote-debugging-port=0",
		"--user-data-dir="+profile,
		"--window-size=1600,1200",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		// The sandbox needs privileges a CI runner does not grant, and the
		// only page this browser opens is the test's own.
		"--no-sandbox",
		"about:blank",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("browser stderr: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(profile)
	})

	// The browser picks its own debugging port and announces it on stderr.
	address := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if match := devToolsAddress.FindStringSubmatch(scanner.Text()); match != nil {
				address <- match[1]
				break
			}
		}
		// Keep reading so the browser never blocks on a full pipe.
		for scanner.Scan() {
		}
	}()

	var url string
	select {
	case url = <-address:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s did not announce a DevTools address", bin)
	}

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("connect to the browser: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	p := &page{conn: conn, pending: map[int]chan cdpMessage{}}
	go p.read()

	var target struct {
		TargetID string `json:"targetId"`
	}
	p.call(t, "Target.createTarget", map[string]any{"url": "about:blank"}, &target)
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	p.call(t, "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached)
	p.session = attached.SessionID

	for _, domain := range []string{"Page", "Runtime", "Log"} {
		p.call(t, domain+".enable", nil, nil)
	}
	return p
}

// read delivers replies to the calls waiting for them and keeps a note of
// everything the page reports as having gone wrong.
func (p *page) read() {
	for {
		var message cdpMessage
		if err := p.conn.ReadJSON(&message); err != nil {
			return
		}

		if message.ID != 0 {
			p.mu.Lock()
			reply := p.pending[message.ID]
			delete(p.pending, message.ID)
			p.mu.Unlock()
			if reply != nil {
				reply <- message
			}
			continue
		}

		if problem := problemIn(message); problem != "" {
			p.mu.Lock()
			p.problems = append(p.problems, problem)
			p.mu.Unlock()
		}
	}
}

// problemIn describes the fault an event reports: an uncaught exception, an
// error written to the console, or one the browser logged itself, such as a
// file that failed to load or a request the content security policy refused.
func problemIn(event cdpMessage) string {
	switch event.Method {
	case "Runtime.exceptionThrown":
		var params struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		_ = json.Unmarshal(event.Params, &params)
		return "uncaught: " + params.ExceptionDetails.Text + " " + params.ExceptionDetails.Exception.Description

	case "Runtime.consoleAPICalled":
		var params struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		_ = json.Unmarshal(event.Params, &params)
		if params.Type != "error" && params.Type != "warning" && params.Type != "assert" {
			return ""
		}
		parts := []string{"console " + params.Type + ":"}
		for _, arg := range params.Args {
			if arg.Description != "" {
				parts = append(parts, arg.Description)
			} else {
				parts = append(parts, fmt.Sprint(arg.Value))
			}
		}
		return strings.Join(parts, " ")

	case "Log.entryAdded":
		var params struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		_ = json.Unmarshal(event.Params, &params)
		if params.Entry.Level != "error" && params.Entry.Level != "warning" {
			return ""
		}
		return "browser " + params.Entry.Level + ": " + params.Entry.Text + " " + params.Entry.URL
	}
	return ""
}

// call sends one command and decodes its result into out, if given. A command
// that fails ends the test.
func (p *page) call(t *testing.T, method string, params, out any) {
	t.Helper()
	if err := p.send(method, params, out); err != nil {
		t.Fatal(err)
	}
}

// send is call for when a refusal is an answer and not a failure.
func (p *page) send(method string, params, out any) error {
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	reply := make(chan cdpMessage, 1)
	p.pending[id] = reply
	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	if p.session != "" {
		request["sessionId"] = p.session
	}
	err := p.conn.WriteJSON(request)
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}

	select {
	case message := <-reply:
		if message.Error != nil {
			return fmt.Errorf("%s: %s", method, message.Error.Message)
		}
		if out != nil {
			if err := json.Unmarshal(message.Result, out); err != nil {
				return fmt.Errorf("%s: decode result: %w", method, err)
			}
		}
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("%s: no reply from the browser", method)
	}
}

// open loads url in the tab.
func (p *page) open(t *testing.T, url string) {
	t.Helper()
	p.call(t, "Page.navigate", map[string]any{"url": url}, nil)
}

// beforeLoad has script run in every page this tab opens from now on, ahead of
// the page's own scripts: a way to arrange what the page finds when it starts.
func (p *page) beforeLoad(t *testing.T, script string) {
	t.Helper()
	p.call(t, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": script}, nil)
}

// eval runs a JavaScript expression in the page and decodes what it evaluates
// to into out, if given. A promise is waited for. An expression that throws,
// or that the browser refuses to run, ends the test.
func (p *page) eval(t *testing.T, expression string, out any) {
	t.Helper()
	if err := p.evaluate(expression, out); err != nil {
		t.Fatal(err)
	}
}

func (p *page) evaluate(expression string, out any) error {
	var result struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := p.send("Runtime.evaluate", map[string]any{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": true,
	}, &result)
	if err != nil {
		return fmt.Errorf("evaluating %s: %w", expression, err)
	}
	if result.ExceptionDetails != nil {
		return fmt.Errorf("evaluating %s: %s %s", expression, result.ExceptionDetails.Text, result.ExceptionDetails.Exception.Description)
	}
	if out != nil && len(result.Result.Value) > 0 {
		if err := json.Unmarshal(result.Result.Value, out); err != nil {
			return fmt.Errorf("evaluating %s: decode %s: %w", expression, result.Result.Value, err)
		}
	}
	return nil
}

// waitFor polls until a JavaScript condition holds. A poll that cannot be
// evaluated counts as the condition not holding yet: the browser refuses a
// script that arrives while the page is on its way somewhere else, as it is
// between being sent to sign in and coming back.
func (p *page) waitFor(t *testing.T, condition string) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		var met bool
		err := p.evaluate("Boolean("+condition+")", &met)
		if err == nil && met {
			return
		}
		if time.Now().After(deadline) {
			var body string
			_ = p.evaluate("document.body ? document.body.innerText.slice(0, 2000) : ''", &body)
			t.Fatalf("timed out waiting for %s\nlast poll failed with: %v\nthe page reads:\n%s\nproblems: %q", condition, err, body, p.reported())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// text returns what the first element matching selector reads as, with runs
// of whitespace collapsed, or "" when nothing matches.
func (p *page) text(t *testing.T, selector string) string {
	t.Helper()
	var text string
	p.eval(t, fmt.Sprintf(`(document.querySelector(%q)?.innerText ?? '').replace(/\s+/g, ' ').trim()`, selector), &text)
	return text
}

// reported returns the problems the page has reported so far.
func (p *page) reported() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.problems...)
}
