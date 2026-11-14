package domain

import (
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
)

// FeeTier charges Rate on an amount of From mesos or more. Rate is in parts
// per 100,000, so 0.4% is 400 and 3% is 3000.
type FeeTier struct {
	From int64 `json:"from"`
	Rate int64 `json:"rate"`
}

// FeeRateScale is the denominator of FeeTier.Rate.
const FeeRateScale = 100000

// MaxFeeTiers is what the native fee table in the shop dialog can show.
const MaxFeeTiers = 6

// Default fee tables: Legacy GameConstants.EntrustedStoreTax (also the table
// the native shop dialog shows) and GameConstants.getTaxAmount.
const (
	DefaultMerchantFees = "100000:0.4,1000000:0.9,5000000:1.5,10000000:2,25000000:2.5,100000000:3"
	DefaultTradeFees    = "100000:0.8,1000000:1.8,5000000:3,10000000:4,25000000:5,100000000:6"
)

var (
	merchantFees atomic.Pointer[[]FeeTier]
	tradeFees    atomic.Pointer[[]FeeTier]
)

func init() {
	SetMerchantFees(mustFees(DefaultMerchantFees))
	SetTradeFees(mustFees(DefaultTradeFees))
}

func mustFees(text string) []FeeTier {
	tiers, err := ParseFeeTiers(text)
	if err != nil {
		panic(err)
	}
	return tiers
}

// ErrFeeTiers reports a fee table that cannot be used.
var ErrFeeTiers = errors.New("fee table must be amount:percent pairs in rising amounts, at most six, each 0 to 100 percent")

// ParseFeeTiers reads "100000:0.4,1000000:0.9": from each amount on, that
// percentage (up to three decimals) is charged. An empty text charges nothing.
func ParseFeeTiers(text string) ([]FeeTier, error) {
	tiers := make([]FeeTier, 0, MaxFeeTiers)
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		amount, percent, ok := strings.Cut(part, ":")
		if !ok {
			return nil, ErrFeeTiers
		}
		from, err := strconv.ParseInt(strings.TrimSpace(amount), 10, 64)
		if err != nil || from < 0 {
			return nil, ErrFeeTiers
		}
		rate, ok := parsePercent(strings.TrimSuffix(strings.TrimSpace(percent), "%"))
		if !ok {
			return nil, ErrFeeTiers
		}
		if n := len(tiers); n > 0 && from <= tiers[n-1].From {
			return nil, ErrFeeTiers
		}
		tiers = append(tiers, FeeTier{From: from, Rate: rate})
	}
	if len(tiers) > MaxFeeTiers {
		return nil, ErrFeeTiers
	}
	return tiers, nil
}

// parsePercent turns "2.5" into 2500 parts per 100,000 without floating point.
func parsePercent(text string) (int64, bool) {
	whole, fraction, _ := strings.Cut(text, ".")
	if whole == "" || len(fraction) > 3 {
		return 0, false
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 || w > 100 {
		return 0, false
	}
	f, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil || f < 0 {
		return 0, false
	}
	rate := w*1000 + f
	return rate, rate <= FeeRateScale
}

// FormatFeeTiers is the canonical text of a fee table.
func FormatFeeTiers(tiers []FeeTier) string {
	parts := make([]string, 0, len(tiers))
	for _, tier := range tiers {
		percent := strconv.FormatInt(tier.Rate/1000, 10)
		if fraction := tier.Rate % 1000; fraction != 0 {
			percent += "." + strings.TrimRight(strconv.FormatInt(1000+fraction, 10)[1:], "0")
		}
		parts = append(parts, strconv.FormatInt(tier.From, 10)+":"+percent)
	}
	return strings.Join(parts, ",")
}

// Fee is what tiers charge on amount, rounded half up. It cannot overflow and
// never exceeds amount.
func Fee(tiers []FeeTier, amount int64) int64 {
	if amount <= 0 {
		return 0
	}
	rate := int64(0)
	for _, tier := range tiers {
		if amount >= tier.From {
			rate = tier.Rate
		}
	}
	return amount/FeeRateScale*rate + (amount%FeeRateScale*rate+FeeRateScale/2)/FeeRateScale
}

// MerchantFees is the hired merchant seller fee table in effect.
func MerchantFees() []FeeTier { return append([]FeeTier(nil), *merchantFees.Load()...) }

// TradeFees is the player trade meso fee table in effect.
func TradeFees() []FeeTier { return append([]FeeTier(nil), *tradeFees.Load()...) }

// SetMerchantFees replaces the hired merchant fee table.
func SetMerchantFees(tiers []FeeTier) {
	copied := append([]FeeTier(nil), tiers...)
	merchantFees.Store(&copied)
}

// SetTradeFees replaces the player trade fee table.
func SetTradeFees(tiers []FeeTier) {
	copied := append([]FeeTier(nil), tiers...)
	tradeFees.Store(&copied)
}

// MerchantFee is the seller's fee on one hired merchant sale.
func MerchantFee(total int64) int64 { return Fee(*merchantFees.Load(), total) }

// TradeFee is the fee on the mesos one side receives in a trade.
func TradeFee(mesos int64) int64 { return Fee(*tradeFees.Load(), mesos) }
