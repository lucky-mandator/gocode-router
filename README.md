# gocode-router (a.k.a. OpenAI-to-Claude Teleporter)

Welcome to the extremely professional API router that thinks it's a barista. Order an OpenAI-style latte, and it serves up Claude foam art without anyone noticing. Handy when you want one client SDK to sweet talk multiple providers.

## Why would I use this gadget?
- Choreographs OpenAI-compatible requests while whispering to Anthropic, NVIDIA, or your other model crushes.
- Lets you alias models so `gpt-4o` secretly becomes your favorite Claude variant.
- Keeps config in a single YAML file so you can brag about "infrastructure-as-doc".
- Written in Go, meaning one binary, zero drama.

## Three-Step Speedrun (a.k.a. "easy setup reference")
1. **Install Go 1.25+** – `brew install go` or whatever gets you that shiny toolchain.
2. **Copy and edit the sample config**
   ```bash
   cp example.config.yaml config.yaml
   ${EDITOR:-nano} config.yaml
   ```
   Drop in your real API keys, ensure your AI model can actually reach those secrets (vault, env vars, however you roll), tweak base URLs if needed, and optionally map aliases (e.g. `claude-sonnet-4-6` → `moonshotai/kimi-k2.5`).
3. **Launch the router**
   ```bash
   make run CONFIG=config.yaml
   ```
   Or if you prefer raw Go energy: `go run . serve --config config.yaml`.

## Configuration Cheat Sheet
- `server.port` – TCP port for the proxy (defaults to `8080` in the sample).
- `providers.openai|claude|nvidia` – supply `api_key`, `base_url`, and at least one `models` block.
- `models[].api_style` – `openai` for OpenAI-ish JSON, `claude` for Anthropic's flavor.
- `aliases` – expose vanity model names that forward to a real provider ID.
- Extra headers? Sprinkle them under `headers:` and we send them on every request.

## Hot Reload Vibes
- The binary polls your config every couple of seconds; tweak YAML and it re-wires providers without a restart.
- Passed `--port`? We keep that override even if the file begs otherwise—consistency over chaos.

## Talking To It
Point your favorite SDK/cli at `http://localhost:<port>` and keep using the usual `/v1/chat/completions` endpoint. Requests are translated on the fly before being handed to the real provider you configured.

## NVIDIA Kimi + Claude CLI Mashup
Want a quick "Kimi brain, Claude wrapper" demo? Drop the following into `config.yaml` so the router knows how to reach NVIDIA's Kimi while keeping the familiar Claude model alias:
```yaml
providers:
  claude:
    api_key: "sk-claude"
    base_url: "https://api.anthropic.com"
    models:
      - id: claude-3.5-sonnet
        api_style: claude

  nvidia:
    api_key: "nvapi"
    base_url: "https://integrate.api.nvidia.com/v1"
    headers:
      NVCF-AI-Resource: "moonshotai/kimi-k2.5"
    models:
      - id: moonshotai/kimi-k2.5
        api_style: openai
    aliases:
      claude-3.5-sonnet: moonshotai/kimi-k2.5
```
Start the router (pick a port you like):
```bash
go run . serve --config config.yaml --port 8080
```
or
```bash
make run CONFIG=config.yaml
```
Then aim the Claude CLI at your local teleporter to get Kimi responses through the Claude UX:
```bash
AUNTROPIC_AUTH_TOKEN=dummy ANTROPIC_BASE_URL=http://127.0.0.1:8080 claude
```
Swap the port in the command if you chose something other than `8080`.

## Development Snacks
- Build the binary: `make build`
- Run tests: `make test`
- Clean artefacts: `make clean`

## Troubleshooting (a.k.a. "Don't Panic")
- **401s** usually mean the upstream key is wrong or missing.
- **404 model not found**? Check your `models` array and `aliases` spelling.
- **Port already in use**? Somebody else is partying on that port—either shut them down or set `--port` when launching.

Happy routing! If it misbehaves, blame the person who typed their API key into Slack.
