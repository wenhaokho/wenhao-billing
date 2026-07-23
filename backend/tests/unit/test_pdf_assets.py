from app.services.pdf.assets import font_face_css, default_logo_data_uri


def test_font_face_css_embeds_all_families():
    css = font_face_css()
    assert css.count("@font-face") == 3
    for family in ("Inter", "Manrope", "JetBrains Mono"):
        assert family in css
    assert "base64," in css
    assert "http://" not in css and "https://" not in css


def test_default_logo_is_data_uri():
    uri = default_logo_data_uri()
    assert uri.startswith("data:image/png;base64,")
    assert len(uri) > 100
