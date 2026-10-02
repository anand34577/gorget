#!/usr/bin/env python3
"""Turns docs/ into GitHub wiki pages.

Usage: docs-to-wiki.py OUT_DIR

Page names come from the titles in mkdocs.yml, so the wiki matches the docs site.
Links between pages are rewritten to wiki page names, links to other files in the
repository point at them on GitHub, and the sidebar follows the mkdocs navigation.
docs/ stays the source of truth: the wiki is regenerated from it and never edited by hand.
"""
import os
import posixpath
import re
import sys

import yaml

REPO = "https://github.com/anand34577/gorget"
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# Pages outside docs/ that also belong in the wiki: (repository path, title, nav section).
EXTRA = [
    ("SECURITY.md", "Reporting a vulnerability", "Security"),
    ("CHANGELOG.md", "Changelog", "Project"),
]

LINK = re.compile(r"(!?\[[^\]]*\]\()([^)\s]+)(\))")
ADMONITION = re.compile(r'^!!! (\w+)(?: "([^"]*)")?\s*$')


def slug(title):
    return re.sub(r"[^A-Za-z0-9]+", "-", title).strip("-")


def walk(nav, section=None):
    """Yields (title, docs-relative path, section) for every page in the mkdocs nav."""
    for item in nav:
        for title, value in item.items():
            if isinstance(value, list):
                yield from walk(value, title)
            else:
                yield title, value, section


def admonitions(text):
    """mkdocs '!!! note "Title"' blocks become blockquotes, which GitHub renders."""
    out, lines, i = [], text.split("\n"), 0
    while i < len(lines):
        m = ADMONITION.match(lines[i])
        if not m:
            out.append(lines[i])
            i += 1
            continue
        out.append("> **%s**" % (m.group(2) or m.group(1).capitalize()))
        out.append(">")
        i += 1
        while i < len(lines) and (lines[i].startswith("    ") or lines[i].strip() == ""):
            if lines[i].strip() == "" and (i + 1 >= len(lines) or not lines[i + 1].startswith("    ")):
                break
            out.append("> " + lines[i][4:] if lines[i].strip() else ">")
            i += 1
    return "\n".join(out)


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: docs-to-wiki.py OUT_DIR")
    out_dir = sys.argv[1]
    with open(os.path.join(ROOT, "mkdocs.yml"), encoding="utf-8") as f:
        mk = yaml.safe_load(f)

    pages = []  # (repository path, page name, title, section)
    for title, path, section in walk(mk["nav"]):
        name = "Home" if path == "index.md" else slug(title)
        pages.append(("docs/" + path, name, title, section))
    for path, title, section in EXTRA:
        pages.append((path, slug(title), title, section))
    order = list(dict.fromkeys(p[3] for p in pages))
    pages.sort(key=lambda p: order.index(p[3]))  # stable: extra pages join the end of their section
    by_path = {p[0]: p[1] for p in pages}

    listed = {p[0] for p in pages}
    for dirpath, _, files in os.walk(os.path.join(ROOT, "docs")):
        for fn in files:
            rel = posixpath.relpath(os.path.join(dirpath, fn).replace(os.sep, "/"), ROOT.replace(os.sep, "/"))
            if fn.endswith(".md") and rel not in listed:
                sys.exit("%s is not in the mkdocs.yml nav; add it so it reaches the docs site and the wiki" % rel)

    def rewrite(src):
        base = posixpath.dirname(src)

        def fix(m):
            target = m.group(2)
            if re.match(r"^([a-z][a-z0-9+.-]*:|#|/)", target, re.I):
                return m.group(0)
            path, _, anchor = target.partition("#")
            resolved = posixpath.normpath(posixpath.join(base, path))
            if resolved in by_path:
                new = by_path[resolved] + ("#" + anchor if anchor else "")
            elif os.path.exists(os.path.join(ROOT, resolved)):
                kind = "tree" if os.path.isdir(os.path.join(ROOT, resolved)) else "blob"
                new = "%s/%s/main/%s%s" % (REPO, kind, resolved, "#" + anchor if anchor else "")
            else:
                sys.exit("%s: broken link %s" % (src, target))
            return m.group(1) + new + m.group(3)

        return fix

    os.makedirs(out_dir, exist_ok=True)
    footer = "\n\n---\n_This page is generated from [`%%s`](%s/blob/main/%%s). Edit that file, not the wiki._\n" % REPO
    for src, name, _, _ in pages:
        with open(os.path.join(ROOT, src), encoding="utf-8") as f:
            text = f.read()
        text = LINK.sub(rewrite(src), admonitions(text)).rstrip("\n")
        with open(os.path.join(out_dir, name + ".md"), "w", encoding="utf-8", newline="\n") as f:
            f.write(text + footer % (src, src))

    sidebar, section = ["**[Home](Home)**", ""], None
    for _, name, title, sec in pages:
        if name == "Home":
            continue
        if sec != section:
            sidebar += ["", "**%s**" % sec, ""]
            section = sec
        sidebar.append("- [%s](%s)" % (title, name))
    with open(os.path.join(out_dir, "_Sidebar.md"), "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(sidebar).replace("\n\n\n", "\n\n") + "\n")
    with open(os.path.join(out_dir, "_Footer.md"), "w", encoding="utf-8", newline="\n") as f:
        f.write("Gorget is licensed under AGPL-3.0. Source: %s\n" % REPO)


if __name__ == "__main__":
    main()
