// Package calc holds the pure weight/BOM math. No DB access; callers supply
// specific gravities and the stone size matrix. Values stay decimal.Decimal
// throughout and are rounded only by RoundGrams/RoundCarats at storage time.
package calc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

// CaratToGram is the fixed carat -> gram conversion.
var CaratToGram = decimal.RequireFromString("0.200")

var thousand = decimal.NewFromInt(1000)

func RoundGrams(d decimal.Decimal) decimal.Decimal  { return d.Round(3) }
func RoundCarats(d decimal.Decimal) decimal.Decimal { return d.Round(4) }

// NetFromVolume: net_g = volume_mm3 x SG / 1000.
func NetFromVolume(volumeMM3, sg decimal.Decimal) decimal.Decimal {
	return volumeMM3.Mul(sg).Div(thousand)
}

// NetFromReference: net_target = net_ref x (SG_target / SG_ref).
func NetFromReference(netRef, sgTarget, sgRef decimal.Decimal) (decimal.Decimal, error) {
	if !sgRef.IsPositive() {
		return decimal.Zero, fmt.Errorf("reference specific gravity must be positive")
	}
	// Multiply before dividing to avoid losing precision on the ratio.
	return netRef.Mul(sgTarget).DivRound(sgRef, 12), nil
}

// StoneWeight: stone_g = total_carats x 0.200.
func StoneWeight(totalCarats decimal.Decimal) decimal.Decimal {
	return totalCarats.Mul(CaratToGram)
}

// Gross: gross_g = net_g + total_carats x 0.200.
func Gross(net, totalCarats decimal.Decimal) decimal.Decimal {
	return net.Add(StoneWeight(totalCarats))
}

// Seat is one stone-seat row. LockedCode is "" for flexible seats.
type Seat struct {
	SizeMM     decimal.Decimal
	Count      int
	LockedCode string
}

// MatrixKey identifies a stone_size_matrix row.
type MatrixKey struct {
	Size string // size_mm normalised to 2 dp
	Code string
}

// Matrix maps (size, code) -> carats_per_stone.
type Matrix map[MatrixKey]decimal.Decimal

func sizeKey(d decimal.Decimal) string { return d.StringFixed(2) }

func (m Matrix) Set(size decimal.Decimal, code string, carats decimal.Decimal) {
	m[MatrixKey{sizeKey(size), code}] = carats
}

// MissingPair is a (size, code) with no matrix row.
type MissingPair struct {
	SizeMM string
	Code   string
}

// MissingMatrixError lists every (size, code) pair that could not be resolved.
type MissingMatrixError struct{ Pairs []MissingPair }

func (e *MissingMatrixError) Error() string {
	parts := make([]string, len(e.Pairs))
	for i, p := range e.Pairs {
		parts[i] = fmt.Sprintf("(%s mm, %s)", p.SizeMM, p.Code)
	}
	return "missing stone_size_matrix rows for: " + strings.Join(parts, ", ")
}

// BOMLine is one resolved seat.
type BOMLine struct {
	SizeMM         decimal.Decimal
	Count          int
	Code           string
	Locked         bool
	CaratsPerStone decimal.Decimal
	LineCarats     decimal.Decimal
}

// BOM is the resolved stone bill of materials.
type BOM struct {
	Lines       []BOMLine
	TotalPcs    int
	TotalCarats decimal.Decimal
}

// ResolveBOM prices every seat. Flexible seats use chosenCode; locked seats
// keep their own. Any missing matrix row yields a *MissingMatrixError that
// lists all gaps; a missing row is never treated as zero.
func ResolveBOM(seats []Seat, chosenCode string, m Matrix) (BOM, error) {
	var bom BOM
	bom.TotalCarats = decimal.Zero
	var missing []MissingPair
	seen := map[MatrixKey]bool{}

	for _, s := range seats {
		code, locked := chosenCode, false
		if s.LockedCode != "" {
			code, locked = s.LockedCode, true
		}
		key := MatrixKey{sizeKey(s.SizeMM), code}
		cps, ok := m[key]
		if !ok {
			if !seen[key] {
				seen[key] = true
				missing = append(missing, MissingPair{key.Size, code})
			}
			continue
		}
		line := cps.Mul(decimal.NewFromInt(int64(s.Count)))
		bom.Lines = append(bom.Lines, BOMLine{
			SizeMM: s.SizeMM, Count: s.Count, Code: code, Locked: locked,
			CaratsPerStone: cps, LineCarats: line,
		})
		bom.TotalPcs += s.Count
		bom.TotalCarats = bom.TotalCarats.Add(line)
	}
	if len(missing) > 0 {
		sort.Slice(missing, func(i, j int) bool {
			if missing[i].SizeMM != missing[j].SizeMM {
				return missing[i].SizeMM < missing[j].SizeMM
			}
			return missing[i].Code < missing[j].Code
		})
		return BOM{}, &MissingMatrixError{Pairs: missing}
	}
	return bom, nil
}
