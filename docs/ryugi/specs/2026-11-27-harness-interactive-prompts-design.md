# Harness 多选项权限审批、交互式提问与 Pi 元数据精准适配设计

支持多选项权限审批（Agy 4 项、Claude 2–3 项、Codex/Pi）、聊天模式内 `AskUserQuestion` / `ask_question` / Pi `rpiv-ask-user-question` 直接交互，以及修复 Pi 头部 `thinking_level_change` 与 `models.json` 精确 `contextWindow`（如 272k）。

- Design: `.ryugi/artifacts/document/harness-interactive-prompts/doc.xml`
- Read: `node /Users/zhangyaowei/.gemini/config/plugins/ryugi/skills/artifacts/app/ryugi.cjs get --artifact document/harness-interactive-prompts --detail --project-dir /Volumes/ExternalSSD/workspace/herdr-nsgo`
- Boards: `artboard/harness-interactive-prompts` (`perm-stack`, `q-inline-card`)
- Approved: 2026-11-27
