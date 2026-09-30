# Learn virtualization

[Open the course website](https://danielmapar.github.io/vm-control-plane/tutorial/learn-virtualization/)

The website edition of **/dev/kvm: from your first VM to virtualization
engineering**: 25 chapters, 16 diagrams, and the original code examples,
references, and validation notes. It includes search, chapter navigation,
copy buttons, light/dark themes, and progress saved in your browser.

The course uses **x86-64 Ubuntu 24.04**. Its commands are not an ARM/Raspberry Pi
port; the advanced kernel labs assume Intel VMX. Start with the
[lab preparation guide](content/overview.md).

## Preview locally

From this directory:

```bash
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
.venv/bin/python build.py
.venv/bin/python check.py
python3 -m http.server 8000 --bind 127.0.0.1 --directory _site
```

Open <http://127.0.0.1:8000>. Rebuild and refresh after editing.

## Edit the course

- `content/`: one Markdown file per chapter or reference page, readable on GitHub.
- `course.json`: page titles, order, and curriculum groups.
- `assets/`: styles, browser enhancements, and the original diagrams.
- `template.html`: shared page layout.
- `build.py`: converts Markdown to a static website; Python-Markdown is its only dependency.
- `check.py`: checks local links, anchors, and exact code preservation.

Keep code inside fenced blocks. The builder preserves literal tabs, spaces,
quotes, and backslashes. Keep the source's validation limits beside its claims.
No example is executed by the website. Only mark a chapter complete after doing
its checks; the browser progress indicator is a personal checklist.

## GitHub Pages

[The publishing workflow](../../../.github/workflows/learning-site.yml) builds and
checks the site on pushes to `main`, then publishes only its generated output.
Pull requests run the checks without deploying. In repository **Settings → Pages**,
the publishing source must be **GitHub Actions**. See
[GitHub's Pages guide](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

## Source

Converted on 30 September 2026 from the **Linux KVM + QEMU + libvirt** tab of
[the original course](https://docs.google.com/document/d/1SnJOT6I0H6RRxl_sySRbxslaiZB3wq71rnAV1vkcHM0/edit?tab=t.9im8uerbhrjl).
The website is a maintained copy, not a live Google Docs embed. Other tabs and
personal lab inventory are not part of this site. Original access permissions
still apply to the Google Doc.
