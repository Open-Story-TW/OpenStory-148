package world

import "maple-go-server/internal/domain"

// The world tests feed captured client packets, which carry the stock
// four-byte meso amounts. Tests of the long money contract switch it on
// themselves.
func init() { domain.SetLongMoney(false) }
