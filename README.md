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
| `pin share <file>` | Upload an HTML or MDX file. Prints the share URL. |
| `pin get <id-or-url>` | Fetch a share's MDX source to stdout. `--html` for the rendered form. |
| `pin publish <id-or-url>` | Make a share publicly viewable (no login) via a capability link. `--ttl <dur>` sets the lifetime (default 7d, max 30d). Prints the public URL. |
| `pin unpublish <token-or-url>` | Revoke a public link before it expires. |
| `pin components` | List the MDX components available in pin, grouped by category. |
| `pin components get <Name>` | Show one component's props + example. |
| `pin components dump` | Print every component's full detail. |
| `pin whoami` | Show the currently-logged-in email. |
| `pin logout` | Revoke the refresh token + clear local creds. |
| `pin version` | Print the CLI version. |

Environment variables:

| Var | Default | What |
|---|---|---|
| `PIN_HOST` | `https://pin.bitcomplete.dev` | Override the pin instance. |
| `PIN_AGENT` | `pin-cli@<hostname>` | Label sent with auth + upload requests for audit. |

Credentials are stored in your OS keychain (macOS Keychain / Windows Credential Manager / libsecret on Linux) with a 0600 file fallback at `~/.config/pin/credentials.json` for headless boxes.

### Public sharing

Shares are private by default — only `@bitcomplete.io` accounts can open `https://pin.bitcomplete.dev/p/{id}`. To share a specific artifact with someone outside the org, `pin publish` mints a login-free capability link:

```sh
pin publish 01HX...            # → https://pin.bitcomplete.dev/public/p/01HX...?token=...
pin publish 01HX... --ttl 24h  # default 7 days, max 30
pin unpublish <token-or-url>   # kill the link early
```

This is a deliberate, separate step — you can't publish by accident. The link works for anyone who has it (no login) until it expires or you revoke it; anyone without the exact token gets a plain `404`.

## How auth works

`pin login` does an OAuth 2.1 PKCE loopback flow against the pin server (which in turn handles Google SSO for the human). `pin login --device` falls back to the RFC 8628 device-code flow when no local browser is available.

Refresh tokens are rotated on every use; reuse triggers family revocation. If a CI script's stored token gets exfiltrated and used elsewhere, the next legitimate refresh detects it and locks everyone out — `pin login` again to recover.

## License

MIT
