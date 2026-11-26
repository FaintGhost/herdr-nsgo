package main

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

type localProjectBackend struct{ fakeTranscriptBackend }

func (b *localProjectBackend) MachineExec(ctx context.Context, _ string, p ExecParams) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, p.Argv[0], p.Argv[1:]...)
	cmd.Dir = p.Cwd
	out, err := cmd.Output()
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{Code: new(int32(0)), Stdout: base64.StdEncoding.EncodeToString(out)}, nil
}
func TestProjectBrowseAndDiff(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "odd $() 名.txt")
	content := strings.Repeat("甲", 6000)
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	git("init", "-q")
	git("add", ".")
	if err := os.WriteFile(file, []byte(content+"\nworking\n"), 0600); err != nil {
		t.Fatal(err)
	}
	term := AggregatedTerminal{Terminal: Terminal{Cwd: dir}}
	load := func(where projectLocation) projectPage {
		t.Helper()
		p, err := loadProjectPage(t.Context(), &localProjectBackend{}, term, where)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := load(projectLocation{view: "files", relative: "."})
	if len(p.Entries) != 1 || p.Entries[0].Name != filepath.Base(file) {
		t.Fatal(p)
	}
	loc := projectLocation{view: "files", relative: p.Entries[0].Name}
	p = load(loc)
	if p.Next == 0 {
		t.Fatal("expected second page")
	}
	loc.offset = p.Next
	if p.Text+load(loc).Text != content+"\nworking\n" {
		t.Fatal("UTF-8 boundary lost bytes")
	}
	if !strings.Contains(load(projectLocation{view: "changes"}).Text, "+working") || strings.Contains(load(projectLocation{view: "staged"}).Text, "+working") {
		t.Fatal("wrong diff")
	}
}
func TestBrowserPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, r.URL.RequestURI()) }))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	local, err := openPreview(ctx, port, (&net.Dialer{}).DialContext)
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(local + "/test?ok=1")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(body) != "/test?ok=1" {
		t.Fatal(string(body), err)
	}
	cancel()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		s, err := net.DialTimeout("tcp", strings.TrimPrefix(local, "http://"), 50*time.Millisecond)
		if err != nil {
			return
		}
		s.Close()
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("preview still listening after close")
}

func TestTerminalBinding(t *testing.T) {
	for src, want := range map[string]string{"C-b, S-t": "\x02T", "F12,h": "\x1b[24~h", "S-Tab": "\x1b[Z", "text:/clear": "/clear", "M-x": "\x1bx"} {
		got, err := terminalBinding(src)
		if err != nil || string(got) != want {
			t.Fatalf("%q: %q %v", src, got, err)
		}
	}
	for _, s := range []string{"Contrl-b", "F13", "C-"} {
		if _, err := terminalBinding(s); err == nil {
			t.Fatal(s)
		}
	}
}

func TestOSC52ClipboardFrames(t *testing.T) {
	s := NewVTScreen(40, 8)
	raw := "\x1b]52;c;5Lit5paH"
	if s.writeFrame([]byte(raw)) != nil {
		t.Fatal("partial frame copied")
	}
	if text := s.writeFrame([]byte("\x1b\\")); text == nil || *text != "中文" {
		t.Fatal(text)
	}
	for _, v := range []string{"?", "!invalid", strings.Repeat("A", 1<<18)} {
		if s.writeFrame([]byte("\x1b]52;c;"+v+"\a")) != nil {
			t.Fatal("invalid clipboard write")
		}
	}
	s.Write([]byte(raw + "\a"))
	if s.writeFrame([]byte("normal")) != nil {
		t.Fatal("replayed cached clipboard")
	}
}

func TestMobileDraftInputs(t *testing.T) {
	var app *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		app = a
		return root
	}, nsgotest.Size(390, 844), nsgotest.Environment(core.Environment{OS: core.OSAndroid, Idiom: core.IdiomPhone, Speech: true}))
	h.Press("id:session-row-mac::t1")
	h.Type("id:session-composer", "原文")
	h.Press("id:session-upload-btn")
	h.Find("id:attachment-document")
	h.Screenshot("chat-input-tools")
	h.Press("id:session-snippets-btn")
	h.Press("id:start-dictation")
	h.HearSpeech("语音文本", false)
	h.Press("id:dictation-stop")
	h.Press("id:dictation-insert")
	if got := app.Compose("mac::t1"); got != "原文\n语音文本" {
		t.Fatal(got)
	}
	f := filepath.Join(t.TempDir(), "document.txt")
	if err := os.WriteFile(f, []byte("内容"), 0600); err != nil {
		t.Fatal(err)
	}
	h.PickFiles = func(nsgotest.FilePick) []core.File {
		return []core.File{{Name: "document.txt", MIME: "text/plain", Path: f, Temporary: true}}
	}
	h.Press("id:session-upload-btn")
	h.Press("id:attachment-document")
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		h.Advance(time.Millisecond * 10)
		if len(h.Query("id:chat-image-attachment")) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	h.Find("id:chat-image-attachment")
	d := app.chatImages.Get()[TermKey{InstanceID: "mac", Addr: "t1"}]
	if !d.document || string(d.data) != "内容" {
		t.Fatal(d)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("temporary picker copy retained")
	}
}
