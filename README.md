# File Review

A local browser-based utility for cleaning up directories. It walks through every file and folder, shows you a preview, and lets you mark items for deletion — then moves them to the bin or permanently deletes them in one click.

---

## Features

- **Browser UI** — runs a local web server and opens automatically in your default browser
- **Smart previews** by file type:
  - 🖼 **Images** — rendered inline
  - 📄 **Text / Code** — syntax-highlighted, first 30 lines (expandable to 100)
  - 📕 **PDF** — extracted text preview
  - 🎵 **Audio** — in-browser playback
  - 🎬 **Video** — in-browser playback
  - 📁 **Directories** — contents listing with option to recurse inside
  - 🗂 **Binary / Unknown** — MIME type and file size
- **Infinite recursion** — enter any directory and review its contents individually
- **Persistent state** — marked paths are written to `to_delete.txt` immediately on every `d` press; resuming skips already-marked files
- **One-click deletion** — Move to Bin (soft delete) or Permanently Delete directly from the done screen
- **Cross-platform** — macOS and Linux

---

## Requirements

- [Go](https://go.dev/dl/) 1.21+
- No other runtime dependencies — the binary embeds the entire UI

### Optional (for Move to Bin on macOS)

```bash
brew install trash
```

If `trash` is not installed, the app falls back to AppleScript / Finder automatically.

On Linux, `gio` (ships with GNOME) or `trash-cli` is used:

```bash
# Debian/Ubuntu
sudo apt install trash-cli

# Arch
sudo pacman -S trash-cli
```

---

## Build

```bash
git clone <repo-url>
cd delete_me_papi
go build -o file-review .
```

---

## Usage

### Run from the terminal

```bash
# Review a specific directory
./file-review ~/Downloads

# Review the directory the binary lives in (no argument needed)
./file-review
```

The server starts on `http://localhost:8080` and opens in your browser automatically.

### Double-click (macOS)

1. Copy `file-review` and `File Review.command` into the folder you want to clean up
2. Double-click **`File Review.command`**
3. Terminal opens briefly, the browser launches, and the review begins

> **First run:** macOS may block the file. Right-click → **Open** → **Open Anyway** to bypass the Gatekeeper prompt (one-time only).

---

## Controls

| Action | Button | Keyboard |
|---|---|---|
| Keep file | ✓ Keep | `k` |
| Mark for deletion | ✗ Delete | `d` |
| Skip (decide later) | → Skip | `s` |
| Enter a directory | ↳ Enter | `e` |
| Go back to parent | ↑ Back | `b` |
| Expand preview to 100 lines | show up to 100 | `x` |
| Quit session | Quit | `q` |

---

## Deletion

When you finish reviewing (or quit), the done screen offers two buttons:

| Button | Behaviour |
|---|---|
| 🗑 **Move to Bin** | Sends each path to the OS trash — recoverable |
| 💥 **Permanently Delete** | Prompts for confirmation, then removes all marked paths irreversibly |

Results are shown per-file (✅ success / ❌ failure). If any fail, the buttons re-enable so you can retry.

---

## to_delete.txt

All paths marked for deletion are appended to `to_delete.txt` in the directory the binary is run from. The file is written **immediately** on every delete action — quitting early won't lose your work.

The file format is plain text, one absolute path per line, with a `#` comment header:

```
# Files/directories marked for deletion

/Users/you/Downloads/old-backup.zip
/Users/you/Downloads/screenshot 2023-01-01.png
```

On the next run, any path already in `to_delete.txt` is automatically skipped.

### Manual deletion via terminal

**Move to Bin — macOS:**
```bash
grep -v '^#' to_delete.txt | grep -v '^$' | xargs -I{} trash "{}"
```

**Move to Bin — Linux:**
```bash
grep -v '^#' to_delete.txt | grep -v '^$' | xargs -I{} gio trash "{}"
```

**Permanently delete (both platforms):**
```bash
grep -v '^#' to_delete.txt | grep -v '^$' | xargs -I{} rm -rf "{}"
```

---

## Project structure

```
.
├── main.go              # Go server — HTTP handlers, file preview logic, session state
├── static/
│   └── index.html       # Single-page browser UI (embedded into the binary)
├── File Review.command  # Double-clickable launcher for macOS
├── to_delete.txt        # Generated at runtime — paths marked for deletion
└── go.mod
```
