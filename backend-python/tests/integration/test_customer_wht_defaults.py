"""Customer default withholding-tax settings (PPh 21 / PPh 23)."""
from __future__ import annotations


def test_create_customer_with_pph23_gross_up(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={
            "name": "Wht Co",
            "default_wht_type": "PPH23",
            "default_wht_gross_up": True,
        },
    )
    assert r.status_code == 201, r.text
    body = r.json()
    assert body["default_wht_type"] == "PPH23"
    assert body["default_wht_rate"] == "2.0000"
    assert body["default_wht_gross_up"] is True


def test_customer_defaults_to_none(client, admin_session):
    r = client.post("/api/v1/customers", json={"name": "Plain Co"})
    assert r.status_code == 201, r.text
    body = r.json()
    assert body["default_wht_type"] == "NONE"
    assert body["default_wht_rate"] is None
    assert body["default_wht_gross_up"] is False


def test_customer_wht_invalid_rate_422(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={"name": "Bad Rate", "default_wht_type": "PPH21", "default_wht_rate": 150},
    )
    assert r.status_code == 422


def test_customer_wht_unknown_type_422(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={"name": "Bad Type", "default_wht_type": "PPH99"},
    )
    assert r.status_code == 422


def test_customer_none_clears_rate(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={
            "name": "Clear Co",
            "default_wht_type": "PPH21",
            "default_wht_rate": 3,
            "default_wht_gross_up": True,
        },
    )
    assert r.status_code == 201, r.text
    cid = r.json()["customer_id"]
    assert r.json()["default_wht_rate"] == "3.0000"

    r = client.patch(f"/api/v1/customers/{cid}", json={"default_wht_type": "NONE"})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["default_wht_type"] == "NONE"
    assert body["default_wht_rate"] is None
    assert body["default_wht_gross_up"] is False


def test_patch_without_wht_fields_keeps_stored(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={"name": "Keep Co", "default_wht_type": "PPH23", "default_wht_gross_up": True},
    )
    cid = r.json()["customer_id"]
    r = client.patch(f"/api/v1/customers/{cid}", json={"notes": "hi"})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["default_wht_type"] == "PPH23"
    assert body["default_wht_rate"] == "2.0000"
    assert body["default_wht_gross_up"] is True


def test_patch_partial_wht_merges_with_stored(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={"name": "Merge Co", "default_wht_type": "PPH23", "default_wht_gross_up": True},
    )
    cid = r.json()["customer_id"]
    r = client.patch(f"/api/v1/customers/{cid}", json={"default_wht_rate": 4})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["default_wht_type"] == "PPH23"
    assert body["default_wht_rate"] == "4.0000"
    assert body["default_wht_gross_up"] is True

    r = client.patch(f"/api/v1/customers/{cid}", json={"default_wht_rate": 150})
    assert r.status_code == 422


def test_patch_type_switch_uses_new_default_rate(client, admin_session):
    r = client.post(
        "/api/v1/customers",
        json={"name": "Switch Co", "default_wht_type": "PPH23", "default_wht_gross_up": True},
    )
    cid = r.json()["customer_id"]
    assert r.json()["default_wht_rate"] == "2.0000"
    r = client.patch(f"/api/v1/customers/{cid}", json={"default_wht_type": "PPH21"})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["default_wht_type"] == "PPH21"
    assert body["default_wht_rate"] == "2.5000"
    assert body["default_wht_gross_up"] is True
