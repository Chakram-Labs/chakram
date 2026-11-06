package api

import (
	"encoding/json"
	"io"
	"math/big"
)

// bigInt is an alias so handlers.go can call new(bigInt).SetBytes(...).String().
type bigInt = big.Int

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
