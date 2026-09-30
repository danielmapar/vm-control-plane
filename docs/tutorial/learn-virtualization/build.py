#!/usr/bin/env python3
"""Build the course from Markdown. No server or JavaScript framework required."""

import argparse
from collections import OrderedDict
from html import escape
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import shutil
from string import Template

import markdown

ROOT = Path(__file__).resolve().parent
REPOSITORY = "https://github.com/danielmapar/vm-control-plane"
SOURCE = "https://docs.google.com/document/d/1SnJOT6I0H6RRxl_sySRbxslaiZB3wq71rnAV1vkcHM0/edit?tab=t.9im8uerbhrjl"
PART_SUMMARIES = [
    "Build your first VM. Understand its lifecycle, network, storage, and identity.",
    "Protect a service, restore its data, observe failures, and maintain its hosts.",
    "Provision Windows guests and turn manual workflows into tested Go programs.",
    "Write a tiny VMM. Follow I/O through QEMU, VirtIO, and the hardware boundary.",
    "Make ownership, reconciliation, recovery, and console access work together.",
    "Build a PCI device, debug a KVM regression, and test a real Linux driver.",
]


class PlainText(HTMLParser):
    def __init__(self):
        super().__init__()
        self.parts = []

    def handle_data(self, data):
        self.parts.append(data)


def plain(html):
    parser = PlainText()
    parser.feed(html)
    return " ".join("".join(parser.parts).split())


def navigation(pages, current):
    groups = OrderedDict()
    for page in pages:
        groups.setdefault(page["group"], []).append(page)
    items = ['<a class="nav-home" href="index.html">← Course overview</a>']
    for group, members in groups.items():
        opened = " open" if any(p["slug"] == current for p in members) or group == "Start here" else ""
        label = group.replace(" — ", " · ")
        items.append(f'<details{opened}><summary>{escape(label)}</summary><ul>')
        for p in members:
            active = ' aria-current="page"' if p["slug"] == current else ""
            number = f'<span class="nav-number">{p["chapter"]:02}</span>' if "chapter" in p else ""
            items.append(f'<li><a href="{p["slug"]}.html"{active}>{number}<span>{escape(p["title"])}</span><span class="nav-done" data-done="{p["slug"]}" aria-hidden="true"></span></a></li>')
        items.append('</ul></details>')
    return '<nav id="chapter-nav" class="chapter-nav" aria-label="Chapters">' + "\n".join(items) + '</nav>'


def code_frame(language, code):
    label = {"bash": "Shell", "c": "C", "python": "Python", "yaml": "YAML"}.get(language, "Plain text")
    return f'<figure class="code-block"><figcaption><span>{label}</span><button class="copy-code" type="button">Copy code</button></figcaption><pre tabindex="0" aria-label="{label} code example"><code class="language-{language}">{escape(code)}</code></pre></figure>'


def render_content(source):
    # Keep literal tabs, quotes, and indentation in executable examples. Markdown's
    # whitespace normalizer otherwise expands tabs, including Makefile recipes.
    code_blocks = []

    def stash_code(match):
        code_blocks.append(code_frame(match[1] or "text", match[2]))
        return f'<div data-code-block="{len(code_blocks) - 1}"></div>'

    source = re.sub(r'^```([\w-]*)\n(.*?)\n```$', stash_code, source, flags=re.M | re.S)
    md = markdown.Markdown(extensions=["fenced_code", "tables", "toc", "sane_lists"], extension_configs={"toc": {"permalink": "#", "toc_depth": "2-3"}})
    # The template supplies the title; keep the source independently readable on GitHub.
    html = md.convert(source.split("\n", 1)[1])
    for i, block in enumerate(code_blocks):
        html = html.replace(f'<div data-code-block="{i}"></div>', block)
    html = html.replace('<table>', '<div class="table-scroll" tabindex="0" role="region" aria-label="Scrollable reference table"><table>').replace('</table>', '</table></div>')
    html = re.sub(r'<p>(<img [^>]+>)</p>', r'<figure class="diagram">\1</figure>', html)
    # Markdown lives in content/ on GitHub; generated pages live at the site root.
    html = html.replace('src="../assets/', 'src="assets/')
    html = html.replace('<img ', '<img loading="lazy" decoding="async" ')
    html = re.sub(r'<p>(<strong>(?:Check:|Pass and cleanup\.|Chapter pass:|Pass:).*?)</p>', r'<div class="checkpoint">\1</div>', html, flags=re.S)
    return html, md.toc


def search_sections(page, html):
    sections = re.split(r'<h[23] id="([^"]+)">(.*?)</h[23]>', html)
    records = [{"title": page["title"], "section": "Introduction", "url": page["slug"] + ".html", "text": plain(sections[0])}]
    for i in range(1, len(sections), 3):
        records.append({"title": page["title"], "section": plain(sections[i + 1]).removesuffix("#").strip(), "url": page["slug"] + ".html#" + sections[i], "text": plain(sections[i + 2])})
    return records


def footer():
    return f'<footer class="site-footer"><span>VM Control Plane <span aria-hidden="true">/</span> Learning lab</span><div><a href="validation.html">Validation record</a><a href="{SOURCE}">Original course ↗</a><a href="{REPOSITORY}">GitHub ↗</a></div></footer>'


def landing(pages):
    parts = OrderedDict()
    for p in pages:
        if "chapter" in p:
            parts.setdefault(p["group"], []).append(p)
    cards = []
    for i, (name, chapters) in enumerate(parts.items()):
        heading = name.split(" — ", 1)[1]
        links = ''.join(f'<li><a href="{p["slug"]}.html"><span class="lesson-number">{p["chapter"]:02}</span><span>{escape(p["title"])}</span><span class="lesson-arrow" aria-hidden="true">↗</span></a></li>' for p in chapters)
        cards.append(f'<section class="part-card"><div class="part-meta"><span>PART {i+1:02}</span><span>{len(chapters)} chapters</span></div><h3>{escape(heading)}</h3><p>{PART_SUMMARIES[i]}</p><ol>{links}</ol></section>')
    return '''<main id="main" class="landing-main">
<section class="hero">
  <div class="hero-copy"><p class="eyebrow"><span class="status-dot"></span> THE VIRTUALIZATION FIELD GUIDE</p>
    <h1>Your first VM.<br>Your own <em>control plane.</em></h1>
    <p class="hero-description">Learn how virtual machines work—from the first boot to the code that makes it possible. A practical course in Linux KVM, QEMU, and libvirt.</p>
    <div class="hero-actions"><a class="button primary" href="overview.html">Start learning <span aria-hidden="true">→</span></a><a class="button secondary" href="#curriculum">Explore the curriculum</a></div>
    <p class="hero-note">Build it. Break it. Understand it. Recover it.</p>
  </div>
  <div class="stack-illustration" aria-label="Virtualization stack: applications and virsh use libvirt, which manages QEMU, which uses Linux KVM for guest execution.">
    <div class="stack-caption"><span>THE STACK, ONE LAYER AT A TIME</span><span aria-hidden="true">↓</span></div>
    <div class="stack-layer"><span class="layer-index">01</span><div><strong>Your control plane</strong><span>Applications · virsh · automation</span></div><span class="layer-tag">MANAGE</span></div>
    <div class="stack-connector" aria-hidden="true"></div>
    <div class="stack-layer"><span class="layer-index">02</span><div><strong>libvirt</strong><span>VMs · networks · storage</span></div><span class="layer-tag">DEFINE</span></div>
    <div class="stack-connector" aria-hidden="true"></div>
    <div class="stack-layer"><span class="layer-index">03</span><div><strong>QEMU</strong><span>Machine model · devices · I/O</span></div><span class="layer-tag">MODEL</span></div>
    <div class="stack-connector" aria-hidden="true"></div>
    <div class="stack-layer kernel"><span class="layer-index">04</span><div><strong>Linux KVM</strong><span>Hardware-assisted guest execution</span></div><span class="layer-tag">EXECUTE</span></div>
    <div class="stack-bottom"><span class="status-dot"></span><code>/dev/kvm</code><span>Follow the request. Know the boundary.</span></div>
  </div>
</section>
<div class="course-facts"><div><strong>25</strong><span>hands-on chapters</span></div><div><strong>06</strong><span>progressive parts</span></div><div><strong>16</strong><span>original diagrams</span></div><div><strong>01</strong><span>rebuildable lab</span></div></div>
<section class="curriculum" id="curriculum"><div class="section-heading"><div><p class="eyebrow">THE LEARNING PATH</p><h2>From operator to engineer.</h2></div><p>One checkpoint at a time.<br>Keep the evidence. Understand the result.</p></div>
<div class="reading-progress" hidden><span>Your progress on this browser</span><strong data-progress-count></strong><a href="chapter-00.html" data-resume>Continue learning →</a></div>
<div class="curriculum-grid">''' + '\n'.join(cards) + '''</div></section>
<section class="lab-notes"><div><p class="eyebrow">BEFORE YOU BEGIN</p><h2>A real lab.<br>A clear baseline.</h2><p>The main path uses x86-64 Ubuntu 24.04 LTS and disposable guests. Start with Linux shell, SSH, and Git familiarity.</p><p>Using a Raspberry Pi? The course is not an ARM port. Its VM recipes need adaptation, and the advanced kernel exercises assume Intel VMX.</p><a class="text-link" href="overview.html#setup-2-arrange-hardware-when-you-need-it">Plan your lab →</a></div><div class="reference-links"><a href="preflight.html"><span>APPENDIX B</span><strong>Check your environment</strong><p>Host preflight and the first-VM checkpoint.</p><b aria-hidden="true">↗</b></a><a href="course-service.html"><span>APPENDIX A</span><strong>A workload worth recovering</strong><p>A small SQLite service for backup and migration labs.</p><b aria-hidden="true">↗</b></a><a href="resources.html"><span>REFERENCE LIBRARY</span><strong>Go deeper, when you need to</strong><p>Project documentation, books, source code, and talks.</p><b aria-hidden="true">↗</b></a></div></section>
<div class="validation-note"><span class="note-symbol" aria-hidden="true">i</span><p><strong>Evidence over assumptions.</strong> This course records which exercises were tested and which remain outstanding. The entire sequence has not been run end to end. <a href="validation.html">Read the validation record →</a></p></div>
</main>''' + footer()


def article(page, pages, rendered, toc):
    title = escape(page["title"])
    number = page.get("chapter")
    eyebrow = f'CHAPTER {number:02} / 24' if number is not None else page["group"].upper()
    complete = '<button class="button complete-button" aria-pressed="false">Mark chapter complete <span aria-hidden="true">✓</span></button>' if number is not None else ''
    ordered = sorted((p for p in pages if "chapter" in p), key=lambda p: p["chapter"])
    route = [next(p for p in pages if p["slug"] == "overview")] + ordered
    previous_next = ''
    if page in route:
        i = route.index(page)
        for label, offset in [('Previous', -1), ('Next', 1)]:
            if 0 <= i + offset < len(route):
                p = route[i + offset]
                previous_next += f'<a href="{p["slug"]}.html"><small>{label} chapter</small><strong>{escape(p["title"])} <span aria-hidden="true">{"←" if offset < 0 else "→"}</span></strong></a>'
    edit = REPOSITORY + '/blob/main/docs/tutorial/learn-virtualization/content/' + page['slug'] + '.md'
    return f'''<div class="reader-layout">{navigation(pages, page['slug'])}
<main id="main" class="reader-main"><div class="article-heading"><p class="eyebrow">{escape(eyebrow)}</p><h1>{title}</h1><div class="article-meta"><span>{escape(page['group'])}</span><a href="{edit}">Read on GitHub ↗</a></div></div>
<div class="mobile-toc"><details><summary>On this page</summary>{toc}</details></div>
<article class="prose">{rendered}</article><div class="completion-row">{complete}<a href="#main">Back to top ↑</a></div><nav class="page-turn" aria-label="Chapter navigation">{previous_next}</nav>
{footer()}</main><aside class="page-outline" aria-label="On this page"><p class="eyebrow">ON THIS PAGE</p>{toc}<div class="outline-note">Follow the checkpoint.<br>Verify before moving on.</div></aside></div>'''


def build(output):
    pages = json.loads((ROOT / "course.json").read_text())
    template = Template((ROOT / "template.html").read_text())
    output.mkdir(parents=True, exist_ok=True)
    shutil.copytree(ROOT / "assets", output / "assets", dirs_exist_ok=True)
    records = []
    for page in pages:
        source = (ROOT / 'content' / (page['slug'] + '.md')).read_text()
        rendered, toc = render_content(source)
        records.extend(search_sections(page, rendered))
        document = template.substitute(title=escape(page['title']), description=escape(plain(rendered)[:160], quote=True), body_class="reader", chapter=page['slug'] if 'chapter' in page else '', body=article(page, pages, rendered, toc))
        (output / (page['slug'] + '.html')).write_text(document)
    home = template.substitute(title="From your first VM to virtualization engineering", description="A hands-on course in Linux KVM, QEMU, and libvirt. 25 chapters covering VM operations, Go automation, control planes, devices, and kernel development.", body_class="home", chapter='', body=landing(pages))
    (output / "index.html").write_text(home)
    (output / "search-index.json").write_text(json.dumps(records, ensure_ascii=False, separators=(',', ':')))
    (output / ".nojekyll").touch()
    print(f"Built {len(pages) + 1} pages and {len(records)} searchable sections in {output}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "_site")
    args = parser.parse_args()
    build(args.output.resolve())
