package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/updater"
)

// appBuildVersion is overridden at release build time via -ldflags "-X main.appBuildVersion=1.0.x".
var appBuildVersion = "1.0.0"

const (
	androidAppcastURL  = "https://github.com/FaintGhost/herdr-nsgo/releases/latest/download/appcast-android_arm64.xml"
	appUpdatePublicKey = "uVpT5oxc0ms2q3v2VWIl2l6GVKNcVyDGajrMsSVFn1U="
)

var reHTMLTag = regexp.MustCompile(`<[^>]+>`)

type sparkleRSS struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			Version     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version"`
			ShortVer    string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString"`
			Enclosure   struct {
				URL         string `xml:"url,attr"`
				Length      int64  `xml:"length,attr"`
				Version     string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version,attr"`
				ShortVer    string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString,attr"`
				SHA256      string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle sha256,attr"`
				EdSignature string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle edSignature,attr"`
			} `xml:"enclosure"`
		} `xml:"item"`
	} `xml:"channel"`
}

func parseAndroidAppcast(data []byte, currentVer string) (core.UpdateCheck, error) {
	var rss sparkleRSS
	if err := xml.Unmarshal(data, &rss); err != nil {
		return core.UpdateCheck{}, fmt.Errorf("解析更新清单失败: %w", err)
	}
	if len(rss.Channel.Items) == 0 {
		return core.UpdateCheck{}, fmt.Errorf("更新清单为空")
	}
	it := rss.Channel.Items[0]
	ver := nonEmpty(strings.TrimSpace(it.Enclosure.ShortVer), nonEmpty(strings.TrimSpace(it.Enclosure.Version), nonEmpty(strings.TrimSpace(it.ShortVer), strings.TrimSpace(it.Version))))
	if ver == "" || strings.TrimSpace(it.Enclosure.URL) == "" {
		return core.UpdateCheck{}, fmt.Errorf("更新清单缺少版本或下载地址")
	}
	notes := strings.TrimSpace(html.UnescapeString(reHTMLTag.ReplaceAllString(it.Description, "")))
	if r := []rune(notes); len(r) > 800 {
		notes = strings.TrimSpace(string(r[:800])) + "…"
	}
	return core.UpdateCheck{
		App:       "herdr-nsgo",
		Current:   currentVer,
		Version:   ver,
		URL:       nonEmpty(strings.TrimSpace(it.Link), "https://github.com/FaintGhost/herdr-nsgo/releases/latest"),
		Notes:     notes,
		Download:  strings.TrimSpace(it.Enclosure.URL),
		Signature: strings.TrimSpace(it.Enclosure.EdSignature),
		SHA256:    strings.TrimSpace(it.Enclosure.SHA256),
		Key:       appUpdatePublicKey,
		Size:      it.Enclosure.Length,
	}, nil
}

// compareSemVer compares dotted version strings like "1.0.4" and "1.0.0".
// Returns >0 if a > b, <0 if a < b, 0 if equal.
func compareSemVer(a, b string) int {
	pa := parseVerParts(a)
	pb := parseVerParts(b)
	n := max(len(pa), len(pb))
	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va != vb {
			if va > vb {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseVerParts(v string) []int {
	s := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.IndexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	if s == "" {
		return nil
	}
	raw := strings.Split(s, ".")
	out := make([]int, len(raw))
	for i, p := range raw {
		n, _ := strconv.Atoi(strings.TrimSpace(p))
		out[i] = n
	}
	return out
}

// CheckForAppUpdates checks for newer releases. On Android, it reads our signed GitHub Releases
// appcast directly and invokes core.InstallUpdateInBackground so third-party package installers
// (such as InstallerX Revived) are never misidentified as an app store.
func (a *App) CheckForAppUpdates(cx *core.Scope, background bool) {
	if cx == nil {
		return
	}
	if cx.Env().OS() != core.OSAndroid {
		if background {
			updater.CheckInBackground(cx)
		} else {
			updater.Check(cx)
		}
		return
	}
	if cx.UpdateActive() {
		if !background {
			a.notifyToast("正在下载或安装更新，请稍候…")
		}
		return
	}
	if !background {
		a.notifyToast("正在检查新版本…")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, androidAppcastURL, nil)
		if err != nil {
			if !background {
				a.notifyToast("检查更新失败：" + err.Error())
			}
			return
		}
		req.Header.Set("User-Agent", "herdr-nsgo/"+appBuildVersion)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if !background {
				a.notifyToast("检查更新失败，请检查网络连接")
			}
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if !background {
				a.notifyToast(fmt.Sprintf("检查更新失败（HTTP %d）", resp.StatusCode))
			}
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		if err != nil {
			if !background {
				a.notifyToast("读取更新信息失败")
			}
			return
		}
		check, err := parseAndroidAppcast(body, appBuildVersion)
		if err != nil {
			if !background {
				a.notifyToast(err.Error())
			}
			return
		}
		if compareSemVer(check.Version, appBuildVersion) <= 0 {
			if !background {
				a.notifyToast("当前已是最新版本 (v" + strings.TrimPrefix(appBuildVersion, "v") + ")")
			}
			return
		}

		a.post(func() {
			msg := fmt.Sprintf("检测到新版本 v%s（当前 v%s），将自动校验 Ed25519 签名并原地升级。",
				strings.TrimPrefix(check.Version, "v"),
				strings.TrimPrefix(appBuildVersion, "v"),
			)
			if check.Notes != "" {
				msg += "\n\n" + check.Notes
			}
			cx.ShowAlert(core.AlertConfig{
				Title:   "发现新版本",
				Message: msg,
				Actions: []core.AlertAction{
					{Label: "立即更新", Role: core.AlertDefault},
					{Label: "稍后", Role: core.AlertCancel},
				},
				Cancelable: true,
			}, func(idx int) {
				if idx != 0 {
					return
				}
				a.startAndroidUpdateInstall(cx, check)
			})
		})
	}()
}

func (a *App) startAndroidUpdateInstall(cx *core.Scope, check core.UpdateCheck) {
	if cx == nil {
		return
	}
	dir := cx.CacheDir()
	if dir == "" {
		dir = os.TempDir()
	}
	staging := filepath.Join(dir, "nsgo.updater")
	_ = os.RemoveAll(staging)

	a.notifyToast("正在下载 v" + strings.TrimPrefix(check.Version, "v") + "…")
	core.InstallUpdateInBackground(cx, check, staging, func(root *core.Scope, _ string, err error) {
		if err != nil {
			root.ShowAlert(core.AlertConfig{
				Title:   "自动更新失败",
				Message: "无法直接安装更新（" + err.Error() + "），是否打开 Release 页面手动下载？",
				Actions: []core.AlertAction{
					{Label: "打开发布页", Role: core.AlertDefault},
					{Label: "取消", Role: core.AlertCancel},
				},
				Cancelable: true,
			}, func(i int) {
				if i == 0 {
					root.OpenURL(check.URL, nil)
				}
			})
			return
		}
		a.notifyToast("已验证签名，正在拉起安装器…")
	})
}
