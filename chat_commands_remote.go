package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Run on the terminal's machine. Only inspect skill front matter and filenames;
// extensions, skills and project scripts are never invoked during completion.
const readAgentCommands = `python3 - <<'PY'
import json, os, pathlib
from pathlib import Path
home = Path.home()
cwd = Path.cwd()
kind = os.environ['HC_AGENT_KIND']
entries = {}
def files(root, pattern):
    try: return sorted(root.glob(pattern))
    except OSError: return []
def add(name, summary=''):
    if name and len(name)<=160 and not any(c.isspace() or ord(c)<32 for c in name):
        entries[name]={'name':name,'description':summary[:180]}
def skills(root, prefix):
    for file in files(root, '*/SKILL.md'):
        try:
            with file.open(encoding='utf-8', errors='replace') as f: lines=f.read(8192).splitlines()
        except OSError: continue
        name, summary, hidden=file.parent.name, '', False
        if lines and lines[0].strip()=='---':
            for line in lines[1:]:
                if line.strip()=='---': break
                key, _, value=line.partition(':'); value=value.strip().strip('"\'')
                if key=='name' and value and kind!='claude': name=value
                if key=='description' and value not in ('>', '|'): summary=value
                if key=='user-invocable' and value.lower()=='false': hidden=True
        if not hidden: add(prefix+name,summary)
def prompts(root,prefix=''):
    for f in files(root,'*.md'): add(prefix+f.stem)
if kind=='claude':
    for base in (Path(os.getenv('CLAUDE_CONFIG_DIR', home/'.claude')), cwd/'.claude'):
        skills(base/'skills',''); prompts(base/'commands')
elif kind=='codex':
    skills(home/'.agents/skills','$'); skills(Path(os.getenv('CODEX_HOME',home/'.codex'))/'skills','$')
    for base in (*reversed(cwd.parents),cwd): skills(base/'.agents/skills','$')
elif kind=='pi':
    agent=Path(os.getenv('PI_CODING_AGENT_DIR',home/'.pi/agent'))
    for base in (agent,cwd/'.pi'): prompts(base/'prompts')
    if os.environ['HC_FALLBACK_SKILLS']=='1':
        for root in (home/'.agents/skills',agent/'skills',cwd/'.agents/skills',cwd/'.pi/skills'): skills(root,'skill:')
print(json.dumps(list(entries.values()),ensure_ascii=False))
PY`

func (c *ChatSessionController) discoverCommands(kind, cwd string) {
	if c.backend == nil || (kind != "pi" && kind != "claude" && kind != "codex") {
		return
	}
	c.mu.Lock()
	if c.closed || c.commandsLoading || time.Since(c.commandsLoaded) < time.Minute {
		c.mu.Unlock()
		return
	}
	c.commandsLoading = true
	parent := c.loopCtx
	fallback := "0"
	if c.piSkills == nil {
		fallback = "1"
	}
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(parent, 6*time.Second)
		defer cancel()
		on, _ := SplitMachineAddr(c.key.Addr)
		res, err := c.backend.MachineExec(ctx, c.key.InstanceID, ExecParams{On: on, Cwd: cwd, Line: readAgentCommands, Timeout: 5, Env: map[string]string{"HC_AGENT_KIND": kind, "HC_FALLBACK_SKILLS": fallback}})
		var entries []slashEntry
		ok := err == nil && !res.Truncated && (res.Code == nil || *res.Code == 0) && json.Unmarshal([]byte(res.StdoutText()), &entries) == nil
		c.mu.Lock()
		c.commandsLoading = false
		if ok && !c.closed {
			c.discoveredCommands = entries
			c.commandsLoaded = time.Now()
		}
		c.mu.Unlock()
		if ok {
			c.bumpRev(false)
		}
	}()
}

func (c *ChatSessionController) commandEntries(kind string) []slashEntry {
	if c.rev != nil {
		_ = c.rev.Get()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var entries []slashEntry
	for _, e := range c.discoveredCommands {
		if kind == "pi" && c.piSkills != nil && strings.HasPrefix(e.Name, "skill:") {
			continue
		}
		entries = append(entries, e)
	}
	if kind == "pi" {
		entries = append(entries, c.piSkills...)
	}
	return entries
}
