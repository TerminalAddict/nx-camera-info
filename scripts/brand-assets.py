#!/usr/bin/env python3
"""Render the shared SVG into checked-in desktop and installer assets.

Requires librsvg's rsvg-convert and ImageMagick 7 (magick).
Normal application builds use the rendered files and need neither tool.
"""
from pathlib import Path
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / 'branding/icon.svg'


def run(*args):
    subprocess.run([str(arg) for arg in args], check=True)


def render(source, target, width, height):
    run('rsvg-convert', '-w', width, '-h', height, source, '-o', target)


def layout(width, height, x, y, size, background, extra=''):
    icon = ET.fromstring(SOURCE.read_text())
    icon.set('x', str(x))
    icon.set('y', str(y))
    icon.set('width', str(size))
    icon.set('height', str(size))
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}">'
            f'<rect width="{width}" height="{height}" fill="{background}"/>'
            + ET.tostring(icon, encoding='unicode') + extra + '</svg>')


shutil.copyfile(SOURCE, ROOT / 'frontend/icon.svg')
render(SOURCE, ROOT / 'packaging/icon.png', 1024, 1024)
icons = ROOT / 'packaging/icons'
icons.mkdir(exist_ok=True)
for size in (16, 24, 32, 48, 64, 128, 256, 512):
    render(SOURCE, icons / f'{size}.png', size, size)
run('magick', *[icons / f'{size}.png' for size in (16, 24, 32, 48, 64, 128, 256)],
    ROOT / 'packaging/icon.ico')

with tempfile.TemporaryDirectory() as directory:
    directory = Path(directory)
    for name, width, height, artwork in (
        ('installer-header', 150, 57, layout(150, 57, 96, 2, 53, '#ffffff')),
        ('installer-sidebar', 164, 314, layout(164, 314, 20, 35, 124, '#102b56',
            '<text x="82" y="202" text-anchor="middle" font-family="DejaVu Sans" '
            'font-size="21" font-weight="bold" fill="#ffffff">NX Camera</text>'
            '<text x="82" y="230" text-anchor="middle" font-family="DejaVu Sans" '
            'font-size="21" font-weight="bold" fill="#ffffff">Info</text>'
            '<text x="82" y="279" text-anchor="middle" font-family="DejaVu Sans" '
            'font-size="10" fill="#75e1f5">CAMERA DATABASE EXPLORER</text>')),
    ):
        svg, png = directory / f'{name}.svg', directory / f'{name}.png'
        svg.write_text(artwork)
        render(svg, png, width * 3, height * 3)
        run('magick', png, '-resize', f'{width}x{height}', '-alpha', 'off',
            '-type', 'TrueColor', 'BMP3:' + str(ROOT / f'packaging/{name}.bmp'))

print('Rendered app icons, Windows ICO, and installer artwork from branding/icon.svg.')
