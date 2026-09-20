#!/usr/bin/env python3
"""Build reproducible docs from repository sources and check the rendered site."""
from __future__ import annotations

import argparse
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
from urllib.parse import unquote, urlsplit

import yaml

ROOT = Path(__file__).resolve().parents[1]
STAGE = ROOT / '.cache/docs-src'
SITE = ROOT / 'site'
REPO = 'https://github.com/nimeshbuilds/replicove'
SITE_URL = 'https://nimeshbuilds.github.io/replicove/'
ROOT_PAGES = {
    'quickstart.md': 'QUICKSTART.md', 'security.md': 'SECURITY.md',
    'contributing.md': 'CONTRIBUTING.md', 'roadmap.md': 'ROADMAP.md',
    'support.md': 'SUPPORT.md',
}
GENERATED = {'reference/api.md', 'reference/cli.md'}


def nav_pages(value):
    if isinstance(value, str):
        if not urlsplit(value).scheme:
            yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from nav_pages(item)
    elif isinstance(value, list):
        for item in value:
            yield from nav_pages(item)


def prepare():
    config = yaml.load((ROOT / 'mkdocs.yml').read_text(), Loader=yaml.BaseLoader)
    pages = list(nav_pages(config['nav']))
    mapping = {(ROOT / ROOT_PAGES.get(p, 'docs/' + p)).resolve(): p for p in pages}
    mapping[ROOT / 'QUICKSTART.md'] = 'quickstart.md'
    mapping[ROOT / 'README.md'] = 'index.md'
    mapping[ROOT / 'docs/README.md'] = 'index.md'
    if STAGE.exists():
        shutil.rmtree(STAGE)
    STAGE.mkdir(parents=True)
    shutil.copytree(ROOT / 'assets/brand', STAGE / 'assets/brand')
    shutil.copytree(ROOT / 'docs/stylesheets', STAGE / 'stylesheets')
    # Publish fixture downloads without credentials or private build artifacts.
    shutil.copytree(ROOT / 'examples/yaml', STAGE / 'examples/yaml')

    def rewrite_link(target, source, destination):
        parts = urlsplit(target)
        if parts.scheme or parts.netloc or not parts.path:
            return target
        candidate = (source.parent / unquote(parts.path)).resolve()
        if not candidate.is_relative_to(ROOT):
            raise ValueError(f'{source.relative_to(ROOT)}: link leaves repository: {target}')
        if candidate in mapping:
            path = os.path.relpath(mapping[candidate], Path(destination).parent).replace(os.sep, '/')
        elif candidate.is_relative_to(ROOT / 'assets/brand'):
            path = os.path.relpath(candidate.relative_to(ROOT), Path(destination).parent).replace(os.sep, '/')
        elif candidate.exists():
            route = 'tree' if candidate.is_dir() else 'blob'
            path = f'{REPO}/{route}/main/{candidate.relative_to(ROOT).as_posix()}'
        else:
            raise ValueError(f'{source.relative_to(ROOT)}: missing link target: {target}')
        return path + ('?' + parts.query if parts.query else '') + ('#' + parts.fragment if parts.fragment else '')

    for page in pages:
        if page in GENERATED:
            continue
        source = ROOT / ROOT_PAGES.get(page, 'docs/' + page)
        content = source.read_text()
        # Repository docs use ordinary Markdown inline links and HTML image srcs.
        content = re.sub(r'(\]\()([^\s)]+)(\))', lambda m: m[1] + rewrite_link(m[2], source, page) + m[3], content)
        content = re.sub(r'(src=")([^"]+)(")', lambda m: m[1] + rewrite_link(m[2], source, page) + m[3], content)
        target = STAGE / page
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
    generate_api()
    generate_cli()
    (STAGE / 'robots.txt').write_text('User-agent: *\nAllow: /\nSitemap: ' + SITE_URL + 'sitemap.xml\n')
    print(f'Prepared {len(pages)} published pages; API and CLI references generated from implementation.')


def cell(text):
    # Encode punctuation so table pipes and regex character classes remain
    # literal code, rather than being parsed as Markdown links or columns.
    def literal(match):
        value = ''.join(char if char.isalnum() or char == ' ' else f'&#{ord(char)};' for char in match[1])
        return '<code>' + value + '</code>'
    text = re.sub(r'`([^`]+)`', literal, str(text))
    return text.replace('|', '&#124;').replace('\n', ' ').strip()


def rows(schema, prefix='', required=False):
    kind = schema.get('type', 'any')
    if schema.get('x-kubernetes-preserve-unknown-fields'):
        kind += ' (arbitrary JSON)'
    constraints = []
    for key in ['enum', 'minimum', 'maximum', 'minItems', 'maxItems', 'minLength', 'maxLength', 'pattern', 'format']:
        if key in schema:
            constraints.append(f'{key}: `{json.dumps(schema[key], ensure_ascii=False)}`')
    for rule in schema.get('x-kubernetes-validations', []):
        constraints.append(rule.get('message', '') + ' (`' + rule['rule'] + '`)')
    if prefix:
        default = '`' + json.dumps(schema['default']) + '`' if 'default' in schema else '—'
        description = schema.get('description', '')
        details = ' '.join(filter(None, [description, '; '.join(constraints)])) or 'See the feature guides for semantic behavior.'
        yield f'| `{prefix}` | {kind} | {"Yes" if required else "No"} | {default} | {cell(details)} |'
    for name, child in schema.get('properties', {}).items():
        yield from rows(child, prefix + '.' + name if prefix else name, name in schema.get('required', []))
    if 'items' in schema:
        yield from rows(schema['items'], prefix + '[]')
    additional = schema.get('additionalProperties')
    if isinstance(additional, dict):
        yield from rows(additional, prefix + '.{key}')


def generate_api():
    lines = ['# Kubernetes API reference', '',
             'Generated at build time from the checked-in CRD OpenAPI schemas. Do not edit this page directly. '
             'API group: `replica.nimeshbuilds.dev/v1alpha1`.', '',
             'Use `kubectl explain clusterreplica.spec --recursive`, `kubectl explain replicagrant.spec --recursive`, '
             'or `kubectl explain replicaaccess.spec --recursive` against an installed cluster. '
             'Required means required within its containing object, not that an optional parent must exist.', '',
             'The schema is only one validation layer. Read [grants](../guides/grants.md), '
             '[selection](../guides/selection.md), [access](../guides/access.md), and '
             '[compatibility](compatibility.md) for controller-enforced policy and lifecycle rules.', '']
    count = 0
    for path in sorted((ROOT / 'config/crd').glob('*.yaml')):
        crd = yaml.safe_load(path.read_text())
        spec = crd['spec']
        lines += ['## ' + spec['names']['kind'], '',
                  f'**Scope:** {spec["scope"]}. **Plural:** `{spec["names"]["plural"]}`. '
                  f'[CRD source]({REPO}/blob/main/config/crd/{path.name}).', '']
        for version in spec['versions']:
            if not version['served']:
                continue
            root = version['schema']['openAPIV3Schema']
            lines += ['Version: `' + version['name'] + '`. Standard Kubernetes `apiVersion`, `kind`, and `metadata` accompany the fields below.', '']
            for section in ('spec', 'status'):
                if section not in root['properties']:
                    continue
                lines += ['### ' + spec['names']['kind'] + ' ' + section, '',
                          '| Field | Type | Required | Default | Description and validation |',
                          '| --- | --- | --- | --- | --- |']
                fields = list(rows(root['properties'][section], section, section in root.get('required', [])))
                count += len(fields)
                lines += fields + ['']
    lines += ['## Operation annotations', '',
              '| Annotation on ClusterReplica | Value | Effect |', '| --- | --- | --- |',
              '| `replicove.nimeshbuilds.dev/approved-plan` | Exact current `status.plan.revision` | Approve a manually gated plan |',
              '| `replicove.nimeshbuilds.dev/refresh` | New unique token | Recapture source state using the immutable request spec |', '',
              'See [plans and refresh](../guides/lifecycle.md). These annotations do not extend TTL or expand administrator grants.', '']
    target = STAGE / 'reference/api.md'
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text('\n'.join(lines))
    print(f'API reference: {count} schema fields/structures.')


def generate_cli():
    commands = ['', 'install', 'create', 'plan', 'approve', 'status', 'refresh', 'access', 'connect', 'delete',
                'completion', 'completion bash', 'completion fish', 'completion powershell', 'completion zsh']
    lines = ['# CLI reference', '',
             'Generated from the built `bin/replicove` help output. The alpha is built from source; '
             '[start with the CLI walkthrough](../quickstart.md) or use the [YAML API](../getting-started/yaml.md).', '',
             'Global flags select the **host** kubeconfig, context, and granted destination namespace. '
             'The default destination is `replica-lab`. `plan` and `status` print sanitized status, '
             'not secret payloads or a full rendered diff. `install` is an initial Helm installation; '
             'follow the [upgrade guide](../getting-started/installation.md#upgrades-and-removal) for an existing installation.', '',
             'The credential commands refuse to overwrite an existing output file. `access` uses an existing '
             'network route; `connect` manages a loopback tunnel for an owned runtime. See [access](../guides/access.md).', '']
    binary = ROOT / 'bin/replicove'
    for command in commands:
        result = subprocess.run([str(binary), *command.split(), '--help'], check=True, capture_output=True, text=True)
        lines += ['## replicove' + (' ' + command if command else ''), '', '```text', result.stdout.rstrip(), '```', '']
    (STAGE / 'reference/cli.md').write_text('\n'.join(lines))


class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.anchors = set()
        self.links = []
        self.canonical = None

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == 'link' and values.get('rel') == 'canonical':
            self.canonical = values.get('href')
        if 'id' in values:
            self.anchors.add(values['id'])
        if tag == 'a' and 'name' in values:
            self.anchors.add(values['name'])
        # Canonical/social URLs are checked separately; inspect navigable links/assets.
        for attr in (['href'] if tag in ('a', 'link') else ['src'] if tag in ('script', 'img') else []):
            if values.get(attr):
                self.links.append(values[attr])


def check():
    documents = {}
    for path in SITE.rglob('*.html'):
        doc = Links()
        doc.feed(path.read_text())
        documents[path.resolve()] = doc
    errors = []
    checked = 0
    for source, doc in documents.items():
        for target in doc.links:
            parts = urlsplit(target)
            if parts.scheme or parts.netloc:
                continue
            path = unquote(parts.path)
            if path.startswith('/replicove/'):
                resolved = SITE / path[len('/replicove/'):]
            elif path.startswith('/'):
                errors.append(f'{source.relative_to(SITE)}: URL escapes Pages prefix: {target}')
                continue
            else:
                resolved = source.parent / path if path else source
            resolved = resolved.resolve()
            if resolved.is_dir():
                resolved /= 'index.html'
            checked += 1
            if not resolved.is_relative_to(SITE.resolve()) or not resolved.exists():
                errors.append(f'{source.relative_to(SITE)}: missing {target}')
            elif parts.fragment and resolved in documents and unquote(parts.fragment) not in documents[resolved].anchors:
                # MkDocs Material injects the search control at runtime on some layouts.
                if not parts.fragment.startswith('__'):
                    errors.append(f'{source.relative_to(SITE)}: missing anchor {target}')
    for required in ['index.html', 'getting-started/yaml/index.html', 'reference/api/index.html',
                     'search/search_index.json', 'sitemap.xml', 'robots.txt', 'assets/brand/replicove-social.png']:
        if not (SITE / required).is_file():
            errors.append('Required site output missing: ' + required)
    if documents[(SITE / 'index.html').resolve()].canonical != SITE_URL:
        errors.append('Homepage canonical URL does not match the GitHub Pages prefix')
    if errors:
        raise SystemExit('\n'.join(errors))
    print(f'Checked {checked} local links/assets across {len(documents)} HTML pages; all targets and anchors resolve.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('command', choices=['prepare', 'check'])
    args = parser.parse_args()
    prepare() if args.command == 'prepare' else check()
