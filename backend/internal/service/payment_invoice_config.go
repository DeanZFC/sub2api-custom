package service

import (
	"math"
	"strconv"
)

const defaultInvoiceMinAmount = 300.0

func validInvoiceMinAmount(amount float64) bool {
	return !math.IsNaN(amount) && !math.IsInf(amount, 0) && amount > 0 && amount <= 1e9 &&
		math.Abs(amount*100-math.Round(amount*100)) < 0.000001
}

func normalizedInvoiceMinAmount(raw string) float64 {
	amount, err := strconv.ParseFloat(raw, 64)
	if err != nil || !validInvoiceMinAmount(amount) {
		return defaultInvoiceMinAmount
	}
	return amount
}
