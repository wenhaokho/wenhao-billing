// Package projects serves the project routes at /api/v1/projects with the
// Python backend's contract: create, list, read, partial update and delete.
// The summary and the project's invoices are in summary.go.
package projects

import (
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects/projectsdb"
)

// ProjectOut is a project, as every route answers.
type ProjectOut struct {
	ProjectID     uuid.UUID      `json:"project_id"`
	CustomerID    uuid.UUID      `json:"customer_id"`
	Code          string         `json:"code"`
	Name          string         `json:"name"`
	Currency      string         `json:"currency"`
	ContractValue *platform.Dec4 `json:"contract_value"`
	Status        string         `json:"status" enum:"ACTIVE,ON_HOLD,COMPLETED,CANCELLED"`
	StartDate     *platform.Date `json:"start_date"`
	EndDate       *platform.Date `json:"end_date"`
	Notes         *string        `json:"notes"`
	Active        bool           `json:"active"`
	CreatedAt     platform.Time  `json:"created_at"`
}

func projectOut(p projectsdb.Project) ProjectOut {
	out := ProjectOut{
		ProjectID: p.ProjectID, CustomerID: p.CustomerID, Code: p.Code, Name: p.Name, Currency: p.Currency,
		Status: p.Status, Notes: p.Notes, Active: p.Active, CreatedAt: platform.Time{Time: p.CreatedAt},
	}
	if p.ContractValue.Valid {
		v := platform.NewDec4(p.ContractValue.Decimal)
		out.ContractValue = &v
	}
	out.StartDate, out.EndDate = datePtr(p.StartDate), datePtr(p.EndDate)
	return out
}

func datePtr(d pgtype.Date) *platform.Date {
	if !d.Valid {
		return nil
	}
	return &platform.Date{Time: d.Time}
}

// ProjectCreate adds a project. The code, name and currency are checked as
// sent and then again as stored (see normalise).
type ProjectCreate struct {
	CustomerID    uuid.UUID                        `json:"customer_id"`
	Code          string                           `json:"code" minLength:"1" maxLength:"40" doc:"Trimmed and upper-cased."`
	Name          string                           `json:"name" minLength:"1" maxLength:"255" doc:"Trimmed."`
	Currency      string                           `json:"currency" minLength:"3" maxLength:"3" doc:"Upper-cased."`
	ContractValue platform.Optional[platform.Dec4] `json:"contract_value" doc:"The project's budget; 0 or more."`
	Status        platform.NotNull[string]         `json:"status" enum:"ACTIVE,ON_HOLD,COMPLETED,CANCELLED" doc:"Defaults to ACTIVE."`
	StartDate     platform.Optional[platform.Date] `json:"start_date"`
	EndDate       platform.Optional[platform.Date] `json:"end_date"`
	Notes         platform.Optional[string]        `json:"notes"`
}

// ProjectUpdate changes the fields sent and leaves the rest. A null clears a
// nullable column and is a 422 for the others. The code and the currency are
// normalised as on create; the name is stored as sent.
type ProjectUpdate struct {
	CustomerID    platform.NotNull[uuid.UUID]      `json:"customer_id"`
	Code          platform.NotNull[string]         `json:"code" minLength:"1" maxLength:"40"`
	Name          platform.NotNull[string]         `json:"name" minLength:"1" maxLength:"255"`
	Currency      platform.NotNull[string]         `json:"currency" minLength:"3" maxLength:"3"`
	ContractValue platform.Optional[platform.Dec4] `json:"contract_value" doc:"0 or more; null clears it."`
	Status        platform.NotNull[string]         `json:"status" enum:"ACTIVE,ON_HOLD,COMPLETED,CANCELLED"`
	StartDate     platform.Optional[platform.Date] `json:"start_date"`
	EndDate       platform.Optional[platform.Date] `json:"end_date"`
	Notes         platform.Optional[string]        `json:"notes"`
	Active        platform.NotNull[bool]           `json:"active"`
}

// The sentences of a value that is out of bounds once normalised (Pydantic's
// wording, as Huma's own for the value as sent).
const (
	blankSentence    = "String should have at least 1 character"
	negativeSentence = "Input should be greater than or equal to 0"
)

// normCode is the stored code: trimmed, then upper-cased (Python's
// strip().upper(), which can lengthen a value: "ß" is "SS"). Python checked
// the length before normalising and so stored "" for a blank code and failed
// on a long one with a 500; both are 422s at code here (port note).
func normCode(s string) (string, error) {
	c, err := platform.PyUpperAtMost("code", strings.TrimSpace(s), 40)
	if err != nil {
		return "", err
	}
	if c == "" {
		return "", platform.FieldError("code", blankSentence)
	}
	return c, nil
}

// normCurrency is the stored currency: upper-cased, and still three characters.
func normCurrency(s string) (string, error) { return platform.PyUpperAtMost("currency", s, 3) }

// contractValue is the budget as stored: nil, or 0 or more.
func contractValue(f platform.Optional[platform.Dec4], cur decimal.NullDecimal) (decimal.NullDecimal, error) {
	if !f.Set {
		return cur, nil
	}
	if f.Null {
		return decimal.NullDecimal{}, nil
	}
	v := f.Value.Decimal()
	if v.IsNegative() {
		return decimal.NullDecimal{}, platform.FieldError("contract_value", negativeSentence)
	}
	return decimal.NullDecimal{Decimal: v, Valid: true}, nil
}

func mergeDate(f platform.Optional[platform.Date], cur pgtype.Date) pgtype.Date {
	if !f.Set {
		return cur
	}
	if f.Null {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: f.Value.Time, Valid: true}
}
