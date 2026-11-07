// Command sqlcscope regenerates sqlc's migration-8 ownership column overlay.
package main

import (
	"fmt"
	"os"

	"github.com/wenhaokho/wenhao-billing/backend/internal/businessscope"
)

func main() {
	if err := os.WriteFile("sqlc_business_scope.sql", []byte(businessscope.SQLCOwnershipOverlay()), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
