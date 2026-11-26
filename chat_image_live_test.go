package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
)

// Use a disposable receiver so a test never types into a person's agent session.
func TestLiveChatImageDelivery(t *testing.T) {
	if os.Getenv("HERDR_TEST_IMAGE") != "1" {
		t.Skip("set HERDR_TEST_IMAGE=1 to run the local attachment receiver")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	rw, err := dialLocal()
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(rw)
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	if _, err = client.HelloFramed(ctx); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "received.png")
	const receiver = `import json, pathlib, sys
for line in sys.stdin:
    if '请读取附图文件 ' in line:
        path, _ = json.JSONDecoder().raw_decode(line.split('请读取附图文件 ', 1)[1])
        pathlib.Path(sys.argv[1]).write_bytes(pathlib.Path(path).read_bytes())
        break`
	run, err := client.Run(ctx, RunParams{Argv: []string{python, "-u", "-c", receiver, output}, NoNotify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = client.Kill(ctx, run.Address); _ = client.Rm(ctx, run.Address) }()
	var payload bytes.Buffer
	if err = png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	var app *App
	h := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		a, root := testSeededApp(cx)
		app = a
		a.SetTranscriptBackend(&fakeTranscriptBackend{})
		// Keep the receiver independent of existing terminals and their session state.
		snaps := a.instances.Get()
		snaps[1].Terminals[0].Address = run.Address
		a.SeedSnapshots(snaps)
		return root
	}, nsgotest.Size(390, 844))
	h.Press("id:session-row-mac::" + run.Address)
	app.pool.mu.Lock()
	app.pool.workers["mac"] = &coreWorker{client: client, cancel: cancel}
	app.pool.mu.Unlock()
	h.PickImages = func(nsgotest.ImagePick) []core.PickedFile {
		return []core.PickedFile{{Name: "dogfood.png", MIME: "image/png", Data: payload.Bytes()}}
	}
	h.Press("id:session-upload-btn")
	h.Press("id:attachment-photo")
	h.Find("id:chat-image-attachment")
	h.Type("id:session-composer", "检查截图")
	h.Press("id:session-send-btn")
	deadline := time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		h.Advance(time.Millisecond * 20)
		data, readErr := os.ReadFile(output)
		if readErr == nil && bytes.Equal(data, payload.Bytes()) && len(h.Query("id:chat-image-attachment")) == 0 {
			return
		}
		time.Sleep(time.Millisecond * 30)
	}
	t.Fatalf("receiver did not confirm PNG and submitted draft, UI diagnostic=%q", app.toastMsg.Get())
}
