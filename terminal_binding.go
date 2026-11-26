package main

import (
	"fmt"
	"strings"
	"unicode"
)

var terminalKeyBytes = map[string]string{"tab": "\t", "enter": "\r", "esc": "\x1b", "space": " ", "bspace": "\x7f", "backspace": "\x7f", "home": "\x1b[H", "end": "\x1b[F", "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D", "pageup": "\x1b[5~", "pagedown": "\x1b[6~", "f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS", "f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~", "f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~"}

func terminalBinding(binding string) ([]byte, error) {
	if literal, ok := strings.CutPrefix(binding, "text:"); ok && literal != "" {
		return []byte(literal), nil
	}
	var seq []byte
	for _, token := range strings.FieldsFunc(binding, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		parts := strings.FieldsFunc(token, func(r rune) bool { return r == '-' || r == '+' })
		if len(parts) == 0 || strings.HasSuffix(token, "-") || strings.HasSuffix(token, "+") {
			return nil, fmt.Errorf("无效快捷键 %q", token)
		}
		ctrl, alt, shift := false, false, false
		for _, mod := range parts[:len(parts)-1] {
			switch strings.ToLower(mod) {
			case "c", "ctrl":
				ctrl = true
			case "m", "alt", "opt":
				alt = true
			case "s", "shift":
				shift = true
			default:
				return nil, fmt.Errorf("未知修饰键 %q", mod)
			}
		}
		key := parts[len(parts)-1]
		lower := strings.ToLower(key)
		v, named := terminalKeyBytes[lower]
		switch {
		case lower == "tab" && shift && !ctrl:
			v = "\x1b[Z"
		case named && (ctrl || shift):
			return nil, fmt.Errorf("暂不支持 %q", token)
		case named:
		case len(key) == 1 || lower == "dash" || lower == "plus":
			if lower == "dash" {
				key = "-"
			} else if lower == "plus" {
				key = "+"
			}
			if shift {
				key = strings.ToUpper(key)
			}
			v = key
			if ctrl {
				ch := strings.ToUpper(key)[0]
				if ch == '-' {
					ch = '_'
				}
				if ch < '@' || ch > '_' {
					return nil, fmt.Errorf("无效 Ctrl 组合 %q", token)
				}
				v = string([]byte{ch & 31})
			}
		default:
			return nil, fmt.Errorf("未知按键 %q", key)
		}
		if alt {
			seq = append(seq, 0x1b)
		}
		seq = append(seq, v...)
	}
	if len(seq) == 0 {
		return nil, fmt.Errorf("请输入快捷键")
	}
	return seq, nil
}
