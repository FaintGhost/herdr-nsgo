package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Read-only host inspection script: reads local agent model/effort metadata files without touching the TUI.
const readAgentCatalogScript = `python3 - <<'PY'
import json, os, re
from pathlib import Path

home = Path.home()
kind = os.environ.get('HC_AGENT_KIND', '')
out = {'models': [], 'efforts': [], 'default_model': '', 'default_effort': ''}

if kind == 'claude':
    cfg = Path(os.getenv('CLAUDE_CONFIG_DIR', home / '.claude')) / 'settings.json'
    try:
        d = json.loads(cfg.read_text(encoding='utf-8', errors='replace'))
        if isinstance(d.get('model'), str):
            out['default_model'] = d['model'].strip()
        if isinstance(d.get('effortLevel'), str) and d['effortLevel'].strip():
            out['default_effort'] = d['effortLevel'].strip().lower()
        if not out['default_effort'] and isinstance(d.get('env'), dict):
            ev = d['env'].get('CLAUDE_CODE_EFFORT_LEVEL')
            if isinstance(ev, str) and ev.strip():
                out['default_effort'] = ev.strip().lower()
        if isinstance(d.get('modelSettings'), dict):
            ms = d['modelSettings']
            dm = out['default_model'].lower()
            fallback_eff = ''
            resolved_model = ''
            for mk, mv in ms.items():
                if dm and (mk.lower() == dm or dm in mk.lower()) and len(mk) > len(resolved_model):
                    resolved_model = mk.strip()
                if not isinstance(mv, dict):
                    continue
                el = mv.get('effortLevel')
                if not isinstance(el, str) or not el.strip():
                    continue
                el = el.strip().lower()
                if not fallback_eff:
                    fallback_eff = el
                if dm and (mk.lower() == dm or dm in mk.lower()):
                    out['default_effort'] = el
            if resolved_model and dm in ('opus', 'sonnet', 'haiku', 'default', ''):
                out['default_model'] = resolved_model
            if not out['default_effort'] and fallback_eff:
                out['default_effort'] = fallback_eff
    except OSError:
        pass
    except ValueError:
        pass

elif kind == 'codex':
    codex_dir = Path(os.getenv('CODEX_HOME', home / '.codex'))
    cfg = codex_dir / 'config.toml'
    try:
        txt = cfg.read_text(encoding='utf-8', errors='replace')
        m = re.search(r'(?m)^\s*model\s*=\s*"([^"]+)"', txt)
        if m:
            out['default_model'] = m.group(1).strip()
        e = re.search(r'(?m)^\s*model_reasoning_effort\s*=\s*"([^"]+)"', txt)
        if e:
            out['default_effort'] = e.group(1).strip()
    except OSError:
        pass
    cache = codex_dir / 'models_cache.json'
    try:
        d = json.loads(cache.read_text(encoding='utf-8', errors='replace'))
        models = d.get('models', [])
        seen = set()
        for item in models:
            if not isinstance(item, dict):
                continue
            vis = item.get('visibility', '')
            if vis and vis != 'list':
                continue
            if item.get('hidden') is True:
                continue
            slug = str(item.get('slug') or '').strip()
            if not slug or slug in seen:
                continue
            seen.add(slug)
            disp = str(item.get('display_name') or slug).strip()
            effs = []
            for lv in item.get('supported_reasoning_levels') or []:
                if isinstance(lv, dict) and lv.get('effort'):
                    effs.append(str(lv['effort']).strip())
            out['models'].append({
                'id': slug,
                'label': disp,
                'detail': slug if disp != slug else '',
                'efforts': effs,
                'direct_value': slug,
            })
            if len(out['models']) >= 24:
                break
        if out['models'] and not out['efforts']:
            for m in out['models']:
                if m['id'] == out['default_model'] and m['efforts']:
                    out['efforts'] = m['efforts']
                    break
            if not out['efforts'] and out['models'][0]['efforts']:
                out['efforts'] = out['models'][0]['efforts']
    except (OSError, ValueError):
        pass

elif kind in ('pi', 'omp'):
    pi_dir = Path(os.getenv('PI_CODING_AGENT_DIR', home / ('.omp' if kind == 'omp' else '.pi') / 'agent'))
    try:
        s = json.loads((pi_dir / 'settings.json').read_text(encoding='utf-8', errors='replace'))
        if isinstance(s.get('defaultModel'), str):
            out['default_model'] = s['defaultModel'].strip()
        if isinstance(s.get('defaultThinkingLevel'), str):
            out['default_effort'] = s['defaultThinkingLevel'].strip()
    except (OSError, ValueError):
        pass
    try:
        m = json.loads((pi_dir / 'models.json').read_text(encoding='utf-8', errors='replace'))
        providers = m.get('providers') or {}
        seen = set()
        for prov, pv in providers.items():
            if not isinstance(pv, dict):
                continue
            for item in pv.get('models') or []:
                if not isinstance(item, dict):
                    continue
                mid = str(item.get('id') or '').strip()
                if not mid:
                    continue
                full = f'{prov}/{mid}' if prov and '/' not in mid else mid
                if full in seen:
                    continue
                seen.add(full)
                name = str(item.get('name') or mid).strip()
                out['models'].append({
                    'id': full,
                    'label': name,
                    'detail': prov,
                    'direct_value': full,
                })
                if len(out['models']) >= 32:
                    break
            if len(out['models']) >= 32:
                break
    except (OSError, ValueError):
        pass

elif kind == 'antigravity':
    for cfg in (home / '.gemini' / 'antigravity-cli' / 'settings.json', home / '.gemini' / 'settings.json'):
        try:
            d = json.loads(cfg.read_text(encoding='utf-8', errors='replace'))
        except (OSError, ValueError):
            continue
        raw_model = ''
        if isinstance(d.get('model'), str):
            raw_model = d['model'].strip()
        elif isinstance(d.get('model'), dict) and isinstance(d['model'].get('name'), str):
            raw_model = d['model']['name'].strip()
        if raw_model and not out['default_model']:
            m = re.match(r'^(.*?)\s*\((off|minimal|low|medium|high|xhigh|max|ultra|auto)\)\s*$', raw_model, re.I)
            if m:
                out['default_model'] = m.group(1).strip()
                if not out['default_effort']:
                    out['default_effort'] = m.group(2).strip().lower()
            else:
                hm = re.match(r'^(.*?)-(off|minimal|low|medium|high|xhigh|max|ultra)$', raw_model, re.I)
                if hm:
                    out['default_model'] = hm.group(1).strip()
                    if not out['default_effort']:
                        out['default_effort'] = hm.group(2).strip().lower()
                else:
                    out['default_model'] = raw_model
        for ek in ('effort', 'effortLevel', 'thinkingLevel', 'reasoningEffort', 'thinking_level', 'effort_level'):
            if isinstance(d.get(ek), str) and d[ek].strip() and not out['default_effort']:
                out['default_effort'] = d[ek].strip().lower()
        if out['default_model']:
            break

print(json.dumps(out, ensure_ascii=False))
PY`

// builtinAgentCatalog returns instant 0ms presets for each supported agent harness before remote cache loads.
func builtinAgentCatalog(kind string) AgentCatalog {
	switch kind {
	case "claude":
		return AgentCatalog{
			Models: []AgentModelOption{
				{ID: "default", Label: "Default", Detail: "跟随账号默认配置", DirectValue: "default"},
				{ID: "opus", Label: "Claude Opus", Detail: "最强推理 · 复杂架构与重构", DirectValue: "opus"},
				{ID: "sonnet", Label: "Claude Sonnet", Detail: "均衡主力 · 日常编码推荐", DirectValue: "sonnet"},
				{ID: "haiku", Label: "Claude Haiku", Detail: "极速轻量 · 简单修改与问答", DirectValue: "haiku"},
			},
			Efforts: []string{"low", "medium", "high", "max", "auto"},
		}
	case "codex":
		return AgentCatalog{
			Models: []AgentModelOption{
				{ID: "gpt-5.4", Label: "GPT-5.4", Detail: "通用编码模型", DirectValue: "gpt-5.4"},
				{ID: "gpt-5.3-codex", Label: "GPT-5.3 Codex", Detail: "代码专用模型", DirectValue: "gpt-5.3-codex"},
			},
			Efforts: []string{"low", "medium", "high", "xhigh"},
		}
	case "pi", "omp":
		return AgentCatalog{
			Efforts: []string{"off", "minimal", "low", "medium", "high", "xhigh"},
		}
	case "antigravity":
		return AgentCatalog{
			Models: []AgentModelOption{
				{ID: "gemini-3.1-pro", Label: "Gemini 3.1 Pro", Detail: "旗舰推理 · 架构与复杂代码", DirectValue: "gemini-3.1-pro"},
				{ID: "gemini-3-pro", Label: "Gemini 3 Pro", Detail: "均衡主力 · 日常工程任务", DirectValue: "gemini-3-pro"},
				{ID: "gemini-3-flash", Label: "Gemini 3 Flash", Detail: "高速响应 · 快速迭代修改", DirectValue: "gemini-3-flash"},
				{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Detail: "稳定经典 · 长上下文分析", DirectValue: "gemini-2.5-pro"},
				{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Detail: "轻量极速 · 简单问答", DirectValue: "gemini-2.5-flash"},
			},
			Efforts: []string{"low", "medium", "high"},
		}
	default:
		return AgentCatalog{}
	}
}

// mergeAgentCatalog combines remote-discovered models/efforts with built-in instant presets.
func mergeAgentCatalog(kind string, remote AgentCatalog) AgentCatalog {
	base := builtinAgentCatalog(kind)
	out := AgentCatalog{
		DefaultModel:  remote.DefaultModel,
		DefaultEffort: remote.DefaultEffort,
		UpdatedMS:     time.Now().UnixMilli(),
	}
	if kind == "claude" {
		// Claude always supports the built-in aliases plus any configured custom model ID.
		seen := make(map[string]bool)
		if remote.DefaultModel != "" {
			low := strings.ToLower(remote.DefaultModel)
			known := low == "default" || low == "opus" || low == "sonnet" || low == "haiku"
			if !known {
				out.Models = append(out.Models, AgentModelOption{
					ID:          remote.DefaultModel,
					Label:       ShortModelLabel(remote.DefaultModel),
					Detail:      remote.DefaultModel,
					DirectValue: remote.DefaultModel,
				})
				seen[low] = true
			}
		}
		for _, m := range base.Models {
			if !seen[strings.ToLower(m.ID)] {
				out.Models = append(out.Models, m)
				seen[strings.ToLower(m.ID)] = true
			}
		}
		for _, m := range remote.Models {
			if !seen[strings.ToLower(m.ID)] {
				out.Models = append(out.Models, m)
				seen[strings.ToLower(m.ID)] = true
			}
		}
		out.Efforts = base.Efforts
		return out
	}

	if len(remote.Models) > 0 {
		out.Models = remote.Models
	} else {
		out.Models = base.Models
	}
	if len(remote.Efforts) > 0 {
		out.Efforts = remote.Efforts
	} else {
		out.Efforts = base.Efforts
	}
	return out
}

// DirectApplyCommand constructs the instant 1-RPC slash command for switching model or effort without opening a TUI menu.
func DirectApplyCommand(kind string, effort bool, value string) (string, bool) {
	v := strings.TrimSpace(value)
	if v == "" || strings.ContainsAny(v, "\r\n\x1b\x00") {
		return "", false
	}
	switch kind {
	case "claude":
		if effort {
			return "/effort " + v, true
		}
		return "/model " + v, true
	case "codex":
		if effort {
			return "/model --reasoning=" + v, true
		}
		return "/model " + v, true
	case "pi", "omp":
		if effort {
			return "/thinking " + v, true
		}
		return "/model " + v, true
	case "antigravity":
		if effort {
			return "/effort " + v, true
		}
		return "/model " + v, true
	default:
		return "", false
	}
}

func isKnownEffortToken(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "auto":
		return true
	}
	return false
}

// splitModelAndEffort splits an optional trailing effort tag like "Gemini 4 Neon (High)"
// or "gemini-4-neon-high" into ("Gemini 4 Neon", "high") / ("gemini-4-neon", "high").
func splitModelAndEffort(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	if strings.HasSuffix(s, ")") {
		if lp := strings.LastIndex(s, "("); lp > 0 {
			inner := strings.ToLower(strings.TrimSpace(s[lp+1 : len(s)-1]))
			if isKnownEffortToken(inner) {
				return strings.TrimSpace(s[:lp]), inner
			}
		}
	}
	if dash := strings.LastIndex(s, "-"); dash > 0 && dash+1 < len(s) {
		suffix := strings.ToLower(strings.TrimSpace(s[dash+1:]))
		switch suffix {
		case "off", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
			return strings.TrimSpace(s[:dash]), suffix
		}
	}
	return s, ""
}

// formatEffortChipLabel formats a thinking/effort level cleanly for the composer chip.
func formatEffortChipLabel(level string) string {
	s := strings.ToLower(strings.TrimSpace(level))
	switch s {
	case "":
		return ""
	case "off":
		return "Off"
	case "minimal":
		return "Min"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh":
		return "XHigh"
	case "max":
		return "Max"
	case "ultra":
		return "Ultra"
	case "auto":
		return "Auto"
	default:
		return strings.TrimSpace(level)
	}
}

// defaultEffortForKind returns the sensible harness default thinking level when neither the
// transcript nor host config has reported an explicit effort level yet.
func defaultEffortForKind(kind string) string {
	switch kind {
	case "claude":
		return "auto"
	case "codex", "pi", "omp", "antigravity":
		return "medium"
	default:
		return ""
	}
}

// EffortDisplay returns a concise display label and human description for a thinking/effort level.
func EffortDisplay(level string) (label, detail string) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off":
		return "off", "关闭深度思考"
	case "minimal":
		return "minimal", "极简思考 · 最快响应"
	case "low":
		return "low", "低 · 快速省流"
	case "medium":
		return "medium", "中 · 日常均衡"
	case "high":
		return "high", "高 · 深度推理"
	case "xhigh":
		return "xhigh", "超高 · 复杂攻坚"
	case "max":
		return "max", "极限 · 最大思考预算"
	case "ultra":
		return "ultra", "极致 · 最高档推理"
	case "auto":
		return "auto", "自动 · 按任务动态调节"
	default:
		return level, ""
	}
}

func isShortDigits(s string) bool {
	if len(s) == 0 || len(s) > 2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ShortModelLabel compresses a full model identifier into a compact composer chip label.
func ShortModelLabel(raw string) string {
	s, _ := splitModelAndEffort(raw)
	if s == "" {
		return ""
	}
	if idx := strings.LastIndex(s, "/"); idx >= 0 && idx+1 < len(s) {
		s = s[idx+1:]
	}
	// Strip optional context-window suffix such as "[1m]" or "[200k]" before parsing version digits.
	if b := strings.Index(s, "["); b > 0 {
		s = strings.TrimSpace(s[:b])
	}
	low := strings.ToLower(s)
	switch low {
	case "opus":
		return "Opus"
	case "sonnet":
		return "Sonnet"
	case "haiku":
		return "Haiku"
	case "default":
		return "Default"
	}
	if strings.HasPrefix(low, "claude-") {
		rest := s[len("claude-"):]
		parts := strings.Split(rest, "-")
		// Format 1: claude-opus-5-5 / claude-sonnet-4-6-20260101
		if len(parts) >= 3 && isShortDigits(parts[1]) && isShortDigits(parts[2]) {
			return strings.ToUpper(parts[0][:1]) + parts[0][1:] + " " + parts[1] + "." + parts[2]
		}
		// Format 2: claude-opus-5 / claude-sonnet-4
		if len(parts) >= 2 && isShortDigits(parts[1]) {
			return strings.ToUpper(parts[0][:1]) + parts[0][1:] + " " + parts[1]
		}
		// Format 3: claude-3-7-sonnet
		if len(parts) >= 3 && isShortDigits(parts[0]) && isShortDigits(parts[1]) && parts[2] != "" {
			return strings.ToUpper(parts[2][:1]) + parts[2][1:] + " " + parts[0] + "." + parts[1]
		}
		if len(parts) >= 1 && parts[0] != "" {
			return strings.ToUpper(parts[0][:1]) + parts[0][1:]
		}
	}
	if strings.HasPrefix(low, "gemini-") {
		rest := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(s[len("gemini-"):], "-tiered"), "-preview"), "-exp")
		parts := strings.Split(rest, "-")
		if len(parts) >= 2 && parts[1] != "" {
			tier := strings.ToUpper(parts[1][:1]) + parts[1][1:]
			return "Gemini " + parts[0] + " " + tier
		}
	}
	if len(s) > 16 {
		return s[:15] + "…"
	}
	return s
}

// InstantCatalogFor returns a 0ms ready-to-render AgentCatalog from cache + built-in presets + live transcript model.
func (a *App) InstantCatalogFor(instanceID, kind, liveModel string) AgentCatalog {
	var cat AgentCatalog
	if a.snapshots != nil {
		if cached, ok := a.snapshots.LoadCatalog(instanceID, kind); ok {
			cat = cached
		}
	}
	base := builtinAgentCatalog(kind)
	if len(cat.Models) == 0 {
		cat.Models = base.Models
	}
	if len(cat.Efforts) == 0 {
		cat.Efforts = base.Efforts
	}
	if live := strings.TrimSpace(liveModel); live != "" {
		cat.DefaultModel = live
		found := false
		for _, m := range cat.Models {
			if strings.EqualFold(m.ID, live) || strings.EqualFold(m.DirectValue, live) ||
				(kind == "claude" && strings.Contains(strings.ToLower(live), strings.ToLower(m.ID))) {
				found = true
				break
			}
		}
		if !found {
			cat.Models = append([]AgentModelOption{{
				ID:          live,
				Label:       ShortModelLabel(live),
				Detail:      live,
				DirectValue: live,
			}}, cat.Models...)
		}
	}
	return cat
}

// PrefetchAgentCatalog silently fetches the host's model & effort metadata via fast read-only MachineExec
// (never touching the interactive TUI) and stores it in SnapshotStore for instant 0ms opens.
func (a *App) PrefetchAgentCatalog(key TermKey, t Terminal, onUpdate func(AgentCatalog)) {
	kind := DetectHarnessKind(t)
	if !supportsModelEffortPicker(kind) {
		return
	}
	backend := a.TranscriptBackend()
	if backend == nil {
		return
	}
	cacheKey := key.InstanceID + "::" + kind
	a.mu.Lock()
	if a.catalogFetchInFly[cacheKey] || (onUpdate == nil && time.Since(a.catalogFetchedAt[cacheKey]) < 3*time.Minute) {
		a.mu.Unlock()
		return
	}
	a.catalogFetchInFly[cacheKey] = true
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.catalogFetchInFly[cacheKey] = false
			a.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		on, _ := SplitMachineAddr(key.Addr)
		res, err := backend.MachineExec(ctx, key.InstanceID, ExecParams{
			On:      on,
			Cwd:     t.CurrentDir(),
			Line:    "HC_AGENT_KIND=" + kind + " " + readAgentCatalogScript,
			Timeout: 4,
			Env:     map[string]string{"HC_AGENT_KIND": kind},
		})
		if err != nil || res.Truncated || (res.Code != nil && *res.Code != 0) {
			return
		}
		var remote AgentCatalog
		if json.Unmarshal([]byte(res.StdoutText()), &remote) != nil {
			return
		}
		merged := mergeAgentCatalog(kind, remote)
		a.mu.Lock()
		a.catalogFetchedAt[cacheKey] = time.Now()
		a.mu.Unlock()
		if a.snapshots != nil {
			a.snapshots.SaveCatalog(key.InstanceID, kind, merged)
		}
		a.post(func() {
			if a.catalogRev != nil {
				a.catalogRev.Set(a.catalogRev.GetUntracked() + 1)
			}
			if onUpdate != nil {
				onUpdate(merged)
			}
		})
	}()
}

// SessionModel returns the optimistically selected model for key, if any.
func (a *App) SessionModel(key TermKey) string {
	return a.sessionModels.Get()[key]
}

// SetSessionModel records an optimistic model selection for key so composer chips update immediately.
func (a *App) SetSessionModel(key TermKey, model string) {
	a.post(func() {
		cur := a.sessionModels.Get()
		next := make(map[TermKey]string, len(cur)+1)
		for k, v := range cur {
			next[k] = v
		}
		next[key] = model
		a.sessionModels.Set(next)
	})
}

// SessionEffort returns the optimistically selected effort/thinking level for key, if any.
func (a *App) SessionEffort(key TermKey) string {
	return a.sessionEfforts.Get()[key]
}

// SetSessionEffort records an optimistic effort selection for key so composer chips update immediately.
func (a *App) SetSessionEffort(key TermKey, effort string) {
	a.post(func() {
		cur := a.sessionEfforts.Get()
		next := make(map[TermKey]string, len(cur)+1)
		for k, v := range cur {
			next[k] = v
		}
		next[key] = effort
		a.sessionEfforts.Set(next)
	})
}
