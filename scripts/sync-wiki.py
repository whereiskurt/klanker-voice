#!/usr/bin/env python3
"""Sync the GitHub wiki from the in-repo docs/ tree.

The repo is the canonical source; the wiki is a generated mirror:

  - docs/wiki/{Home,_Sidebar,_Footer}.md are copied verbatim
  - the deep-dive pages below are copied from docs/ with links rewritten
    (doc-to-doc links become wiki page links, everything else becomes an
    absolute github.com/blob link)

One-time prerequisite: the wiki git repo does not exist until the first
page is created in the GitHub UI (Wiki tab -> "Create the first page" —
any placeholder content, it will be overwritten). After that:

  python3 scripts/sync-wiki.py            # clone, rebuild, push
  python3 scripts/sync-wiki.py --dry-run  # rebuild into a temp dir, no push
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile

REPO_SLUG = "whereiskurt/klanker-voice"
WIKI_URL = f"https://github.com/{REPO_SLUG}.wiki.git"
BLOB = f"https://github.com/{REPO_SLUG}/blob/main"
TREE = f"https://github.com/{REPO_SLUG}/tree/main"

# repo path -> wiki page name
PAGE_MAP = {
    "docs/architecture/overview.md": "Architecture",
    "docs/dataflows/browser-webrtc.md": "Data-Flow-Browser-WebRTC",
    "docs/dataflows/telephony-voipms.md": "Data-Flow-Telephony-VoIPms",
    "docs/dataflows/conversation-loop.md": "Data-Flow-Conversation-Loop",
    "docs/dataflows/auth-quota.md": "Auth-and-Quotas",
    "docs/dataflows/knowledge-retrieval.md": "Knowledge-and-Retrieval",
    "docs/techniques/highlights.md": "Techniques",
    "docs/guides/getting-started.md": "Getting-Started",
    "docs/guides/development.md": "Development",
    "docs/guides/testing.md": "Testing",
    "docs/guides/configuration.md": "Configuration",
    "docs/guides/deployment.md": "Deployment",
}

VERBATIM = ["docs/wiki/Home.md", "docs/wiki/_Sidebar.md", "docs/wiki/_Footer.md"]

LINK_RE = re.compile(r"\[([^\]]*)\]\(([^)\s]*)\)")


def repo_root() -> str:
    out = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=True
    )
    return out.stdout.strip()


def rewrite_links(root: str, src_repo_path: str, text: str) -> str:
    base = os.path.dirname(src_repo_path)

    def sub(m: "re.Match[str]") -> str:
        label, target = m.group(1), m.group(2)
        anchor = ""
        if "#" in target:
            target, anchor = target.split("#", 1)
            anchor = "#" + anchor
        if not target or target.startswith(("http://", "https://", "mailto:")):
            return m.group(0)
        norm = os.path.normpath(os.path.join(base, target))
        if norm in PAGE_MAP:
            return f"[{label}]({PAGE_MAP[norm]}{anchor})"
        if os.path.isdir(os.path.join(root, norm)):
            return f"[{label}]({TREE}/{norm})"
        return f"[{label}]({BLOB}/{norm}{anchor})"

    return LINK_RE.sub(sub, text)


def build(root: str, out_dir: str) -> None:
    for src in VERBATIM:
        shutil.copy(os.path.join(root, src), os.path.join(out_dir, os.path.basename(src)))
    for src, page in PAGE_MAP.items():
        with open(os.path.join(root, src)) as fh:
            text = fh.read()
        text = re.sub(r"^<!-- generated-by:.*?-->\n", "", text)
        text = rewrite_links(root, src, text)
        footer = (
            f"\n\n---\n\n_Canonical source: [`{src}`]({BLOB}/{src}) — "
            "edit there and run `scripts/sync-wiki.py`._\n"
        )
        with open(os.path.join(out_dir, page + ".md"), "w") as fh:
            fh.write(text.rstrip() + footer)
        print(f"  {page}.md  <-  {src}")


def main() -> int:
    dry_run = "--dry-run" in sys.argv
    root = repo_root()
    tmp = tempfile.mkdtemp(prefix="kv-wiki-")
    try:
        if dry_run:
            build(root, tmp)
            print(f"\nDry run: wiki built in {tmp} (kept for inspection, not pushed)")
            return 0
        clone = subprocess.run(["git", "clone", "--depth", "1", WIKI_URL, tmp + "/wiki"])
        if clone.returncode != 0:
            print(
                "\nCould not clone the wiki repo. If this is the first sync, create the\n"
                f"first page in the GitHub UI (https://github.com/{REPO_SLUG}/wiki) and re-run.",
                file=sys.stderr,
            )
            return 1
        wiki = tmp + "/wiki"
        for name in os.listdir(wiki):
            if name.endswith(".md"):
                os.unlink(os.path.join(wiki, name))
        build(root, wiki)
        subprocess.run(["git", "-C", wiki, "add", "-A"], check=True)
        status = subprocess.run(
            ["git", "-C", wiki, "status", "--porcelain"], capture_output=True, text=True, check=True
        )
        if not status.stdout.strip():
            print("Wiki already up to date.")
            return 0
        head = subprocess.run(
            ["git", "-C", root, "rev-parse", "--short", "HEAD"],
            capture_output=True, text=True, check=True,
        ).stdout.strip()
        subprocess.run(
            ["git", "-C", wiki, "commit", "-m", f"sync from {REPO_SLUG}@{head} docs/"],
            check=True,
        )
        subprocess.run(["git", "-C", wiki, "push"], check=True)
        print("Wiki pushed.")
        return 0
    finally:
        if not dry_run:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
