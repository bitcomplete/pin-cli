# pin-cli

Command-line client for pin — a tiny tool for sharing HTML files behind Google SSO.

## Install

```sh
brew install bitcomplete/tap/pin
```

Or `go install github.com/bitcomplete/pin-cli@latest` if you'd rather build from source.

## Use

```sh
pin login                       # one-time, opens browser → Google SSO
pin share /tmp/report.html      # → https://pin.bitcomplete.dev/p/01HX...
```

Subcommands:

| Command | What |
|---|---|
| `pin login [--device]` | Sign in. `--device` for SSH / headless boxes. |
| `pin share <file>` | Upload an HTML file. Prints the share URL. |
| `pin whoami` | Show the currently-logged-in email. |
| `pin logout` | Revoke the refresh token + clear local creds. |
| `pin version` | Print the CLI version. |

Environment variables:

| Var | Default | What |
|---|---|---|
| `PIN_HOST` | `https://pin.bitcomplete.dev` | Override the pin instance. |
| `PIN_AGENT` | `pin-cli@<hostname>` | Label sent with auth + upload requests for audit. |

Credentials are stored in your OS keychain (macOS Keychain / Windows Credential Manager / libsecret on Linux) with a 0600 file fallback at `~/.config/pin/credentials.json` for headless boxes.

## How auth works

`pin login` does an OAuth 2.1 PKCE loopback flow against the pin server (which in turn handles Google SSO for the human). `pin login --device` falls back to the RFC 8628 device-code flow when no local browser is available.

Refresh tokens are rotated on every use; reuse triggers family revocation. If a CI script's stored token gets exfiltrated and used elsewhere, the next legitimate refresh detects it and locks everyone out — `pin login` again to recover.

## License

MIT
