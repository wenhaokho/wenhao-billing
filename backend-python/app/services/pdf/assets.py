"""Load and cache bundled PDF assets (fonts, default logo) as data URIs.

Everything is embedded so the rendered HTML is fully self-contained — the
renderer never needs network or filesystem access.
"""
from __future__ import annotations

from base64 import b64encode
from functools import lru_cache
from pathlib import Path

_ASSETS = Path(__file__).parent / "assets"
_FONTS = _ASSETS / "fonts"

# family name -> vendored variable woff2 file (weight axis 100..900)
_FONT_FILES = {
    "Inter": "inter.woff2",
    "Manrope": "manrope.woff2",
    "JetBrains Mono": "jetbrains-mono.woff2",
}


@lru_cache(maxsize=1)
def font_face_css() -> str:
    rules = []
    for family, fname in _FONT_FILES.items():
        b64 = b64encode((_FONTS / fname).read_bytes()).decode("ascii")
        rules.append(
            # No font-weight range descriptor (WeasyPrint rejects it); Pango
            # still drives the variable font's weight axis per element.
            f"@font-face{{font-family:'{family}';font-style:normal;"
            f"src:url(data:font/woff2;base64,{b64}) format('woff2');}}"
        )
    return "\n".join(rules)


@lru_cache(maxsize=1)
def default_logo_data_uri() -> str:
    b64 = b64encode((_ASSETS / "default-logo.png").read_bytes()).decode("ascii")
    return "data:image/png;base64," + b64
