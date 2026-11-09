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

func validInvoiceFeeRate(rate float64) bool {
	return validInvoiceFeeValue(rate, 100)
}

func validInvoiceFeeMinAmount(amount float64) bool {
	return validInvoiceFeeValue(amount, 1e9)
}

func validInvoiceFeeValue(value, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= maximum &&
		math.Abs(value*100-math.Round(value*100)) < 0.000001
}

func normalizedInvoiceFeeRate(raw string) float64 {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !validInvoiceFeeRate(value) {
		return 0
	}
	return value
}

func normalizedInvoiceFeeMinAmount(raw string) float64 {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !validInvoiceFeeMinAmount(value) {
		return 0
	}
	return value
}
