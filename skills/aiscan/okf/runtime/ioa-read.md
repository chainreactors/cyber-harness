---
type: Tool Playbook
title: ioa read
description: Read addressed, recent, or threaded messages from the current IOA collaboration space with cursor and limit controls.
tags: [runtime, collaboration, ioa]
status: stable
generated: { by: process:tool-registry-description, at: 2026-08-02T00:00:00Z }
---

# ioa read — Collaboration Inbox

`ioa read` retrieves messages from the current IOA space — addressed by
default, `--all` for everything, `--message` for thread context, `--after` for
cursor updates, `--listen` for a live stream.

See the ioa skill (`aiscan://skills/ioa/SKILL.md`) for message formats and coordination rules.
