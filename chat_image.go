package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/nsgo-dev/nsgo/core"
	"io"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The file stays with this terminal's draft until both upload and submission succeed.
type chatImageDraft struct {
	name       string
	data       []byte
	document   bool
	remotePath string
	sending    bool
}

func (a *App) setChatImage(key TermKey, image chatImageDraft) {
	next := maps.Clone(a.chatImages.Get())
	if image.name == "" {
		delete(next, key)
	} else {
		next[key] = image
	}
	a.chatImages.Set(next)
}

const maxAttachmentBytes = 8 << 20

func readPickedAttachment(f core.File) ([]byte, error) {
	if f.Temporary && f.Path != "" {
		defer os.Remove(f.Path)
	}
	if f.Size > maxAttachmentBytes {
		return nil, errors.New("文件超过 8 MiB 上传上限")
	}
	reader, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAttachmentBytes {
		return nil, errors.New("文件超过 8 MiB 上传上限")
	}
	return data, nil
}

func (a *App) attachChatImage(key TermKey, name, mediaType string, data []byte) {
	if a.chatImages.Get()[key].name != "" {
		a.notifyToast("请先发送或移除当前附件")
		return
	}
	if len(data) == 0 || len(data) > maxAttachmentBytes {
		a.notifyToast("附件为空或超过 hc 的 8 MiB 上传上限")
		return
	}
	if name == "" {
		name = "attachment"
		if extensions, _ := mime.ExtensionsByType(mediaType); len(extensions) > 0 {
			name += extensions[0]
		}
	}
	a.setChatImage(key, chatImageDraft{name: filepath.Base(name), data: data, document: !strings.HasPrefix(mediaType, "image/")})
}

func chatSendError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "发送回执超时，请先查看远端会话，确认这条消息是否已到达。"
	}
	return "发送消息：" + explain(err)
}

func (a *App) sendChatImage(key TermKey, prompt, kind string, mode chatSubmitMode, onSent func(string)) {
	image := a.chatImages.Get()[key]
	if image.name == "" || image.sending {
		return
	}
	client := a.pool.ClientFor(key.InstanceID)
	if client == nil {
		a.notifyToast("实例离线，附件和文字已保留在草稿中。")
		return
	}
	image.sending = true
	a.setChatImage(key, image)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		var err error
		if image.remotePath == "" {
			var result PutResult
			result, err = client.PutFile(ctx, key.Addr, image.name, image.data)
			if err == nil && (result.Path == "" || result.Bytes != len(image.data)) {
				err = errors.New("上传回执缺少路径或文件大小不符")
			}
			if err == nil {
				image.remotePath = result.Path
				image.data = nil
			}
		}
		cancel()
		var message, problem string
		if err != nil {
			problem = "上传附件失败：" + explain(err)
		} else {
			label := "附图文件"
			if image.document {
				label = "附件文件"
			}
			message = strings.TrimSpace(prompt + "\n\n" + fmt.Sprintf("请读取%s %q。", label, image.remotePath))
			sendCtx, done := context.WithTimeout(context.Background(), 90*time.Second)
			err = client.submitChat(sendCtx, key.Addr, message, kind, mode)
			done()
			if err != nil {
				problem = chatSendError(err)
			}
		}
		a.post(func() {
			if problem != "" {
				image.sending = false
				a.setChatImage(key, image)
				a.notifyToast(problem)
				return
			}
			a.setChatImage(key, chatImageDraft{})
			onSent(message)
		})
	}()
}
