package ce

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// CreditExp is the base-unit exponent: 1 credit = 10^18 base units (wei-style). Money is always
// integer base units — never floats. On the wire an amount is a DECIMAL STRING of base units
// (values exceed JSON's 2^53 safe integer), identical to ce-rs `Amount(i128)` and ce-ts `Amount`.
const CreditExp = 18

var creditFactor = new(big.Int).Exp(big.NewInt(10), big.NewInt(CreditExp), nil)

// Amount is an integer quantity of credit base units. The zero value is 0. It is safe to copy:
// arithmetic never mutates its operands.
type Amount struct{ v *big.Int }

// Zero is the additive identity.
var Zero = Amount{}

func (a Amount) big() *big.Int {
	if a.v == nil {
		return new(big.Int)
	}
	return a.v
}

// FromBase builds an Amount from base units.
func FromBase(base *big.Int) Amount { return Amount{new(big.Int).Set(base)} }

// FromBaseInt builds an Amount from an int64 count of base units.
func FromBaseInt(base int64) Amount { return Amount{big.NewInt(base)} }

// FromCredits builds an Amount from a whole number of credits.
func FromCredits(whole int64) Amount {
	return Amount{new(big.Int).Mul(big.NewInt(whole), creditFactor)}
}

// ParseCredits parses a human decimal credit string (e.g. "1.5", "0.000000000000000001") into an
// Amount of base units. At most 18 decimal places; no sign; rejects empty/garbage. Mirrors
// ce-rs `Amount::parse_credits`.
func ParseCredits(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Amount{}, errors.New("amount: empty")
	}
	if strings.ContainsAny(s, "+-") {
		return Amount{}, errors.New("amount: sign not allowed")
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if intPart == "" && fracPart == "" {
		return Amount{}, errors.New("amount: no digits")
	}
	if len(fracPart) > CreditExp {
		return Amount{}, fmt.Errorf("amount: more than %d decimal places", CreditExp)
	}
	if intPart == "" {
		intPart = "0"
	}
	for _, r := range intPart + fracPart {
		if r < '0' || r > '9' {
			return Amount{}, fmt.Errorf("amount: invalid number %q", s)
		}
	}
	combined := intPart + fracPart + strings.Repeat("0", CreditExp-len(fracPart))
	n, ok := new(big.Int).SetString(combined, 10)
	if !ok {
		return Amount{}, fmt.Errorf("amount: invalid number %q", s)
	}
	return Amount{n}, nil
}

// Base returns a copy of the amount in base units.
func (a Amount) Base() *big.Int { return new(big.Int).Set(a.big()) }

// IsZero reports whether the amount is exactly zero.
func (a Amount) IsZero() bool { return a.big().Sign() == 0 }

// Add returns a + b.
func (a Amount) Add(b Amount) Amount { return Amount{new(big.Int).Add(a.big(), b.big())} }

// Sub returns a - b.
func (a Amount) Sub(b Amount) Amount { return Amount{new(big.Int).Sub(a.big(), b.big())} }

// Cmp compares a and b: -1 if a<b, 0 if equal, +1 if a>b.
func (a Amount) Cmp(b Amount) int { return a.big().Cmp(b.big()) }

// Credits renders the amount as a human decimal credit string, trailing zeros trimmed
// ("1500000000000000000" base units -> "1.5").
func (a Amount) Credits() string {
	n := a.big()
	neg := n.Sign() < 0
	q, r := new(big.Int), new(big.Int)
	q.DivMod(new(big.Int).Abs(n), creditFactor, r)
	out := q.String()
	if r.Sign() != 0 {
		rs := r.String()
		frac := strings.TrimRight(strings.Repeat("0", CreditExp-len(rs))+rs, "0")
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// String renders the amount as "<credits> credits".
func (a Amount) String() string { return a.Credits() + " credits" }

// MarshalJSON encodes the amount as a decimal string of base units (the CE wire form).
func (a Amount) MarshalJSON() ([]byte, error) {
	return []byte(`"` + a.big().String() + `"`), nil
}

// UnmarshalJSON decodes a decimal string (or a bare JSON number, or null) of base units.
func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		a.v = new(big.Int)
		return nil
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return fmt.Errorf("amount: invalid base-unit integer %q", s)
	}
	a.v = n
	return nil
}
