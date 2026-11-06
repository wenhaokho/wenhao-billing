import json, sys
from datetime import date
from decimal import Decimal
from types import SimpleNamespace
from uuid import UUID
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
import app.models  # registers relationships
from app.services.ledger import post_payment_cleared, post_reversal

engine = create_engine(sys.argv[1].replace('postgres://', 'postgresql+psycopg://', 1))
with Session(engine) as db:
    db.execute(text("INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date) VALUES ('USD','IDR',2,'2020-01-01')"))
    cases = [('same', 'IDR', 'IDR', '10', '0', '0'), ('withheld', 'USD', 'USD', '10', '0.0001', '0'), ('gain','USD','IDR','10','0','15'), ('loss','USD','IDR','10','0','25')]
    result = {}
    for name, pc, ic, amount, withheld, balance in cases:
        payment = SimpleNamespace(payment_id=UUID('11111111-1111-4111-8111-111111111111'), status='CLEARED', currency=pc, amount=Decimal(amount), payment_date=date(2020,1,2))
        invoice = SimpleNamespace(invoice_id=UUID('22222222-2222-4222-8222-222222222222'), currency=ic, balance_due=Decimal(balance))
        entry = post_payment_cleared(db, payment, invoice, Decimal(withheld))
        for kind, journal in [('post', entry), ('reverse', post_reversal(db, entry.entry_id))]:
            rows = db.execute(text('SELECT a.code,l.currency,l.debit,l.credit,l.base_amount_debit,l.base_amount_credit,l.fx_rate FROM journal_lines l JOIN chart_of_accounts a USING(account_id) WHERE l.entry_id=:id ORDER BY a.code'), {'id':journal.entry_id}).all()
            result[name+'_'+kind] = {'source_type':journal.source_type,'source_id':str(journal.source_id),'memo': journal.memo.replace(str(entry.entry_id), '<entry>') if kind == 'reverse' else journal.memo,'lines':[[str(x) for x in row] for row in rows]}
    print(json.dumps(result,sort_keys=True))
