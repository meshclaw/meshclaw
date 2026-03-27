#!/usr/bin/env python3
"""
CLI wrapper that downloads and executes Go binaries.
"""

import os
import sys
import stat
import platform
import subprocess
from pathlib import Path

try:
    import requests
except ImportError:
    requests = None

VERSION = "1.0.0"
GITHUB_REPO = "meshclaw/meshclaw"
BINARIES = ["wire", "vssh", "mpop", "meshclaw", "meshdb", "vault"]


def get_bin_dir() -> Path:
    """Get the directory for storing binaries."""
    if os.name == "nt":
        base = Path(os.environ.get("LOCALAPPDATA", Path.home() / "AppData" / "Local"))
    else:
        base = Path(os.environ.get("XDG_DATA_HOME", Path.home() / ".local" / "share"))

    bin_dir = base / "meshclaw" / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    return bin_dir


def get_platform() -> str:
    """Get platform string for binary download."""
    system = platform.system().lower()
    machine = platform.machine().lower()

    if system == "darwin":
        os_name = "darwin"
    elif system == "linux":
        os_name = "linux"
    else:
        raise RuntimeError(f"Unsupported OS: {system}")

    if machine in ("x86_64", "amd64"):
        arch = "amd64"
    elif machine in ("arm64", "aarch64"):
        arch = "arm64"
    else:
        raise RuntimeError(f"Unsupported architecture: {machine}")

    return f"{os_name}_{arch}"


def download_binary(name: str, version: str = VERSION) -> Path:
    """Download a binary from GitHub releases."""
    if requests is None:
        raise RuntimeError("requests library required. Run: pip install requests")

    bin_dir = get_bin_dir()
    platform_str = get_platform()
    binary_name = f"{name}_{platform_str}"
    local_path = bin_dir / name

    # Check if already exists and is correct version
    version_file = bin_dir / f".{name}.version"
    if local_path.exists() and version_file.exists():
        if version_file.read_text().strip() == version:
            return local_path

    # Download from GitHub releases
    url = f"https://github.com/{GITHUB_REPO}/releases/download/v{version}/{binary_name}"
    print(f"Downloading {name} v{version}...", file=sys.stderr)

    try:
        resp = requests.get(url, stream=True, timeout=60)
        resp.raise_for_status()

        with open(local_path, "wb") as f:
            for chunk in resp.iter_content(chunk_size=8192):
                f.write(chunk)

        # Make executable
        local_path.chmod(local_path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

        # Save version
        version_file.write_text(version)

        print(f"Installed {name} to {local_path}", file=sys.stderr)
        return local_path

    except requests.RequestException as e:
        raise RuntimeError(f"Failed to download {name}: {e}")


def ensure_binary(name: str) -> Path:
    """Ensure binary is available, downloading if needed."""
    bin_dir = get_bin_dir()
    local_path = bin_dir / name

    if local_path.exists():
        return local_path

    return download_binary(name)


def run_binary(name: str, args: list) -> int:
    """Run a binary with the given arguments."""
    try:
        binary_path = ensure_binary(name)
        result = subprocess.run([str(binary_path)] + args)
        return result.returncode
    except KeyboardInterrupt:
        return 130
    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1


def main():
    """Entry point for meshclaw command."""
    sys.exit(run_binary("meshclaw", sys.argv[1:]))


def mpop():
    """Entry point for mpop command."""
    sys.exit(run_binary("mpop", sys.argv[1:]))


def wire():
    """Entry point for wire command."""
    sys.exit(run_binary("wire", sys.argv[1:]))


def vssh():
    """Entry point for vssh command."""
    sys.exit(run_binary("vssh", sys.argv[1:]))


def meshdb():
    """Entry point for meshdb command."""
    sys.exit(run_binary("meshdb", sys.argv[1:]))


def vault():
    """Entry point for vault command."""
    sys.exit(run_binary("vault", sys.argv[1:]))


def download_all():
    """Download all binaries."""
    print("Downloading all meshclaw binaries...")
    for name in BINARIES:
        try:
            download_binary(name)
        except Exception as e:
            print(f"Warning: Failed to download {name}: {e}", file=sys.stderr)
    print("Done!")


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--download-all":
        download_all()
    else:
        main()
