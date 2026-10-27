#!/usr/bin/env python3
"""Collect licenses for Go modules linked by the desktop build on any target."""
import json
import os
from pathlib import Path
import subprocess

modules = {}
for target in ('linux', 'windows', 'darwin'):
    env = dict(os.environ, GOOS=target, GOARCH='amd64', CGO_ENABLED='1')
    result = subprocess.check_output(['go', 'list', '-tags', 'gui', '-deps', '-f', '{{if .Module}}{{.Module.Path}} {{.Module.Version}} {{.Module.Dir}}{{end}}', '.'], env=env, text=True)
    for line in result.splitlines():
        fields = line.split(' ', 2)
        if len(fields) == 3 and fields[1]:
            modules[fields[0]] = (fields[1], fields[2])

parts = ['Third-party notices for NX Camera Info GUI\n\nAlso retain THIRD_PARTY_NOTICES.txt, which includes Go, SQLite and other bundled dependency notices. GTK, WebKit and WebView2 are supplied under their respective system/runtime licenses.\n']
for name, (version, directory) in sorted(modules.items()):
    root = Path(directory)
    licenses = sorted(p for p in root.iterdir() if p.is_file() and p.name.lower().startswith(('license', 'copying', 'notice')))
    if not licenses:
        raise RuntimeError(f'No license found for {name}')
    parts.append(f'\n{name} {version}\n')
    for license_file in licenses:
        parts.append(f'\n{license_file.name}\n{license_file.read_text(errors="replace")}\n')
Path('GUI_THIRD_PARTY_NOTICES.txt').write_text(''.join(parts).rstrip() + '\n')
print(f'Collected notices for {len(modules)} desktop dependencies.')
