package memory

import (
	"testing"

	"github.com/young1ll/keelage/internal/port"
	"github.com/young1ll/keelage/internal/port/ledgertest"
)

func TestLedger_Contract(t *testing.T) {
	ledgertest.Run(t, func(*testing.T) port.Ledger { return NewLedger(nil) })
}
