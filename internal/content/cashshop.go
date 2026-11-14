package content

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"maple-go-server/internal/domain"
)

//go:embed cash_catalog.json
var cashCatalogRaw []byte
var cashCatalogOnce sync.Once
var cashCatalog map[int]domain.CashProduct
var cashCatalogError error

func CashProduct(sn int) (domain.CashProduct, error) {
	cashCatalogOnce.Do(loadCashCatalog)
	if cashCatalogError != nil {
		return domain.CashProduct{}, cashCatalogError
	}
	item, ok := cashCatalog[sn]
	if !ok {
		return domain.CashProduct{}, fmt.Errorf("unknown cash serial %d", sn)
	}
	return item, nil
}
func CashProducts() ([]domain.CashProduct, error) {
	cashCatalogOnce.Do(loadCashCatalog)
	if cashCatalogError != nil {
		return nil, cashCatalogError
	}
	items := make([]domain.CashProduct, 0, len(cashCatalog))
	for _, item := range cashCatalog {
		items = append(items, item)
	}
	return items, nil
}
func loadCashCatalog() {
	var data struct {
		Version int       `json:"version"`
		Items   [][]int64 `json:"items"`
	}
	if err := json.Unmarshal(cashCatalogRaw, &data); err != nil {
		cashCatalogError = err
		return
	}
	if data.Version != 2 {
		cashCatalogError = fmt.Errorf("unsupported cash catalog version")
		return
	}
	cashCatalog = make(map[int]domain.CashProduct)
	for _, row := range data.Items {
		if len(row) != 8 || (row[7] != 0 && row[7] != 2 && row[7] != 3) {
			cashCatalogError = fmt.Errorf("bad cash catalog row")
			return
		}
		cashCatalog[int(row[0])] = domain.CashProduct{SN: int(row[0]), TemplateID: int(row[1]), Quantity: int(row[2]), Price: row[3], PeriodDays: int(row[4]), Gender: int(row[5]), OnSale: row[6] > 0 && row[3] > 0, LimitMode: int(row[7])}
	}
}
