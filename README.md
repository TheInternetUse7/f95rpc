# `f95rpc`

`f95rpc` is a lightweight Windows daemon and CLI tool that broadcasts rich Discord presence for games tracked on F95zone.

---

## Key Highlights

- **Confirmed-Only Broadcasting:** Only games that have been confirmed via scan, library import, or explicit manual binding appear on Discord.
- **Zero Asset Uploads Required:** Uses direct public URLs for image assets. You don't need to create a Discord Developer Application or upload icons to Developer Portal.
- **Status Display Type Details:** Discord status in member lists reads **`Playing <Game Title>`**.
- **Arbitrary Root Folders (`N` Roots):** Add any number of game directories. Uses safe Windows path matching (case-insensitive, directory-boundary checked, with longest-prefix nested root resolution and exclusion rules).
- **Dual-Scope Runtime Watcher:** Monitors processes inside configured `roots` **OR** inside any approved game directory in `f95rpc.db` (enabling scattered games from F95Checker / Playnite to be tracked seamlessly).
- **Clean Single IPC Connection:** Connects over `\\.\pipe\discord-ipc-*` with auto-reconnect. If multiple games are running, simple deterministic rules select the active game (`lock_first` by default, or `foreground`).
- **Development-Friendly:** Automatically uses `./config.yaml` and `./f95rpc.db` from the repository root when present, keeping local development cleanly isolated from `%APPDATA%`.

---

## Getting Started

### 1. Download or Build

Download `f95rpc-vX.Y.Z-windows-amd64.zip` from the [Releases page](https://github.com/TheInternetUse7/f95rpc/releases) and extract the entire ZIP into one folder. Keep `f95rpc.exe` and `f95rpc-tray.exe` together: the CLI's `start` command launches the companion beside it. The ZIP also contains `config.example.yaml` and this README.

To build from source, use the Go version specified in [`go.mod`](go.mod):

```powershell
# Build the CLI tool
go build -o f95rpc.exe ./cmd/f95rpc

# Build the background System Tray companion (zero console window)
go build -ldflags="-H windowsgui" -o f95rpc-tray.exe ./cmd/f95rpc-tray
```

Plain `go build` binaries report `dev`. Use the [tagged release builder](#tagged-releases) to embed a release version and Windows file properties.

### 2. Discord Application Setup

You supply your own Discord Application ID:

1. **Create Application:** Open the [Discord Developer Portal](https://discord.com/developers/applications) → click **New Application** → name it `f95rpc` (or any name you prefer) → copy the **Application ID**.
2. **(Optional) Art Assets:** Under **Rich Presence** → **Art Assets**, you can optionally upload PNGs from the [`assets/`](assets/) folder (e.g. `f95_logo.png`). _(This is optional: by default, `f95rpc` references direct HTTPS image URLs so icons display automatically without manual uploads)._
3. **Paste into Config:** Paste your Application ID into `config.yaml`:
   ```yaml
   discord_client_id: "YOUR_APPLICATION_ID_HERE"
   ```
   _(Alternatively, you can export the `DISCORD_CLIENT_ID` environment variable)._

### 3. Configuration & Game Roots

You can configure `f95rpc` in two ways:

- **Repository Root (Development):** Copy `config.example.yaml` to `./config.yaml` in the repo root. `f95rpc` automatically detects and uses it.
- **Global User Config (Installed):** Saved at `%APPDATA%\f95rpc\config.yaml`.

```powershell
# Copy example configuration for local development
Copy-Item config.example.yaml config.yaml

# Inspect active configuration
.\f95rpc.exe config

# List configured roots
.\f95rpc.exe roots list

# Add a game root folder
.\f95rpc.exe roots add "D:\Games"

# Remove a root folder
.\f95rpc.exe roots remove "D:\Games"
```

### 4. Import & Scan Games

```powershell
# Import library from F95Checker (extracts local paths, thread IDs, and enriches metadata)
.\f95rpc.exe import f95checker
.\f95rpc.exe import f95checker --dry-run

# Import library from Playnite
.\f95rpc.exe import playnite --list-categories
.\f95rpc.exe import playnite --category "F95,Visual Novels" --dry-run
.\f95rpc.exe import playnite

# Scan configured roots and interactively approve detected games
.\f95rpc.exe scan --dry-run
.\f95rpc.exe scan

# Manually bind or ignore a folder
.\f95rpc.exe bind "D:\Games\MyGame" 12345
.\f95rpc.exe ignore "D:\Games\NotAGame"

# List all approved games in the local registry
.\f95rpc.exe list
```

### 5. Running f95rpc

#### Option A: System Tray Companion (Recommended)

Runs completely silently in the background with zero CMD window:

```powershell
# Launch in Windows System Tray
.\f95rpc.exe start

# Check status
.\f95rpc.exe status

# Stop background tray companion and wait for clean shutdown
.\f95rpc.exe stop
```

`stop` uses the same cleanup as the tray's **Quit** action: it clears Discord presence and closes SQLite before reporting success.

**Tray Menu Features:**

- **Build Version:** Shows the same Git tag as `f95rpc version`.
- **Live Status:** Displays current game title.
- **Toggle Cover Art (Active Game):** Flips the active game between F95 logo and game cover art.
- **Stealth Mode (Pause RPC):** Instantly clears and pauses Discord Rich Presence.
- **Pending Matches (N)...:** Shows newly detected games waiting for review (clicking opens `f95rpc scan`).
- **Reload config.yaml:** Re-reads configuration without restarting.
- **Open Config Folder:** Opens the config directory in Windows Explorer.
- **Start with Windows:** Toggles Windows auto-start on logon.
- **Quit:** Gracefully clears Discord presence and shuts down.

#### Option B: Foreground Terminal Console

Runs with detailed real-time event logging (ideal for debugging):

```powershell
# Run in foreground console
.\f95rpc.exe run

# Start in stealth mode
.\f95rpc.exe run --stealth

# Run with a custom poll interval (e.g. 10 seconds)
.\f95rpc.exe run --poll 10
```

### 6. Run Smoke Tests

Verify SQLite database, F95ApiDB connectivity, and Discord IPC:

```powershell
# Quick connectivity check
.\f95rpc.exe smoke-test

# Test search against f95api.xyz with an API key
.\f95rpc.exe smoke-test -api-key "<YOUR_F95API_KEY>"

# Broadcast a 10-second test presence to running Discord Desktop
.\f95rpc.exe smoke-test -send-presence
```

---

## Running Unit Tests

All unit tests use standard Go testing and run zero-CGO SQLite in temporary isolated directories:

```powershell
go test ./...
```

---

[`release.yml`](.github/workflows/release.yml) checks out that exact tag, uses the Go version from `go.mod`, runs tests and vet, builds both Windows x64 executables, and publishes a GitHub Release with generated notes and two assets:

- `f95rpc-v0.1.0-windows-amd64.zip`
- `SHA256SUMS.txt`

For a local release build on Windows with PowerShell 7, start from a clean checkout of the tag:

```powershell
git switch --detach v0.1.0
pwsh -NoProfile -File scripts/build-release.ps1 -Tag v0.1.0
```

The builder refuses missing tags or tags pointing at a different commit, verifies both EXEs' versions and console/GUI settings, and writes the ZIP and checksum to `dist/`. Its pinned resource generator is build-only; running the downloaded EXEs does not require Go, PowerShell, or an external assets folder.

To upgrade, stop or quit the existing tray, then replace **both** EXEs together. Preserve your `config.yaml` and database; do not copy the example configuration over an existing configuration. Release binaries are not code-signed, so Windows may still show SmartScreen or unknown-publisher warnings.

---

## Configuration Reference

Key settings in `config.yaml`:

| Setting                 | Type   | Default                                              | Description                                         |
| ----------------------- | ------ | ---------------------------------------------------- | --------------------------------------------------- |
| `f95api_key`            | string | `""`                                                 | F95ApiDB key (or via `F95API_KEY` env var)          |
| `discord_client_id`     | string | `""`                                                 | Your Discord Application ID (BYO App ID)            |
| `roots`                 | list   | `[]`                                                 | List of game root directories                       |
| `exclude_paths`         | list   | `["_mods", "backups", ...]`                          | Folder names/patterns to skip                       |
| `selection_mode`        | string | `"lock_first"`                                       | Multi-game resolution: `lock_first` or `foreground` |
| `poll_interval_seconds` | int    | `5`                                                  | Process scanning interval                           |
| `stealth_mode`          | bool   | `false`                                              | Global pause toggle                                 |
| `default_cover_mode`    | string | `"f95_logo"`                                         | `f95_logo` (privacy default) or `game_cover`        |
| `default_logo_url`      | string | `"https://f95zone.to/assets/images/logo-sqw192.png"` | Official F95zone fallback image URL                 |
| `show_thread_button`    | bool   | `true`                                               | Add "View on F95zone" link button                   |
| `show_repo_button`      | bool   | `true`                                               | Add repository / source link button                 |
| `repo_button_label`     | string | `"GitHub"`                                           | Label for repository button                         |
| `repo_button_url`       | string | `"https://github.com/TheInternetUse7/f95rpc"`        | URL for repository button                           |
| `display_type`          | string | `"name"`                                             | Status display mode: `name` or `details`            |
| `templates.name`        | string | `"{title}"`                                          | Heading / Activity name template                    |
| `templates.details`     | string | `"v{installed_version} • {creator}"`                 | Secondary details line template                     |
| `templates.state`       | string | `"Engine: {engine}"`                                 | State line template                                 |

---

## F95Api (`f95api.xyz`) Integration

`f95rpc` integrates with [F95ApiDB](https://f95api.xyz) to resolve game metadata, cover art, thread links, and release versions.

### How It Works

- **Metadata Enrichment:** When scanning folders or importing from F95Checker/Playnite, `f95rpc` queries `f95api.xyz` to automatically fetch titles, creators, game engines, versions, and ratings.
- **Direct Image Hosting:** Images returned by the API (`cover`, `banner`, `previews`) are direct public URLs on `preview.f95zone.to`, allowing Discord to display them instantly without requiring you to manually upload art to Discord's Developer Portal.
- **Client Throttling:** `f95rpc` enforces a minimum 500ms delay between consecutive API calls (~120 requests/minute maximum) to respect server capacity and avoid HTTP 429 rate limits.
- **Key Authentication:** Pass your key in `config.yaml` (`f95api_key`) or via the `F95API_KEY` environment variable. Sent as the `X-API-Key` request header.

### Key API Endpoints

| Endpoint                              | Method | Description                                                            |
| ------------------------------------- | ------ | ---------------------------------------------------------------------- |
| `/v1/f95/handiwork?thread_id=<ID>`    | `GET`  | Fetches full game details, tags, ratings, and media by F95 thread ID.  |
| `/v1/search?q=<QUERY>&limit=<N>`      | `GET`  | Searches the database by title, returning thread IDs and covers.       |
| `/v1/threads/batch?ids=<ID1,ID2,...>` | `GET`  | Batch-fetches summaries for multiple threads in a single HTTP request. |

### Available Media Fields

When fetching handiwork details for a game, the API provides several image types:

- **`cover`**: The primary thread cover image.
- **`banner`**: Widescreen banner/header artwork (if provided in the thread header; otherwise `null`).
- **`previews`**: An array of direct screenshot URLs taken from the release post. Any of these URLs can be used for custom game cover art in `config.yaml` under `games.<path>.cover_mode`.

### Inspecting the API (`curl`)

You can test and inspect raw API data using `curl`:

```powershell
# Fetch full game metadata and preview images by thread ID
curl.exe -s -H "X-API-Key: YOUR_API_KEY" -H "User-Agent: f95rpc/0.1" "https://f95api.xyz/v1/f95/handiwork?thread_id=29666"

# Search for a game by title
curl.exe -s -H "X-API-Key: YOUR_API_KEY" -H "User-Agent: f95rpc/0.1" "https://f95api.xyz/v1/search?q=Away+from+Home&limit=3"
```

---

## License

MIT
