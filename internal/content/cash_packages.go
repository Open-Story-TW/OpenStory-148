package content

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"maple-go-server/internal/domain"
)

//go:embed cash_packages.json
var cashPackageRaw []byte
var packageOnce sync.Once
var packageData map[string][]int
var packageErr error

func CashPackage(templateID int) ([]domain.CashProduct, error) {
	packageOnce.Do(func() {
		var data struct {
			Packages map[string][]int `json:"packages"`
		}
		packageErr = json.Unmarshal(cashPackageRaw, &data)
		packageData = data.Packages
	})
	if packageErr != nil {
		return nil, packageErr
	}
	serials := packageData[strconv.Itoa(templateID)]
	if len(serials) == 0 || len(serials) > 100 {
		return nil, fmt.Errorf("unknown cash package %d", templateID)
	}
	products := make([]domain.CashProduct, 0, len(serials))
	for _, sn := range serials {
		p, err := CashProduct(sn)
		if err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}
