#!/usr/bin/env python3
"""Check generated pages, local links, search targets, and verbatim code examples."""

import argparse
from html.parser import HTMLParser
import json
from pathlib import Path
import re
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent


class Page(HTMLParser):
    def __init__(self, source):
        super().__init__()
        self.ids = set()
        self.links = []
        self.code = []
        self.in_pre = False
        self.h1_count = 0
        self.feed(source)

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if 'id' in attrs:
            assert attrs['id'] not in self.ids, f'Duplicate id: {attrs["id"]}'
            self.ids.add(attrs['id'])
        if tag == 'h1':
            self.h1_count += 1
        if tag == 'img':
            assert attrs.get('alt'), 'Diagram missing alternative text'
        for attribute in ('href', 'src'):
            if attrs.get(attribute):
                self.links.append(attrs[attribute])
        if tag == 'pre':
            self.in_pre = True
            self.code.append('')

    def handle_endtag(self, tag):
        if tag == 'pre':
            self.in_pre = False

    def handle_data(self, data):
        if self.in_pre:
            self.code[-1] += data


def check(directory):
    pages = {path.name: Page(path.read_text()) for path in directory.glob('*.html')}
    assert pages, 'Build the site before checking it'

    def link_exists(current, url):
        parsed = urlsplit(url)
        if parsed.scheme or parsed.netloc:
            return
        name = unquote(parsed.path) or current
        target = directory / name
        assert target.is_file(), f'{current}: missing target {url}'
        if parsed.fragment and target.suffix == '.html':
            assert unquote(parsed.fragment) in pages[target.name].ids, f'{current}: missing anchor {url}'

    links = 0
    for name, page in pages.items():
        assert page.h1_count == 1, f'{name}: expected one page title'
        for url in page.links:
            link_exists(name, url)
            links += 1
    for result in json.loads((directory / 'search-index.json').read_text()):
        link_exists('index.html', result['url'])
    blocks = 0
    for page in json.loads((ROOT / 'course.json').read_text()):
        slug = page['slug']
        source = (ROOT / 'content' / f'{slug}.md').read_text()
        for image in re.findall(r'!\[[^\]]*\]\(([^)]+)\)', source):
            assert (ROOT / 'content' / image).is_file(), f'{slug}: broken Markdown image {image}'
        expected = re.findall(r'^```[\w-]*\n(.*?)\n```$', source, flags=re.M | re.S)
        assert expected == pages[f'{slug}.html'].code, f'{slug}: code changed during rendering'
        blocks += len(expected)
    print(f'Passed: {len(pages)} pages, {links} links/assets, all search anchors, {blocks} verbatim code blocks')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', nargs='?', type=Path, default=ROOT / '_site')
    check(parser.parse_args().directory.resolve())
