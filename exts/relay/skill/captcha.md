# Captcha solving — `relay solve-captcha <type>`

Sends a captcha to a solving service and returns the answer. `<type>` names
the kind of puzzle; the relay picks a backend in the routing group that
solves it. The result format depends only on the type.

| Type | Puzzle | Required | Optional | `result` |
| --- | --- | --- | --- | --- |
| `image_text` | Characters in one image | `--image` | – | The characters |
| `image_math` | Arithmetic question in one image | `--image` | – | The computed answer |
| `image_question` | A question asked in one image | `--image` | `--prompt` (the question, when it is not in the image) | The answer |
| `slider` | Slide a piece into a gap | `--background-image` (the background, or one screenshot showing piece and gap) | `--slide-image` (the piece, when separate) | Pixel distance |
| `click` | Click targets in an image | `--image` | `--prompt` (what to click; characters in click order: `天,地,人`), `--prompt-image` (the puzzle's picture of the targets) | `x,y\|x,y` in click order |
| `rotate` | Rotate an image upright | `--image` | `--background-image` (a fixed outer picture it must line up with) | Clockwise angle in degrees |
| `trajectory` | Drag along a path | `--image` | `--prompt` (what to drag, when the image does not say) | Path `x,y\|x,y`; strokes joined by `;` |
| `recaptcha_v2` | Google reCAPTCHA v2 | `--site-key`, `--page-url` | `--invisible`, `--enterprise` | Response token |
| `recaptcha_v3` | Google reCAPTCHA v3 | `--site-key`, `--page-url` | `--action`, `--min-score` (0.1–0.9), `--enterprise` | Response token |
| `hcaptcha` | hCaptcha | `--site-key`, `--page-url` | `--enterprise` | Response token |
| `turnstile` | Cloudflare Turnstile | `--site-key`, `--page-url` | `--action`, `--cdata`, `--page-data`, `--user-agent` | Response token |

Give images as `@path` (relative to the working directory); the command sends
the file base64-encoded. A base64 string, with or without a
`data:image/...;base64,` prefix, also works.

```bash
relay solve-captcha image_text --image @captcha.png
relay solve-captcha slider --background-image @bg.png --slide-image @piece.png
relay solve-captcha recaptcha_v2 --site-key <sitekey> --page-url https://target/login
```

`--group` (default `captcha`) and `--options key=value` work as everywhere
else.

- Coordinates are pixels of the image you sent, from its top-left corner:
  scale them if the page shows the image at another size. A slider distance
  or rotation angle is measured on the image too.
- Send an optional input only when the puzzle has it. `400
  UNSUPPORTED_BY_GROUP` means no backend in the routing group can serve the
  type, input or option: drop it if the puzzle allows, or tell the user. Do
  not retry it unchanged.
- `400 MISSING_TYPE` / `INVALID_CAPTCHA_TYPE`: the type is empty or not in the
  table. `400 CAPTCHA_INPUT_MISSING` names the input the type still needs.
  `400 CAPTCHA_INPUT_INVALID`: an input or image was rejected (a `--min-score`
  outside 0.1–0.9, a bad image encoding). Fix the input.
- `422 CAPTCHA_SOLVE_FAILED`: the backend could not solve the puzzle. Check
  the inputs or refresh the captcha, and retry once.
- A solve is slow: picture puzzles take up to about 20 seconds, token puzzles
  20 to 80 seconds; the command waits up to 180 seconds.
- `504 UPSTREAM_TIMEOUT` / `REQUEST_TIMEOUT`: the solve may have been charged.
  If the charge matters, find out before retrying; retry at most once.
- Each solve is charged. If the target rejects an answer, refresh the captcha
  before solving again — never resubmit the same image.
- Get the image from the page you are testing (a browser screenshot or the
  captcha endpoint's response). Pick the type from what the puzzle asks; if
  none fits, ask the user.
