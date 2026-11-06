package fx

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func date(s string) time.Time      { v, _ := time.Parse("2006-01-02", s); return v }
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func TestFX(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil || u.Hostname() == "" {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	conn, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close(ctx)
	_, e = conn.Exec(ctx, `INSERT INTO fx_rates (from_currency,to_currency,rate,as_of_date) VALUES ('USD','IDR',2,'2020-01-01'),('USD','IDR',3,'2020-01-02'),('USD','IDR',4,'2020-01-04'),('EUR','IDR',9,'2020-01-02'),('USD','EUR',8,'2020-01-02'),('JPY','IDR',1.23456789,'2020-01-01'),('GBP','IDR','NaN','2020-01-01')`)
	if e != nil {
		t.Fatal(e)
	}
	for _, tt := range []struct{ name, src, base, day, want string }{
		{"identity no row", "idr", "IdR", "1900-01-01", "1"}, {"case and cutoff", "usd", "idr", "2020-01-03", "3"}, {"inclusive", "USD", "IDR", "2020-01-02", "3"}, {"source isolation", "EUR", "IDR", "2020-01-03", "9"}, {"target isolation", "USD", "EUR", "2020-01-03", "8"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v, e := GetRate(ctx, conn, tt.src, tt.base, date(tt.day))
			if e != nil || !v.Equal(dec(tt.want)) {
				t.Fatalf("rate %s error %v", v, e)
			}
		})
	}
	for _, tt := range []struct{ src, base, day string }{{"USD", "IDR", "2019-12-31"}, {"IDR", "USD", "2020-01-03"}, {"EUR", "USD", "2020-01-03"}} {
		_, e := GetRate(ctx, conn, tt.src, tt.base, date(tt.day))
		var missing *MissingRateError
		if !errors.As(e, &missing) {
			t.Fatalf("expected missing rate for %+v: %v", tt, e)
		}
	}
	for _, convert := range []bool{false, true} {
		var err error
		if convert {
			_, err = Convert(ctx, conn, dec("2"), "GBP", "IDR", date("2020-01-03"))
		} else {
			_, err = GetRate(ctx, conn, "GBP", "IDR", date("2020-01-03"))
		}
		var invalid *InvalidRateError
		if !errors.As(err, &invalid) {
			t.Fatalf("convert=%v: expected invalid rate error, got %v", convert, err)
		}
	}

	for _, tt := range []struct{ amount, src, base, want, rate string }{
		{"0", "USD", "IDR", "0.0000", "3"}, {"-1.23455", "IDR", "IDR", "-1.2346", "1"}, {"1.23445", "IDR", "IDR", "1.2344", "1"}, {"1.23455", "IDR", "IDR", "1.2346", "1"}, {"-1.23445", "IDR", "IDR", "-1.2344", "1"},
		{"123456789012345.6789", "JPY", "IDR", "152415787517146.7888", "1.23456789"},
		{"0.000049999999999999999999999999999", "IDR", "IDR", "0.0000", "1"},
		{"1.23454999999999999999999999999", "IDR", "IDR", "1.2346", "1"},
	} {
		t.Run("convert "+tt.amount, func(t *testing.T) {
			v, e := Convert(ctx, conn, dec(tt.amount), tt.src, tt.base, date("2020-01-03"))
			if e != nil || !v.BaseAmount.Equal(dec(tt.want)) || !v.Rate.Equal(dec(tt.rate)) || v.BaseAmount.Exponent() != -4 {
				t.Fatalf("got %+v, error %v", v, e)
			}
		})
	}
	_, err := Convert(ctx, conn, dec("1000000000000000000000000"), "IDR", "IDR", date("2020-01-03"))
	if !errors.Is(err, ErrPrecision) {
		t.Fatalf("expected precision error, got %v", err)
	}

}
