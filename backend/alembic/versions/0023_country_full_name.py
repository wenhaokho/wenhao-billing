"""store full country names on customers

Revision ID: 0023_country_full_name
Revises: 0022_payment_accounts
Create Date: 2026-07-24

Widens ``billing_country`` / ``shipping_country`` from a 2-char ISO code to
a 100-char full country name so the value renders directly on invoices and
quotations. Existing 2-char codes are best-effort mapped to their full name;
any unrecognised value is left untouched (still valid in the wider column).
"""
from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = "0023_country_full_name"
down_revision = "0022_payment_accounts"
branch_labels = None
depends_on = None

# ISO 3166-1 alpha-2 -> full name (mirrors frontend src/constants/countries.ts).
_CODE_TO_NAME = {
    "AF": "Afghanistan", "AL": "Albania", "DZ": "Algeria", "AD": "Andorra",
    "AO": "Angola", "AG": "Antigua and Barbuda", "AR": "Argentina",
    "AM": "Armenia", "AU": "Australia", "AT": "Austria", "AZ": "Azerbaijan",
    "BS": "Bahamas", "BH": "Bahrain", "BD": "Bangladesh", "BB": "Barbados",
    "BY": "Belarus", "BE": "Belgium", "BZ": "Belize", "BJ": "Benin",
    "BT": "Bhutan", "BO": "Bolivia", "BA": "Bosnia and Herzegovina",
    "BW": "Botswana", "BR": "Brazil", "BN": "Brunei", "BG": "Bulgaria",
    "BF": "Burkina Faso", "BI": "Burundi", "CV": "Cabo Verde",
    "KH": "Cambodia", "CM": "Cameroon", "CA": "Canada",
    "CF": "Central African Republic", "TD": "Chad", "CL": "Chile",
    "CN": "China", "CO": "Colombia", "KM": "Comoros",
    "CG": "Congo (Brazzaville)", "CD": "Congo (Kinshasa)", "CR": "Costa Rica",
    "CI": "Côte d'Ivoire", "HR": "Croatia", "CU": "Cuba", "CY": "Cyprus",
    "CZ": "Czechia", "DK": "Denmark", "DJ": "Djibouti", "DM": "Dominica",
    "DO": "Dominican Republic", "EC": "Ecuador", "EG": "Egypt",
    "SV": "El Salvador", "GQ": "Equatorial Guinea", "ER": "Eritrea",
    "EE": "Estonia", "SZ": "Eswatini", "ET": "Ethiopia", "FJ": "Fiji",
    "FI": "Finland", "FR": "France", "GA": "Gabon", "GM": "Gambia",
    "GE": "Georgia", "DE": "Germany", "GH": "Ghana", "GR": "Greece",
    "GD": "Grenada", "GT": "Guatemala", "GN": "Guinea", "GW": "Guinea-Bissau",
    "GY": "Guyana", "HT": "Haiti", "HN": "Honduras", "HK": "Hong Kong",
    "HU": "Hungary", "IS": "Iceland", "IN": "India", "ID": "Indonesia",
    "IR": "Iran", "IQ": "Iraq", "IE": "Ireland", "IL": "Israel",
    "IT": "Italy", "JM": "Jamaica", "JP": "Japan", "JO": "Jordan",
    "KZ": "Kazakhstan", "KE": "Kenya", "KI": "Kiribati", "XK": "Kosovo",
    "KW": "Kuwait", "KG": "Kyrgyzstan", "LA": "Laos", "LV": "Latvia",
    "LB": "Lebanon", "LS": "Lesotho", "LR": "Liberia", "LY": "Libya",
    "LI": "Liechtenstein", "LT": "Lithuania", "LU": "Luxembourg",
    "MO": "Macau", "MG": "Madagascar", "MW": "Malawi", "MY": "Malaysia",
    "MV": "Maldives", "ML": "Mali", "MT": "Malta", "MH": "Marshall Islands",
    "MR": "Mauritania", "MU": "Mauritius", "MX": "Mexico",
    "FM": "Micronesia", "MD": "Moldova", "MC": "Monaco", "MN": "Mongolia",
    "ME": "Montenegro", "MA": "Morocco", "MZ": "Mozambique", "MM": "Myanmar",
    "NA": "Namibia", "NR": "Nauru", "NP": "Nepal", "NL": "Netherlands",
    "NZ": "New Zealand", "NI": "Nicaragua", "NE": "Niger", "NG": "Nigeria",
    "KP": "North Korea", "MK": "North Macedonia", "NO": "Norway",
    "OM": "Oman", "PK": "Pakistan", "PW": "Palau", "PS": "Palestine",
    "PA": "Panama", "PG": "Papua New Guinea", "PY": "Paraguay", "PE": "Peru",
    "PH": "Philippines", "PL": "Poland", "PT": "Portugal", "QA": "Qatar",
    "RO": "Romania", "RU": "Russia", "RW": "Rwanda",
    "KN": "Saint Kitts and Nevis", "LC": "Saint Lucia",
    "VC": "Saint Vincent and the Grenadines", "WS": "Samoa",
    "SM": "San Marino", "ST": "Sao Tome and Principe", "SA": "Saudi Arabia",
    "SN": "Senegal", "RS": "Serbia", "SC": "Seychelles", "SL": "Sierra Leone",
    "SG": "Singapore", "SK": "Slovakia", "SI": "Slovenia",
    "SB": "Solomon Islands", "SO": "Somalia", "ZA": "South Africa",
    "KR": "South Korea", "SS": "South Sudan", "ES": "Spain",
    "LK": "Sri Lanka", "SD": "Sudan", "SR": "Suriname", "SE": "Sweden",
    "CH": "Switzerland", "SY": "Syria", "TW": "Taiwan", "TJ": "Tajikistan",
    "TZ": "Tanzania", "TH": "Thailand", "TL": "Timor-Leste", "TG": "Togo",
    "TO": "Tonga", "TT": "Trinidad and Tobago", "TN": "Tunisia",
    "TR": "Turkey", "TM": "Turkmenistan", "TV": "Tuvalu", "UG": "Uganda",
    "UA": "Ukraine", "AE": "United Arab Emirates", "GB": "United Kingdom",
    "US": "United States", "UY": "Uruguay", "UZ": "Uzbekistan",
    "VU": "Vanuatu", "VA": "Vatican City", "VE": "Venezuela",
    "VN": "Vietnam", "YE": "Yemen", "ZM": "Zambia", "ZW": "Zimbabwe",
}


def upgrade() -> None:
    for col in ("billing_country", "shipping_country"):
        op.alter_column(
            "customers",
            col,
            existing_type=sa.String(length=2),
            type_=sa.String(length=100),
            existing_nullable=True,
        )

    # Best-effort convert existing 2-char codes to full names.
    conn = op.get_bind()
    for col in ("billing_country", "shipping_country"):
        col_stmt = sa.text(
            f"UPDATE customers SET {col} = :name WHERE upper({col}) = :code"
        )
        for code, name in _CODE_TO_NAME.items():
            conn.execute(col_stmt, {"name": name, "code": code})


def downgrade() -> None:
    # Truncate any full names back to a 2-char value before narrowing the
    # column so the type change does not fail on long strings.
    for col in ("billing_country", "shipping_country"):
        op.execute(f"UPDATE customers SET {col} = left({col}, 2)")
        op.alter_column(
            "customers",
            col,
            existing_type=sa.String(length=100),
            type_=sa.String(length=2),
            existing_nullable=True,
        )
