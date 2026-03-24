#!/usr/bin/env python3
"""
file_review.py — Interactive file review utility.

Usage:
    python file_review.py [DIRECTORY]

Iterates through each item in DIRECTORY (default: current dir), shows a
preview, and asks whether to mark it for deletion. Saves marked paths to
`to_delete.txt`. Files already listed in `to_delete.txt` are skipped.
"""

import base64
import mimetypes
import os
import shutil
import sys
import warnings
from datetime import datetime
from pathlib import Path

# Suppress urllib3/term-image noise
warnings.filterwarnings("ignore", category=UserWarning)
warnings.filterwarnings("ignore", category=DeprecationWarning)

from rich.console import Console
from rich.panel import Panel
from rich.syntax import Syntax
from rich.table import Table
from rich.text import Text
from rich.prompt import Prompt

try:
    from pypdf import PdfReader
    PYPDF_AVAILABLE = True
except ImportError:
    PYPDF_AVAILABLE = False


console = Console()

IMAGE_EXTS = {".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".tiff", ".ico"}
TEXT_EXTS = {
    ".txt", ".md", ".rst", ".csv", ".log", ".json", ".yaml", ".yml",
    ".toml", ".ini", ".cfg", ".conf", ".env", ".sh", ".bash", ".zsh",
    ".py", ".js", ".ts", ".tsx", ".jsx", ".html", ".htm", ".css",
    ".scss", ".sass", ".less", ".xml", ".sql", ".rb", ".go", ".rs",
    ".c", ".cpp", ".h", ".hpp", ".java", ".kt", ".swift", ".php",
    ".r", ".lua", ".vim", ".dockerfile", ".makefile",
}
AUDIO_EXTS = {".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma"}
VIDEO_EXTS = {".mp4", ".mov", ".avi", ".mkv", ".wmv", ".flv", ".webm", ".m4v"}

OUTPUT_FILE = Path("to_delete.txt")
QUIT = "quit"

SHORT_LINES = 30
LONG_LINES  = 100


# ---------------------------------------------------------------------------
# Image display
# ---------------------------------------------------------------------------

def _try_iterm2(path: Path) -> bool:
    """Render image using iTerm2 inline image protocol."""
    term = os.environ.get("TERM_PROGRAM", "") + os.environ.get("LC_TERMINAL", "")
    if "iTerm" not in term and "iTerm2" not in os.environ.get("TERM_PROGRAM", ""):
        # Still try — many terminals silently ignore unknown OSC sequences
        pass
    try:
        data = path.read_bytes()
        b64 = base64.b64encode(data).decode()
        # width=auto lets the terminal scale it; preserveAspectRatio keeps proportions
        sys.stdout.write(f"\033]1337;File=inline=1;width=60%;preserveAspectRatio=1:{b64}\a\n")
        sys.stdout.flush()
        return True
    except Exception:
        return False


def _try_kitty(path: Path) -> bool:
    """Render image using Kitty graphics protocol."""
    if "kitty" not in os.environ.get("TERM", "").lower():
        return False
    try:
        data = path.read_bytes()
        b64 = base64.b64encode(data).decode()
        chunk_size = 4096
        chunks = [b64[i:i+chunk_size] for i in range(0, len(b64), chunk_size)]
        for idx, chunk in enumerate(chunks):
            m = 1 if idx < len(chunks) - 1 else 0
            if idx == 0:
                sys.stdout.write(f"\033_Ga=T,f=100,m={m};{chunk}\033\\")
            else:
                sys.stdout.write(f"\033_Gm={m};{chunk}\033\\")
        sys.stdout.write("\n")
        sys.stdout.flush()
        return True
    except Exception:
        return False


def _try_sixel(path: Path) -> bool:
    """Render image using img2sixel if available."""
    if not shutil.which("img2sixel"):
        return False
    try:
        os.system(f'img2sixel -w 400 "{path}"')
        return True
    except Exception:
        return False


def _try_imgcat(path: Path) -> bool:
    """Use imgcat CLI if available."""
    if not shutil.which("imgcat"):
        return False
    try:
        os.system(f'imgcat "{path}"')
        return True
    except Exception:
        return False


def preview_image(path: Path) -> None:
    rendered = (
        _try_iterm2(path)
        or _try_kitty(path)
        or _try_sixel(path)
        or _try_imgcat(path)
    )
    if not rendered:
        # Fallback: dimensions via Pillow
        try:
            from PIL import Image
            with Image.open(path) as im:
                console.print(f"  [dim]Dimensions:[/dim] {im.width} × {im.height} px  |  Mode: {im.mode}")
        except Exception:
            console.print("  [dim](cannot render image — no supported terminal protocol found)[/dim]")


# ---------------------------------------------------------------------------
# Text / code preview
# ---------------------------------------------------------------------------

LEXER_MAP = {
    ".py": "python", ".js": "javascript", ".ts": "typescript",
    ".tsx": "tsx", ".jsx": "jsx", ".html": "html", ".htm": "html",
    ".css": "css", ".scss": "scss", ".json": "json", ".yaml": "yaml",
    ".yml": "yaml", ".toml": "toml", ".sh": "bash", ".bash": "bash",
    ".zsh": "bash", ".rb": "ruby", ".go": "go", ".rs": "rust",
    ".c": "c", ".cpp": "cpp", ".h": "c", ".hpp": "cpp",
    ".java": "java", ".kt": "kotlin", ".swift": "swift", ".php": "php",
    ".sql": "sql", ".xml": "xml", ".md": "markdown", ".r": "r",
}


def preview_text(path: Path, max_lines: int = SHORT_LINES) -> int:
    """
    Render up to `max_lines` lines with syntax highlighting.
    Returns the total line count of the file.
    """
    ext = path.suffix.lower()
    lexer = LEXER_MAP.get(ext, "text")
    try:
        lines = path.read_text(errors="replace").splitlines()
        total = len(lines)
        content = "\n".join(lines[:max_lines])
        syntax = Syntax(content, lexer, theme="monokai", line_numbers=True, word_wrap=False)
        console.print(syntax)
        if total > max_lines:
            console.print(
                f"  [dim]Showing {max_lines}/{total} lines. "
                f"Use [bold](x)[/bold] in the action prompt to show up to {LONG_LINES} lines.[/dim]"
            )
        return total
    except Exception as e:
        console.print(f"  [red]Cannot read file: {e}[/red]")
        return 0


# ---------------------------------------------------------------------------
# Other previews
# ---------------------------------------------------------------------------

def preview_pdf(path: Path) -> None:
    if not PYPDF_AVAILABLE:
        console.print("  [dim](pypdf not available)[/dim]")
        return
    try:
        reader = PdfReader(str(path))
        num_pages = len(reader.pages)
        console.print(f"  [dim]Pages:[/dim] {num_pages}")
        lines_shown = 0
        for i, page in enumerate(reader.pages):
            if lines_shown >= 50:
                console.print(f"  [dim]... (remaining {num_pages - i} pages not shown)[/dim]")
                break
            text = page.extract_text() or ""
            console.print(f"  [bold dim]─── Page {i + 1} ───[/bold dim]")
            for line in text.splitlines():
                if lines_shown >= 50:
                    break
                console.print(f"  {line}")
                lines_shown += 1
    except Exception as e:
        console.print(f"  [red]Cannot read PDF: {e}[/red]")


def preview_directory(path: Path) -> None:
    try:
        entries = sorted(path.iterdir(), key=lambda p: (p.is_file(), p.name.lower()))
        if not entries:
            console.print("  [dim](empty directory)[/dim]")
            return
        for entry in entries:
            prefix = "📁 " if entry.is_dir() else "📄 "
            extra = ""
            if entry.is_file():
                try:
                    extra = f"  [dim]{human_size(entry.stat().st_size)}[/dim]"
                except OSError:
                    pass
            elif entry.is_dir():
                extra = f"  [dim]{sum(1 for _ in entry.iterdir() if True)} items[/dim]"
            console.print(f"  {prefix}[cyan]{entry.name}[/cyan]{extra}")
    except PermissionError:
        console.print("  [red](permission denied)[/red]")


def preview_audio_video(path: Path) -> None:
    mime, _ = mimetypes.guess_type(str(path))
    console.print(f"  [dim]MIME:[/dim] {mime or 'unknown'}")
    console.print(f"  [dim]Size:[/dim] {human_size(path.stat().st_size)}")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def human_size(size_bytes: int) -> str:
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if size_bytes < 1024:
            return f"{size_bytes:.1f} {unit}"
        size_bytes /= 1024
    return f"{size_bytes:.1f} PB"


def get_file_info(path: Path) -> dict:
    try:
        stat = path.stat()
        return {
            "size": human_size(stat.st_size),
            "modified": datetime.fromtimestamp(stat.st_mtime).strftime("%Y-%m-%d %H:%M"),
        }
    except OSError:
        return {"size": "?", "modified": "?"}


def load_already_deleted() -> set:
    """Return set of absolute path strings already in to_delete.txt."""
    if not OUTPUT_FILE.exists():
        return set()
    paths = set()
    for line in OUTPUT_FILE.read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            paths.add(line)
    return paths


def append_to_delete(path: Path) -> None:
    """Immediately append a single path to to_delete.txt."""
    first_write = not OUTPUT_FILE.exists()
    with OUTPUT_FILE.open("a") as f:
        if first_write:
            f.write("# Files/directories marked for deletion\n\n")
        f.write(f"{path}\n")


def save_to_delete(to_delete: list, directory: Path) -> None:
    # No-op: entries are already written incrementally via append_to_delete.
    pass


def build_header(path: Path, index: int, total: int, breadcrumb: str) -> Panel:
    info = get_file_info(path) if path.is_file() else {"size": "—", "modified": "?"}
    kind = "Directory" if path.is_dir() else (path.suffix.upper().lstrip(".") or "File")

    text = Text()
    if breadcrumb:
        text.append(f"{breadcrumb} / ", style="dim")
    text.append(path.name, style="bold white")
    text.append(f"   {kind}", style="cyan")
    if path.is_file():
        text.append(f"  │  {info['size']}", style="dim")
        text.append(f"  │  {info['modified']}", style="dim")
    text.append(f"\n[{index}/{total}]", style="dim")
    return Panel(text, style="bold blue", padding=(0, 1))


# ---------------------------------------------------------------------------
# Prompts
# ---------------------------------------------------------------------------

def prompt_file_action(path: Path, lines_shown: int, total_lines: int) -> str:
    """
    Returns: k / d / s / q
    For text files with more content, also offers x (expand).
    """
    is_text = path.suffix.lower() in TEXT_EXTS or path.suffix == ""
    can_expand = is_text and total_lines > lines_shown and lines_shown < LONG_LINES

    console.print()
    options = (
        "  [bold green](k)[/bold green] keep  "
        "[bold red](d)[/bold red] mark for deletion  "
        "[bold yellow](s)[/bold yellow] skip  "
        "[bold cyan](q)[/bold cyan] quit & save"
    )
    if can_expand:
        options += "  [bold](x)[/bold] show more lines"
    console.print(options)

    valid = {"k", "d", "s", "q"} | ({"x"} if can_expand else set())
    while True:
        choice = Prompt.ask("  Action", default="k").strip().lower()
        if choice in valid:
            return choice
        console.print("  [red]Invalid choice.[/red]")


def prompt_dir_action() -> str:
    console.print()
    console.print(
        "  [bold green](k)[/bold green] keep  "
        "[bold red](d)[/bold red] delete entire directory  "
        "[bold magenta](e)[/bold magenta] enter & review contents  "
        "[bold yellow](s)[/bold yellow] skip  "
        "[bold cyan](q)[/bold cyan] quit & save"
    )
    while True:
        choice = Prompt.ask("  Action", default="k").strip().lower()
        if choice in ("k", "d", "e", "s", "q"):
            return choice
        console.print("  [red]Invalid choice.[/red]")


# ---------------------------------------------------------------------------
# Core recursive reviewer
# ---------------------------------------------------------------------------

def review_directory(directory: Path, to_delete: list, already_deleted: set,
                     breadcrumb: str = "") -> str:
    try:
        entries = sorted(directory.iterdir(), key=lambda p: (p.is_file(), p.name.lower()))
    except PermissionError:
        console.print(f"  [red]Permission denied:[/red] {directory}")
        return None

    # Filter out entries already marked for deletion
    entries = [e for e in entries if str(e.resolve()) not in already_deleted]

    if not entries:
        console.print(f"  [dim](nothing new to review in {directory})[/dim]")
        return None

    total = len(entries)
    crumb = (breadcrumb + "/" + directory.name) if breadcrumb else directory.name

    for i, path in enumerate(entries, start=1):
        lines_shown = SHORT_LINES
        total_lines = 0

        while True:  # re-render loop for (x) expand
            console.clear()
            console.print(build_header(path, i, total, breadcrumb))
            console.print()

            ext = path.suffix.lower()
            if path.is_dir():
                preview_directory(path)
            elif ext in IMAGE_EXTS:
                preview_image(path)
            elif ext == ".pdf":
                preview_pdf(path)
            elif ext in TEXT_EXTS or ext == "":
                total_lines = preview_text(path, max_lines=lines_shown)
            elif ext in AUDIO_EXTS or ext in VIDEO_EXTS:
                preview_audio_video(path)
            else:
                mime, _ = mimetypes.guess_type(str(path))
                info = get_file_info(path)
                console.print(f"  [dim]MIME:[/dim] {mime or 'unknown binary'}")
                console.print(f"  [dim]Size:[/dim] {info['size']}")

            console.print()

            if path.is_dir():
                action = prompt_dir_action()
            else:
                action = prompt_file_action(path, lines_shown, total_lines)

            if action == "x":
                lines_shown = LONG_LINES
                continue  # re-render with more lines
            break  # any other action exits the re-render loop

        if action == "q":
            return QUIT
        elif action == "d":
            resolved = path.resolve()
            to_delete.append(resolved)
            already_deleted.add(str(resolved))
            append_to_delete(resolved)
            console.print(f"  [red]✗ Marked for deletion:[/red] {path.name}")
        elif action == "e":
            result = review_directory(path, to_delete, already_deleted, breadcrumb=crumb)
            if result == QUIT:
                return QUIT
        elif action == "k":
            console.print(f"  [green]✓ Kept:[/green] {path.name}")
        else:
            console.print(f"  [dim]→ Skipped:[/dim] {path.name}")

    return None


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def run(directory: Path) -> None:
    if not directory.exists():
        console.print(f"[red]Directory not found:[/red] {directory}")
        sys.exit(1)
    if not directory.is_dir():
        console.print(f"[red]Not a directory:[/red] {directory}")
        sys.exit(1)

    already_deleted = load_already_deleted()
    to_delete: list = []

    console.clear()
    console.rule("[bold blue]File Review Utility[/bold blue]")
    console.print(f"  Directory : [cyan]{directory.resolve()}[/cyan]")
    try:
        total = sum(1 for _ in directory.iterdir())
    except PermissionError:
        total = "?"
    console.print(f"  Items     : [bold]{total}[/bold]")
    if already_deleted:
        console.print(f"  Skipping  : [yellow]{len(already_deleted)} already-marked paths[/yellow]")
    console.print()
    console.print("  Press Enter to begin...", end="")
    try:
        input()
    except (EOFError, KeyboardInterrupt):
        pass

    result = review_directory(directory, to_delete, already_deleted, breadcrumb="")

    if result == QUIT:
        console.print("\n[yellow]Quitting early. Saving progress...[/yellow]")

    # --- Summary ---
    console.clear()
    console.rule("[bold blue]Summary[/bold blue]")

    if to_delete:
        save_to_delete(to_delete, directory)

        table = Table(title="Newly Marked for Deletion", show_header=False, box=None, padding=(0, 2))
        table.add_column(style="red")
        for p in to_delete:
            table.add_row(str(p))
        console.print(table)
        console.print()
        console.print(f"[bold]→ Saved to:[/bold] [cyan]{OUTPUT_FILE.resolve()}[/cyan]")
        console.print()
        console.print("[dim]To delete everything in the list:[/dim]")
        console.print("[bold]  grep -v '^#' to_delete.txt | grep -v '^$' | xargs -I{} rm -rf \"{}\"[/bold]")
    else:
        console.print("[green]No new files marked for deletion.[/green]")


if __name__ == "__main__":
    target = Path(sys.argv[1]) if len(sys.argv) > 1 else Path.cwd()
    run(target)
