package test

import (
	"testing"

	"github.com/lornshark/shark/sharkdecimal"
	"github.com/shopspring/decimal"
)

func TestNormalize2(t *testing.T) {
	tests := []struct {
		input    any
		expected string
	}{
		{19.999, "19.99"},
		{"19.999", "19.99"},
		{20, "20.00"},
		{0, "0.00"},
		{19.99, "19.99"},
		{"abc", "0.00"},
	}
	for _, tt := range tests {
		result := sharkdecimal.Normalize2(tt.input)
		if result.StringFixedBank(2) != tt.expected {
			t.Errorf("Normalize2(%v) = %s, want %s", tt.input, result.StringFixedBank(2), tt.expected)
		}
	}
}

func TestNormalize6(t *testing.T) {
	tests := []struct {
		input    any
		expected string
	}{
		{7.12345678, "7.123456"},
		{"7.23456789", "7.234567"},
		{3.14159265358979, "3.141592"},
	}
	for _, tt := range tests {
		result := sharkdecimal.Normalize6(tt.input)
		if result.String() != tt.expected {
			t.Errorf("Normalize6(%v) = %s, want %s", tt.input, result.String(), tt.expected)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input  any
		places int32
		want   string
	}{
		{19.999, 2, "19.99"},
		{19.999, 0, "19"},
		{3.14159, 3, "3.141"},
		{42, 4, "42.0000"},
		{"19.99", 2, "19.99"},
		{"1e2", 2, "100.00"},
		{"not-a-number", 2, "0.00"},
		{nil, 2, "0.00"},
		{int64(100), 2, "100.00"},
		{float32(3.14), 2, "3.14"},
		{float64(1.0 / 3.0), 6, "0.333333"},
	}
	for _, tt := range tests {
		result := sharkdecimal.Normalize(tt.input, tt.places)
		if result.StringFixedBank(tt.places) != tt.want {
			t.Errorf("Normalize(%v, %d) = %s, want %s", tt.input, tt.places, result.StringFixedBank(tt.places), tt.want)
		}
	}
}

func TestNormalizeNegativePlaces(t *testing.T) {
	result := sharkdecimal.Normalize(19.999, -1)
	if result.String() != "19" {
		t.Errorf("Normalize with negative places: got %s, want 19", result.String())
	}
}

func TestNormalizeDecimalInput(t *testing.T) {
	d := decimal.NewFromFloat(1.0 / 3.0)
	result := sharkdecimal.Normalize(d, 6)
	if result.String() != "0.333333" {
		t.Errorf("Normalize decimal input: got %s, want 0.333333", result.String())
	}
}

func TestNormalize2Calculation(t *testing.T) {
	price := sharkdecimal.Normalize2(19.99)
	tax := sharkdecimal.Normalize2(1.50)
	total := sharkdecimal.Normalize2(price.Add(tax))
	if total.String() != "21.49" {
		t.Errorf("价格计算: 19.99 + 1.50 = %s, want 21.49", total.String())
	}
}
