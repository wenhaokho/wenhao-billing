def test_replace_and_list_payment_accounts(admin_session):
    client = admin_session
    body = [
        {"currency": "IDR", "instructions": "OCBC NISP IDR", "is_default": True},
        {"currency": "SGD", "instructions": "DBS SGD", "is_default": False},
    ]
    r = client.put("/api/v1/business-profile/payment-accounts", json=body)
    assert r.status_code == 200, r.text
    got = r.json()
    assert [a["currency"] for a in got] == ["IDR", "SGD"]  # ordered by currency
    assert [a["is_default"] for a in got] == [True, False]

    r2 = client.get("/api/v1/business-profile/payment-accounts")
    assert r2.status_code == 200
    assert {a["currency"] for a in r2.json()} == {"IDR", "SGD"}


def test_replace_is_full_overwrite(admin_session):
    client = admin_session
    client.put("/api/v1/business-profile/payment-accounts",
               json=[{"currency": "USD", "instructions": "Wells", "is_default": True}])
    client.put("/api/v1/business-profile/payment-accounts",
               json=[{"currency": "SGD", "instructions": "DBS", "is_default": True}])
    r = client.get("/api/v1/business-profile/payment-accounts")
    assert {a["currency"] for a in r.json()} == {"SGD"}  # USD gone


def test_reject_duplicate_currency(admin_session):
    r = admin_session.put(
        "/api/v1/business-profile/payment-accounts",
        json=[
            {"currency": "SGD", "instructions": "DBS", "is_default": False},
            {"currency": "SGD", "instructions": "UOB", "is_default": True},
        ],
    )
    assert r.status_code == 422


def test_reject_two_defaults(admin_session):
    r = admin_session.put(
        "/api/v1/business-profile/payment-accounts",
        json=[
            {"currency": "SGD", "instructions": "DBS", "is_default": True},
            {"currency": "USD", "instructions": "Wells", "is_default": True},
        ],
    )
    assert r.status_code == 422


def test_reject_unsupported_currency(admin_session):
    r = admin_session.put(
        "/api/v1/business-profile/payment-accounts",
        json=[{"currency": "EUR", "instructions": "SEPA", "is_default": True}],
    )
    assert r.status_code == 422
