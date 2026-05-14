---
name: vision
description: Use this skill to learn how to use the vision pseudo-command.
internal: true
---

# vision

Analyze a local image file using a vision-capable LLM. Requires a local path — download remote images first.

```bash
vision <image_path> <prompt...>
vision <image_path> --prompt <text>
```

- `<image_path>`: local file, supports PNG, JPG, JPEG, GIF, WEBP, BMP. Max 20 MB.
- `<prompt...>`: remaining args joined as the analysis prompt.

```bash
vision screenshot.png Read all visible text
vision /tmp/captcha.png "Solve this CAPTCHA"
vision topology.png --prompt "Describe the network topology"
```
